import assert from 'node:assert/strict';
import {
  chmod,
  link,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { applyTree, neededBlobs } from '../apply.mjs';
import { captureTree, restoreTree, scanTree } from '../tree.mjs';

test('delta apply handles directory removal, type replacement, mode changes and hardlink rebinding', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-apply-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source');
  await mkdir(source, { mode: 0o700 });
  await mkdir(path.join(source, 'directory'));
  await writeFile(path.join(source, 'directory/child'), 'delete');
  await writeFile(path.join(source, 'master'), 'old');
  await link(path.join(source, 'master'), path.join(source, 'second'));
  await writeFile(path.join(source, 'mode'), 'keep', { mode: 0o644 });
  await symlink('master', path.join(source, 'symlink'));
  const initial = await captureTree(source, path.join(root, 'captures'));
  const mirror = await restoreTree(initial, path.join(root, 'restores'));
  await rm(path.join(source, 'directory'), { recursive: true });
  await writeFile(path.join(source, 'directory'), 'now a file');
  await writeFile(path.join(source, 'master'), 'new');
  await rename(path.join(source, 'mode'), path.join(source, 'renamed'));
  await chmod(path.join(source, 'renamed'), 0o755);
  await rm(path.join(source, 'symlink'));
  await symlink('renamed', path.join(source, 'symlink'));
  const next = await captureTree(source, path.join(root, 'captures'));
  const applied = await applyTree(mirror.root, initial.digest, next);
  assert.equal(applied.digest, next.digest);
  assert.deepEqual(
    await readFile(path.join(mirror.root, 'second'), 'utf8'),
    'new',
  );
  assert.equal((await scanTree(mirror.root)).digest, next.digest);
  assert.equal(
    (await applyTree(mirror.root, next.digest, next)).unchanged,
    true,
  );
  await assert.rejects(applyTree(mirror.root, initial.digest, next), {
    code: 'Conflict',
  });
  assert.ok(neededBlobs(initial.manifest, next.manifest).length);
});

test('a failure after an apply effect cannot be treated as a confirmed snapshot', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-apply-fault-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source'),
    destination = path.join(root, 'destination');
  await mkdir(source, { mode: 0o700 });
  await mkdir(destination, { mode: 0o700 });
  const previous = await scanTree(destination);
  await writeFile(path.join(source, 'new'), 'data');
  const next = await captureTree(source, path.join(root, 'captures'));
  await assert.rejects(
    applyTree(destination, previous.digest, next, { failAfter: 1 }),
    { code: 'InjectedApplyFailure' },
  );
  assert.equal(
    await readFile(path.join(next.root, 'receipt.json'), 'utf8').then(
      (text) => JSON.parse(text).state,
    ),
    'sealed',
  );
});
