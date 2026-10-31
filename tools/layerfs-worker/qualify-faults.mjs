import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFile, execFileSync, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmod, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { promisify } from 'node:util';
import { administer } from './administration.mjs';
import { openHome } from './home.mjs';
import { encodeCapture } from './transfer.mjs';
import { captureWireBytes } from './protocol.mjs';
import { loadCapture, manifestDigest, scanTree } from './tree.mjs';
import { readState, saveState } from './state.mjs';

const execute = promisify(execFile);
assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
const base = path.resolve('.data');
await mkdir(base, { recursive: true, mode: 0o700 });
const root = await mkdtemp(path.join(base, 'worker-faults-'));
await chmod(root, 0o700);
const endpoint = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
).trim();
assert.ok(endpoint.startsWith('unix://'));
const environment = {
  PATH: process.env.PATH,
  HOME: root,
  DOCKER_HOST: endpoint,
};
const report = {
  schema: 1,
  root,
  scope:
    'actual Mac raw pipe, native ARM64 worker, real SDK mutation/Commit and controlled process faults',
  modelRequests: 0,
  cases: [],
  passed: false,
};

async function fixture(name) {
  const directory = path.join(root, name),
    home = path.join(directory, 'layerfs-worker'),
    source = path.join(directory, 'source'),
    parent = path.join(directory, 'restores');
  await mkdir(directory, { mode: 0o700 });
  await mkdir(home, { mode: 0o700 });
  await mkdir(source, { mode: 0o700 });
  await mkdir(parent, { mode: 0o700 });
  await writeFile(path.join(source, 'binary'), Buffer.alloc(700_000, 0xa7));
  await writeFile(path.join(source, 'note'), `${name}\n`);
  const config = {
    dataRoot: directory,
    home,
    source,
    parent,
    docker: endpoint.slice(7),
    runnerId: 'controlled-fault-runner',
    storageId: 'controlled-fault-storage',
    qualification: true,
    workspaceId: createHash('sha256').update(name).digest('hex'),
  };
  return { config, active: await openHome(config) };
}

async function stopRetainedSandboxes(worker) {
  const names = worker
    .docker(
      [
        'ps',
        '-q',
        '--filter',
        `label=layerfs.project=${worker.status.project}`,
        '--filter',
        'label=layerfs.role=sandbox',
      ],
      false,
    )
    .split('\n')
    .filter(Boolean);
  const stopped = [];
  for (const id of names) {
    const record = worker.docker(['container', 'inspect', id])[0];
    assert.equal(
      record.Config.Labels['layerfs.project'],
      worker.status.project,
    );
    assert.equal(record.Config.Labels['layerfs.role'], 'sandbox');
    assert.equal(record.Image, worker.installed.image);
    worker.docker(['stop', '--time', '2', record.Id], false);
    stopped.push(record.Id);
  }
  return stopped;
}

async function closeObserved(active, observation) {
  try {
    await active.worker.close();
  } catch (error) {
    observation.retainedDrainError = {
      code: error.code,
      message: error.message,
    };
  }
  observation.stoppedRetainedContainers = await stopRetainedSandboxes(
    active.worker,
  );
}

