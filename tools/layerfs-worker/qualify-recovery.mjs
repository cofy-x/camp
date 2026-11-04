import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  chmod,
  link,
  mkdir,
  mkdtemp,
  readFile,
  symlink,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { pathToFileURL } from 'node:url';
import { qualifyExportRelease } from './qualify-export-release.mjs';

const runtime = process.env.CAMP_TEST_LAYERFS_WORKER_RUNTIME
  ? path.resolve(process.env.CAMP_TEST_LAYERFS_WORKER_RUNTIME)
  : import.meta.dirname;
const load = (name) => import(pathToFileURL(path.join(runtime, name)).href);
const { administer } = await load('administration.mjs');
const { openHome, selectedEpoch } = await load('home.mjs');
const { readState, saveState } = await load('state.mjs');
const { scanTree } = await load('tree.mjs');

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
const base = path.resolve('.data');
await mkdir(base, { recursive: true, mode: 0o700 });
const root = await mkdtemp(path.join(base, 'worker-recovery-'));
await chmod(root, 0o700);
const home = path.join(root, 'layerfs-worker');
await mkdir(home, { mode: 0o700 });
const source = path.join(root, 'source');
await mkdir(source, { mode: 0o700 });
await writeFile(
  path.join(source, 'retained.txt'),
  'retained native reconstruction bytes\n',
);
await link(path.join(source, 'retained.txt'), path.join(source, 'twin.txt'));
await symlink('retained.txt', path.join(source, 'link'));
const parent = path.join(root, 'restores');
await mkdir(parent, { mode: 0o700 });
await writeFile(path.join(parent, 'existing-marker'), 'never overwritten');
const socket = process.env.CAMP_TEST_DOCKER_SOCKET;
assert.ok(
  socket && path.isAbsolute(socket),
  'Select an explicit local Docker socket.',
);
const dockerBinary = process.env.CAMP_TEST_DOCKER_BINARY;
assert.ok(
  dockerBinary && path.isAbsolute(dockerBinary),
  'Select an explicit Docker executable.',
);
const config = {
  dataRoot: root,
  home,
  docker: socket,
  dockerBinary,
  installation: path.join(runtime, 'installed.json'),
  runnerId: 'controlled-recovery-runner',
  storageId: 'controlled-recovery-storage',
};
const identity = path.join(root, 'controlled-identity.json'),
  journal = path.join(root, 'controlled-journal.bin');
await saveState(identity, {
  runnerId: config.runnerId,
  storageId: config.storageId,
  registrationId: 'controlled-registration',
  credential: 'synthetic-fixture-only',
});
await writeFile(journal, 'controlled durable journal bytes', { mode: 0o600 });
const beforeIdentity = await readFile(identity),
  beforeJournal = await readFile(journal);
