import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { failure, readState, saveState } from './state.mjs';

export async function requireReleased(home, uncertain = false) {
  if (uncertain)
    throw failure(
      'Unknown',
      'Administrative release is uncertain for this worker.',
    );
  const state = await readState(path.join(home, 'release-state.json'));
  if (state && (state.schema !== 1 || state.state !== 'released'))
    throw failure(
      'Unknown',
      'An administrative release remains uncertain; retain this home and recover explicitly.',
    );
}

// An SDK release is dispatched once. A failed close can mean that Docker has
// stopped a container but has not confirmed finalization; never repeat it during
// drain, delete its records, or infer success from the transferred file bytes.
export async function releaseResource(owner, resource, sample) {
  if (resource.releaseAttempted)
    throw failure('Unknown', 'Administrative release is already uncertain.');
  resource.releaseAttempted = true;
  const started = performance.now();
  const calls = {};
  let journalMs = 0;
  const record = {
    schema: 1,
    id: randomUUID(),
    worker: owner.bootId,
    storeId: owner.state.storeId,
    sandbox: resource.sandbox,
    container: resource.container,
    workspace: resource.workspace,
    purpose: resource.purpose,
    operationId: resource.operationId,
    state: 'release-dispatched',
    calls,
  };
  const file = path.join(owner.config.home, 'releases', `${record.id}.json`);
  const phase = async (state) => {
    const writing = performance.now();
    try {
      record.state = state;
      record.observedAt = new Date().toISOString();
      await saveState(file, record);
      if (!owner.releaseUncertain)
        await saveState(path.join(owner.config.home, 'release-state.json'), {
          schema: 1,
          id: record.id,
          worker: record.worker,
          storeId: record.storeId,
          state,
          observedAt: record.observedAt,
        });
    } finally {
      journalMs += performance.now() - writing;
    }
  };
  const measured = async (name, action) => {
    const dispatched = performance.now();
    try {
      return await action();
    } finally {
      calls[name] = performance.now() - dispatched;
    }
  };
  try {
    await phase('release-dispatched');
    if (sample) await measured('sampleMs', sample);
    if (resource.workspace) {
      await phase('unmount-dispatched');
      await measured('unmountMs', () =>
        owner.client.workspace.unmount(resource.workspace),
      );
    }
    await phase('close-dispatched');
    if (
      resource.purpose === 'export' &&
      owner.takeFault('export-release-failure')
    )
      throw failure('Docker', 'Controlled export release failure.');
    await measured('closeMs', () =>
      owner.client.sandbox.close(resource.sandbox),
    );
    await phase('released');
    resource.releaseMetrics = {
      ...calls,
      journalMs,
      totalMs: performance.now() - started,
    };
    owner.mounts.delete(resource);
  } catch (error) {
    record.failedPhase = record.state;
    record.error = {
      code: String(error.code ?? 'Unknown').slice(0, 128),
      message: String(error.message).slice(0, 2048),
    };
    resource.releaseError = failure(
      'Unknown',
      `Administrative ${record.failedPhase} was not confirmed (${record.error.code}): ${record.error.message}`,
    );
    try {
      await phase('unknown');
    } catch (recordError) {
      resource.releaseError.message += ` Release evidence could not be saved: ${String(recordError.code ?? 'Unknown').slice(0, 128)}.`;
    } finally {
      owner.releaseUncertain = true;
    }
    throw resource.releaseError;
  }
}

export async function drainResources(owner) {
  const errors = [];
  for (const resource of owner.mounts) {
    if (resource.releaseAttempted) {
      errors.push(
        resource.releaseError ??
          failure('Unknown', 'Administrative release was interrupted.'),
      );
      continue;
    }
    try {
      await owner.release(resource);
    } catch (error) {
      errors.push(error);
    }
  }
  // Both SDK owners still close even when a mount has an uncertain release.
  // Their public close methods do not sweep or replay sandbox mutations.
  for (const client of [owner.client, owner.runtime])
    try {
      await client.close();
    } catch (error) {
      errors.push(error);
    }
  if (errors.length)
    throw failure(
      'Unknown',
      `Worker drain retained uncertain resources: ${String(errors[0].message).slice(0, 2048)}`,
    );
}