async function uploadFault(point) {
  const { config, active } = await fixture(point);
  const observation = { name: point, passed: false };
  let reopened;
  try {
    const worker = active.worker;
    worker.upload = async (operationId, capture, wanted, metrics) => {
      async function* broken() {
        let number = 0;
        for await (const chunk of encodeCapture(capture, wanted, metrics)) {
          number++;
          if (number === 3 && point === 'corrupt-transfer') {
            const damaged = Buffer.from(chunk);
            damaged[0] ^= 1;
            yield damaged;
          } else if (number === 3 && point === 'truncated-transfer') {
            yield chunk.subarray(0, Math.floor(chunk.length / 2));
            worker.child.stdin.end();
            return;
          } else if (
            number === 3 &&
            point === 'worker-killed-during-transfer'
          ) {
            yield chunk.subarray(0, Math.floor(chunk.length / 2));
            worker.docker(['kill', '--signal', 'KILL', worker.name], false);
            return;
          } else yield chunk;
        }
      }

      return worker.request(
        {
          op: 'upload',
          args: { operationId },
          binaryBytes: captureWireBytes(capture, wanted),
        },
        { binary: broken() },
      );
    };
    let failed;
    try {
      await active.checkpoints.checkpoint(
        config.source,
        config.workspaceId,
        point,
        { quiescent: true },
      );
    } catch (error) {
      failed = error;
    }
    assert.ok(failed?.operationId);
    observation.failure = {
      code: failed.code,
      operationId: failed.operationId,
    };
    await closeObserved(active, observation);
    reopened = await openHome(config);
    const observed = await reopened.checkpoints.observe(failed.operationId);
    assert.notEqual(observed.state, 'confirmed');
    observation.observedWorkerState = observed.observedWorkerState;
    const actual = await reopened.worker.request({
      op: 'qualification.inspect',
      args: { operationId: failed.operationId },
    });
    assert.equal(actual.commits.length, 0);
    assert.equal(actual.namespace.generation, 0);
    await assert.rejects(
      reopened.worker.request({
        op: 'commit',
        args: { operationId: failed.operationId },
      }),
      { code: 'Unknown' },
    );
    await assert.rejects(
      reopened.checkpoints.checkpoint(
        config.source,
        config.workspaceId,
        'no-replay',
        { quiescent: true },
      ),
      { code: 'Unknown' },
    );
    const restored = await administer({
      ...config,
      action: 'restore',
      operationId: failed.operationId,
      fromNative: true,
    });
    assert.equal(
      (await scanTree(restored.root)).digest,
      observed.binding.digest,
    );
    observation.passed = true;
    observation.commitCount = actual.commits.length;
    observation.nativeCapture = observed.captureRoot;
  } catch (error) {
    observation.error = { code: error.code, message: error.message };
  } finally {
    if (reopened) await closeObserved(reopened, observation);
    report.cases.push(observation);
  }
}

async function commitFault(point, completed) {
  const { config, active } = await fixture(point),
    observation = { name: point, passed: false };
  let reopened;
  try {
    await active.worker.request({ op: 'qualification.fault', args: { point } });
    let failed;
    try {
      await active.checkpoints.checkpoint(
        config.source,
        config.workspaceId,
        point,
        { quiescent: true },
      );
    } catch (error) {
      failed = error;
    }
    assert.ok(failed?.operationId);
    observation.failure = {
      code: failed.code,
      operationId: failed.operationId,
    };
    await closeObserved(active, observation);
    reopened = await openHome(config);
    const observed = await reopened.checkpoints.observe(failed.operationId);
    const actual = await reopened.worker.request({
      op: 'qualification.inspect',
      args: { operationId: failed.operationId },
    });
    if (completed) {
      assert.equal(observed.state, 'confirmed');
      assert.equal(
        actual.commits.filter(
          (item) =>
            item.message ===
            `camp:${observed.binding.operationId}:${observed.binding.generation}:${observed.binding.digest}`,
        ).length,
        1,
      );
      const restored = await reopened.checkpoints.restore(
        failed.operationId,
        config.parent,
      );
      assert.equal(
        (await scanTree(restored.root)).digest,
        observed.binding.digest,
      );
    } else {
      assert.notEqual(observed.state, 'confirmed');
      assert.equal(actual.commits.length, 0);
      assert.equal(actual.namespace.generation, 0);
    }
    await assert.rejects(
      reopened.worker.request({
        op: 'commit',
        args: { operationId: failed.operationId },
      }),
      { code: 'Unknown' },
    );
    observation.observedState = observed.state;
    observation.observedWorkerState = observed.observedWorkerState;
    observation.commitCount = actual.commits.length;
    observation.passed = true;
    assert.ok(await loadCapture(observed.captureRoot));
  } catch (error) {
    observation.error = { code: error.code, message: error.message };
  } finally {
    if (reopened) await closeObserved(reopened, observation);
    else await closeObserved(active, observation);
    report.cases.push(observation);
  }
}

async function foreignNames(names, label) {
  const { config, active } = await fixture(label),
    observation = { name: label, passed: false };
  try {
    const captureRoot = path.join(config.home, 'foreign-capture');
    await mkdir(path.join(captureRoot, 'blobs'), {
      recursive: true,
      mode: 0o700,
    });
    const entries = [];
    for (const [index, name] of names.entries()) {
      const data = Buffer.from(`distinct Linux member ${index}`),
        sha256 = createHash('sha256').update(data).digest('hex');
      await writeFile(path.join(captureRoot, 'blobs', sha256), data, {
        mode: 0o600,
      });
      entries.push({
        path: name,
        type: 'file',
        mode: 0o600,
        bytes: data.length,
        sha256,
      });
    }
    entries.sort((a, b) =>
      Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)),
    );
    const manifest = { schema: 1, rootMode: 0o700, entries },
      digest = manifestDigest(manifest);
    await saveState(path.join(captureRoot, 'manifest.json'), manifest);
    await saveState(path.join(captureRoot, 'receipt.json'), {
      state: 'sealed',
      digest,
    });
    const record = await active.checkpoints.submitCapture(
      { root: captureRoot, manifest, digest },
      config.workspaceId,
      label,
    );
    await writeFile(path.join(config.parent, 'existing'), 'unchanged');
    await assert.rejects(
      active.checkpoints.restore(record.binding.operationId, config.parent),
      { code: 'NameConflict' },
    );
    assert.equal(
      await readFile(path.join(config.parent, 'existing'), 'utf8'),
      'unchanged',
    );
    assert.equal((await loadCapture(captureRoot)).digest, digest);
    observation.operationId = record.binding.operationId;
    observation.digest = digest;
    observation.passed = true;
  } catch (error) {
    observation.error = { code: error.code, message: error.message };
  } finally {
    await closeObserved(active, observation);
    report.cases.push(observation);
  }
}

