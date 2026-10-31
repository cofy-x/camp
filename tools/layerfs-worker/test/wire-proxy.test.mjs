import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmod, mkdtemp, rm } from 'node:fs/promises';
import { createServer, connect } from 'node:net';
import path from 'node:path';
import process from 'node:process';
import { test } from 'node:test';
import { setTimeout as pause } from 'node:timers/promises';

test('the byte counter preserves half-close and drains a large response before closing', async (t) => {
  const directory = await mkdtemp('/tmp/camp-wire-test-');
  await chmod(directory, 0o700);
  t.after(() => rm(directory, { recursive: true, force: true }));
  const payload = Buffer.alloc(32 << 20, 0xa7),
    socket = path.join(directory, 'source.sock');
  const server = createServer({ allowHalfOpen: true }, (peer) => {
    peer.resume();
    peer.on('end', () => peer.end(payload));
  });
  await new Promise((resolve) => server.listen(socket, resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const proxy = spawn(
    process.execPath,
    [path.join(import.meta.dirname, '../wire-proxy.mjs'), socket],
    { stdio: ['ignore', 'ignore', 'inherit', 'ipc'] },
  );
  const ended = new Promise((resolve) => proxy.once('close', resolve));
  t.after(() => {
    if (proxy.exitCode === null) proxy.kill('SIGTERM');
  });
  const ready = await new Promise((resolve) => proxy.once('message', resolve));
  const front = connect(ready.socket);
  front.end('one request');
  front.pause();
  await pause(50);
  const hash = createHash('sha256');
  let bytes = 0;
  for await (const chunk of front) {
    hash.update(chunk);
    bytes += chunk.length;
  }
  assert.equal(bytes, payload.length);
  assert.equal(
    hash.digest('hex'),
    createHash('sha256').update(payload).digest('hex'),
  );
  const stats = new Promise((resolve) => proxy.once('message', resolve));
  proxy.send({ action: 'stats' });
  assert.equal((await stats).received, payload.length);
  proxy.send({ action: 'close' });
  assert.equal(await ended, 0);
});
