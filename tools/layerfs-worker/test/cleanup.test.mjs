import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import {
  chmod,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import {
  applyCleanupPlan,
  cancelCleanup,
  captureInventory,
  cleanupStatus,
  createCleanupPlan,
} from '../cleanup.mjs';
import { inspectStorage } from '../retention.mjs';
import { sampleStorage } from '../storage-budget.mjs';
import { saveState } from '../state.mjs';
import { captureTree } from '../tree.mjs';

async function fixture(t) {
  const dataRoot = await mkdtemp(path.join(os.tmpdir(), 'camp-cleanup-'));
  await chmod(dataRoot, 0o700);
  t.after(() => rm(dataRoot, { recursive: true, force: true }));
  const input = {
    dataRoot,
    runnerId: 'controlled-runner',
    storageId: 'controlled-storage',
    journal: { schema: 1, fingerprint: 'a'.repeat(64), pending: 0, intents: 0 },
  };
  const home = path.join(dataRoot, 'layerfs-worker');
  await writeFile(
    path.join(dataRoot, 'daemon.json'),
    'private-config-sentinel',
    { mode: 0o600 },
  );
  await writeFile(
    path.join(dataRoot, 'journal.sqlite'),
    'retained-journal-sentinel',
    { mode: 0o600 },
  );
  const source = path.join(dataRoot, 'source');
  await mkdir(source, { mode: 0o700 });
  await writeFile(path.join(source, 'note.txt'), 'synthetic immutable bytes');
  const capture = async () =>
    captureTree(source, path.join(home, 'checkpoints/captures'));
  return { input, home, capture };
}

async function reference(input, home, capture) {
  const id = randomUUID();
  await saveState(path.join(home, 'checkpoints/operations', id + '.json'), {
    schema: 1,
    state: 'commit-dispatched',
    binding: {
      runnerId: input.runnerId,
      storageId: input.storageId,
      attemptId: 'controlled-attempt',
      workspaceId: 'a'.repeat(64),
      storeId: randomUUID(),
      generation: 1,
      operationId: id,
      digest: capture.digest,
      parentCheckpoint: null,
    },
    captureRoot: capture.root,
    captureRelative: path.relative(home, capture.root),
  });
}

const apply = (input, p, extra = {}) =>
  applyCleanupPlan({ ...input, planId: p.planId, ...extra });
const plan = (input, action, batchId) =>
  createCleanupPlan({ ...input, action, batchId });

test('quarantine protects referenced history, preserves bytes and budget usage, and restores exactly', async (t) => {
  const { input, home, capture } = await fixture(t);
  const protectedCapture = await capture(),
    orphan = await capture();
  await reference(input, home, protectedCapture);
  const before = await captureInventory(orphan.root),
    usage = await sampleStorage(input);
  const p = await plan(input, 'quarantine');
  assert.equal(p.captures, 1);
  assert.equal(p.deletion, false);
  const result = await apply(input, p);
  assert.equal(result.state, 'complete');
  assert.equal((await inspectStorage(input)).retention.protected, 1);
  await assert.rejects(() => readFile(path.join(orphan.root, 'receipt.json')), {
    code: 'ENOENT',
  });
  assert.equal((await sampleStorage(input)).logicalBytes, usage.logicalBytes);
  assert.equal((await apply(input, p)).state, 'complete');
  const restore = await plan(input, 'restore', p.batchId);
  assert.equal((await apply(input, restore)).state, 'complete');
  assert.deepEqual(await captureInventory(orphan.root), before);
  assert.equal(
    await readFile(path.join(input.dataRoot, 'daemon.json'), 'utf8'),
    'private-config-sentinel',
  );
  assert.equal(
    await readFile(path.join(input.dataRoot, 'journal.sqlite'), 'utf8'),
    'retained-journal-sentinel',
  );
});
test('stale journal, new references, recovery intents and pending operations fail closed', async (t) => {
  for (const mode of ['journal', 'reference', 'recovery', 'pending'])
    await t.test(mode, async (t) => {
      const { input, home, capture } = await fixture(t);
      const c = await capture(),
        p = await plan(input, 'quarantine');
      const changed = { ...input };
      if (mode === 'journal')
        changed.journal = { ...input.journal, fingerprint: 'b'.repeat(64) };
      if (mode === 'reference') await reference(input, home, c);
      if (mode === 'recovery') {
        const id = randomUUID();
        await saveState(path.join(home, 'recoveries', id + '.json'), {
          schema: 1,
          recoveryId: id,
        });
      }
      if (mode === 'pending')
        changed.journal = { ...input.journal, pending: 1 };
      await assert.rejects(() => apply(changed, p));
      await readFile(path.join(c.root, 'receipt.json'));
    });
});
test('lost move acknowledgement resumes the same intent without replacing or replaying data', async (t) => {
  const { input, capture } = await fixture(t);
  const c = await capture(),
    before = await captureInventory(c.root),
    p = await plan(input, 'quarantine');
  await assert.rejects(() =>
    applyCleanupPlan({ ...input, planId: p.planId }, (stage) => {
      if (stage === 'moved') throw new Error('injected lost response');
    }),
  );
  assert.equal(
    (await cleanupStatus({ ...input, planId: p.planId })).state,
    'running',
  );
  assert.equal((await apply(input, p)).state, 'complete');
  const undo = await plan(input, 'restore', p.batchId);
  await apply(input, undo);
  assert.deepEqual(await captureInventory(c.root), before);
});
test('partial quarantine can be explicitly cancelled and its moved subset restored', async (t) => {
  const { input, capture } = await fixture(t);
  const first = await capture(),
    second = await capture(),
    p = await plan(input, 'quarantine');
  await assert.rejects(() =>
    applyCleanupPlan({ ...input, planId: p.planId }, (stage) => {
      if (stage === 'moved') throw new Error('injected interruption');
    }),
  );
  await assert.rejects(() => plan(input, 'restore', p.batchId), {
    code: 'OperationPending',
  });
  assert.equal(
    (await cancelCleanup({ ...input, planId: p.planId })).state,
    'cancelled',
  );
  const undo = await plan(input, 'restore', p.batchId);
  assert.equal(undo.captures, 1);
  await apply(input, undo);
  await readFile(path.join(first.root, 'receipt.json'));
  await readFile(path.join(second.root, 'receipt.json'));
  await assert.rejects(() => apply(input, p), { code: 'Conflict' });
});
test('purge requires exact permanent intent and resumes verified remaining files after interruption', async (t) => {
  const { input, capture } = await fixture(t);
  await capture();
  const p = await plan(input, 'quarantine');
  await apply(input, p);
  const purge = await plan(input, 'purge', p.batchId);
  await assert.rejects(() => apply(input, purge), {
    code: 'ConfirmationRequired',
  });
  await assert.rejects(() =>
    applyCleanupPlan(
      { ...input, planId: purge.planId, confirmPermanent: true },
      (stage) => {
        if (stage === 'purged-file') throw new Error('injected after unlink');
      },
    ),
  );
  await assert.rejects(
    () => cancelCleanup({ ...input, planId: purge.planId }),
    { code: 'OperationPending' },
  );
  assert.equal(
    (await apply(input, purge, { confirmPermanent: true })).state,
    'complete',
  );
  assert.equal((await sampleStorage(input)).logicalBytes, 0);
});
test('changed quarantine contents and a newly referenced original prevent permanent deletion', async (t) => {
  for (const mode of ['changed', 'reference'])
    await t.test(mode, async (t) => {
      const { input, home, capture } = await fixture(t);
      const c = await capture(),
        p = await plan(input, 'quarantine');
      await apply(input, p);
      const purge = await plan(input, 'purge', p.batchId);
      if (mode === 'changed')
        await writeFile(
          path.join(
            input.dataRoot,
            'checkpoint-storage/quarantine',
            p.batchId,
            '0',
            'unexpected',
          ),
          'retained',
          { mode: 0o600 },
        );
      else await reference(input, home, c);
      await assert.rejects(() =>
        apply(input, purge, { confirmPermanent: true }),
      );
      await readFile(
        path.join(
          input.dataRoot,
          'checkpoint-storage/quarantine',
          p.batchId,
          '0/receipt.json',
        ),
      );
    });
});
test('quarantine remains restorable after a stopped backup is relocated', async (t) => {
  const { input, capture } = await fixture(t);
  const c = await capture(),
    before = await captureInventory(c.root),
    p = await plan(input, 'quarantine');
  await apply(input, p);
  const backup = await mkdtemp(path.join(os.tmpdir(), 'camp-cleanup-backup-'));
  t.after(() => rm(backup, { recursive: true, force: true }));
  await cp(input.dataRoot, backup, {
    recursive: true,
    preserveTimestamps: true,
  });
  await chmod(backup, 0o700);
  const relocated = { ...input, dataRoot: backup },
    undo = await plan(relocated, 'restore', p.batchId);
  await apply(relocated, undo);
  assert.deepEqual(
    await captureInventory(
      path.join(backup, path.relative(input.dataRoot, c.root)),
    ),
    before,
  );
});

test('an unapplied plan can be cancelled without moving any bytes', async (t) => {
  const { input, capture } = await fixture(t);
  const c = await capture(),
    before = await captureInventory(c.root);
  const p = await plan(input, 'quarantine');
  assert.equal(
    (await cancelCleanup({ ...input, planId: p.planId })).state,
    'cancelled',
  );
  assert.deepEqual(await captureInventory(c.root), before);
  await assert.rejects(() => apply(input, p), { code: 'Conflict' });
});

test('restore repairs missing referenced bytes even while the journal has pending work', async (t) => {
  const { input, home, capture } = await fixture(t);
  const c = await capture(),
    before = await captureInventory(c.root);
  const q = await plan(input, 'quarantine');
  await apply(input, q);
  await reference(input, home, c);
  const pending = {
    ...input,
    journal: { ...input.journal, fingerprint: 'b'.repeat(64), pending: 1 },
  };
  const restore = await plan(pending, 'restore', q.batchId);
  assert.equal((await apply(pending, restore)).state, 'complete');
  assert.deepEqual(await captureInventory(c.root), before);
  assert.equal((await inspectStorage(input)).retention.protected, 1);
});
