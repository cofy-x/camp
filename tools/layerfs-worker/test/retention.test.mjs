import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import {
  chmod,
  cp,
  lstat,
  mkdir,
  mkdtemp,
  open,
  readdir,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { inspectStorage } from '../retention.mjs';
import { captureTree } from '../tree.mjs';
import { saveState } from '../state.mjs';

async function fixture(t) {
  const dataRoot = await mkdtemp(path.join(os.tmpdir(), 'camp-retention-'));
  await chmod(dataRoot, 0o700);
  t.after(() => rm(dataRoot, { recursive: true, force: true }));
  const config = {
    dataRoot,
    runnerId: 'controlled-runner',
    storageId: 'controlled-storage',
  };
  await writeFile(
    path.join(dataRoot, 'daemon.json'),
    'private-credential-sentinel',
    { mode: 0o600 },
  );
  await writeFile(
    path.join(dataRoot, 'journal.sqlite'),
    'retained-journal-sentinel',
    { mode: 0o600 },
  );
  return { config, home: path.join(dataRoot, 'layerfs-worker') };
}

async function snapshot(root) {
  const rows = [];

  async function visit(file) {
    const stat = await lstat(file);
    rows.push([
      path.relative(root, file),
      stat.mode,
      stat.size,
      stat.mtimeMs,
      stat.isFile() && stat.size < 1 << 20
        ? createHash('sha256')
            .update(await readFile(file))
            .digest('hex')
        : null,
    ]);
    if (stat.isDirectory())
      for (const name of await readdir(file))
        await visit(path.join(file, name));
  }

  await visit(root);
  return rows.sort((a, b) => a[0].localeCompare(b[0]));
}

async function captured(home, epoch = '') {
  const source = await mkdtemp(path.join(path.dirname(home), 'source-'));
  await writeFile(path.join(source, 'sample.txt'), 'synthetic frozen data');
  return captureTree(source, path.join(home, epoch, 'checkpoints/captures'));
}

async function operation(
  config,
  home,
  capture,
  {
    epoch = '',
    state = 'confirmed',
    relative = true,
    storeId = randomUUID(),
  } = {},
) {
  const binding = {
    runnerId: config.runnerId,
    storageId: config.storageId,
    attemptId: 'controlled-attempt',
    workspaceId: 'a'.repeat(64),
    storeId,
    generation: 1,
    operationId: randomUUID(),
    digest: capture.digest,
    parentCheckpoint: null,
  };
  const value = {
    schema: 1,
    binding,
    kind: 'capture',
    state,
    captureRoot: capture.root,
    ...(relative ? { captureRelative: path.relative(home, capture.root) } : {}),
  };
  const root = path.join(home, epoch, 'checkpoints');
  await saveState(
    path.join(root, 'operations', binding.operationId + '.json'),
    value,
  );
  await saveState(
    path.join(root, 'workspaces', binding.workspaceId + '.json'),
    {
      schema: 1,
      storeId,
      generation: 1,
      head: '12' + 'b'.repeat(64),
      operationId: binding.operationId,
      pending: state === 'confirmed' ? null : binding.operationId,
    },
  );
  return value;
}

test('native-local recovery inspection is offline, bounded and does not read retained file contents', async (t) => {
  const { config } = await fixture(t);
  const recovery = path.join(
    config.dataRoot,
    'native-recovery/checkpoint-fixture',
  );
  await mkdir(recovery, { recursive: true, mode: 0o700 });
  const file = await open(
    path.join(recovery, 'sparse-retained-file'),
    'wx',
    0o000,
  );
  await file.truncate(1 << 30);
  await file.close();
  await symlink('/must/not/be/followed', path.join(recovery, 'opaque-link'));
  const before = await snapshot(config.dataRoot);
  const r = await inspectStorage(config);
  assert.equal(r.complete, true);
  assert.equal(r.workerConfigured, false);
  assert.equal(r.nativeRecovery.count, 1);
  assert.equal(r.nativeRecovery.disposition, 'protected');
  assert(r.totals.logicalBytes >= 1 << 30);
  assert.equal(r.totals.symlinks, 1);
  assert.equal(r.workerStore.observed, false);
  assert.equal(r.workerStore.bytes, null);
  assert.equal(r.retention.deletionSupported, false);
  assert.deepEqual(await snapshot(config.dataRoot), before);
  assert(!JSON.stringify(r).includes('private-credential-sentinel'));
});

test('preview protects referenced and uncertain history while identifying only unreferenced sealed captures', async (t) => {
  const { config, home } = await fixture(t);
  const retained = await captured(home),
    orphan = await captured(home);
  await operation(config, home, retained, { state: 'commit-dispatched' });
  const before = await snapshot(config.dataRoot);
  const r = await inspectStorage(config);
  assert.equal(r.complete, true);
  assert.equal(r.retention.protected, 1);
  assert.equal(r.retention.unreferenced, 1);
  const candidate = r.captures.find((v) =>
    v.path.endsWith(path.basename(orphan.root)),
  );
  assert.equal(candidate.disposition, 'unreferenced');
  assert(candidate.logicalBytes > 0);
  assert.equal(r.retention.unreferencedLogicalBytes, candidate.logicalBytes);
  assert.deepEqual(await snapshot(config.dataRoot), before);
});

test('epochs and relative capture references remain protected after a stopped backup relocation', async (t) => {
  const { config, home } = await fixture(t);
  const capture = await captured(home);
  await operation(config, home, capture);
  const epoch = randomUUID(),
    storeId = randomUUID();
  const current = await operation(config, home, capture, {
    epoch: 'epochs/' + epoch,
    storeId,
  });
  await saveState(path.join(home, 'current.json'), {
    schema: 1,
    epoch,
    storeId,
    operationId: current.binding.operationId,
  });
  const backup = await mkdtemp(
    path.join(os.tmpdir(), 'camp-retention-backup-'),
  );
  t.after(() => rm(backup, { recursive: true, force: true }));
  await cp(config.dataRoot, backup, {
    recursive: true,
    preserveTimestamps: true,
  });
  await chmod(backup, 0o700);
  const before = await snapshot(backup);
  const r = await inspectStorage({ ...config, dataRoot: backup });
  assert.equal(r.complete, true);
  assert.equal(r.epochs, 2);
  assert.equal(r.selectedEpoch, epoch);
  assert.equal(r.captures.length, 1);
  assert.equal(r.captures[0].references.length, 2);
  assert.equal(r.retention.protected, 1);
  assert.equal(r.retention.unreferenced, 0);
  assert.deepEqual(await snapshot(backup), before);
  assert(!JSON.stringify(r).includes(config.dataRoot));
});

test('reconstruction history blocks expiration decisions without hiding native disk observations', async (t) => {
  const { config, home } = await fixture(t);
  await captured(home);
  const recoveryId = randomUUID();
  await saveState(path.join(home, 'recoveries', recoveryId + '.json'), {
    schema: 1,
    recoveryId,
    state: 'reconstruction-dispatched',
  });
  const r = await inspectStorage(config);
  assert.equal(r.complete, true);
  assert(r.totals.logicalBytes > 0);
  assert.equal(r.recoveryIntents, 1);
  assert.equal(r.retention.previewAllowed, false);
  assert.equal(r.retention.unreferenced, 0);
  assert.equal(r.retention.blocked, 1);
});

test('corrupt, linked, foreign or missing metadata blocks every unreferenced candidate', async (t) => {
  for (const mode of [
    'corrupt',
    'linked',
    'foreign',
    'missing-operation',
    'capacity',
  ]) {
    await t.test(mode, async (t) => {
      const { config, home } = await fixture(t);
      const capture = await captured(home);
      await captured(home);
      const record = await operation(config, home, capture);
      const file = path.join(
        home,
        'checkpoints/operations',
        record.binding.operationId + '.json',
      );
      if (mode === 'corrupt') await writeFile(file, 'broken JSON');
      if (mode === 'linked') {
        await rm(file);
        await symlink(path.join(config.dataRoot, 'daemon.json'), file);
      }
      if (mode === 'foreign') {
        record.binding.runnerId = 'different-runner';
        await saveState(file, record);
      }
      if (mode === 'missing-operation') await rm(file);
      const before = await snapshot(config.dataRoot);
      const r = await inspectStorage({
        ...config,
        ...(mode === 'capacity' ? { limits: { maxEntries: 2 } } : {}),
      });
      assert.equal(r.complete, false);
      assert.equal(r.retention.previewAllowed, false);
      assert.equal(r.retention.unreferenced, 0);
      assert(r.problems.length > 0);
      assert.deepEqual(await snapshot(config.dataRoot), before);
    });
  }
});
