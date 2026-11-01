// Offline metadata inspection. No worker/client import, content replay or delete.
import { lstat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { storageInventory } from './inventory.mjs';
import path from 'node:path';
import {
  checkpointPattern,
  digestPattern,
  uuidPattern,
  validateBinding,
} from './contracts.mjs';
import { resolveCapture, selectedEpoch } from './references.mjs';
import { failure } from './state.mjs';

const empty = () => ({
  logicalBytes: 0,
  files: 0,
  directories: 0,
  symlinks: 0,
});
const add = (target, value) => {
  for (const key of Object.keys(target)) target[key] += value[key];
};
const witness = (s) => `${s.dev}:${s.ino}:${s.size}:${s.mtimeMs}:${s.ctimeMs}`;

export async function inspectStorage({
  dataRoot,
  runnerId,
  storageId,
  limits = {},
}) {
  const inventory = await storageInventory({ dataRoot, limits });
  ({ dataRoot } = inventory);
  const {
    home,
    recovery,
    trash,
    nodes,
    problems,
    issue,
    readMetadata,
    records,
    controls,
  } = inventory;
  let selected = null;
  if (nodes.has(home)) {
    try {
      selected = await selectedEpoch(home, readMetadata);
    } catch (error) {
      issue(home, error.code ?? 'InvalidEpoch');
    }
  }
  const roots = [home];
  const epochs = nodes.get(path.join(home, 'epochs'));
  if (epochs && !epochs.stat.isDirectory())
    issue(path.join(home, 'epochs'), 'UnsafeMetadataDirectory');
  for (const file of epochs?.children ?? []) {
    if (
      uuidPattern.test(path.basename(file)) &&
      nodes.get(file)?.stat.isDirectory()
    )
      roots.push(file);
    else issue(file, 'UnrecognizedEpoch');
  }
  const operations = new Map(),
    references = new Map(),
    captures = [],
    namespaceRefs = [];
  const critical = [
    home,
    recovery,
    path.join(home, 'epochs'),
    path.join(home, 'recoveries'),
  ];
  for (const root of roots) {
    const base = path.join(root, 'checkpoints');
    for (const file of [
      root,
      base,
      path.join(base, 'captures'),
      path.join(base, 'operations'),
      path.join(base, 'workspaces'),
    ]) {
      critical.push(file);
      const node = nodes.get(file);
      if (node && !node.stat.isDirectory())
        issue(file, 'UnsafeMetadataDirectory');
    }
    for (const directory of nodes.get(path.join(base, 'captures'))?.children ??
      []) {
      if (nodes.get(directory)?.stat.isDirectory()) captures.push(directory);
      else issue(directory, 'UnrecognizedCapture');
    }
    for (const { file, value } of await records(
      path.join(base, 'operations'),
      /^[a-f0-9-]{36}\.json$/,
    )) {
      try {
        validateBinding(value.binding);
        if (
          value.schema !== 1 ||
          ![
            'sealed',
            'prepare-dispatched',
            'upload-dispatched',
            'uploaded',
            'commit-dispatched',
            'confirmed',
          ].includes(value.state) ||
          value.binding.operationId + '.json' !== path.basename(file) ||
          value.binding.runnerId !== runnerId ||
          value.binding.storageId !== storageId ||
          operations.has(value.binding.operationId)
        )
          throw failure(
            'InvalidIdentity',
            'A retained operation has a different owner or identity.',
          );
        const record = resolveCapture(value, home);
        if (!nodes.get(record.captureRoot)?.stat.isDirectory())
          throw failure('MissingCapture', 'A retained capture is unavailable.');
        operations.set(value.binding.operationId, record);
        const refs = references.get(record.captureRoot) ?? [];
        refs.push({
          operationId: value.binding.operationId,
          state: value.state,
        });
        references.set(record.captureRoot, refs);
      } catch (error) {
        issue(file, error.code ?? 'InvalidOperation');
      }
    }
    for (const { file, value } of await records(
      path.join(base, 'workspaces'),
      /^[a-f0-9]{64}\.json$/,
    )) {
      if (
        value.schema !== 1 ||
        !uuidPattern.test(value.storeId) ||
        !Number.isSafeInteger(value.generation) ||
        value.generation < 0 ||
        !(value.head === null || checkpointPattern.test(value.head))
      )
        issue(file, 'InvalidWorkspace');
      for (const id of [value.operationId, value.pending].filter(Boolean)) {
        // References can point into an earlier epoch; resolve after all roots.
        if (!uuidPattern.test(id)) issue(file, 'InvalidOperationReference');
        else
          namespaceRefs.push({
            file,
            id,
            storeId: value.storeId,
            workspaceId: path.basename(file, '.json'),
          });
      }
    }
  }
  for (const ref of namespaceRefs) {
    const record = operations.get(ref.id);
    if (
      !record ||
      record.binding.storeId !== ref.storeId ||
      record.binding.workspaceId !== ref.workspaceId
    )
      issue(ref.file, 'MissingOrMismatchedOperation');
  }
  for (const file of references.keys())
    if (!captures.includes(file)) issue(file, 'UnrecognizedCaptureReference');
  // A recovery intent has independent lineage. Preserve every possible source
  // while this first preview deliberately has no history-expiration policy.
  const recoveries = await records(
    path.join(home, 'recoveries'),
    /^[a-f0-9-]{36}\.json$/,
  );
  for (const { file, value } of recoveries)
    if (value.schema !== 1 || !uuidPattern.test(value.recoveryId))
      issue(file, 'InvalidRecovery');
  if (
    selected?.operationId &&
    (!operations.has(selected.operationId) ||
      operations.get(selected.operationId).binding.storeId !== selected.storeId)
  )
    issue(path.join(home, 'current.json'), 'MissingSelectedOperation');
  const rows = [];
  for (const directory of captures) {
    let sealed = false;
    try {
      const receipt = await readMetadata(path.join(directory, 'receipt.json'));
      sealed =
        receipt?.schema === 1 &&
        receipt.state === 'sealed' &&
        digestPattern.test(receipt.digest);
      for (const ref of references.get(directory) ?? [])
        if (operations.get(ref.operationId).binding.digest !== receipt?.digest)
          issue(directory, 'CaptureDigestMismatch');
    } catch (error) {
      issue(directory, error.code ?? 'InvalidCapture');
    }
    const refs = references.get(directory) ?? [];
    rows.push({
      path: path.relative(dataRoot, directory),
      ...nodes.get(directory).total,
      sealed,
      references: refs,
      disposition: refs.length
        ? 'protected'
        : sealed
          ? 'unreferenced'
          : 'protected',
      reason: refs.length
        ? 'Referenced checkpoint history or pending operation.'
        : sealed
          ? 'No retained operation refers to this sealed capture; inspect before any removal.'
          : 'Unsealed or rejected capture requires recovery inspection.',
    });
  }
  for (const file of new Set(critical)) {
    const node = nodes.get(file);
    if (!node) continue;
    try {
      if (witness(await lstat(file)) !== witness(node.stat))
        issue(file, 'ChangedDuringInspection');
    } catch {
      issue(file, 'ChangedDuringInspection');
    }
  }
  const complete = problems.length === 0;
  const previewAllowed = complete && recoveries.length === 0;
  for (const row of rows)
    if (row.disposition === 'unreferenced' && !previewAllowed) {
      row.disposition = 'blocked';
      row.reason =
        'Incomplete metadata or reconstruction history requires inspection.';
    }
  const total = empty();
  for (const root of [home, recovery, trash])
    if (nodes.has(root)) add(total, nodes.get(root).total);
  const recoveryStats = nodes.get(recovery)?.total ?? empty();
  return {
    schema: 1,
    kind: 'checkpoint-storage-preview',
    recordedAt: new Date().toISOString(),
    complete,
    scope: ['Mac retained worker files', 'native recovery workspaces'],
    totals: {
      ...total,
      allocatedBytes: inventory.allocatedBytes,
    },
    referenceFingerprint: createHash('sha256')
      .update(
        JSON.stringify(
          [...controls.entries()]
            .filter(([name]) => !name.includes('/captures/'))
            .sort(([a], [b]) => a.localeCompare(b)),
        ),
      )
      .digest('hex'),
    quarantined: nodes.get(trash)?.total ?? empty(),
    workerConfigured: nodes.has(home),
    selectedEpoch: selected?.epoch ?? null,
    epochs: roots.filter((r) => nodes.has(r)).length,
    operations: operations.size,
    recoveryIntents: recoveries.length,
    nativeRecovery: {
      count: nodes.get(recovery)?.children.length ?? 0,
      ...recoveryStats,
      disposition: 'protected',
    },
    captures: rows,
    retention: {
      policy: 'preserve-references-v1',
      previewAllowed,
      protected: rows.filter((r) => r.disposition === 'protected').length,
      unreferenced: rows.filter((r) => r.disposition === 'unreferenced').length,
      blocked: rows.filter((r) => r.disposition === 'blocked').length,
      unreferencedLogicalBytes: rows
        .filter((r) => r.disposition === 'unreferenced')
        .reduce((n, r) => n + r.logicalBytes, 0),
      deletionSupported: false,
    },
    workerStore: {
      observed: false,
      bytes: null,
      reason:
        'Linux Store and VM storage are not sampled by this offline Mac inspection.',
    },
    notes: [
      'No files are removed, worker started or model request made.',
      'All referenced history, pending operations, reconstruction lineage and native recovery workspaces are retained.',
      'Unreferenced means metadata-only inspection, not verified file contents or permission to delete.',
      'Logical totals include file entries; allocated blocks deduplicate hardlinks but cannot establish APFS clone sharing or actual reclaimable space.',
      'Stop the owning Runner and native jobs before inspecting a stable inventory.',
    ],
    problems,
  };
}
