import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import {
  chmod,
  cp,
  link,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { MacCheckpoints, resolveCapture } from '../checkpoint.mjs';
import { saveState } from '../state.mjs';
import { captureTree } from '../tree.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-publication-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source');
  await mkdir(source, { mode: 0o700 });
  await mkdir(path.join(source, '.git'));
  await writeFile(path.join(source, '.git/HEAD'), 'private git membership');
  await mkdir(path.join(source, 'node_modules'));
  await writeFile(path.join(source, 'node_modules/pkg'), 'private dependency');
  await symlink('pkg', path.join(source, 'node_modules/alias'));
  await writeFile(path.join(source, 'result'), 'frozen result');
  await link(path.join(source, 'result'), path.join(source, 'result-alias'));
  const capture = await captureTree(source, path.join(root, 'captures'));
  const binding = {
    runnerId: 'runner',
    storageId: 'storage',
    workspaceId: '1'.repeat(64),
    attemptId: 'attempt',
    operationId: randomUUID(),
    generation: 1,
    digest: capture.digest,
    parentCheckpoint: null,
    storeId: randomUUID(),
  };
  const manager = new MacCheckpoints({
    root: path.join(root, 'checkpoints'),
    worker: { status: { storeId: binding.storeId } },
    runnerId: 'runner',
    storageId: 'storage',
  });
  await saveState(manager.operationFile(binding.operationId), {
    state: 'confirmed',
    binding,
    captureRoot: capture.root,
    captureRelative: path.relative(root, capture.root),
  });
  await saveState(manager.namespaceFile(binding.workspaceId), {
    storeId: binding.storeId,
    generation: 1,
    operationId: binding.operationId,
    pending: null,
  });
  return { manager, binding, capture, source };
}

test('a stopped private backup resolves captures under its new home and rejects escaping references', async (t) => {
  const { manager, binding, capture } = await fixture(t);
  const moved = await mkdtemp(
    path.join(os.tmpdir(), 'camp-relocated-capture-'),
  );
  t.after(() => rm(moved, { recursive: true, force: true }));
  await cp(manager.captureBase, moved, { recursive: true });
  await rm(capture.root, { recursive: true });
  const restored = new MacCheckpoints({
    root: path.join(moved, 'checkpoints'),
    worker: manager.worker,
    runnerId: 'runner',
    storageId: 'storage',
  });
  const retained = await restored.retained(binding.operationId);
  assert.equal(retained.digest, capture.digest);
  assert.ok(retained.root.startsWith(`${moved}/`));
  const view = await restored.publication(binding.operationId, [
    'node_modules',
  ]);
  assert.equal(
    await readFile(path.join(view.directory, 'result'), 'utf8'),
    'frozen result',
  );
  assert.throws(
    () => resolveCapture({ captureRelative: '../other/capture' }, moved),
    { code: 'Unauthorized' },
  );
  assert.throws(() => resolveCapture({ captureRoot: capture.root }, moved), {
    code: 'RelocatedLegacyCapture',
  });
});

test('publication derives frozen regular bytes, retains the full checkpoint and flattens hardlink aliases', async (t) => {
  const { manager, binding, capture, source } = await fixture(t);
  await writeFile(path.join(source, 'result'), 'later external mutation');
  const view = await manager.publication(binding.operationId, ['node_modules']);
  assert.equal(view.sourceDigest, capture.digest);
  assert.equal(
    await readFile(path.join(view.directory, 'result'), 'utf8'),
    'frozen result',
  );
  assert.equal(
    await readFile(path.join(view.directory, 'result-alias'), 'utf8'),
    'frozen result',
  );
  assert.notEqual(
    (await lstat(path.join(view.directory, 'result'))).ino,
    (await lstat(path.join(view.directory, 'result-alias'))).ino,
  );
  await assert.rejects(lstat(path.join(view.directory, '.git')), {
    code: 'ENOENT',
  });
  await assert.rejects(lstat(path.join(view.directory, 'node_modules')), {
    code: 'ENOENT',
  });
  assert.ok(capture.manifest.entries.some((row) => row.path === '.git/HEAD'));
  assert.ok(capture.manifest.entries.some((row) => row.type === 'hardlink'));
  const current = {
    storeId: binding.storeId,
    generation: 2,
    operationId: randomUUID(),
    pending: null,
  };
  await saveState(manager.namespaceFile(binding.workspaceId), current);
  await assert.rejects(
    manager.publication(binding.operationId, ['node_modules']),
    { code: 'StaleGeneration' },
  );
});

test('a remaining symlink is an explicit publication refusal without discarding the checkpoint', async (t) => {
  const { manager, binding, capture } = await fixture(t);
  await assert.rejects(manager.publication(binding.operationId), {
    code: 'UnsupportedPublication',
  });
  assert.equal(
    JSON.parse(await readFile(path.join(capture.root, 'receipt.json'), 'utf8'))
      .state,
    'sealed',
  );
});
