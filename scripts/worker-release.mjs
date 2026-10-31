import assert from 'node:assert/strict';
import { lstat, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { hashFile } from './release.mjs';

export async function verifyWorkerArtifact(directory, campSourceCommit) {
  const descriptor = JSON.parse(
    await readFile(join(directory, 'checkpoint-worker-release.json'), 'utf8'),
  );
  const pin = JSON.parse(
    await readFile(join(root, 'tools/layerfs-runtime/pin.json'), 'utf8'),
  );
  assert.equal(descriptor.schema, 1);
  assert.equal(descriptor.platform, 'linux/arm64');
  assert.equal(descriptor.sourceCommit, pin.commit);
  assert.equal(descriptor.version, pin.version);
  assert.match(descriptor.image, /^sha256:[a-f0-9]{64}$/);
  assert.match(descriptor.buildId, /^[a-f0-9]{64}$/);
  assert.match(descriptor.campSourceCommit, /^[a-f0-9]{40}$/);
  if (campSourceCommit)
    assert.equal(descriptor.campSourceCommit, campSourceCommit);
  const artifact = descriptor.artifact;
  assert.match(artifact.sha256, /^[a-f0-9]{64}$/);
  assert.equal(
    artifact.filename,
    `camp-layerfs-worker-linux-arm64-${artifact.sha256}.tar.gz`,
  );
  const file = join(directory, artifact.filename),
    info = await lstat(file);
  assert.ok(info.isFile() && info.size > 0 && info.size <= 2 ** 31);
  assert.equal(info.size, artifact.bytes);
  assert.equal(await hashFile(file), artifact.sha256);
  assert.equal(
    await readFile(`${file}.sha256`, 'utf8'),
    `${artifact.sha256}  ${artifact.filename}\n`,
  );
  return descriptor;
}
