import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { setTimeout as pause } from 'node:timers/promises';
import { WorkerClient } from '../client.mjs';
import { captureTree } from '../tree.mjs';
import { ByteReader, encodeCapture } from '../transfer.mjs';
import {
  captureWireBytes,
  readMessage,
  writeBytes,
  writeMessage,
} from '../protocol.mjs';

async function fixture(t, kind) {
  const home = await mkdtemp(path.join(os.tmpdir(), 'camp-export-client-'));
  const source = path.join(home, 'source');
  await mkdir(source);
  await writeFile(
    path.join(source, 'notes.md'),
    'Retained synthetic export bytes.\n',
  );
  const capture = await captureTree(source, home);
  const outgoing = new PassThrough(),
    incoming = new PassThrough();
  const child = new EventEmitter();
  child.stdin = outgoing;
  let killed = 0;
  child.kill = () => {
    killed++;
    incoming.end();
  };
  child.stdin.on('error', () => {});
  const status = {
    storeId: 'test-store',
    image: 'test-image',
    protocol: 1,
    sourceCommit: 'test-sdk',
  };
  let release, transferred, transferFailed;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const bodySent = new Promise((resolve, reject) => {
    transferred = resolve;
    transferFailed = reject;
  });
  bodySent.catch(() => {});
  const client = new WorkerClient({
    home,
    child,
    status,
    timeoutMs: kind === 'timeout' ? 3000 : 5000,
    input: new ByteReader(incoming),
  });
  client.finished = Promise.resolve(kind === 'eof' ? 1 : 0);
  const requests = [];
  const server = (async () => {
    const reader = new ByteReader(outgoing);
    const request = await readMessage(reader);
    requests.push(request);
    assert.equal(request.op, 'export');
    await writeMessage(incoming, {
      id: request.id,
      value: {
        receipt: { binding: { digest: capture.digest } },
        binaryBytes: captureWireBytes(capture),
      },
    });
    for await (const bytes of encodeCapture(capture))
      await writeBytes(incoming, bytes);
    transferred();
    const barrier = await readMessage(reader);
    requests.push(barrier);
    assert.equal(barrier.op, 'status');
    assert.notEqual(barrier.id, request.id);
    await gate;
    if (kind === 'eof') {
      client.diagnostic = 'camp-worker-failure close-dispatched Docker timeout';
      client.closed = true;
      incoming.end();
    } else if (kind === 'timeout') {
      // The existing request deadline must cover release after complete bytes.
    } else {
      await writeMessage(incoming, {
        id: kind === 'wrong-id' ? request.id : barrier.id,
        value:
          kind === 'wrong-store'
            ? { ...status, storeId: 'other-store' }
            : status,
      });
    }
  })();
  server.catch(transferFailed);
  t.after(async () => {
    release();
    outgoing.destroy();
    incoming.destroy();
    await server.catch(() => {});
    await rm(home, { recursive: true, force: true });
  });
  return {
    home,
    client,
    requests,
    release,
    bodySent,
    server,
    killed: () => killed,
  };
}

test('export keeps admission closed until the worker confirms release after all bytes', async (t) => {
  const f = await fixture(t, 'success');
  const destination = path.join(f.home, 'readback');
  let settled = false;
  const operation = f.client
    .export('original-operation', destination)
    .finally(() => {
      settled = true;
    });
  await f.bodySent;
  await pause(30);
  assert.equal(settled, false);
  assert.equal(f.client.busy, true);
  await assert.rejects(f.client.request({ op: 'commit' }), {
    code: 'Unavailable',
  });
  f.release();
  const capture = await operation;
  assert.equal(capture.root, destination);
  assert.equal(f.client.busy, false);
  await f.server;
  assert.deepEqual(
    f.requests.map((row) => row.op),
    ['export', 'status'],
  );
  assert.equal(f.requests[0].args.operationId, 'original-operation');
});

for (const kind of ['eof', 'wrong-id', 'wrong-store', 'timeout'])
  test(`complete export bytes cannot hide ${kind} during release`, async (t) => {
    const f = await fixture(t, kind);
    const destination = path.join(f.home, 'readback');
    const operation = f.client.export('original-operation', destination);
    // Attach before releasing the peer so a fast rejection is always owned.
    const rejected = assert.rejects(operation, { code: 'Unknown' });
    await f.bodySent;
    f.release();
    await rejected;
    assert.equal(f.client.uncertain, true);
    await assert.rejects(f.client.request({ op: 'export' }), {
      code: 'Unavailable',
    });
    assert.equal(f.requests.filter((row) => row.op === 'export').length, 1);
    const names = await readdir(path.join(f.home, 'failures'));
    const record = JSON.parse(
      await readFile(path.join(f.home, 'failures', names[0]), 'utf8'),
    );
    assert.equal(record.state, 'unknown');
    assert.equal(record.retainedCapture, destination);
    assert.equal(record.operationId, 'original-operation');
    assert.ok(
      (await readdir(destination)).length > 0,
      'unconfirmed received bytes must remain retained',
    );
    if (kind === 'timeout') assert.equal(f.killed(), 1);
    if (kind === 'eof') {
      await assert.rejects(f.client.close(), { code: 'Unknown' });
      const records = await Promise.all(
        (await readdir(path.join(f.home, 'failures'))).map(async (name) =>
          JSON.parse(
            await readFile(path.join(f.home, 'failures', name), 'utf8'),
          ),
        ),
      );
      assert.ok(
        records.some(
          (row) =>
            row.phase === 'drain' &&
            row.diagnostic.includes('close-dispatched'),
        ),
      );
    }
  });

test('failure evidence I/O errors cannot turn an uncertain drain into success or a known outcome', async (t) => {
  const f = await fixture(t, 'eof');
  await writeFile(path.join(f.home, 'failures'), 'controlled non-directory');
  const operation = assert.rejects(
    f.client.export('original-operation', path.join(f.home, 'readback')),
    { code: 'Unknown' },
  );
  await f.bodySent;
  f.release();
  await operation;
  await assert.rejects(
    f.client.close(),
    (error) =>
      error.code === 'Unknown' &&
      error.message.includes('evidence could not be saved'),
  );
});
