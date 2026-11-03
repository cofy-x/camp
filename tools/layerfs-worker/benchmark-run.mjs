import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawn, execFileSync } from 'node:child_process';
import { createCipheriv, createHash } from 'node:crypto';
import {
  chmod,
  link,
  mkdir,
  rename,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import process from 'node:process';
import { clearTimeout, setTimeout } from 'node:timers';
import { pathToFileURL } from 'node:url';
import { saveState } from './state.mjs';
import { stop } from '../../scripts/lib.mjs';
import {
  boundedJSON,
  excludedPackageArms,
  packageArms,
  sampleBenchmarkDiagnostic,
  verifyBenchmarkRuntime,
  verifyBenchmarkEnvelope,
  verifyPackageWorker,
} from './benchmark-inputs.mjs';

let inputs;
if (process.env.CAMP_MAC_BENCHMARK_PACKAGE_INPUTS) {
  const input = await boundedJSON(
    process.env.CAMP_MAC_BENCHMARK_PACKAGE_INPUTS,
  );
  assert.equal(
    input.sha256,
    process.env.CAMP_MAC_BENCHMARK_PACKAGE_INPUTS_SHA256,
  );
  inputs = await verifyBenchmarkEnvelope(input.value, process.argv[3]);
}
const controller = inputs ? inputs.controller : import.meta.dirname;
const loadController = (name) =>
  import(pathToFileURL(path.join(controller, name)));
const { MacCheckpoints } = await loadController('checkpoint.mjs');
const { WorkerClient } = await loadController('client.mjs');
const { CaptureBlobs, captureTree, restoreTree } =
  await loadController('tree.mjs');
const { receiveCapture } = await loadController('transfer.mjs');
const { openUpstream, measureCodec } = inputs
  ? {}
  : await import('./upstream-comparison.mjs');

const [base, engineSocket, number] = process.argv.slice(2),
  iteration = Number(number);
const root = path.join(base, `round-${iteration}`);
await mkdir(root, { mode: 0o700 });
const report = {
  schema: 1,
  iteration,
  observations: [],
  nativeFixtures: [],
  passed: false,
  ...(inputs
    ? { packageIdentity: inputs.identity, excludedArms: excludedPackageArms }
    : {}),
  host: {
    platform: process.platform,
    architecture: process.arch,
    release: os.release(),
    cpu: os.cpus()[0].model,
    logicalCpus: os.cpus().length,
    memoryBytes: os.totalmem(),
    loadAtStart: os.loadavg(),
  },
  measurement: {
    cpu: 'Mac process.resourceUsage deltas; excludes subprocess CPU, Linux cgroup v2 costs collected separately before each Sandbox stops',
    memory:
      'Mac process lifetime maxRSS and point RSS; Linux lifetime cgroup peaks include file cache; no double counting with VM allocation',
    wire: 'exact application frames plus exact bidirectional selected Docker Unix socket bytes; excludes VM/hypervisor network framing and Linux-local Docker traffic',
    timing:
      'phase wall time includes required capture, hashes, transport, apply, Commit and readback; atomic upstream fs.apply includes Commit, so those two times remain combined',
    cache:
      'OS and engine caches not flushed; each repetition has a fresh owned Store, reused across arms and phases in that repetition; first full is a new Branch from main; VM already running',
  },
};
let proxy;
const proxyMessage = (action) =>
  new Promise((resolve, reject) => {
    const finish = (error, value) => {
      clearTimeout(timer);
      proxy.removeListener('error', failed);
      proxy.removeListener('close', closed);
      proxy.removeListener('message', received);
      if (error) reject(error);
      else resolve(value);
    };
    const failed = () => finish(new Error('Owned wire counter failed.'));
    const closed = () =>
      finish(new Error('Owned wire counter closed before its reply.'));
    const received = (value) => finish(null, value);
    const timer = setTimeout(
      () => finish(new Error('Owned wire counter timed out.')),
      10000,
    );
    proxy.once('error', failed);
    proxy.once('close', closed);
    proxy.once('message', received);
    if (action)
      proxy.send({ action }, (error) => {
        if (error) failed();
      });
  });
const wireStats = () => proxyMessage('stats');
const difference = (before, after) =>
  Object.fromEntries(
    Object.keys(after).map((key) => [key, after[key] - before[key]]),
  );
let worker, upstream;

async function resources() {
  const start = performance.now();
  const cgroup = worker.docker([
    'exec',
    worker.name,
    'node',
    '/opt/camp/layerfs/worker/cost.mjs',
  ]);
  const disk = worker.docker(
    [
      'exec',
      worker.name,
      'du',
      '-sk',
      worker.config.home,
      `/var/lib/docker/volumes/${worker.status.volume}/_data`,
    ],
    false,
  );
  return {
    workerCgroup: cgroup,
    diskKiB: disk
      .trim()
      .split('\n')
      .map((line) => Number(line.split(/\s+/)[0])),
    samplingMs: performance.now() - start,
  };
}

async function measured(meta, action) {
  let beforeResource, beforeWire, beforeCpu, beforeUpstream;
  const start = performance.now();
  let value;
  try {
    beforeResource = await resources();
    beforeWire = await wireStats();
    beforeCpu = process.resourceUsage();
    if (upstream) beforeUpstream = { ...upstream.wire };
    meta.samplingBeforeMs = performance.now() - start;
    value = await action();
    return value;
  } catch (error) {
    meta.error = { code: error.code, message: error.message };
    throw error;
  } finally {
    meta.elapsedMs = performance.now() - start - (meta.samplingBeforeMs ?? 0);
    if (meta.sharedSealMs !== undefined)
      meta.includingSharedSealMs = meta.elapsedMs + meta.sharedSealMs;
    if (beforeCpu) meta.macCPU = difference(beforeCpu, process.resourceUsage());
    meta.macMaxRSSKiB = process.resourceUsage().maxRSS;
    meta.macMemoryBytes = process.memoryUsage();
    if (beforeUpstream)
      meta.upstreamWire = difference(beforeUpstream, upstream.wire);
    if (beforeWire)
      await sampleBenchmarkDiagnostic(meta, 'dockerSocketWire', async () =>
        difference(beforeWire, await wireStats()),
      );
    meta.beforeResources = beforeResource;
    await sampleBenchmarkDiagnostic(meta, 'afterResources', resources);
    if (value) meta.result = value;
    report.observations.push(meta);
    await saveState(path.join(root, 'report.json'), report);
    process.stdout.write(
      `round ${iteration} ${meta.workload} ${meta.arm} ${meta.phase}: ${meta.error ? 'failed' : `${Math.round(meta.elapsedMs)} ms`}\n`,
    );
  }
}

async function makeFixture(name, files, bytes) {
  const source = path.join(root, name, 'source');
  await mkdir(source, { recursive: true, mode: 0o700 });
  for (let index = 0; index < files; index++) {
    const data = Buffer.alloc(bytes),
      iv = Buffer.alloc(16);
    iv.writeUInt32BE(index, 12);
    if (name === 'text') data.fill(65 + index);
    else {
      const cipher = createCipheriv('aes-256-ctr', Buffer.alloc(32, 0x43), iv);
      cipher.update(data).copy(data);
    }
    if (bytes > 8) data.writeUInt32LE(index);
    await writeFile(
      path.join(source, `file-${String(index).padStart(5, '0')}.bin`),
      data,
      { mode: index === 3 ? 0o755 : 0o644 },
    );
  }
  if (name === 'dependencies-links') {
    await mkdir(path.join(source, '.git'), { mode: 0o700 });
    await writeFile(path.join(source, '.git/HEAD'), 'ref: refs/heads/main\n');
    await writeFile(path.join(source, '.gitignore'), 'node_modules\n');
    await mkdir(path.join(source, 'node_modules/.pnpm'), {
      recursive: true,
      mode: 0o750,
    });
    for (let index = 0; index < 256; index++) {
      const pkg = `pkg-${String(index).padStart(3, '0')}`;
      await mkdir(path.join(source, `node_modules/.pnpm/${pkg}`), {
        mode: 0o750,
      });
      await writeFile(
        path.join(source, `node_modules/.pnpm/${pkg}/index.mjs`),
        `export const value=${index};\n`,
      );
      await symlink(`.pnpm/${pkg}`, path.join(source, `node_modules/${pkg}`));
    }
    await mkdir(path.join(source, 'empty'), { mode: 0o700 });
    await symlink('/opaque/not-authorized', path.join(source, 'opaque-link'));
    await link(
      path.join(source, 'file-00004.bin'),
      path.join(source, 'hardlink-alias'),
    );
  }
  return source;
}

async function delta(source) {
  await writeFile(
    path.join(source, 'file-00000.bin'),
    Buffer.from('changed binary\0\xff', 'binary'),
  );
  await rm(path.join(source, 'file-00001.bin'));
  await rename(
    path.join(source, 'file-00002.bin'),
    path.join(source, 'renamed.bin'),
  );
  await chmod(path.join(source, 'file-00003.bin'), 0o644);
}

async function upstreamChanges(previous, capture) {
  const old = new Map(previous?.entries.map((row) => [row.path, row]) ?? []),
    next = new Map(capture.manifest.entries.map((row) => [row.path, row]));
  if (
    [...next.values()].some((row) => !['file', 'directory'].includes(row.type))
  )
    return null;
  const changes = [];
  const blobs = await CaptureBlobs.open(capture);
  try {
    for (const row of [...old.values()].reverse())
      if (!next.has(row.path) || next.get(row.path).type !== row.type)
        changes.push({ op: 'rm', path: `/${row.path}` });
    for (const row of next.values()) {
      const before = old.get(row.path);
      if (row.type === 'directory' && before?.type !== 'directory')
        changes.push({ op: 'mkdir', path: `/${row.path}`, mode: row.mode });
      else if (
        row.type === 'file' &&
        (before?.type !== 'file' || before.sha256 !== row.sha256)
      )
        changes.push({
          op: 'write',
          path: `/${row.path}`,
          mode: row.mode,
          bytes: await blobs.buffer(row),
        });
      else if (before?.mode !== row.mode)
        changes.push({ op: 'chmod', path: `/${row.path}`, mode: row.mode });
    }
    return changes;
  } finally {
    await blobs.close();
  }
}

async function mounted(branch, action) {
  const sandbox = (await upstream.client.sandbox.create()).sandbox,
    workspace = await upstream.client.workspace.mount(sandbox, branch);
  const records = await upstream.client.project.list_sandboxes(1000, 0),
    record = records.items.find((row) => row.sandbox === sandbox);
  const container = worker.docker(['inspect', record.name])[0];
  assert.equal(container.Image, worker.installed.image);
  assert.equal(
    container.Config.Labels['layerfs.project'],
    worker.status.project,
  );
  try {
    return await action(workspace, container.Id);
  } finally {
    await upstream.client.workspace.unmount(workspace);
    await upstream.client.sandbox.close(sandbox);
  }
}

async function upstreamScan(branch) {
  return mounted(branch, async (workspace, container) => {
    const scan = worker.docker([
      'exec',
      '--user',
      '501:20',
      container,
      'node',
      '/opt/camp/layerfs/worker/helper.mjs',
      'scan',
      workspace.path,
    ]);
    const costs = worker.docker([
      'exec',
      container,
      'node',
      '/opt/camp/layerfs/worker/cost.mjs',
    ]);
    return { ...scan, sandboxCgroup: costs };
  });
}

async function upstreamRestore(branch, capture) {
  const restored = await mounted(branch, async (workspace, container) => {
    const child = spawn(
      'docker',
      [
        'exec',
        '--user',
        '501:20',
        container,
        'node',
        '/opt/camp/layerfs/worker/helper.mjs',
        'export',
        workspace.path,
        capture.digest,
      ],
      { env: upstream.dockerEnvironment, stdio: ['ignore', 'pipe', 'pipe'] },
    );
    let diagnostic = '';
    child.stderr.on('data', (chunk) => {
      diagnostic = `${diagnostic}${chunk}`.slice(-8192);
    });
    const end = new Promise((resolve) => child.once('close', resolve));
    const destination = path.join(
      root,
      `upstream-export-${report.observations.length}`,
    );
    await mkdir(destination, { mode: 0o700 });
    let returned;
    try {
      returned = await receiveCapture(child.stdout, destination, {
        digest: capture.digest,
        smallFilePack: true,
      });
    } catch (error) {
      error.message = `${error.message}; export exit ${await end}; ${diagnostic}`;
      throw error;
    }
    assert.equal(await end, 0);
    return { root: destination, ...returned };
  });
  return restoreTree(restored, path.join(root, 'restores'));
}

try {
  proxy = spawn(
    process.execPath,
    [path.join(import.meta.dirname, 'wire-proxy.mjs'), engineSocket],
    {
      env: { PATH: process.env.PATH, HOME: root },
      stdio: ['ignore', 'ignore', 'inherit', 'ipc'],
    },
  );
  const ready = await proxyMessage();
  const opening = performance.now();
  worker = await WorkerClient.open({
    home: path.join(root, 'worker'),
    socket: ready.socket,
    qualification: true,
    ...(inputs
      ? { installation: inputs.installation, dockerBinary: inputs.dockerBinary }
      : {}),
  });
  if (inputs) verifyPackageWorker(worker, inputs);
  report.worker = worker.status;
  report.engine = worker.engine;
  report.coldWorkerProcessMs = performance.now() - opening;
  report.sdkWorkerStartupMs = worker.startupMs;
  if (!inputs) {
    upstream = await openUpstream(worker, root);
    report.upstreamStartupMs = upstream.startupMs;
    report.upstreamTransport = upstream.transport;
  }
  const checkpoints = new MacCheckpoints({
    root: path.join(root, 'checkpoints'),
    worker,
    runnerId: 'controlled-benchmark-runner',
    storageId: 'controlled-benchmark-storage',
  });
  await mkdir(path.join(root, 'restores'), { mode: 0o700 });
  report.backgroundContainers = worker
    .docker(['ps', '--format', '{{json .}}'], false)
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const row = JSON.parse(line);
      return { image: row.Image, state: row.State, status: row.Status };
    });
  for (const [workload, files, bytes] of [
    ['text', 6, 128],
    ['10000-files', 10000, 128],
    ['128-MiB-binary', 16, 8 << 20],
    ['dependencies-links', 6, 128],
  ]) {
    if (process.argv.includes('smoke') && workload !== 'text') continue;
    const prep = performance.now();
    let source = await makeFixture(workload, files, bytes);
    report.observations.push({
      workload,
      phase: 'synthetic-fixture-generation',
      elapsedMs: performance.now() - prep,
      excludedFromCheckpoint: true,
    });
    const namespace = createHash('sha256')
      .update(`${iteration}:${workload}`)
      .digest('hex');
    let lastStream,
      branch = 'main',
      previous,
      initialCapture;
    const disabled = new Set();
    for (const phase of ['full', 'hot', 'incremental', 'unchanged']) {
      if (phase === 'incremental') {
        const preparing = performance.now();
        const input = await restoreTree(
          initialCapture,
          path.join(root, 'delta-sources'),
        );
        source = input.root;
        await delta(source);
        report.fixtureMaterializations ??= [];
        report.fixtureMaterializations.push({
          workload,
          elapsedMs: performance.now() - preparing,
          metrics: input.metrics,
          scope:
            'retained full and delta inputs for identical Go baseline; separate from checkpoint arms',
        });
      }
      // One actual seal supplies the identical immutable capture to each arm.
      // Report its CPU/IO independently and add its wall time explicitly to
      // each end-to-end cost; never infer a separate unmeasured seal sample.
      const frozen = await measured(
        { workload, phase, arm: 'native-sealing' },
        () => captureTree(source, path.join(root, 'shared-captures')),
      );
      const sharedSealMs = report.observations.at(-1).elapsedMs;
      if (phase === 'full') initialCapture = frozen;
      report.nativeFixtures.push({
        workload,
        iteration,
        phase,
        source,
        digest: frozen.digest,
        inputCaptureMetrics: frozen.metrics,
      });
      const arms = inputs ? packageArms : [...packageArms, 'upstream'];
      const offset = (iteration - 1) % arms.length;
      const order = [...arms.slice(offset), ...arms.slice(0, offset)];
      for (const arm of order) {
        if (disabled.has(arm)) {
          report.observations.push({
            workload,
            phase,
            arm,
            armOrder: order,
            error: {
              code: 'UncertainPriorPhase',
              message:
                'Prior failed outcome retained; no operation is blindly replayed.',
            },
          });
          continue;
        }
        await measured(
          {
            workload,
            phase,
            arm,
            expectedDigest: frozen.digest,
            sharedSealMs,
          },
          async () => {
            const capture = frozen;
            if (arm === 'native-local-full-tree')
              return {
                capture: capture.metrics,
                digest: capture.digest,
                scope:
                  'shared complete-tree capture profile; actual native-local publication/Prepare measured separately by Go',
              };
            if (arm === 'streamed') {
              lastStream = await checkpoints.submitCapture(
                capture,
                namespace,
                `${phase}-${iteration}`,
              );
              return lastStream;
            }
            const built = performance.now(),
              changes = await upstreamChanges(previous, capture),
              materializeChangesMs = performance.now() - built;
            if (changes === null)
              return {
                status: 'unsupported',
                reason:
                  'The public fs.apply change set has no symlink or hardlink creation operation; no entries removed to create a timing.',
                capture: capture.metrics,
              };
            const forking = performance.now();
            branch = await upstream.fork(branch);
            const forkMs = performance.now() - forking;
            const before = { ...upstream.wire },
              writing = performance.now();
            const codec = await measureCodec(async () =>
              changes.length
                ? await upstream.client.fs.apply(
                    branch,
                    changes,
                    `Controlled ${workload} ${iteration} ${phase}`,
                  )
                : { kind: 'UpToDate', noMutationCalled: true },
            );
            const checkpoint = codec.value;
            const applyCommitMs = performance.now() - writing,
              checkpointWire = difference(before, upstream.wire),
              reading = performance.now(),
              scanned = await upstreamScan(branch);
            assert.equal(scanned.digest, capture.digest);
            previous = capture.manifest;
            return {
              capture: capture.metrics,
              digest: capture.digest,
              materializeChangesMs,
              codec: codec.metrics,
              forkMs,
              applyCommitMs,
              checkpoint,
              checkpointWire,
              readbackMs: performance.now() - reading,
              readback: scanned,
              applyMs: null,
              commitMs: null,
              note: 'fs.apply performs the atomic apply and Commit; independent remount uses the same trusted helper and Docker transport as the streamed arm.',
            };
          },
        ).catch(() => disabled.add(arm));
      }
    }
    if (lastStream) {
      const expectedDigest = lastStream.binding.digest;
      await measured(
        { workload, arm: 'streamed', phase: 'restore', expectedDigest },
        () =>
          checkpoints.restore(
            lastStream.binding.operationId,
            path.join(root, 'restores'),
          ),
      ).catch(() => {});
      await measured(
        { workload, arm: 'streamed', phase: 'fork', expectedDigest },
        () =>
          checkpoints.fork(
            lastStream.binding.operationId,
            createHash('sha256').update(`fork:${namespace}`).digest('hex'),
            'benchmark-fork',
          ),
      ).catch(() => {});
      const retained = await checkpoints.retained(
        lastStream.binding.operationId,
      );
      await measured(
        {
          workload,
          arm: 'native-local-full-tree',
          phase: 'restore',
          expectedDigest,
        },
        () => restoreTree(retained, path.join(root, 'restores')),
      ).catch(() => {});
      await measured(
        {
          workload,
          arm: 'native-local-full-tree',
          phase: 'fork',
          expectedDigest,
        },
        () => restoreTree(retained, path.join(root, 'restores')),
      ).catch(() => {});
      if (previous) {
        await measured(
          { workload, arm: 'upstream', phase: 'restore', expectedDigest },
          () => upstreamRestore(branch, retained),
        ).catch(() => {});
        await measured(
          { workload, arm: 'upstream', phase: 'fork', expectedDigest },
          async () => {
            const fork = await upstream.fork(branch),
              readback = await upstreamScan(fork);
            assert.equal(readback.digest, retained.digest);
            return { branch: fork, readback };
          },
        ).catch(() => {});
      } else if (!inputs) {
        for (const phase of ['restore', 'fork'])
          await measured(
            { workload, arm: 'upstream', phase, expectedDigest },
            async () => ({
              status: 'unsupported',
              reason:
                'No complete dependency/link checkpoint is representable through public fs.apply.',
            }),
          );
      }
    }
  }
  const expected =
    (process.argv.includes('smoke') ? 1 : 4) * (inputs ? 17 : 23);
  report.expectedObservations = expected;
  report.passed =
    report.observations.length === expected &&
    report.observations.some(
      (item) =>
        item.arm === 'streamed' && item.phase === 'restore' && item.result,
    ) &&
    !report.observations.some((item) => item.error || item.samplingError);
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  for (const [owner, close] of [
    ['upstream', () => upstream?.close()],
    ['worker', () => worker?.close()],
  ]) {
    try {
      await close();
    } catch (error) {
      report.closeError = { owner, code: error.code, message: error.message };
      (report.closeErrors ??= []).push(report.closeError);
      report.passed = false;
    }
  }
  if (proxy) {
    try {
      report.totalDockerSocketWire = await wireStats();
    } catch (error) {
      report.closeError = { owner: 'counter', message: error.message };
      report.passed = false;
    }
    try {
      if (proxy.exitCode === null && proxy.signalCode === null) {
        const end = new Promise((resolve, reject) => {
          const timer = setTimeout(() => {
            proxy.kill('SIGTERM');
            reject(new Error('Owned counter did not close.'));
          }, 10000);
          proxy.once('close', () => {
            clearTimeout(timer);
            resolve();
          });
          proxy.once('error', () => {
            clearTimeout(timer);
            reject(new Error('Owned counter failed to close.'));
          });
        });
        proxy.send({ action: 'close' }, (error) => {
          if (error) proxy.kill('SIGTERM');
        });
        await end;
      }
    } catch (error) {
      report.closeError = { owner: 'counter', message: error.message };
      report.passed = false;
      await stop(proxy);
    }
  }
  if (inputs) {
    try {
      await verifyBenchmarkRuntime(
        inputs.runtime,
        inputs.identity,
        inputs.nodeVersion,
      );
    } catch (error) {
      report.closeError = { owner: 'package-inputs', message: error.message };
      report.passed = false;
    }
  }
  report.host.loadAtEnd = os.loadavg();
  report.host.freeMemoryAtEnd = os.freemem();
  try {
    report.sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], {
      encoding: 'utf8',
    }).trim();
  } catch {
    report.closeError = {
      owner: 'driver',
      message: 'Qualification source identity became unavailable.',
    };
    report.passed = false;
  }
  await saveState(path.join(root, 'report.json'), report);
  if (!report.passed) process.exitCode = 1;
}
