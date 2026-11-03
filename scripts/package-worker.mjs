import { createHash, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { mkdir, readFile, rename, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { pipeline } from 'node:stream/promises';
import { createGzip } from 'node:zlib';
import { command, root } from './lib.mjs';
import {
  requireSourceEngine,
  saveSourceEngineProof,
  selectSourceEngine,
  sourceDocker,
  sourceEngineArguments,
  stopSourceContainer,
  inspectSourceContainer,
} from '../tools/layerfs-worker/source-engine.mjs';

// Local packaging only. This command never pulls, pushes or publishes images.
const options = sourceEngineArguments(process.argv.slice(2));
const installed = JSON.parse(
  await readFile(
    path.join(root, 'tools/layerfs-worker/installed.json'),
    'utf8',
  ),
);
if (
  process.arch !== 'arm64' ||
  installed.platform !== 'linux/arm64' ||
  !/^sha256:[a-f0-9]{64}$/.test(installed.image)
)
  throw new Error(
    'Build the pinned native ARM64 checkpoint worker before packaging.',
  );
const selected = await selectSourceEngine(options);
const info = requireSourceEngine(selected);
if (!['arm64', 'aarch64'].includes(info.Architecture))
  throw new Error('Package on an actual native ARM64 Linux engine.');
const actual = sourceDocker(selected, ['image', 'inspect', installed.image], {
  json: true,
})[0];
if (
  actual.Id !== installed.image ||
  actual.Architecture !== 'arm64' ||
  actual.Os !== 'linux' ||
  actual.Config?.Labels?.['camp.layerfs.commit'] !== installed.commit ||
  actual.Config?.Labels?.['camp.runtime'] !== 'layerfs-worker'
)
  throw new Error('The actual worker image differs from its installation.');
const nonce = randomUUID().replaceAll('-', '');
const owner = {
  engineID: info.ID,
  container: 'camp-worker-manifest-' + nonce,
  image: installed.image,
  labels: {
    'cofy.camp.scope': 'worker-image-packaging',
    'cofy.camp.operation': nonce,
    'cofy.camp.nonce': nonce,
    'cofy.camp.image': installed.image,
  },
};
if (inspectSourceContainer(selected, owner.container))
  throw new Error('The source manifest container name is already occupied.');
await saveSourceEngineProof(selected, {
  operation: 'package-native-arm64-worker',
  phase: 'manifest-read-dispatched',
  owner,
  sourceInputs: {
    'package-worker.mjs': createHash('sha256')
      .update(await readFile(import.meta.filename))
      .digest('hex'),
    'source-engine.mjs': createHash('sha256')
      .update(
        await readFile(
          path.join(root, 'tools/layerfs-worker/source-engine.mjs'),
        ),
      )
      .digest('hex'),
  },
  interruptionLimit:
    'SIGKILL cannot run cleanup; retain this intent for operator inspection. No automatic replay is performed.',
});
const interrupted = new AbortController();
const int = () =>
  interrupted.abort(new Error('Worker packaging interrupted by SIGINT.'));
const term = () =>
  interrupted.abort(new Error('Worker packaging interrupted by SIGTERM.'));
process.on('SIGINT', int);
process.on('SIGTERM', term);
try {
  let manifest;
  try {
    manifest = JSON.parse(
      sourceDocker(selected, [
        'run',
        '--rm',
        '--name',
        owner.container,
        ...Object.entries(owner.labels).flatMap(([key, value]) => [
          '--label',
          key + '=' + value,
        ]),
        '--platform',
        'linux/arm64',
        '--pull=never',
        '--read-only',
        '--network=none',
        installed.image,
        '-e',
        'process.stdout.write(require("fs").readFileSync("/opt/camp/layerfs/manifest.json"))',
      ]),
    );
  } finally {
    stopSourceContainer(selected, owner);
  }
  interrupted.signal.throwIfAborted();
  if (manifest.status !== 'sealed' || manifest.architecture !== 'arm64')
    throw new Error('The actual image has no sealed ARM64 manifest.');
  for (const [filename, expected] of Object.entries(manifest.files).filter(
    ([name]) => name.startsWith('worker/'),
  )) {
    const bytes = await readFile(
      path.join(root, 'tools/layerfs-worker', filename.slice('worker/'.length)),
    );
    if (createHash('sha256').update(bytes).digest('hex') !== expected)
      throw new Error(
        'Rebuild the checkpoint image after changing its administrative helper.',
      );
  }
  const destination = path.join(root, 'dist');
  await mkdir(destination, { recursive: true });
  const temporary = path.join(destination, `.worker-${randomUUID()}`);
  await saveSourceEngineProof(selected, {
    phase: 'image-save-dispatched',
    temporaryArchive: `${temporary}.tar`,
  });
  await command(
    selected.binary,
    [
      ...selected.args,
      'image',
      'save',
      '--output',
      `${temporary}.tar`,
      installed.image,
    ],
    { env: selected.environment },
  );
  requireSourceEngine(selected, info.ID);
  interrupted.signal.throwIfAborted();
  await pipeline(
    createReadStream(`${temporary}.tar`),
    createGzip({ level: 6 }),
    createWriteStream(`${temporary}.tar.gz`, { flags: 'wx', mode: 0o600 }),
  );
  interrupted.signal.throwIfAborted();
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(`${temporary}.tar.gz`)) {
    interrupted.signal.throwIfAborted();
    hash.update(chunk);
  }
  interrupted.signal.throwIfAborted();
  const sha256 = hash.digest('hex'),
    filename = `camp-layerfs-worker-linux-arm64-${sha256}.tar.gz`;
  await rename(`${temporary}.tar.gz`, path.join(destination, filename));
  // Retain the uncompressed owned archive for evidence and offline inspection.
  await rename(
    `${temporary}.tar`,
    path.join(
      destination,
      `camp-layerfs-worker-${installed.image.slice(7)}.tar`,
    ),
  );
  const descriptor = {
    schema: 1,
    platform: installed.platform,
    sourceCommit: installed.commit,
    version: installed.version,
    image: installed.image,
    buildId: manifest.build_id,
    campSourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], {
      cwd: root,
      encoding: 'utf8',
      env: selected.environment,
    }).trim(),
    artifact: {
      filename,
      sha256,
      bytes: (await stat(path.join(destination, filename))).size,
    },
  };
  interrupted.signal.throwIfAborted();
  await writeFile(
    path.join(destination, 'checkpoint-worker-release.json'),
    `${JSON.stringify(descriptor, null, 2)}\n`,
  );
  await writeFile(
    path.join(destination, `${filename}.sha256`),
    `${sha256}  ${filename}\n`,
  );
  process.stdout.write(
    `Packaged optional checkpoint worker ${filename}: ${descriptor.artifact.bytes} bytes\n`,
  );

  interrupted.signal.throwIfAborted();
  await saveSourceEngineProof(selected, {
    phase: 'passed',
    archiveSHA256: sha256,
    image: installed.image,
  });
  interrupted.signal.throwIfAborted();
} catch (error) {
  await saveSourceEngineProof(selected, {
    phase: 'failed-or-cleanup-unknown',
    error: error.message,
  });
  throw error;
} finally {
  process.removeListener('SIGINT', int);
  process.removeListener('SIGTERM', term);
}
