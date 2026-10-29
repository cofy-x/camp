import assert from 'node:assert/strict';
import { createWriteStream } from 'node:fs';
import { cp, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { command, root } from './lib.mjs';
import { repository, validVersion, verifyRelease } from './release.mjs';

const version = process.env.VERSION;
assert.ok(validVersion(version));
const directory = join(root, 'dist');
await mkdir(directory, { recursive: true });
const url = (name) =>
  `https://github.com/${repository}/releases/download/${version}/${name}`;
const metadata = async (name) => {
  const response = await fetch(url(name), {
    signal: AbortSignal.timeout(60000),
  });
  assert.equal(
    response.status,
    200,
    `Public release metadata unavailable: ${name}`,
  );
  const data = Buffer.from(await response.arrayBuffer());
  assert.ok(data.length < 64 << 10);
  await writeFile(join(directory, name), data);
  return JSON.parse(data);
};
const catalog = await metadata('release.json');
assert.equal(catalog.schema, 1);
assert.equal(catalog.version, version);
assert.equal(
  catalog.sourceCommit,
  process.env.GITHUB_SHA,
  'Public release source differs from the reviewed workflow source.',
);
assert.match(catalog.image, /^ghcr\.io\/cofy-x\/camp@sha256:[a-f0-9]{64}$/);
const runner = await metadata('runner-release.json');
const server = await metadata('server-release.json');
for (const [key, expected] of [
  ['installer', 'camp-runner-linux-amd64.tar.gz'],
  ['offline', 'camp-runner-offline-linux-amd64.tar.gz'],
  ['runtime', `camp-runtime-linux-amd64-${runner.runtime?.sha256}.tar.gz`],
  ['server', 'camp-server-linux-amd64.tar.gz'],
]) {
  const artifact = key === 'server' ? server.server : runner[key];
  assert.deepEqual(artifact, catalog[key]);
  assert.equal(artifact.filename, expected);
  assert.match(artifact.sha256, /^[a-f0-9]{64}$/);
  assert.ok(
    Number.isSafeInteger(artifact.bytes) &&
      artifact.bytes > 0 &&
      artifact.bytes < 2 ** 31,
  );
  const response = await fetch(url(artifact.filename), {
    signal: AbortSignal.timeout(30 * 60000),
  });
  assert.equal(
    response.status,
    200,
    `Public release archive unavailable: ${artifact.filename}`,
  );
  let bytes = 0;
  await pipeline(
    Readable.fromWeb(response.body),
    new Transform({
      transform(chunk, encoding, callback) {
        bytes += chunk.length;
        callback(
          bytes > artifact.bytes
            ? new Error('Public asset exceeds its declared size.')
            : null,
          chunk,
        );
      },
    }),
    createWriteStream(join(directory, artifact.filename)),
  );
  assert.equal(bytes, artifact.bytes);
  const checksum = await fetch(url(`${artifact.filename}.sha256`), {
    signal: AbortSignal.timeout(60000),
  });
  assert.equal(checksum.status, 200);
  assert.equal(
    await checksum.text(),
    `${artifact.sha256}  ${artifact.filename}\n`,
  );
  await writeFile(
    join(directory, `${artifact.filename}.sha256`),
    `${artifact.sha256}  ${artifact.filename}\n`,
  );
}
await verifyRelease(directory, version, catalog.sourceCommit);
await command('tar', [
  '-xzf',
  join(directory, catalog.server.filename),
  '-C',
  directory,
]);
await cp(
  join(directory, 'camp-server-linux-amd64/camp'),
  join(directory, 'camp'),
);
console.log(
  `Downloaded and verified public ${version} assets without credentials.`,
);
