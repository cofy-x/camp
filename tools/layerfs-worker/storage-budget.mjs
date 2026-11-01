import { statfs } from 'node:fs/promises';
import path from 'node:path';
import { storageInventory } from './inventory.mjs';
import { failure } from './state.mjs';
import {
  ownedRecord,
  readControl,
  storageContext,
  writeControl,
} from './storage-state.mjs';

export const DEFAULT_STORAGE_POLICY = Object.freeze({
  retainedBudgetBytes: 10 * 2 ** 30,
  minFreeBytes: 2 * 2 ** 30,
});

export function parseStorageSize(value) {
  if (Number.isSafeInteger(value) && value >= 0) return value;
  const match =
    typeof value === 'string' &&
    /^(0|[1-9][0-9]*)(B|KiB|MiB|GiB|TiB)?$/.exec(value);
  const bytes = match
    ? Number(match[1]) *
      { B: 1, KiB: 2 ** 10, MiB: 2 ** 20, GiB: 2 ** 30, TiB: 2 ** 40 }[
        match[2] ?? 'B'
      ]
    : NaN;
  if (!Number.isSafeInteger(bytes) || bytes < 0)
    throw failure(
      'InvalidInput',
      'Choose an integer byte size with B, KiB, MiB, GiB or TiB units.',
    );
  return bytes;
}

export async function storagePolicy(input) {
  const context = await storageContext(input);
  const saved = await readControl(
    path.join(context.admin, 'policy.json'),
    16384,
  );
  const policy = saved
    ? ownedRecord(context, saved, 'checkpoint-storage-policy')
    : DEFAULT_STORAGE_POLICY;
  if (
    ![policy.retainedBudgetBytes, policy.minFreeBytes].every(
      (v) => Number.isSafeInteger(v) && v >= 0,
    )
  )
    throw failure(
      'InvalidMetadata',
      'Storage budgets must be nonnegative integer byte counts.',
    );
  return {
    retainedBudgetBytes: policy.retainedBudgetBytes,
    minFreeBytes: policy.minFreeBytes,
  };
}

export async function setStoragePolicy(input) {
  const previous = await storagePolicy(input);
  const next = { ...previous };
  for (const key of ['retainedBudgetBytes', 'minFreeBytes'])
    if (input[key] !== undefined) next[key] = parseStorageSize(input[key]);
  const context = await storageContext(input, true);
  const value = {
    schema: 1,
    kind: 'checkpoint-storage-policy',
    runnerId: context.runnerId,
    storageId: context.storageId,
    ...next,
  };
  await writeControl(path.join(context.admin, 'policy.json'), value);
  return value;
}

export function assessStorage({ logicalBytes, complete, freeBytes, policy }) {
  const warnings = [];
  if (
    policy.retainedBudgetBytes > 0 &&
    logicalBytes >= policy.retainedBudgetBytes
  )
    warnings.push('retained-budget-exceeded');
  else if (
    policy.retainedBudgetBytes > 0 &&
    logicalBytes >= policy.retainedBudgetBytes * 0.8
  )
    warnings.push('retained-budget-near');
  if (
    freeBytes !== null &&
    policy.minFreeBytes > 0 &&
    freeBytes < policy.minFreeBytes
  )
    warnings.push('disk-free-low');
  if (!complete || freeBytes === null) warnings.push('observation-incomplete');
  return {
    level: warnings.some(
      (v) => v === 'retained-budget-exceeded' || v === 'disk-free-low',
    )
      ? 'warning'
      : warnings.length
        ? 'notice'
        : 'ok',
    warnings,
  };
}

export async function sampleStorage(input) {
  const policy = await storagePolicy(input);
  const inventory = await storageInventory({
    ...input,
    limits: { maxEntries: 20000, maxMetadataBytes: 16384 },
    deadlineMs: 1500,
  });
  const logicalBytes = [
    inventory.home,
    inventory.recovery,
    inventory.trash,
  ].reduce((n, r) => n + (inventory.nodes.get(r)?.total.logicalBytes ?? 0), 0);
  return observeBudget({
    dataRoot: inventory.dataRoot,
    logicalBytes,
    complete: inventory.problems.length === 0,
    policy,
  });
}

export async function observeBudget({
  dataRoot,
  logicalBytes,
  complete,
  policy,
}) {
  let freeBytes = null;
  try {
    const fs = await statfs(dataRoot);
    const bytes = fs.bavail * fs.bsize;
    if (Number.isSafeInteger(bytes) && bytes >= 0) freeBytes = bytes;
  } catch {
    /* An unavailable filesystem observation is not zero free space. */
  }
  return {
    schema: 1,
    observedAt: new Date().toISOString(),
    logicalBytes,
    complete,
    freeBytes,
    ...policy,
    ...assessStorage({ logicalBytes, complete, freeBytes, policy }),
  };
}
