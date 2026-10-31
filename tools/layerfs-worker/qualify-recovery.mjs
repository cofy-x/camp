import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
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
import { administer } from './administration.mjs';
import { openHome, selectedEpoch } from './home.mjs';
import { readState, saveState } from './state.mjs';
import { scanTree } from './tree.mjs';

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
const endpoint = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
).trim();
assert.ok(endpoint.startsWith('unix://'));
const config = {
  dataRoot: root,
  home,
  docker: endpoint.slice(7),
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
  process.stdout.write(
    `Actual Mac recovery ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
}