const report = {
  schema: 1,
  root,
  scope: 'actual Mac Store loss and explicit reconstruction',
  checks: [],
  passed: false,
  modelRequests: 0,
};
let active;
try {
  active = await openHome(config);
  const initial = await active.checkpoints.checkpoint(
    source,
    createHash('sha256').update('controlled-recovery-workspace').digest('hex'),
    'initial',
    { quiescent: true },
  );
  report.initial = initial.binding;
  report.worker = active.worker.status;
  await active.worker.request({
    op: 'qualification.fault',
    args: { point: 'before-unchanged-readback' },
  });
  let unchangedOperation;
  try {
    await active.checkpoints.checkpoint(
      source,
      initial.binding.workspaceId,
      'unchanged-interrupted',
      { quiescent: true },
    );
    assert.fail('The unchanged readback interruption did not occur.');
  } catch (error) {
    assert.equal(error.code, 'Unknown');
    unchangedOperation = error.operationId;
    assert.ok(unchangedOperation);
  }
  assert.equal(await active.worker.finished, 74);
  await assert.rejects(active.worker.close(), { code: 'Unknown' });
  const retainedUnchanged =
    await active.checkpoints.operation(unchangedOperation);
  assert.equal(retainedUnchanged.state, 'commit-dispatched');
  assert.notEqual(retainedUnchanged.captureRoot, initial.captureRoot);
  assert.equal(
    (await scanTree(source)).digest,
    retainedUnchanged.binding.digest,
  );
  active = await openHome(config);
  const interrupted = await active.worker.request({
    op: 'qualification.inspect',
    args: { operationId: unchangedOperation },
  });
  assert.equal(interrupted.receipt.state, 'unchanged-readback-dispatched');
  assert.equal(interrupted.receipt.branch, interrupted.receipt.parentBranch);
  assert.equal(interrupted.head, initial.workerReceipt.checkpoint.head);
  await assert.rejects(
    active.worker.request({
      op: 'commit',
      args: { operationId: unchangedOperation },
    }),
    { code: 'Unknown' },
  );
  const confirmedUnchanged =
    await active.checkpoints.observe(unchangedOperation);
  assert.equal(confirmedUnchanged.state, 'confirmed');
  assert.equal(
    confirmedUnchanged.workerReceipt.mutationSkipped,
    'unchanged-snapshot',
  );
  assert.equal(
    confirmedUnchanged.workerReceipt.checkpoint.head,
    initial.workerReceipt.checkpoint.head,
  );
  assert.equal(
    confirmedUnchanged.workerReceipt.verifiedDigest,
    initial.binding.digest,
  );
  const observed = await active.worker.request({
    op: 'qualification.inspect',
    args: { operationId: unchangedOperation },
  });
  assert.deepEqual(observed.commits, interrupted.commits);
  assert.equal(observed.namespace.generation, 2);
  assert.equal(observed.namespace.pending, null);
  report.unchanged = {
    operationId: unchangedOperation,
    retained: retainedUnchanged,
    confirmed: confirmedUnchanged,
    commitsUnchanged: true,
  };
  report.checks.push(
    'A worker exit before unchanged readback retains the fresh seal and request ID; restart performs only a fresh independent readback with confirmed release, advances the generation once and creates no Commit or branch.',
  );
  const volume = active.worker.status.volume;
  const owned = active.worker.docker(['volume', 'inspect', volume])[0];
  assert.equal(owned.Labels['layerfs.role'], 'store');
  assert.equal(owned.Labels['layerfs.project'], active.worker.status.project);
  assert.equal(owned.Labels['layerfs.home'], active.worker.config.home);
  await active.worker.close();
  const docker = active.worker.docker;
  active = null;
  docker(['volume', 'rm', volume], false);
  report.removedOwnedStore = volume;
  await assert.rejects(openHome(config), { code: 'LostStore' });
  report.checks.push(
    'Actual deletion of the verified owned Store is detected without automatic initialization or mutation replay.',
  );
  const native = await administer({
    ...config,
    action: 'restore',
    operationId: initial.binding.operationId,
    parent,
    fromNative: true,
  });
  assert.equal(native.currentWorkerVerified, false);
  assert.equal((await scanTree(native.root)).digest, initial.binding.digest);
  report.nativeRestore = native;
  report.checks.push(
    'Explicit native-only recovery retains all bytes and link topology and does not claim a current verified worker checkpoint.',
  );
  let recoveryId;
  try {
    await administer({
      ...config,
      action: 'reconstruct',
      operationId: initial.binding.operationId,
      qualification: { fault: 'before-select' },
    });
    assert.fail('The controlled reconstruction interruption did not occur.');
  } catch (error) {
    assert.equal(error.code, 'InjectedRecoveryPause');
    recoveryId = error.recoveryId;
  }
  assert.equal((await selectedEpoch(home)).epoch, 'initial');
  const intent = await readState(
    path.join(home, 'recoveries', `${recoveryId}.json`),
  );
  assert.equal(intent.state, 'verified');
  const observed = await administer({
    ...config,
    action: 'observe-reconstruction',
    recoveryId,
  });
  assert.equal(observed.kind, 'selected-reconstruction');
  report.reconstruction = observed.intent;
  assert.notEqual(observed.intent.result.storeId, initial.binding.storeId);
  assert.notEqual(
    observed.intent.result.operationId,
    initial.binding.operationId,
  );
  assert.equal(observed.intent.result.digest, initial.binding.digest);
  assert.equal(
    observed.intent.result.recoveryFrom.operationId,
    initial.binding.operationId,
  );
  assert.equal(observed.intent.result.parentCheckpoint, null);
  report.checks.push(
    'Interruption before epoch selection retains the verified new operation; observation independently reopens/readbacks and selects it without replaying the reconstruction.',
  );
  active = await openHome(config);
  assert.equal(active.worker.status.storeId, observed.intent.result.storeId);
  const restored = await active.checkpoints.restore(
    observed.intent.result.operationId,
    parent,
  );
  assert.equal((await scanTree(restored.root)).digest, initial.binding.digest);
  report.verifiedRestore = restored;
  assert.equal(
    await readFile(path.join(parent, 'existing-marker'), 'utf8'),
    'never overwritten',
  );
  assert.deepEqual(await readFile(identity), beforeIdentity);
  assert.deepEqual(await readFile(journal), beforeJournal);
  assert.ok(await readFile(path.join(initial.captureRoot, 'manifest.json')));
  report.checks.push(
    'The new Store restores actual checkpoint content while original identity, journal, captures, old receipts and pre-existing destination files remain unchanged.',
  );
  report.exportRelease = await qualifyExportRelease({
    runtime,
    root,
    source,
    socket,
    dockerBinary,
  });
  report.passed = true;
} catch (error) {
  report.error = {
    code: error.code,
    message: error.message,
    recoveryId: error.recoveryId,
  };
  process.exitCode = 1;
} finally {
  try {
    await active?.worker.close();
    report.workerDrainConfirmed = true;
  } catch (error) {
    report.cleanupError = error.message;
    report.passed = false;
    process.exitCode = 1;
  }
  await saveState(path.join(root, 'report.json'), report);
  if (process.env.CAMP_TEST_WORKER_REPORT)
    await saveState(path.resolve(process.env.CAMP_TEST_WORKER_REPORT), report);
  process.stdout.write(
    `Actual Mac recovery ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
}
