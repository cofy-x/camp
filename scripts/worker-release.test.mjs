import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { root } from './lib.mjs';
import { verifyWorkerArtifact } from './worker-release.mjs';

test('optional worker artifacts bind source, architecture, exact archive and safe filenames', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-worker-release-'));
  await mkdir(directory, { recursive: true, mode: 0o700 });
  t.after(() => rm(directory, { recursive: true, force: true }));
  const pin = JSON.parse(
    await readFile(join(root, 'tools/layerfs-runtime/pin.json'), 'utf8'),
  );
  const content = new Uint8Array([0, 255, 1]),
    sha256 = createHash('sha256').update(content).digest('hex');
  const descriptor = {
    schema: 1,
    platform: 'linux/arm64',
    sourceCommit: pin.commit,
    version: pin.version,
    image: `sha256:${'1'.repeat(64)}`,
    buildId: '2'.repeat(64),
    campSourceCommit: '3'.repeat(40),
    artifact: {
      sha256,
      bytes: content.length,
      filename: `camp-layerfs-worker-linux-arm64-${sha256}.tar.gz`,
    },
  };
  const file = join(directory, descriptor.artifact.filename),
    metadata = join(directory, 'checkpoint-worker-release.json');
  await writeFile(file, content);
  await writeFile(
    `${file}.sha256`,
    `${sha256}  ${descriptor.artifact.filename}\n`,
  );
  await writeFile(metadata, JSON.stringify(descriptor));
  assert.deepEqual(
    await verifyWorkerArtifact(directory, descriptor.campSourceCommit),
    descriptor,
  );
  await assert.rejects(verifyWorkerArtifact(directory, '4'.repeat(40)));
  await writeFile(
    metadata,
    JSON.stringify({ ...descriptor, platform: 'linux/amd64' }),
  );
  await assert.rejects(verifyWorkerArtifact(directory));
  await writeFile(
    metadata,
    JSON.stringify({
      ...descriptor,
      artifact: { ...descriptor.artifact, filename: '../outside' },
    }),
  );
  await assert.rejects(verifyWorkerArtifact(directory));
  await writeFile(metadata, JSON.stringify(descriptor));
  await writeFile(file, new Uint8Array([1, 255, 0]));
  await assert.rejects(verifyWorkerArtifact(directory));
});
