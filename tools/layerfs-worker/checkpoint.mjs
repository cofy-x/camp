import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp } from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure, readState, saveState } from './state.mjs';
import {
  captureTree,
  loadCapture,
  restoreTree,
  manifestDigest,
} from './tree.mjs';
import { checkpointPattern, validateBinding } from './contracts.mjs';

export function resolveCapture(record, base) {
  if (!record) return record;
  if (record.captureRelative !== undefined) {
    const name = record.captureRelative;
    if (
      typeof name !== 'string' ||
      !name ||
      path.isAbsolute(name) ||
      name.includes('\\') ||
      name.includes('\0') ||
      name.split('/').some((part) => !part || part === '.' || part === '..')
    )
      throw failure(
        'Unauthorized',
        'The retained capture reference escapes its owning home.',
      );
    return { ...record, captureRoot: path.join(base, name) };
  }
  const relative = path.relative(base, record.captureRoot);
  if (
    !relative ||
    relative === '..' ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  )
    throw failure(
      'RelocatedLegacyCapture',
      'An absolute-only capture receipt is retained for explicit inspection; its relocated path is not guessed.',
    );
  return record;
}

export class MacCheckpoints {
  constructor({
    root,
    worker,
    runnerId,
    storageId,
    captureBase = path.dirname(path.resolve(root)),
  }) {
    Object.assign(this, {
      root: path.resolve(root),
      worker,
      runnerId,
      storageId,
      captureBase: path.resolve(captureBase),
    });
  }
  namespaceFile(id) {
    if (!/^[a-f0-9]{64}$/.test(id))
      throw failure(
        'InvalidInput',
        'Invalid native checkpoint workspace identity.',
      );
    return path.join(this.root, 'workspaces', `${id}.json`);
  }
  operationFile(id) {
    if (!/^[a-f0-9-]{36}$/.test(id))
      throw failure(
        'InvalidInput',
        'Invalid native checkpoint operation identity.',
      );
    return path.join(this.root, 'operations', `${id}.json`);
  }
  async operation(id) {
    return resolveCapture(
      await readState(this.operationFile(id)),
      this.captureBase,
    );
  }
  async checkpoint(
    directory,
    workspaceId,
    attemptId,
    { quiescent = false } = {},
  ) {
    if (!quiescent)
      throw failure(
        'Busy',
        'Seal tools and confirm all native descendants ended before checkpoint capture.',
      );
    const file = this.namespaceFile(workspaceId);
    const previous = await readState(file);
    if (previous?.pending)
      throw failure(
        'Unknown',
        'A previous checkpoint remains uncertain; observe it without replaying tools or mutations.',
      );
    if (previous && previous.storeId !== this.worker.status.storeId)
      throw failure(
        'LostStore',
        'The Store incarnation changed; retain the previous capture and recover under an explicitly new operation.',
      );
    const capture = await captureTree(
      directory,
      path.join(this.root, 'captures'),
    );
    return this.submitCapture(capture, workspaceId, attemptId);
  }
  async submitCapture(capture, workspaceId, attemptId, { recoveryFrom } = {}) {
    const file = this.namespaceFile(workspaceId);
    const previous = await readState(file);
    if (previous?.pending)
      throw failure(
        'Unknown',
        'The prior operation is uncertain; no mutation is replayed.',
      );
    if (previous && previous.storeId !== this.worker.status.storeId)
      throw failure(
        'LostStore',
        'Explicit reconstruction must select a new Store epoch and workspace.',
      );
    const binding = {
      runnerId: this.runnerId,
      storageId: this.storageId,
      workspaceId,
      attemptId,
      operationId: randomUUID(),
      generation: (previous?.generation ?? 0) + 1,
      digest: capture.digest,
      parentCheckpoint: previous?.head ?? null,
      storeId: this.worker.status.storeId,
    };
    if (recoveryFrom) binding.recoveryFrom = recoveryFrom;
    const frozenBinding = validateBinding(binding);
    const record = {
      schema: 1,
      binding: frozenBinding,
      kind: recoveryFrom ? 'reconstruction' : 'capture',
      captureRoot: capture.root,
      captureRelative: path.relative(this.captureBase, capture.root),
      state: 'sealed',
      captureMetrics: capture.metrics,
      workerImage: this.worker.installed.image,
      durableDelivery: false,
    };
    resolveCapture(record, this.captureBase);
    const namespace = {
      schema: 1,
      storeId: binding.storeId,
      generation: previous?.generation ?? 0,
      head: previous?.head ?? null,
      pending: binding.operationId,
    };
    await saveState(this.operationFile(binding.operationId), record);
    await saveState(file, namespace);
    const transition = async (state) => {
      record.state = state;
      await saveState(this.operationFile(binding.operationId), record);
    };
    try {
      await transition('prepare-dispatched');
      const prepared = await this.worker.request({
        op: 'prepare',
        args: { binding: frozenBinding, manifest: capture.manifest },
      });
      record.cacheValidationMs = prepared.cacheValidationMs;
      await transition('upload-dispatched');
      const wire = {};
      const started = performance.now();
      const uploaded = await this.worker.upload(
        binding.operationId,
        capture,
        prepared.wanted,
        wire,
      );
      record.transferMs = performance.now() - started;
      record.wire = wire;
      record.receive = uploaded.transfer;
      await transition('uploaded');
      await transition('commit-dispatched');
      const workerReceipt = await this.worker.request({
        op: 'commit',
        args: { operationId: binding.operationId },
      });
      return await this.confirm(record, namespace, workerReceipt);
    } catch (error) {
      record.error = { code: error.code ?? 'Unknown', message: error.message };
      await saveState(this.operationFile(binding.operationId), record);
      error.operationId = binding.operationId;
      error.capture = capture.root;
      throw error;
    }
  }
  async confirm(record, namespace, workerReceipt) {
    validateBinding(record.binding);
    const acknowledged = workerReceipt.checkpoint;
    const checkpointValid =
      acknowledged?.kind === 'Committed'
        ? checkpointPattern.test(acknowledged.head) &&
          acknowledged.commit?.parent === record.binding.parentCheckpoint
        : acknowledged?.kind === 'UpToDate' &&
          acknowledged.head === namespace.head;
    if (
      !checkpointValid ||
      workerReceipt.disposable !== true ||
      workerReceipt.durableDelivery !== false ||
      workerReceipt.state !== 'verified' ||
      workerReceipt.verifiedDigest !== record.binding.digest ||
      JSON.stringify(workerReceipt.binding) !==
        JSON.stringify(record.binding) ||
      record.binding.storeId !== this.worker.status.storeId ||
      namespace.pending !== record.binding.operationId ||
      namespace.generation + 1 !== record.binding.generation ||
      namespace.head !== record.binding.parentCheckpoint
    )
      throw failure(
        'Conflict',
        'Worker receipt does not match the current sealed operation, Store and generation.',
      );
    record.workerReceipt = workerReceipt;
    record.state = 'confirmed';
    await saveState(this.operationFile(record.binding.operationId), record);
    namespace.generation = record.binding.generation;
    namespace.head = workerReceipt.checkpoint.head;
    namespace.pending = null;
    namespace.operationId = record.binding.operationId;
    await saveState(this.namespaceFile(record.binding.workspaceId), namespace);
    return record;
  }
  async observe(operationId) {
    const record = await this.operation(operationId);
    if (!record)
      throw failure('NotFound', 'The native checkpoint intent is unavailable.');
    const namespace = await readState(
      this.namespaceFile(record.binding.workspaceId),
    );
    if (record.state === 'confirmed') {
      const observed = await this.worker.request({
        op: 'observe',
        args: { operationId },
      });
      if (
        observed.state !== 'verified' ||
        observed.verifiedDigest !== record.binding.digest ||
        JSON.stringify(observed.binding) !== JSON.stringify(record.binding) ||
        observed.checkpoint.head !== record.workerReceipt.checkpoint.head
      )
        throw failure(
          'LostStore',
          'The retained receipt no longer has a matching observed worker checkpoint.',
        );
      if (namespace.pending === operationId)
        return this.confirm(record, namespace, observed);
      return record;
    }
    const workerReceipt = await this.worker.request({
      op: 'observe',
      args: { operationId },
    });
    if (workerReceipt.state === 'verified')
      return this.confirm(record, namespace, workerReceipt);
    return { ...record, observedWorkerState: workerReceipt.state };
  }
  async restore(operationId, parent) {
    const record = await this.observe(operationId);
    if (record.state !== 'confirmed')
      throw failure(
        'Unknown',
        'An uncertain checkpoint cannot be restored as a confirmed result.',
      );
    await mkdir(path.join(this.root, 'downloads'), {
      recursive: true,
      mode: 0o700,
    });
    const destination = await mkdtemp(
      path.join(this.root, 'downloads', 'readback-'),
    );
    const capture = await this.worker.export(operationId, destination);
    if (capture.digest !== record.binding.digest)
      throw failure(
        'Mismatch',
        'Checkpoint restore differs from the native sealed capture.',
      );
    await saveState(path.join(destination, 'manifest.json'), capture.manifest);
    await saveState(path.join(destination, 'receipt.json'), {
      schema: 1,
      state: 'readback',
      sourceOperation: operationId,
      digest: capture.digest,
    });
    const restored = await restoreTree(capture, parent);
    return { ...restored, transfer: capture.metrics };
  }
  async retained(operationId) {
    const record = await this.operation(operationId);
    if (!record)
      throw failure('NotFound', 'The native checkpoint intent is unavailable.');
    return loadCapture(record.captureRoot);
  }
  async publication(operationId, excluded = []) {
    excluded ??= [];
    const record = await this.operation(operationId);
    if (record?.state !== 'confirmed')
      throw failure(
        'Unknown',
        'Publication requires a confirmed complete-tree checkpoint.',
      );
    const namespace = await readState(
      this.namespaceFile(record.binding.workspaceId),
    );
    if (
      !namespace ||
      namespace.pending ||
      namespace.operationId !== operationId ||
      namespace.generation !== record.binding.generation ||
      namespace.storeId !== record.binding.storeId ||
      namespace.storeId !== this.worker.status.storeId
    )
      throw failure(
        'StaleGeneration',
        'A historical checkpoint is restorable but cannot be published as the current native attempt.',
      );
    const capture = await loadCapture(record.captureRoot);
    if (capture.digest !== record.binding.digest)
      throw failure(
        'Corrupt',
        'The frozen publication source differs from its checkpoint.',
      );
    if (
      !Array.isArray(excluded) ||
      excluded.some(
        (name) =>
          typeof name !== 'string' ||
          !name ||
          name.includes('/') ||
          name.includes('\\') ||
          name.includes('\0'),
      )
    )
      throw failure(
        'InvalidInput',
        'Publication exclusions must be literal entry names.',
      );
    const skip = new Set(excluded),
      originals = new Map(
        capture.manifest.entries.map((row) => [row.path, row]),
      );
    const entries = [];
    for (const row of capture.manifest.entries) {
      if (
        row.path
          .split('/')
          .some((name) => name.toLowerCase() === '.git' || skip.has(name))
      )
        continue;
      if (row.type === 'symlink')
        throw failure(
          'UnsupportedPublication',
          'The complete checkpoint retains a symlink that the Server publication archive cannot represent.',
        );
      if (row.type === 'hardlink') {
        const target = originals.get(row.target);
        entries.push({
          path: row.path,
          type: 'file',
          mode: row.mode,
          bytes: target.bytes,
          sha256: target.sha256,
        });
      } else entries.push(row);
    }
    const manifest = {
      schema: 1,
      rootMode: capture.manifest.rootMode,
      entries,
    };
    const derived = {
      root: capture.root,
      manifest,
      digest: manifestDigest(manifest),
    };
    const restored = await restoreTree(
      derived,
      path.join(this.root, 'publication'),
    );
    return {
      directory: restored.root,
      digest: derived.digest,
      sourceDigest: capture.digest,
      operationId,
    };
  }
  async fork(sourceOperation, workspaceId, attemptId) {
    const source = await this.operation(sourceOperation);
    if (
      source?.state !== 'confirmed' ||
      source.binding.storeId !== this.worker.status.storeId
    )
      throw failure(
        'Unknown',
        'Fork requires a confirmed checkpoint in the current Store.',
      );
    if (await readState(this.namespaceFile(workspaceId)))
      throw failure(
        'Conflict',
        'Choose a new workspace identity for the fork.',
      );
    const binding = {
      runnerId: this.runnerId,
      storageId: this.storageId,
      workspaceId,
      attemptId,
      operationId: randomUUID(),
      generation: 1,
      digest: source.binding.digest,
      parentCheckpoint: source.workerReceipt.checkpoint.head,
      storeId: this.worker.status.storeId,
    };
    const record = {
      schema: 1,
      kind: 'fork',
      sourceOperation,
      binding,
      captureRoot: source.captureRoot,
      captureRelative:
        source.captureRelative ??
        path.relative(this.captureBase, source.captureRoot),
      state: 'fork-dispatched',
      workerImage: this.worker.installed.image,
      durableDelivery: false,
    };
    const namespace = {
      schema: 1,
      storeId: binding.storeId,
      generation: 0,
      head: binding.parentCheckpoint,
      pending: binding.operationId,
    };
    await saveState(this.operationFile(binding.operationId), record);
    await saveState(this.namespaceFile(workspaceId), namespace);
    const receipt = await this.worker.request({
      op: 'fork',
      args: { binding, sourceOperation },
    });
    return this.confirm(record, namespace, receipt);
  }
}
