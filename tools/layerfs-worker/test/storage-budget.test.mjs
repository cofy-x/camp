import assert from 'node:assert/strict';
import {
  chmod,
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
import {
  assessStorage,
  DEFAULT_STORAGE_POLICY,
  parseStorageSize,
  sampleStorage,
  setStoragePolicy,
  storagePolicy,
} from '../storage-budget.mjs';

async function fixture(t) {
  const dataRoot = await mkdtemp(
    path.join(os.tmpdir(), 'camp-storage-budget-'),
  );
  await chmod(dataRoot, 0o700);
  t.after(() => rm(dataRoot, { recursive: true, force: true }));
  return {
    dataRoot,
    runnerId: 'controlled-runner',
    storageId: 'controlled-storage',
  };
}

test('budget policy has one persisted owner, exact units and explicit disabled thresholds', async (t) => {
  const input = await fixture(t);
  assert.deepEqual(await storagePolicy(input), DEFAULT_STORAGE_POLICY);
  await setStoragePolicy({
    ...input,
    retainedBudgetBytes: '20GiB',
    minFreeBytes: '0',
  });
  assert.deepEqual(await storagePolicy(input), {
    retainedBudgetBytes: 20 * 2 ** 30,
    minFreeBytes: 0,
  });
  await assert.rejects(
    () => storagePolicy({ ...input, runnerId: 'other-runner' }),
    { code: 'InvalidIdentity' },
  );
  for (const v of ['-1', '1.5GiB', '1GB', '9007199254740993', {}, null])
    assert.throws(() => parseStorageSize(v));
  assert.equal(parseStorageSize('2MiB'), 2 * 2 ** 20);
});
test('live usage counts quarantine, never reads payloads or follows opaque links', async (t) => {
  const input = await fixture(t);
  const dir = path.join(
    input.dataRoot,
    'checkpoint-storage/quarantine/retained',
  );
  await mkdir(dir, { recursive: true, mode: 0o700 });
  await writeFile(path.join(dir, 'bytes'), 'payload', { mode: 0o000 });
  await symlink('/must/not/be/read', path.join(dir, 'opaque'));
  const sample = await sampleStorage(input);
  assert(sample.logicalBytes >= 7);
  assert.equal(sample.complete, true);
  assert(sample.freeBytes === null || sample.freeBytes > 0);
  await chmod(path.join(dir, 'bytes'), 0o600);
  assert.equal(await readFile(path.join(dir, 'bytes'), 'utf8'), 'payload');
  await setStoragePolicy({ ...input, retainedBudgetBytes: 1, minFreeBytes: 0 });
  const warning = await sampleStorage(input);
  assert.equal(warning.level, 'warning');
  assert(warning.warnings.includes('retained-budget-exceeded'));
});
test('partial observations and unavailable disk samples cannot be healthy zero-cost reports', () => {
  const policy = { retainedBudgetBytes: 100, minFreeBytes: 10 };
  assert.equal(
    assessStorage({ logicalBytes: 80, complete: true, freeBytes: 20, policy })
      .level,
    'notice',
  );
  assert.equal(
    assessStorage({ logicalBytes: 0, complete: false, freeBytes: null, policy })
      .level,
    'notice',
  );
  assert.equal(
    assessStorage({
      logicalBytes: 100,
      complete: false,
      freeBytes: null,
      policy,
    }).level,
    'warning',
  );
  assert.deepEqual(
    assessStorage({ logicalBytes: 0, complete: true, freeBytes: 1, policy })
      .warnings,
    ['disk-free-low'],
  );
});
test('linked policy and aliased administration directories are rejected without repair', async (t) => {
  const input = await fixture(t);
  await setStoragePolicy(input);
  const file = path.join(input.dataRoot, 'checkpoint-storage/policy.json');
  await rm(file);
  await symlink('/must/not/be/read', file);
  await assert.rejects(() => storagePolicy(input));
});
