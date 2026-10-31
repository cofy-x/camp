import { createHash, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { mkdir, readFile, rename, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { pipeline } from 'node:stream/promises';
import { createGzip } from 'node:zlib';
import { command, root } from './lib.mjs';

// Local packaging only. This command never pulls, pushes or publishes images.
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
const manifest = JSON.parse(
  execFileSync(
    'docker',
    [
      'run',
      '--rm',
      '--platform',
      'linux/arm64',
      '--pull=never',
      '--read-only',
      '--network=none',
      installed.image,
      '-e',
      'process.stdout.write(require("fs").readFileSync("/opt/camp/layerfs/manifest.json"))',
    ],
    { encoding: 'utf8' },
  ),
);
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
await command('docker', [
  'image',
  'save',
  '--output',
  `${temporary}.tar`,
  installed.image,
]);
await pipeline(
  createReadStream(`${temporary}.tar`),
  createGzip({ level: 6 }),
  createWriteStream(`${temporary}.tar.gz`, { flags: 'wx', mode: 0o600 }),
);
const hash = createHash('sha256');
for await (const chunk of createReadStream(`${temporary}.tar.gz`))
  hash.update(chunk);
const sha256 = hash.digest('hex'),
  filename = `camp-layerfs-worker-linux-arm64-${sha256}.tar.gz`;
await rename(`${temporary}.tar.gz`, path.join(destination, filename));
// Retain the uncompressed owned archive for evidence and offline inspection.
await rename(
  `${temporary}.tar`,
  path.join(destination, `camp-layerfs-worker-${installed.image.slice(7)}.tar`),
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
  }).trim(),
  artifact: {
    filename,
    sha256,
    bytes: (await stat(path.join(destination, filename))).size,
  },
};
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
