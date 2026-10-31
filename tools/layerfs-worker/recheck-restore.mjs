// Recheck only independent readback after a diagnostic proxy EOF fix. It does
// not repeat fs.apply, Commit, model work or replace an original failed sample.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { performance } from 'node:perf_hooks';
import { WorkerClient } from './client.mjs';
import { openUpstream } from './upstream-comparison.mjs';
import { loadCapture, restoreTree, scanTree } from './tree.mjs';
import { receiveCapture } from './transfer.mjs';
import { saveState } from './state.mjs';

const [campaign, engineSocket, repetition] = process.argv.slice(2),
  round = path.join(campaign, `round-${Number(repetition)}`);
const original = JSON.parse(
  await readFile(path.join(round, 'report.json'), 'utf8'),
);
const identity = JSON.parse(
  await readFile(path.join(round, 'worker/worker.json'), 'utf8'),
);
const root = await mkdtemp(path.join(round, 'restore-recheck-'));
const proxy = spawn(
  process.execPath,
  [
    path.join(import.meta.dirname, 'wire-proxy.mjs'),
    engineSocket,
    identity.socket,
  ],
  {
    stdio: ['ignore', 'ignore', 'inherit', 'ipc'],
    env: { PATH: process.env.PATH, HOME: root },
  },
);
const ready = await new Promise((resolve) => proxy.once('message', resolve));
const wireStats = () =>
  new Promise((resolve) => {
    proxy.once('message', resolve);
    proxy.send({ action: 'stats' });
  });
const report = {
  schema: 1,
  passed: false,
  repetition: Number(repetition),
  scope:
    'Read-only restoration recheck from retained public Branches after diagnostic counter EOF fix; original outcomes and costs remain unchanged',
  originalReport: path.join(round, 'report.json'),
  samples: [],
  modelRequests: 0,
  mutationsReplayed: 0,
};
let worker, upstream;
try {
  worker = await WorkerClient.open({
    home: path.join(round, 'worker'),
    socket: ready.socket,
    qualification: true,
  });
  assert.equal(worker.status.storeId, original.worker.storeId);
  assert.equal(worker.status.image, original.worker.image);
  upstream = await openUpstream(worker, root);
  for (const workload of ['text', '10000-files', '128-MiB-binary']) {
    const branch = original.observations.find(
      (row) =>
        row.workload === workload &&
        row.arm === 'upstream' &&
        row.phase === 'fork',
    )?.result.branch;
    assert.ok(
      branch,
      'Rechecks require an acknowledged immutable public fork, never a mutation retry.',
    );
    const record = original.observations.find(
      (row) =>
        row.workload === workload &&
        row.arm === 'streamed' &&
        row.phase === 'unchanged',
    ).result;
    const capture = await loadCapture(record.captureRoot);
    const sample = { workload, branch, digest: capture.digest, passed: false },
      before = await wireStats(),
      cpu = process.resourceUsage(),
      started = performance.now();
    let sandbox,
      workspace,
      child,
      ended,
      diagnostic = '';
    try {
      sandbox = (await upstream.client.sandbox.create()).sandbox;
      workspace = await upstream.client.workspace.mount(sandbox, branch);
      const records = await upstream.client.project.list_sandboxes(1000, 0),
        selected = records.items.find((row) => row.sandbox === sandbox),
        container = worker.docker(['inspect', selected.name])[0];
      assert.equal(container.Image, worker.installed.image);
      assert.equal(
        container.Config.Labels['layerfs.project'],
        worker.status.project,
      );
      child = spawn(
        'docker',
        [
          'exec',
          '--user',
          '501:20',
          container.Id,
          'node',
          '/opt/camp/layerfs/worker/helper.mjs',
          'export',
          workspace.path,
          capture.digest,
        ],
        { env: upstream.dockerEnvironment, stdio: ['ignore', 'pipe', 'pipe'] },
      );
      child.stderr.on('data', (chunk) => {
        diagnostic = `${diagnostic}${chunk}`.slice(-8192);
      });
      ended = new Promise((resolve) => child.once('close', resolve));
      const destination = path.join(root, `${workload}-download`);
      await mkdir(destination, { mode: 0o700 });
      const received = await receiveCapture(child.stdout, destination, {
        digest: capture.digest,
      });
      assert.equal(await ended, 0, diagnostic);
      sample.exportMs = performance.now() - started;
      sample.receive = received.metrics;
      const restoring = performance.now();
      const restored = await restoreTree(
        { root: destination, ...received },
        path.join(root, 'restores'),
      );
      assert.equal((await scanTree(restored.root)).digest, capture.digest);
      sample.restoreMs = performance.now() - restoring;
      sample.sandboxCgroup = worker.docker([
        'exec',
        container.Id,
        'node',
        '/opt/camp/layerfs/worker/cost.mjs',
      ]);
      sample.passed = true;
    } catch (error) {
      sample.error = { code: error.code, message: error.message };
      if (ended) sample.exportExit = await ended;
      sample.diagnostic = diagnostic;
    } finally {
      if (workspace) await upstream.client.workspace.unmount(workspace);
      if (sandbox) await upstream.client.sandbox.close(sandbox);
      sample.elapsedMs = performance.now() - started;
      const after = await wireStats(),
        usage = process.resourceUsage();
      sample.wire = {
        sent: after.sent - before.sent,
        received: after.received - before.received,
      };
      sample.cpu = {
        userUsec: usage.userCPUTime - cpu.userCPUTime,
        systemUsec: usage.systemCPUTime - cpu.systemCPUTime,
      };
      sample.macLifetimeMaxRSSKiB = usage.maxRSS;
      report.samples.push(sample);
      await saveState(path.join(root, 'report.json'), report);
      process.stdout.write(
        `Restore recheck ${repetition} ${workload}: ${sample.passed ? 'passed' : 'failed'}\n`,
      );
    }
  }
  report.passed =
    report.samples.length === 3 &&
    report.samples.every((sample) => sample.passed);
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  await upstream?.close();
  await worker?.close();
  proxy.send({ action: 'close' });
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Retained restore recheck: ${path.join(root, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
