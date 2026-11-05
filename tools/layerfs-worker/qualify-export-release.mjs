import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { pathToFileURL } from 'node:url';

// Actual packaged-client/image fault qualification, never a performance sample.
// Only a fresh home created beneath the caller's controlled recovery fixture is
// used. A known injected fault has one explicit, observed stop-only teardown;
// neither SDK records nor the original operation are retried or removed.
export async function qualifyExportRelease({
  runtime,
  root,
  source,
  socket,
  dockerBinary,
}) {
  const load = (name) => import(pathToFileURL(path.join(runtime, name)).href);
  const { WorkerClient } = await load('client.mjs');
  const { openHome } = await load('home.mjs');
  const { administer } = await load('administration.mjs');
  const { scanTree } = await load('tree.mjs');
  const { saveState } = await load('state.mjs');
  const directory = path.join(root, 'export-release');
  await mkdir(directory, { mode: 0o700 });
  const logicalHome = path.join(directory, 'layerfs-worker');
  await mkdir(logicalHome, { mode: 0o700 });
  const config = {
    dataRoot: directory,
    home: logicalHome,
    docker: socket,
    dockerBinary,
    installation: path.join(runtime, 'installed.json'),
    qualification: true,
    runnerId: 'export-release-runner',
    storageId: 'export-release-storage',
  };
  const report = {
    passed: false,
    scope:
      'export release fault qualification; artificial delay excluded from costs',
    modelRequests: 0,
  };
  let worker;
  let originalError;
  let drainError;
  let faultConfirmed = false;
  try {
    const active = await openHome(config);
    worker = active.worker;
    const checkpoints = active.checkpoints;
    const home = worker.home;
    const options = {
      home,
      socket,
      dockerBinary,
      installation: config.installation,
      qualification: true,
    };
    const initial = await checkpoints.checkpoint(
      source,
      createHash('sha256').update('export-release-workspace').digest('hex'),
      'initial',
      { quiescent: true },
    );
    const originalReceipt = await readFile(
      path.join(initial.captureRoot, 'receipt.json'),
    );
    const parent = path.join(directory, 'results');
    await mkdir(parent, { mode: 0o700 });
    const request = worker.request.bind(worker);
    let receivedAt;
    worker.request = (value, options) =>
      request(
        value,
        value.op === 'export'
          ? {
              ...options,
              receive: async (...args) => {
                const result = await options.receive(...args);
                receivedAt = performance.now();
                return result;
              },
            }
          : options,
      );
    await worker.request({
      op: 'qualification.fault',
      args: { point: 'export-delay-release' },
    });
    await checkpoints.restore(initial.binding.operationId, parent);
    const releaseWaitMs = performance.now() - receivedAt;
    assert.ok(
      releaseWaitMs >= 1500,
      'Export returned before its controlled release delay settled.',
    );
    report.delayedReleaseWaitMs = releaseWaitMs;
    report.delayConfirmed = true;
    const before = await readdir(parent);
    await worker.request({
      op: 'qualification.fault',
      args: { point: 'export-release-failure' },
    });
    await assert.rejects(
      checkpoints.restore(initial.binding.operationId, parent),
      { code: 'Unknown' },
    );
    assert.equal(worker.uncertain, true);
    assert.deepEqual(
      await readdir(parent),
      before,
      'Unconfirmed stream became a restored result.',
    );
    await assert.rejects(
      worker.request({
        op: 'export',
        args: { operationId: initial.binding.operationId },
      }),
      { code: 'Unavailable' },
    );
    await assert.rejects(worker.close(), { code: 'Unknown' });
    report.failedDrainConfirmed = false;
    const files = await readdir(path.join(home, 'failures'));
    const failures = await Promise.all(
      files.map(async (name) =>
        JSON.parse(await readFile(path.join(home, 'failures', name), 'utf8')),
      ),
    );
    const failed = failures.find((row) => row.phase === 'export-release');
    assert.ok(failed?.retainedCapture);
    assert.equal(failed.operationId, initial.binding.operationId);
    assert.ok((await readdir(failed.retainedCapture)).length > 0);
    assert.ok(
      failures.some((row) => row.diagnostic?.includes('close-dispatched')),
    );
    assert.deepEqual(
      await readFile(path.join(initial.captureRoot, 'receipt.json')),
      originalReceipt,
    );
    const inspectRelease = () =>
      worker.docker([
        'run',
        '--rm',
        '--network=none',
        '--read-only',
        '--memory=64m',
        '--pids-limit=16',
        '--label',
        'cofy.camp.scope=export-release-inspection',
        '--label',
        `cofy.camp.worker=${worker.identity.id}`,
        '--mount',
        `type=volume,source=${worker.identity.volume},target=/evidence,readonly`,
        worker.installed.image,
        '-e',
        "const fs=require('node:fs'); const pointer=JSON.parse(fs.readFileSync('/evidence/release-state.json')); if(!/^[a-f0-9-]{36}$/.test(pointer.id)) throw Error('Invalid release ID'); const release=JSON.parse(fs.readFileSync('/evidence/releases/'+pointer.id+'.json')); process.stdout.write(JSON.stringify({pointer,release}));",
      ]);
    const evidence = inspectRelease();
    assert.equal(evidence.pointer.state, 'unknown');
    assert.equal(evidence.release.failedPhase, 'close-dispatched');
    assert.equal(
      evidence.release.error.message,
      'Controlled export release failure.',
    );
    assert.equal(evidence.release.storeId, worker.status.storeId);
    assert.equal(evidence.release.operationId, initial.binding.operationId);
    await assert.rejects(WorkerClient.open(options), { code: 'Unknown' });
    assert.deepEqual(
      inspectRelease(),
      evidence,
      'Reopening replayed or cleared release history.',
    );
    const owned = worker.docker([
      'container',
      'inspect',
      evidence.release.container,
    ])[0];
    assert.equal(owned.Id, evidence.release.container);
    assert.equal(owned.Image, worker.installed.image);
    for (const [key, value] of Object.entries({
      'layerfs.project': worker.status.project,
      'layerfs.sandbox': evidence.release.sandbox,
    }))
      assert.equal(owned.Config.Labels[key], value);
    assert.ok(owned.Name.startsWith('/' + worker.config.prefix));
    assert.equal(owned.State.Running, true);
    faultConfirmed = true;
    report.unknownRejected = true;
    report.receivedCaptureRetained = true;
    report.releaseJournalRetained = true;
    report.reopenRefusedWithoutReplay = true;
    report.originalOperation = initial.binding.operationId;
    report.release = evidence;
    // A separate test-owner teardown stops only this freshly created, fully
    // identified injected-fault Sandbox. It does not delete or replay SDK state.
    worker.docker(['container', 'stop', '--time', '5', owned.Id], false);
    const stopped = worker.docker(['container', 'inspect', owned.Id])[0];
    assert.equal(stopped.Id, owned.Id);
    assert.equal(stopped.State.Running, false);
    assert.deepEqual(inspectRelease(), evidence);
    report.controlledSandboxStopped = true;
    const recovered = await administer({
      ...config,
      action: 'reconstruct',
      operationId: initial.binding.operationId,
    });
    assert.notEqual(recovered.result.storeId, initial.binding.storeId);
    assert.notEqual(recovered.result.operationId, initial.binding.operationId);
    assert.equal(recovered.result.digest, initial.binding.digest);
    const next = await openHome(config);
    try {
      const restored = await next.checkpoints.restore(
        recovered.result.operationId,
        parent,
      );
      assert.equal(
        (await scanTree(restored.root)).digest,
        initial.binding.digest,
      );
    } finally {
      await next.worker.close();
    }
    assert.deepEqual(inspectRelease(), evidence);
    assert.deepEqual(
      await readFile(path.join(initial.captureRoot, 'receipt.json')),
      originalReceipt,
    );
    report.recoveredNewEpoch = true;
    report.recovery = recovered.result;
    report.passed = true;
  } catch (error) {
    originalError = error;
    report.error = { code: error.code, message: error.message };
    throw error;
  } finally {
    if (worker && !faultConfirmed)
      try {
        await worker.close();
      } catch (error) {
        report.drainError = error.message;
        report.passed = false;
        if (!originalError) drainError = error;
      }
    await saveState(path.join(directory, 'report.json'), report);
  }
  if (drainError) throw drainError;
  return report;
}
