import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { missingBlobs } from '../cache.mjs';

test('parallel cache validation rehashes every operation and preserves requested membership order', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-cache-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const entries = Array.from({ length: 40 }, (_, index) => {
    const bytes = Buffer.from(`blob-${String(index).padStart(2, '0')}`);
    return {
      path: `file-${String(index).padStart(2, '0')}`,
      type: 'file',
      mode: 0o600,
      bytes: bytes.length,
      sha256: createHash('sha256').update(bytes).digest('hex'),
      content: bytes,
    };
  });
  const manifest = {
    schema: 1,
    rootMode: 0o700,
    entries: entries.map((row) => {
      const entry = { ...row };
      delete entry.content;
      return entry;
    }),
  };
  const wanted = entries.map((row) => row.sha256);
  assert.deepEqual(await missingBlobs(root, manifest), wanted);
  await Promise.all(
    entries.map((row) => writeFile(path.join(root, row.sha256), row.content)),
  );
  assert.deepEqual(await missingBlobs(root, manifest), []);
  await writeFile(
    path.join(root, entries[3].sha256),
    Buffer.alloc(entries[3].bytes, 7),
  );
  await rm(path.join(root, entries[11].sha256));
  await writeFile(path.join(root, entries[17].sha256), 'short');
  await rm(path.join(root, entries[29].sha256));
  await symlink(entries[0].sha256, path.join(root, entries[29].sha256));
  const expected = [3, 11, 17, 29].map((index) => entries[index].sha256);
  assert.deepEqual(await missingBlobs(root, manifest), expected);
  assert.deepEqual(await missingBlobs(root, manifest), expected);
});
