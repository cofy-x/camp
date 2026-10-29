import { Buffer } from 'node:buffer';
import { realpath, stat } from 'node:fs/promises';
import { get } from 'node:http';
import path from 'node:path';
import { BridgeError } from './validation.mjs';

// The selected local engine owns its data directory. Never infer it from the
// socket location or assume the host's default Docker installation.
export async function dockerVolumeDirectory(socket) {
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
    get(
      {
        socketPath: socket,
        path: '/v1.54/info',
        agent: false,
        signal: globalThis.AbortSignal.timeout(15_000),
      },
      resolve,
    ).once('error', reject);
  });
  try {
    if (response.statusCode !== 200)
      throw new BridgeError(
        'Unsupported',
        `Docker data directory discovery returned HTTP ${response.statusCode}.`,
      );
    const chunks = [];
    let bytes = 0;
    for await (const chunk of response) {
      bytes += chunk.length;
      if (bytes > 1024 * 1024)
        throw new BridgeError(
          'InvalidInput',
          'Docker system information is too large.',
        );
      chunks.push(chunk);
    }
    const info = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    const root = info?.DockerRootDir;
    if (
      typeof root !== 'string' ||
      !path.isAbsolute(root) ||
      root.includes('\0')
    )
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
  } finally {
    response.destroy();
  }
}
