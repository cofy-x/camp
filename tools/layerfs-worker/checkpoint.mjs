import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp } from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure, readState, saveState } from './state.mjs';
import { captureTree, loadCapture, restoreTree } from './tree.mjs';
import { checkpointPattern, validateBinding } from './contracts.mjs';

export class MacCheckpoints {
  constructor({ root, worker, runnerId, storageId }) {
    Object.assign(this, {
      root: path.resolve(root),
      worker,
      runnerId,
      storageId,
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
    const record = {
      schema: 1,
      binding,
      captureRoot: capture.root,
      state: 'sealed',
      captureMetrics: capture.metrics,
      workerImage: this.worker.installed.image,
      durableDelivery: false,
    };
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
        args: { binding, manifest: capture.manifest },
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
    const record = await readState(this.operationFile(operationId));
    if (!record)
      throw failure('NotFound', 'The native checkpoint intent is unavailable.');
    const namespace = await readState(
      this.namespaceFile(record.binding.workspaceId),
    );
    if (record.state === 'confirmed') {
      if (namespace.pending === operationId)
        return this.confirm(record, namespace, record.workerReceipt);
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
    const record = await readState(this.operationFile(operationId));
    if (!record)
      throw failure('NotFound', 'The native checkpoint intent is unavailable.');
    return loadCapture(record.captureRoot);
  }
}
