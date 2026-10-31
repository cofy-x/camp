import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { chmod, mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { checkpointPattern, validateBinding } from '../contracts.mjs';
import { MacCheckpoints } from '../checkpoint.mjs';

const clone = (value) => JSON.parse(JSON.stringify(value));

const binding = () => ({
  runnerId: 'controlled-runner',
  storageId: 'controlled-storage',
  workspaceId: '1'.repeat(64),
  attemptId: 'attempt',
  operationId: randomUUID(),
  generation: 1,
  digest: '2'.repeat(64),
  parentCheckpoint: null,
  storeId: randomUUID(),
});

test('public Commit IDs retain their type tag and cannot be confused with snapshot digests', () => {
  const value = binding();
  assert.ok(checkpointPattern.test(`12${'a'.repeat(64)}`));
  assert.ok(!checkpointPattern.test('a'.repeat(64)));
  validateBinding({ ...value, parentCheckpoint: `12${'a'.repeat(64)}` });
  assert.throws(
    () => validateBinding({ ...value, parentCheckpoint: 'a'.repeat(64) }),
    { code: 'InvalidInput' },
  );
  for (const field of [
    'runnerId',
    'storageId',
    'workspaceId',
    'attemptId',
    'operationId',
    'digest',
    'storeId',
  ])
    assert.throws(() => validateBinding({ ...value, [field]: '../escape' }), {
      code: 'InvalidInput',
    });
});

test('confirmation binds the current generation, parent, Store, full snapshot and volatile receipt', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-confirm-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const input = binding();
  const coordinator = new MacCheckpoints({
    root,
    worker: { status: { storeId: input.storeId } },
    runnerId: input.runnerId,
    storageId: input.storageId,
  });
  const record = { binding: input };
  const namespace = { generation: 0, head: null, pending: input.operationId };
  const receipt = {
    binding: input,
    state: 'verified',
    verifiedDigest: input.digest,
    disposable: true,
    durableDelivery: false,
    checkpoint: {
      kind: 'Committed',
      head: `12${'a'.repeat(64)}`,
      commit: { parent: null },
    },
  };
  for (const field of [
    'runnerId',
    'storageId',
    'workspaceId',
    'attemptId',
    'operationId',
    'digest',
    'storeId',
    'generation',
    'parentCheckpoint',
  ])
    await assert.rejects(
      coordinator.confirm(clone(record), clone(namespace), {
        ...receipt,
        binding: { ...input, [field]: 'different' },
      }),
      { code: 'Conflict' },
    );
  await assert.rejects(
    coordinator.confirm(clone(record), clone(namespace), {
      ...receipt,
      durableDelivery: true,
    }),
    { code: 'Conflict' },
  );
  await assert.rejects(
    coordinator.confirm(clone(record), clone(namespace), {
      ...receipt,
      checkpoint: {
        ...receipt.checkpoint,
        commit: { parent: `12${'b'.repeat(64)}` },
      },
    }),
    { code: 'Conflict' },
  );
  const confirmed = await coordinator.confirm(record, namespace, receipt);
  assert.equal(confirmed.state, 'confirmed');
  assert.equal(namespace.generation, 1);
  await assert.rejects(coordinator.confirm(record, namespace, receipt), {
    code: 'Conflict',
  });
  await assert.rejects(
    coordinator.checkpoint('/unread', input.workspaceId, input.attemptId),
    { code: 'Busy' },
  );
});
