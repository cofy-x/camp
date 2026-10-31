import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Readable } from 'node:stream';
import { test } from 'node:test';
import { captureTree, restoreTree } from '../tree.mjs';
import { encodeCapture, receiveCapture } from '../transfer.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-transfer-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source');
  await mkdir(source, { mode: 0o700 });
  await writeFile(path.join(source, 'binary'), Buffer.alloc(700_000, 0xff));
  await writeFile(path.join(source, 'small'), 'text');
  const captured = await captureTree(source, path.join(root, 'captures'));
  return { root, captured };
}

test('raw transfer preserves exact bytes with bounded chunks and actual byte accounting', async (t) => {
  const { root, captured } = await fixture(t);
  const metrics = {},
    chunks = [];
  for await (const chunk of encodeCapture(captured, undefined, metrics))
    chunks.push(chunk);
  assert.ok(chunks.slice(2).every((chunk) => chunk.length <= 256 << 10));
  const wire = Buffer.concat(chunks);
  assert.equal(metrics.wireBytes, wire.length);
  const destination = path.join(root, 'worker');
  const received = await receiveCapture(Readable.from(chunks), destination, {
    digest: captured.digest,
  });
  assert.equal(received.metrics.wireBytes, wire.length);
  const restored = await restoreTree(
    { root: destination, ...received },
    path.join(root, 'restores'),
  );
  assert.deepEqual(
    await readFile(path.join(restored.root, 'binary')),
    Buffer.alloc(700_000, 0xff),
  );
});

test('truncation, corruption, wrong digest and trailing data never become a valid upload', async (t) => {
  const { root, captured } = await fixture(t);
  const chunks = [];
  for await (const chunk of encodeCapture(captured)) chunks.push(chunk);
  const wire = Buffer.concat(chunks);
  for (const size of [0, 3, 50, wire.length - 1])
    await assert.rejects(
      receiveCapture(
        Readable.from([wire.subarray(0, size)]),
        path.join(root, `truncated-${size}`),
        { digest: captured.digest },
      ),
      { code: 'Truncated' },
    );
  const corrupt = Buffer.from(wire);
  corrupt[corrupt.length - 1] ^= 1;
  await assert.rejects(
    receiveCapture(Readable.from([corrupt]), path.join(root, 'corrupt'), {
      digest: captured.digest,
    }),
    { code: 'Corrupt' },
  );
  await assert.rejects(
    receiveCapture(Readable.from([wire]), path.join(root, 'wrong'), {
      digest: '0'.repeat(64),
    }),
    { code: 'Conflict' },
  );
  await assert.rejects(
    receiveCapture(
      Readable.from([wire, Buffer.from('x')]),
      path.join(root, 'trailing'),
      { digest: captured.digest },
    ),
    { code: 'InvalidSnapshot' },
  );
  const huge = Buffer.alloc(4);
  huge.writeUInt32BE(0xffff_ffff);
  await assert.rejects(
    receiveCapture(Readable.from([huge]), path.join(root, 'huge'), {
      digest: captured.digest,
    }),
    { code: 'Capacity' },
  );
  assert.equal(
    await readFile(path.join(captured.root, 'manifest.json'), 'utf8').then(
      () => true,
    ),
    true,
  );
});

test('the sender checks membership and detects changed retained blobs', async (t) => {
  const { captured } = await fixture(t);
  const drain = async (generator) => {
    for await (const chunk of generator) void chunk;
  };
  await assert.rejects(drain(encodeCapture(captured, ['0'.repeat(64)])), {
    code: 'InvalidInput',
  });
  const row = captured.manifest.entries[0];
  await writeFile(
    path.join(captured.root, 'blobs', row.sha256),
    Buffer.alloc(row.bytes, 0),
  );
  await assert.rejects(drain(encodeCapture(captured)), { code: 'Corrupt' });
});
