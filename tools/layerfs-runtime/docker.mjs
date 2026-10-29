import { Buffer } from 'node:buffer';
import { realpath, stat } from 'node:fs/promises';
import { request } from 'node:http';
import path from 'node:path';
import { BridgeError } from './validation.mjs';

// The selected local engine owns its data directory. Never infer it from the
// socket location or assume the host's default Docker installation.
async function dockerRequest(socket, method, target) {
  if (
    typeof socket !== 'string' ||
    !path.isAbsolute(socket) ||
    socket.includes('\0')
  )
    throw new BridgeError(
      'InvalidInput',
      'Docker requires a local socket path.',
    );
  const response = await new Promise((resolve, reject) => {
    request(
      {
        socketPath: socket,
        path: `/v1.54${target}`,
        method,
        agent: false,
        signal: globalThis.AbortSignal.timeout(15_000),
      },
      resolve,
    )
      .once('error', reject)
      .end();
  });
  try {
    if (response.statusCode === 404) return null;
    if (response.statusCode !== 200 && response.statusCode !== 204)
      throw new BridgeError(
        'Unsupported',
        `Docker ${method} request returned HTTP ${response.statusCode}.`,
      );
    const chunks = [];
    let bytes = 0;
    for await (const chunk of response) {
      bytes += chunk.length;
      if (bytes > 1024 * 1024)
        throw new BridgeError('InvalidInput', 'Docker response is too large.');
      chunks.push(chunk);
    }
    return bytes ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : {};
  } finally {
    response.destroy();
  }
}

export async function dockerVolumeDirectory(socket) {
  const info = await dockerRequest(socket, 'GET', '/info');
  const root = info?.DockerRootDir;
  if (typeof root !== 'string' || !path.isAbsolute(root) || root.includes('\0'))
    throw new BridgeError(
      'Unsupported',
      'Docker did not report an absolute local data directory.',
    );
  const volumes = await realpath(path.join(root, 'volumes'));
  if (!(await stat(volumes)).isDirectory())
    throw new BridgeError(
      'Unsupported',
      'Docker volumes must be accessible locally.',
    );
  return volumes;
}

function ownedVolume(volume, home) {
  return (
    volume?.Labels?.['layerfs.role'] === 'store' &&
    volume.Labels['layerfs.home'] === home &&
    typeof volume.Name === 'string' &&
    /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$/.test(volume.Name)
  );
}

// Cleanup needs only this home's Stores, never engine-wide image/disk inventory.
// Docker's label filter bounds the response; check ownership again on receipt.
export async function ownedStoreVolumes(socket, home) {
  const filters = encodeURIComponent(
    JSON.stringify({ label: ['layerfs.role=store', `layerfs.home=${home}`] }),
  );
  const response = await dockerRequest(
    socket,
    'GET',
    `/volumes?filters=${filters}`,
  );
  if (
    !response ||
    (response.Volumes !== null && !Array.isArray(response.Volumes))
  )
    throw new BridgeError(
      'Unknown',
      'Docker did not return a volume inventory.',
    );
  return (response.Volumes ?? []).map((volume) => {
    if (!ownedVolume(volume, home))
      throw new BridgeError(
        'Unknown',
        'Docker returned a Store outside this private LayerFS home.',
      );
    return { name: volume.Name, project: volume.Labels['layerfs.project'] };
  });
}

export async function removeOwnedStoreVolumes(socket, home, names) {
  for (const name of names) {
    if (
      typeof name !== 'string' ||
      !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$/.test(name)
    )
      throw new BridgeError('InvalidInput', 'Invalid Store volume name.');
    const target = `/volumes/${encodeURIComponent(name)}`;
    const volume = await dockerRequest(socket, 'GET', target);
    if (volume === null) continue;
    if (volume.Name !== name || !ownedVolume(volume, home))
      throw new BridgeError(
        'Unknown',
        'Store ownership changed; cleanup stopped.',
      );
    // Without force, Docker refuses volumes referenced by any container.
    await dockerRequest(socket, 'DELETE', `${target}?force=false`);
  }
}
