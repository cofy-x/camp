import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import {
  chmod,
  link,
  mkdir,
  mkdtemp,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { LinuxWorker } from '../linux.mjs';
import { readState, saveState } from '../state.mjs';
import { requireReleased } from '../release.mjs';
import { captureTree, scanTree } from '../tree.mjs';
import { encodeCapture } from '../transfer.mjs';

async function fixture(t, { empty = false } = {}) {
  const home = await mkdtemp(path.join(os.tmpdir(), 'camp-unchanged-'));
  t.after(() => rm(home, { recursive: true, force: true }));
  const source = path.join(home, 'source');
  await mkdir(source, { mode: 0o700 });
  if (!empty) {
    await writeFile(path.join(source, 'note'), 'frozen bytes', { mode: 0o600 });
    await link(path.join(source, 'note'), path.join(source, 'twin'));
    await symlink('note', path.join(source, 'link'));
  }
  const capture = await captureTree(source, path.join(home, 'captures'));
  const events = [];
  const head = empty ? null : '12' + '1'.repeat(64);
  const binding = {
    runnerId: 'fixture-runner',
    storageId: 'fixture-storage',
    workspaceId: '2'.repeat(64),
    attemptId: 'fixture-attempt',
    operationId: randomUUID(),
    generation: empty ? 1 : 2,
    digest: capture.digest,
    parentCheckpoint: head,
    storeId: randomUUID(),
  };
  const worker = new LinuxWorker({
    config: { home },
    state: { storeId: binding.storeId },
    cache: path.join(home, 'cache'),
    client: {
      branch: {
        fork: async () => {
          events.push('fork');
          throw new Error('Unexpected mutation');
        },
      },
      workspace: {
        commit: async () => {
          events.push('commit');
          throw new Error('Unexpected mutation');
        },
        unmount: async () => {
          events.push('unmount');
        },
      },
      sandbox: {
        close: async () => {
          events.push('close');
        },
      },
    },
  });
  await mkdir(path.join(worker.cache, 'blobs'), { recursive: true });
  const namespace = {
    schema: 1,
    runnerId: binding.runnerId,
    storageId: binding.storageId,
    workspaceId: binding.workspaceId,
    branch: 'retained-parent',
    generation: empty ? 0 : 1,
    head,
    digest: capture.digest,
    manifest: capture.manifest,
    pending: null,
  };
  await saveState(worker.namespaceFile(binding.workspaceId), namespace);
  worker.head = async (branch) => {
    assert.equal(branch, namespace.branch);
    return namespace.head;
  };
  worker.mounted = async (branch) => {
    events.push('mount');
    assert.equal(branch, namespace.branch);
    const resource = {
      sandbox: 'owned-reader',
      container: 'owned-container',
      workspace: { sandbox: 'owned-reader', path: '/owned/reader' },
    };
    worker.mounts.add(resource);
    return resource;
  };
  worker.runHelper = async (resource, operation) => {
    events.push(operation);
    assert.equal(operation, 'scan');
    assert.equal(resource.workspace.path, '/owned/reader');
    return scanTree(source);
  };
  const prepared = await worker.prepare({
    binding,
    manifest: capture.manifest,
  });
  await worker.upload(
    { operationId: binding.operationId },
    encodeCapture(capture, prepared.wanted),
  );
  return { worker, binding, namespace, capture, source, events };
}

test('an initial empty snapshot verifies its real parent branch without inventing a Commit', async (t) => {
  const f = await fixture(t, { empty: true });
  const receipt = await f.worker.commit({ operationId: f.binding.operationId });
  assert.equal(receipt.checkpoint.kind, 'UpToDate');
  assert.equal(receipt.checkpoint.head, null);
  assert.equal(receipt.state, 'verified');
  assert.equal(
    (await readState(f.worker.namespaceFile(f.binding.workspaceId))).generation,
    1,
  );
  assert.deepEqual(f.events, ['mount', 'scan', 'unmount', 'close']);
});

test('unchanged upload uses a fresh complete readback without fork, apply or Commit', async (t) => {
  const f = await fixture(t);
  const receipt = await f.worker.commit({ operationId: f.binding.operationId });
  assert.deepEqual(f.events, ['mount', 'scan', 'unmount', 'close']);
  assert.equal(receipt.state, 'verified');
  assert.equal(receipt.checkpoint.kind, 'UpToDate');
  assert.equal(receipt.checkpoint.head, f.binding.parentCheckpoint);
  assert.equal(receipt.mutationSkipped, 'unchanged-snapshot');
  assert.equal(receipt.verifiedDigest, f.capture.digest);
  assert.equal(receipt.branch, f.namespace.branch);
  assert.equal(receipt.apply, undefined);
  assert.ok(receipt.readbackRelease.totalMs >= receipt.readbackRelease.closeMs);
  assert.equal(f.worker.mounts.size, 0);
  const current = await readState(
    f.worker.namespaceFile(f.binding.workspaceId),
  );
  assert.equal(current.pending, null);
  assert.equal(current.generation, f.binding.generation);
  await assert.rejects(
    f.worker.commit({ operationId: f.binding.operationId }),
    { code: 'Unknown' },
  );
  f.worker.bootId = randomUUID();
  await f.worker.observe({ operationId: f.binding.operationId });
  assert.deepEqual(f.events, [
    'mount',
    'scan',
    'unmount',
    'close',
    'mount',
    'scan',
    'unmount',
    'close',
  ]);
});

for (const mutation of ['bytes', 'mode', 'link', 'hardlink', 'membership'])
  test(`unchanged readback rejects changed ${mutation} without advancing the generation`, async (t) => {
    const f = await fixture(t);
    if (mutation === 'bytes')
      await writeFile(path.join(f.source, 'note'), 'altered data');
    if (mutation === 'mode') await chmod(path.join(f.source, 'note'), 0o640);
    if (mutation === 'link') {
      await rm(path.join(f.source, 'link'));
      await symlink('twin', path.join(f.source, 'link'));
    }
    if (mutation === 'hardlink') {
      await rm(path.join(f.source, 'twin'));
      await writeFile(path.join(f.source, 'twin'), 'frozen bytes', {
        mode: 0o600,
      });
    }
    if (mutation === 'membership')
      await writeFile(path.join(f.source, 'extra'), 'extra');
    await assert.rejects(
      f.worker.commit({ operationId: f.binding.operationId }),
      { code: 'Mismatch' },
    );
    const current = await readState(
      f.worker.namespaceFile(f.binding.workspaceId),
    );
    assert.equal(current.pending, f.binding.operationId);
    assert.equal(current.generation, 1);
    const receipt = await readState(
      f.worker.operationFile(f.binding.operationId),
    );
    assert.equal(receipt.state, 'unchanged-readback-dispatched');
    assert.equal(receipt.error.code, 'Mismatch');
    assert.equal(f.worker.mounts.size, 0);
  });

test('unchanged readback requires current parent identity before and after the scan', async (t) => {
  const f = await fixture(t);
  let reads = 0;
  f.worker.head = async () =>
    ++reads < 3 ? f.namespace.head : '12' + '9'.repeat(64);
  await assert.rejects(
    f.worker.commit({ operationId: f.binding.operationId }),
    { code: 'Mismatch' },
  );
  assert.equal(
    (await readState(f.worker.namespaceFile(f.binding.workspaceId))).generation,
    1,
  );
});

test('interrupted unchanged readback resumes only the same read-only operation', async (t) => {
  const f = await fixture(t);
  const mounted = f.worker.mounted;
  f.worker.mounted = async () => {
    throw Object.assign(new Error('controlled interruption'), {
      code: 'Interrupted',
    });
  };
  await assert.rejects(
    f.worker.commit({ operationId: f.binding.operationId }),
    { code: 'Interrupted' },
  );
  assert.deepEqual(f.events, []);
  await assert.rejects(
    f.worker.commit({ operationId: f.binding.operationId }),
    { code: 'Unknown' },
  );
  f.worker.bootId = randomUUID();
  f.worker.mounted = mounted;
  const receipt = await f.worker.observe({
    operationId: f.binding.operationId,
  });
  assert.equal(receipt.state, 'verified');
  assert.deepEqual(f.events, ['mount', 'scan', 'unmount', 'close']);
});

test('unchanged resume refuses a changed pending generation or parent branch', async (t) => {
  const f = await fixture(t);
  const receipt = await readState(
    f.worker.operationFile(f.binding.operationId),
  );
  Object.assign(receipt, {
    state: 'unchanged-readback-dispatched',
    branch: 'other-parent',
    checkpoint: { kind: 'UpToDate', head: f.namespace.head },
  });
  await saveState(f.worker.operationFile(f.binding.operationId), receipt);
  await assert.rejects(
    f.worker.observe({ operationId: f.binding.operationId }),
    { code: 'Conflict' },
  );
  assert.deepEqual(f.events, []);
});

test('unchanged successful bytes never override an unconfirmed administrative close', async (t) => {
  const f = await fixture(t);
  f.worker.client.sandbox.close = async () => {
    f.events.push('close');
    throw new Error('unconfirmed Docker acknowledgement');
  };
  await assert.rejects(
    f.worker.commit({ operationId: f.binding.operationId }),
    { code: 'Unknown' },
  );
  await assert.rejects(requireReleased(f.worker.config.home), {
    code: 'Unknown',
  });
  assert.deepEqual(f.events, ['mount', 'scan', 'unmount', 'close']);
  assert.equal(f.worker.mounts.size, 1);
  const current = await readState(
    f.worker.namespaceFile(f.binding.workspaceId),
  );
  assert.equal(current.generation, 1);
  assert.equal(current.pending, f.binding.operationId);
  const receipt = await readState(
    f.worker.operationFile(f.binding.operationId),
  );
  assert.equal(receipt.state, 'unchanged-readback-dispatched');
  assert.equal(receipt.verifiedDigest, undefined);
});

test('unchanged prepare rehashes corrupted cache bytes before requesting a fresh upload', async (t) => {
  const f = await fixture(t);
  await f.worker.commit({ operationId: f.binding.operationId });
  const file = f.capture.manifest.entries.find((e) => e.type === 'file');
  await writeFile(
    path.join(f.worker.cache, 'blobs', file.sha256),
    'corrupt data',
  );
  const binding = { ...f.binding, operationId: randomUUID(), generation: 3 };
  const prepared = await f.worker.prepare({
    binding,
    manifest: f.capture.manifest,
  });
  assert.deepEqual(prepared.wanted, [file.sha256]);
  await f.worker.upload(
    { operationId: binding.operationId },
    encodeCapture(f.capture, prepared.wanted),
  );
  assert.equal(
    (await f.worker.commit({ operationId: binding.operationId })).state,
    'verified',
  );
});
