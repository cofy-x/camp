// One host-owned lifecycle: immutable plans, recoverable quarantine, explicit
// irreversible purge. Server data, journals and worker Stores are never targets.
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import {
  lstat,
  open,
  readdir,
  realpath,
  rename,
  rmdir,
  unlink,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { digestPattern, uuidPattern } from './contracts.mjs';
import { inspectStorage } from './retention.mjs';
import { failure, syncDirectory } from './state.mjs';
import {
  ownedDirectory,
  ownedRecord,
  readControl,
  storageContext,
  writeControl,
} from './storage-state.mjs';

const hash = (value) => createHash('sha256').update(value).digest('hex');
const MAX_BYTES = 16 * 2 ** 30,
  MAX_ENTRIES = 100000,
  MAX_CAPTURES = 128;
const capturePath =
  /^layerfs-worker\/(?:epochs\/[a-f0-9-]{36}\/)?checkpoints\/captures\/capture-[a-f0-9-]{36}$/;
const uuid = (value) => {
  if (!uuidPattern.test(value))
    throw failure('InvalidInput', 'Choose an exact cleanup plan or batch ID.');
  return value;
};
const exists = async (file) => {
  try {
    return await lstat(file);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
};

function originalPath(context, name) {
  if (
    !capturePath.test(name) ||
    !uuidPattern.test(path.basename(name).slice(8)) ||
    (name.includes('/epochs/') && !uuidPattern.test(name.split('/')[2]))
  )
    throw failure(
      'InvalidMetadata',
      'A cleanup target is not a retained capture path.',
    );
  return path.join(context.dataRoot, name);
}

async function directories(context) {
  for (const name of ['plans', 'operations', 'batches', 'quarantine'])
    await ownedDirectory(path.join(context.admin, name), true);
}

async function record(context, kind, id) {
  const names = { plan: 'plans', operation: 'operations', batch: 'batches' };
  await ownedDirectory(path.join(context.admin, names[kind]));
  const value = ownedRecord(
    context,
    await readControl(
      path.join(context.admin, names[kind], uuid(id) + '.json'),
    ),
    'checkpoint-cleanup-' + kind,
  );
  if (value.id !== id)
    throw failure('InvalidIdentity', 'Cleanup metadata has a different ID.');
  return value;
}

async function persist(context, kind, value) {
  const names = { plan: 'plans', operation: 'operations', batch: 'batches' };
  await writeControl(
    path.join(context.admin, names[kind], uuid(value.id) + '.json'),
    value,
  );
}

const envelope = (context, kind, id, body) => ({
  schema: 1,
  kind: 'checkpoint-cleanup-' + kind,
  id,
  runnerId: context.runnerId,
  storageId: context.storageId,
  ...body,
});

function validateJournal(input, action) {
  const j = input.journal;
  if (
    !j ||
    j.schema !== 1 ||
    !digestPattern.test(j.fingerprint) ||
    !Number.isSafeInteger(j.pending) ||
    !Number.isSafeInteger(j.intents) ||
    j.pending < 0 ||
    j.intents < 0
  )
    throw failure(
      'JournalUnavailable',
      'Inspect the existing Runner journal through the Camp CLI before cleanup.',
    );
  if (action !== 'restore' && (j.pending !== 0 || j.intents !== 0))
    throw failure(
      'PendingOperations',
      'Reconcile pending Runner operations and coordination intents before cleanup.',
    );
  return j.fingerprint;
}

async function fileDigest(file, stat) {
  const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const before = await handle.stat();
    if (
      before.ino !== stat.ino ||
      before.dev !== stat.dev ||
      before.size !== stat.size ||
      before.ctimeMs !== stat.ctimeMs
    )
      throw failure(
        'ChangedDuringInspection',
        'A capture changed during verification.',
      );
    const digest = createHash('sha256');
    for await (const bytes of handle.createReadStream({ autoClose: false }))
      digest.update(bytes);
    const after = await handle.stat();
    if (
      before.size !== after.size ||
      before.mtimeMs !== after.mtimeMs ||
      before.ctimeMs !== after.ctimeMs
    )
      throw failure(
        'ChangedDuringInspection',
        'A capture changed during verification.',
      );
    return digest.digest('hex');
  } finally {
    await handle.close();
  }
}

// Hash physical retained bytes only for an explicit cleanup plan/apply. Usage
// observations stay metadata-only. Portable hashes support stopped backups.
export async function captureInventory(root, expected = null, partial = false) {
  const initial = await exists(root);
  if (!initial) {
    if (partial) return [];
    throw failure('MissingCapture', 'Retained capture data is missing.');
  }
  if (!initial.isDirectory())
    throw failure(
      'UnsafeCapture',
      'A retained capture must be a real directory.',
    );
  root = await realpath(root);
  const rows = [],
    known = expected && new Map(expected.map((r) => [r.path, r]));
  let bytes = 0;

  async function visit(file, depth = 0) {
    if (rows.length >= MAX_ENTRIES || depth > 64)
      throw failure(
        'Capacity',
        'Cleanup verification exceeds its bounded inventory.',
      );
    const stat = await lstat(file),
      name = path.relative(root, file);
    if (
      name !== '' &&
      !/^(?:manifest\.json|receipt\.json|blobs(?:\/(?:[a-f0-9]{64}|small-files\.(?:pack|json)))?)$/.test(
        name,
      )
    )
      throw failure(
        'UnsafeCapture',
        'Capture storage contains an unrecognized physical entry.',
      );
    if (
      stat.uid !== process.getuid() ||
      stat.mode & 0o077 ||
      (!stat.isDirectory() && !stat.isFile()) ||
      (stat.isFile() && stat.nlink !== 1)
    )
      throw failure(
        'UnsafeCapture',
        'Capture storage must contain private owned regular files and directories without aliases or external hardlinks.',
      );
    if ((await realpath(path.dirname(file))) !== path.dirname(file))
      throw failure('Unauthorized', 'A capture parent is a filesystem alias.');
    const row = {
      path: name,
      type: stat.isDirectory() ? 'directory' : 'file',
      mode: stat.mode & 0o777,
    };
    if (stat.isFile()) {
      bytes += stat.size;
      if (!Number.isSafeInteger(bytes) || bytes > MAX_BYTES)
        throw failure(
          'Capacity',
          'Cleanup bytes exceed the verification bound.',
        );
      row.bytes = stat.size;
      row.sha256 = await fileDigest(file, stat);
    }
    if (known && JSON.stringify(row) !== JSON.stringify(known.get(name)))
      throw failure(
        'ChangedCapture',
        'Retained bytes or membership differ from the authorized cleanup plan.',
      );
    rows.push(row);
    if (stat.isDirectory()) {
      for (const child of (await readdir(file)).sort())
        await visit(path.join(file, child), depth + 1);
      const after = await lstat(file);
      if (
        stat.ino !== after.ino ||
        stat.mtimeMs !== after.mtimeMs ||
        stat.ctimeMs !== after.ctimeMs
      )
        throw failure(
          'ChangedDuringInspection',
          'A capture namespace changed.',
        );
    }
  }

  await visit(root);
  rows.sort((a, b) => a.path.localeCompare(b.path));
  if (known && !partial && rows.length !== known.size)
    throw failure(
      'ChangedCapture',
      'Capture membership differs from the authorized plan.',
    );
  return rows;
}

function validatePlan(context, plan) {
  const { digest, ...body } = plan;
  if (
    !digestPattern.test(digest) ||
    hash(JSON.stringify(body)) !== digest ||
    !['quarantine', 'restore', 'purge'].includes(plan.action) ||
    !Array.isArray(plan.entries) ||
    plan.entries.length > MAX_CAPTURES ||
    !digestPattern.test(plan.referenceFingerprint) ||
    !digestPattern.test(plan.journalFingerprint)
  )
    throw failure(
      'InvalidMetadata',
      'Cleanup plan is corrupt or incompatible.',
    );
  uuid(plan.batchId);
  const seen = new Set(),
    originals = new Set();
  let totalEntries = 0,
    totalBytes = 0;
  for (const e of plan.entries) {
    originalPath(context, e.original);
    if (
      !/^[0-9]{1,3}$/.test(e.key) ||
      seen.has(e.key) ||
      originals.has(e.original) ||
      !Array.isArray(e.files) ||
      e.files.length > MAX_ENTRIES
    )
      throw failure('InvalidMetadata', 'Cleanup membership is invalid.');
    seen.add(e.key);
    originals.add(e.original);
    const files = new Set();
    totalEntries += e.files.length;
    for (const file of e.files) {
      if (
        typeof file.path !== 'string' ||
        file.path.includes('\\') ||
        file.path.includes('\0') ||
        path.isAbsolute(file.path) ||
        file.path.split('/').some((p) => p === '.' || p === '..') ||
        files.has(file.path) ||
        !['file', 'directory'].includes(file.type) ||
        !Number.isInteger(file.mode) ||
        file.mode < 0 ||
        file.mode > 0o700 ||
        file.mode & 0o077 ||
        (file.type === 'file' &&
          (!Number.isSafeInteger(file.bytes) ||
            file.bytes < 0 ||
            !digestPattern.test(file.sha256)))
      )
        throw failure('InvalidMetadata', 'A cleanup file escapes its capture.');
      files.add(file.path);
      totalBytes += file.bytes ?? 0;
    }
    if (
      !files.has('') ||
      totalEntries > MAX_ENTRIES ||
      !Number.isSafeInteger(totalBytes) ||
      totalBytes > MAX_BYTES
    )
      throw failure('InvalidMetadata', 'Cleanup inventory exceeds its bound.');
  }
  return plan;
}

async function references(input, action) {
  const report = await inspectStorage(input);
  if (
    action !== 'restore' &&
    (!report.complete || !report.retention.previewAllowed)
  )
    throw failure(
      'RecoveryRequired',
      'Incomplete metadata or reconstruction history blocks cleanup.',
    );
  return report;
}

const trashPath = (context, batchId, key) =>
  path.join(context.admin, 'quarantine', uuid(batchId), key);

function summary(plan, operation) {
  return {
    schema: 1,
    kind: 'checkpoint-cleanup-result',
    planId: plan.id,
    batchId: plan.batchId,
    action: plan.action,
    state: operation?.state ?? 'planned',
    captures: plan.entries.length,
    logicalBytes: plan.entries.reduce(
      (n, e) => n + e.files.reduce((s, f) => s + (f.bytes ?? 0), 0),
      0,
    ),
    recoverable: plan.action !== 'purge',
    deletion: plan.action === 'purge',
    note:
      plan.action === 'quarantine'
        ? 'Quarantine retains all bytes and does not reclaim disk space. Use a restore plan to undo it.'
        : plan.action === 'purge'
          ? 'Permanent purge cannot be undone. APFS sharing and actual reclaimed space are not inferred.'
          : 'Restore returns the exact retained capture without overwriting existing work.',
  };
}

export async function createCleanupPlan(input) {
  const context = await storageContext(input, true);
  await directories(context);
  const action = input.action ?? 'quarantine',
    journalFingerprint = validateJournal(input, action);
  if (!['quarantine', 'restore', 'purge'].includes(action))
    throw failure('InvalidInput', 'Choose quarantine, restore or purge.');
  const before = await references(input, action),
    id = randomUUID();
  const entries = [];
  let batchId = id,
    batchVersion = 0;
  if (action === 'quarantine') {
    for (const row of before.captures
      .filter((r) => r.disposition === 'unreferenced')
      .sort((a, b) => a.path.localeCompare(b.path))) {
      if (entries.length === MAX_CAPTURES) break;
      const files = await captureInventory(originalPath(context, row.path));
      if (
        entries.reduce((n, e) => n + e.files.length, 0) + files.length >
        MAX_ENTRIES
      )
        break;
      if (
        entries.reduce(
          (n, e) => n + e.files.reduce((s, f) => s + (f.bytes ?? 0), 0),
          0,
        ) +
          files.reduce((n, f) => n + (f.bytes ?? 0), 0) >
        MAX_BYTES
      )
        break;
      entries.push({ key: String(entries.length), original: row.path, files });
    }
  } else {
    batchId = uuid(input.batchId);
    const batch = await record(context, 'batch', batchId);
    if (batch.activePlanId)
      throw failure(
        'OperationPending',
        'Resume or cancel the active batch operation before creating another plan.',
      );
    batchVersion = batch.version;
    const origin = validatePlan(
      context,
      await record(context, 'plan', batchId),
    );
    for (const e of origin.entries)
      if (batch.locations[e.key] === 'quarantined') {
        const source = originalPath(context, e.original);
        if (await exists(source))
          throw failure(
            'Conflict',
            'The original capture path is occupied; existing work is preserved.',
          );
        await captureInventory(trashPath(context, batchId, e.key), e.files);
        entries.push(e);
      }
  }
  if (!entries.length)
    throw failure(
      'NoCandidates',
      'There are no eligible captures for this cleanup action.',
    );
  const after = await references(input, action);
  if (before.referenceFingerprint !== after.referenceFingerprint)
    throw failure(
      'ChangedDuringInspection',
      'Checkpoint references changed while planning.',
    );
  const body = envelope(context, 'plan', id, {
    action,
    batchId,
    batchVersion,
    createdAt: new Date().toISOString(),
    referenceFingerprint: before.referenceFingerprint,
    journalFingerprint,
    entries,
  });
  const plan = { ...body, digest: hash(JSON.stringify(body)) };
  await persist(context, 'plan', plan);
  return summary(plan);
}

async function removeVerified(root, files, hook) {
  const remaining = await captureInventory(root, files, true);
  for (const file of remaining.filter((r) => r.type === 'file')) {
    const target = path.join(root, file.path);
    if ((await realpath(path.dirname(target))) !== path.dirname(target))
      throw failure('Unauthorized', 'A purge parent became an alias.');
    const stat = await lstat(target);
    if (
      !stat.isFile() ||
      stat.nlink !== 1 ||
      (await fileDigest(target, stat)) !== file.sha256
    )
      throw failure(
        'ChangedCapture',
        'A purge entry changed after verification.',
      );
    await unlink(target);
    await hook?.('purged-file', file.path);
  }
  for (const file of remaining
    .filter((r) => r.type === 'directory')
    .sort((a, b) => b.path.length - a.path.length)) {
    const target = path.join(root, file.path);
    if ((await realpath(target)) !== target)
      throw failure('Unauthorized', 'A purge directory became an alias.');
    await rmdir(target);
  }
}

export async function applyCleanupPlan(input, hook) {
  const context = await storageContext(input, true);
  await directories(context);
  const plan = validatePlan(
    context,
    await record(context, 'plan', uuid(input.planId)),
  );
  let operation = await readControl(
    path.join(context.admin, 'operations', plan.id + '.json'),
  );
  if (operation) {
    ownedRecord(context, operation, 'checkpoint-cleanup-operation');
    if (operation.planDigest !== plan.digest)
      throw failure('Conflict', 'This operation belongs to another plan body.');
    if (operation.state === 'cancelled')
      throw failure(
        'Conflict',
        'This cleanup operation was cancelled. Create a fresh plan.',
      );
    if (operation.state === 'complete') {
      const batch = await record(context, 'batch', plan.batchId);
      if (batch.activePlanId === plan.id) {
        batch.activePlanId = null;
        batch.version++;
        await persist(context, 'batch', batch);
      }
      return summary(plan, operation);
    }
  }
  if (plan.action === 'purge' && input.confirmPermanent !== true)
    throw failure(
      'ConfirmationRequired',
      'Permanent purge requires --confirm-permanent and the exact plan ID.',
    );
  if (validateJournal(input, plan.action) !== plan.journalFingerprint)
    throw failure(
      'StalePlan',
      'Runner journal changed. Create a fresh cleanup plan.',
    );
  const observed = await references(input, plan.action);
  if (observed.referenceFingerprint !== plan.referenceFingerprint)
    throw failure(
      'StalePlan',
      'Checkpoint references changed. Create a fresh plan.',
    );
  let batch = await readControl(
    path.join(context.admin, 'batches', plan.batchId + '.json'),
  );
  if (!batch) {
    if (plan.action !== 'quarantine')
      throw failure('MissingBatch', 'The quarantine batch is unavailable.');
    batch = envelope(context, 'batch', plan.batchId, {
      version: 1,
      activePlanId: plan.id,
      locations: Object.fromEntries(
        plan.entries.map((e) => [e.key, 'original']),
      ),
    });
    await ownedDirectory(
      path.join(context.admin, 'quarantine', plan.batchId),
      true,
    );
    await persist(context, 'batch', batch);
  } else {
    ownedRecord(context, batch, 'checkpoint-cleanup-batch');
    if (batch.activePlanId !== plan.id) {
      if (batch.activePlanId || batch.version !== plan.batchVersion)
        throw failure(
          'StalePlan',
          'This batch changed or has an active operation.',
        );
      batch.activePlanId = plan.id;
      batch.version++;
      await persist(context, 'batch', batch);
    }
  }
  operation ??= envelope(context, 'operation', plan.id, {
    planDigest: plan.digest,
    state: 'running',
  });
  await persist(context, 'operation', operation);
  await hook?.('intent-saved');
  for (const e of plan.entries) {
    const current = await references(input, plan.action);
    if (current.referenceFingerprint !== plan.referenceFingerprint)
      throw failure(
        'StalePlan',
        'Checkpoint references changed during cleanup.',
      );
    const original = originalPath(context, e.original),
      trash = trashPath(context, plan.batchId, e.key);
    if (plan.action === 'quarantine') {
      if (await exists(trash)) {
        if (await exists(original))
          throw failure(
            'Conflict',
            'Both capture locations exist; neither is overwritten.',
          );
        await captureInventory(trash, e.files);
      } else {
        if (
          !current.captures.some(
            (r) => r.path === e.original && r.disposition === 'unreferenced',
          )
        )
          throw failure(
            'ReferencedCapture',
            'This capture is no longer eligible.',
          );
        await captureInventory(original, e.files);
        await rename(original, trash);
        await syncDirectory(path.dirname(original));
        await syncDirectory(path.dirname(trash));
        await hook?.('moved', e.key);
      }
      batch.locations[e.key] = 'quarantined';
    } else if (plan.action === 'restore') {
      if (await exists(original)) {
        if (await exists(trash))
          throw failure(
            'Conflict',
            'The original path is occupied; neither copy is overwritten.',
          );
        await captureInventory(original, e.files);
      } else {
        await ownedDirectory(path.dirname(original));
        await captureInventory(trash, e.files);
        await rename(trash, original);
        await syncDirectory(path.dirname(trash));
        await syncDirectory(path.dirname(original));
        await hook?.('moved', e.key);
      }
      batch.locations[e.key] = 'restored';
    } else {
      if (await exists(original))
        throw failure(
          'ReferencedCapture',
          'An original capture path reappeared; purge is blocked.',
        );
      if (
        batch.locations[e.key] !== 'purging' &&
        batch.locations[e.key] !== 'purged'
      ) {
        await captureInventory(trash, e.files);
        batch.locations[e.key] = 'purging';
        batch.version++;
        await persist(context, 'batch', batch);
      }
      if (batch.locations[e.key] !== 'purged') {
        await removeVerified(trash, e.files, hook);
        await syncDirectory(path.dirname(trash));
        await hook?.('purged-capture', e.key);
      }
      batch.locations[e.key] = 'purged';
    }
    batch.version++;
    await persist(context, 'batch', batch);
    await hook?.('entry-saved', e.key);
  }
  operation.state = 'complete';
  await persist(context, 'operation', operation);
  batch.activePlanId = null;
  batch.version++;
  await persist(context, 'batch', batch);
  return summary(plan, operation);
}

export async function cleanupStatus(input) {
  const context = await storageContext(input),
    plan = validatePlan(
      context,
      await record(context, 'plan', uuid(input.planId)),
    );
  const op = await readControl(
    path.join(context.admin, 'operations', plan.id + '.json'),
  );
  if (op) ownedRecord(context, op, 'checkpoint-cleanup-operation');
  return summary(plan, op);
}

export async function cancelCleanup(input) {
  const context = await storageContext(input, true),
    plan = validatePlan(
      context,
      await record(context, 'plan', uuid(input.planId)),
    );
  if (plan.action === 'purge')
    throw failure(
      'OperationPending',
      'A purge must be resumed with its original plan; partially removed bytes cannot be restored.',
    );
  const savedBatch = await readControl(
    path.join(context.admin, 'batches', plan.batchId + '.json'),
  );
  const savedOperation = await readControl(
    path.join(context.admin, 'operations', plan.id + '.json'),
  );
  const operation =
    savedOperation ??
    envelope(context, 'operation', plan.id, {
      planDigest: plan.digest,
      state: 'running',
    });
  ownedRecord(context, operation, 'checkpoint-cleanup-operation');
  if (!savedBatch) {
    if (plan.action !== 'quarantine')
      throw failure('MissingBatch', 'The quarantine batch is unavailable.');
    operation.state = 'cancelled';
    await persist(context, 'operation', operation);
    return summary(plan, operation);
  }
  const batch = ownedRecord(context, savedBatch, 'checkpoint-cleanup-batch');
  if (!savedOperation && batch.activePlanId !== plan.id) {
    operation.state = 'cancelled';
    await persist(context, 'operation', operation);
    return summary(plan, operation);
  }
  if (operation.state === 'complete') {
    if (batch.activePlanId === plan.id) {
      batch.activePlanId = null;
      batch.version++;
      await persist(context, 'batch', batch);
    }
    return summary(plan, operation);
  }
  if (operation.state === 'cancelled' && batch.activePlanId !== plan.id)
    return summary(plan, operation);
  if (batch.activePlanId !== plan.id)
    throw failure('Conflict', 'Another operation owns this batch.');
  for (const e of plan.entries) {
    const original = originalPath(context, e.original),
      trash = trashPath(context, plan.batchId, e.key),
      hasOriginal = await exists(original),
      hasTrash = await exists(trash);
    if (Boolean(hasOriginal) === Boolean(hasTrash))
      throw failure(
        'UnknownOutcome',
        'Capture locations require inspection before cancellation.',
      );
    if (hasTrash) {
      await captureInventory(trash, e.files);
      batch.locations[e.key] = 'quarantined';
    } else
      batch.locations[e.key] =
        plan.action === 'restore' ? 'restored' : 'original';
  }
  operation.state = 'cancelled';
  await persist(context, 'operation', operation);
  batch.activePlanId = null;
  batch.version++;
  await persist(context, 'batch', batch);
  return summary(plan, operation);
}
