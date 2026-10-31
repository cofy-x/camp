import { createHash, randomUUID } from 'node:crypto';
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
  installed.platform !== 'linux/arm64' ||
  !/^sha256:[a-f0-9]{64}$/.test(installed.image)
)
  throw new Error(
    'Build the pinned native ARM64 checkpoint worker before packaging.',
  );
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