async function coordinatorRestart() {
  const { config, active } = await fixture('coordinator-process-restart');
  await active.worker.close();
  const observation = {
    name: 'coordinator-process-restart',
    scope:
      'actual Mac checkpoint coordinator process; full packaged Runner restart is separate acceptance',
    passed: false,
  };
  let reopened, child;
  try {
    const input = path.join(config.dataRoot, 'coordinator.json');
    await saveState(input, config);
    child = spawn(
      process.execPath,
      [path.join(import.meta.dirname, 'qualify-fault-client.mjs'), input],
      {
        env: { PATH: process.env.PATH, HOME: config.dataRoot },
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    child.stderr.resume();
    const ended = new Promise((resolve) => child.once('close', resolve));
    let ready, operation;
    for await (const line of createInterface({ input: child.stdout })) {
      const message = JSON.parse(line);
      if (message.phase === 'ready') ready = message;
      if (message.phase === 'commit-dispatched') {
        operation = message.operationId;
        break;
      }
    }
    assert.ok(ready && operation);
    const receiptFile = `${ready.linuxHome}/operations/${operation}.json`;
    let remote;
    for (let attempt = 0; attempt < 50; attempt++) {
      const result = await execute(
        'docker',
        [
          'exec',
          ready.container,
          'node',
          '-e',
          'const fs=require("fs");console.log(fs.readFileSync(process.argv[1],"utf8"))',
          receiptFile,
        ],
        { env: environment },
      );
      remote = JSON.parse(result.stdout);
      if (remote.state === 'verified') break;
      await pause(30);
    }
    assert.equal(remote.state, 'verified');
    child.kill('SIGKILL');
    await ended;
    for (let attempt = 0; attempt < 50; attempt++) {
      try {
        await execute('docker', ['inspect', ready.container], {
          env: environment,
        });
      } catch {
        break;
      }
      await pause(100);
    }
    const local = await readState(
      path.join(config.home, 'checkpoints', 'operations', `${operation}.json`),
    );
    assert.equal(local.state, 'commit-dispatched');
    reopened = await openHome(config);
    const observed = await reopened.checkpoints.observe(operation);
    assert.equal(observed.state, 'confirmed');
    const actual = await reopened.worker.request({
      op: 'qualification.inspect',
      args: { operationId: operation },
    });
    assert.equal(
      actual.commits.filter((item) => item.message.includes(operation)).length,
      1,
    );
    observation.operationId = operation;
    observation.commitCount = 1;
    observation.passed = true;
  } catch (error) {
    observation.error = { code: error.code, message: error.message };
  } finally {
    if (child && child.exitCode === null) child.kill('SIGKILL');
    if (reopened) await closeObserved(reopened, observation);
    report.cases.push(observation);
  }
}

try {
  for (const point of [
    'corrupt-transfer',
    'truncated-transfer',
    'worker-killed-during-transfer',
  ]) {
    await uploadFault(point);
    await saveState(path.join(root, 'report.json'), report);
  }
  for (const point of [
    'apply-failure',
    'before-commit',
    'after-commit-before-receipt',
    'drop-commit-reply',
  ]) {
    await commitFault(
      point,
      ['after-commit-before-receipt', 'drop-commit-reply'].includes(point),
    );
    await saveState(path.join(root, 'report.json'), report);
  }
  await foreignNames(['A', 'a'], 'actual-apfs-case-conflict');
  await foreignNames(['é', 'e\u0301'], 'actual-apfs-unicode-conflict');
  await coordinatorRestart();
  report.passed = report.cases.every((item) => item.passed);
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  await saveState(path.join(root, 'report.json'), report);
  if (!report.passed) process.exitCode = 1;
  process.stdout.write(
    `Actual Mac fault qualification ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
}
