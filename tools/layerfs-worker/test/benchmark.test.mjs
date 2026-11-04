import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  symlink,
  stat,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { test } from 'node:test';
import { fileURLToPath, URL } from 'node:url';
import { promisify } from 'node:util';
import {
  benchmarkOptions,
  benchmarkRoundCoverage,
  benchmarkRoundsComplete,
  benchmarkSetupComplete,
  checkpointPhases,
  excludedPackageArms,
  excludedCheckpointArms,
  onlineBenchmarkFixture,
  packageArms,
  sampleBenchmarkDiagnostic,
  verifyBenchmarkRuntime,
  verifyBenchmarkEnvelope,
  verifiedDockerBinary,
  verifyNativeComparison,
  verifyPackageEngine,
  verifyPackageWorker,
  verifySetupOnlyReport,
  workloads,
} from '../benchmark-inputs.mjs';
import {
  completeOfflineSetupReport,
  offlineSetupChecks,
  offlineSetupOnly,
  retainSetupAttempt,
} from '../../../scripts/setup-progress.mjs';

const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const hash = 'a'.repeat(64);
const root = '/private/controlled-comparison';
const options = {
  qualification: '/private/qualification.json',
  'online-report': '/private/online.json',
  'docker-binary': '/private/docker',
  'docker-socket': '/private/docker.sock',
  'engine-profile': 'orbstack',
  'output-base': '/private/reports',
};

function round(iteration, packaged = true) {
  const observations = [],
    nativeFixtures = [];
  const arms = packaged ? packageArms : [...packageArms, 'upstream'];
  for (const workload of workloads) {
    observations.push({
      workload,
      phase: 'synthetic-fixture-generation',
      elapsedMs: 1,
    });
    for (const phase of checkpointPhases) {
      const digest =
        phase === 'full' || phase === 'hot' ? hash : 'b'.repeat(64);
      for (const arm of ['native-sealing', ...arms])
        observations.push({
          workload,
          phase,
          arm,
          elapsedMs: 1,
          ...(arm === 'native-sealing' ? {} : { expectedDigest: digest }),
          result:
            arm === 'streamed'
              ? {
                  state: 'confirmed',
                  binding: { digest },
                  workerReceipt: { verifiedDigest: digest },
                }
              : { digest },
        });
      nativeFixtures.push({
        workload,
        iteration,
        phase,
        source: `${root}/round-${iteration}/${workload}/${phase === 'full' || phase === 'hot' ? 'original' : 'delta'}`,
        digest: phase === 'full' || phase === 'hot' ? hash : 'b'.repeat(64),
      });
    }
    for (const phase of ['restore', 'fork'])
      for (const arm of arms)
        observations.push({
          workload,
          phase,
          arm,
          elapsedMs: 1,
          expectedDigest: 'b'.repeat(64),
          result:
            arm === 'streamed' && phase === 'fork'
              ? { state: 'confirmed', binding: { digest: 'b'.repeat(64) } }
              : { restored: true },
        });
  }
  return {
    schema: 1,
    iteration,
    passed: true,
    observations,
    nativeFixtures,
    ...(packaged ? { excludedArms: excludedPackageArms } : {}),
  };
}

test('package benchmark requires complete explicit inputs and preserves existing source mode', async () => {
  assert.deepEqual(benchmarkOptions({}), {
    packaged: false,
    checkpointOnly: false,
    explicitEngine: false,
    rounds: 3,
    setupSeries: 'none',
  });
  assert.equal(benchmarkOptions(options).rounds, 3);
  assert.equal(benchmarkOptions({ ...options, smoke: true }).rounds, 1);
  assert.equal(
    benchmarkOptions({
      ...options,
      packages: '/private/packages',
      'setup-series': 'both',
    }).setupSeries,
    'both',
  );
  for (const key of [
    'qualification',
    'online-report',
    'docker-binary',
    'docker-socket',
    'engine-profile',
    'output-base',
  ]) {
    const incomplete = { ...options };
    delete incomplete[key];
    assert.throws(() => benchmarkOptions(incomplete));
  }
  assert.throws(() => benchmarkOptions({ ...options, 'setup-series': 'both' }));
  assert.throws(() =>
    benchmarkOptions({ ...options, 'docker-socket': 'relative.sock' }),
  );
  const result = await promisify(execFile)(
    process.execPath,
    [fileURLToPath(new URL('../benchmark.mjs', import.meta.url)), '--help'],
    { env: {}, timeout: 10000 },
  );
  assert.match(result.stdout, /--qualification/);
});

test('focused source campaigns require an explicit engine/helper and retain complete tuples without claiming package acceptance', () => {
  const focused = {
    'checkpoint-only': true,
    'sync-helper': '/private/helper',
    'docker-binary': '/private/docker',
    'docker-socket': '/private/docker.sock',
    'engine-profile': 'orbstack',
    'output-base': root,
  };
  assert.equal(benchmarkOptions(focused).checkpointOnly, true);
  assert.throws(() => benchmarkOptions({ ...options, ...focused }));
  for (const key of [
    'sync-helper',
    'docker-binary',
    'docker-socket',
    'engine-profile',
    'output-base',
  ]) {
    const partial = { ...focused };
    delete partial[key];
    assert.throws(() => benchmarkOptions(partial));
  }
  const measured = round(1);
  measured.scope = 'source-checkpoint-only';
  measured.excludedArms = excludedCheckpointArms;
  measured.synchronization = { policy: 'staged-full-sync', helperSHA256: hash };
  benchmarkRoundCoverage(measured, { checkpointOnly: true, root });
  const missing = JSON.parse(JSON.stringify(measured));
  missing.observations.pop();
  assert.throws(() =>
    benchmarkRoundCoverage(missing, { checkpointOnly: true, root }),
  );
  const mislabeled = JSON.parse(JSON.stringify(measured));
  mislabeled.packageIdentity = {};
  assert.throws(() =>
    benchmarkRoundCoverage(mislabeled, { checkpointOnly: true, root }),
  );
});

test('retained online fixture is selected only through its exact qualified report bytes and package identity', () => {
  const online = {
    status: 'passed',
    mode: 'online',
    workspaceBackend: 'layerfs-worker',
    platform: 'darwin',
    architecture: 'arm64',
    version: 'v0.1.0-alpha.11',
    sourceCommit: 'b'.repeat(40),
    archiveSHA256: hash,
    retainedFixtureDirectory: '/private/camp-release-online',
  };
  const bytes = JSON.stringify(online);
  const qualification = {
    identity: {
      version: online.version,
      sourceCommit: online.sourceCommit,
      packages: { installer: { sha256: hash } },
    },
    scenarios: { online: { reportSHA256: sha(bytes) } },
  };
  assert.equal(
    onlineBenchmarkFixture(qualification, bytes),
    online.retainedFixtureDirectory,
  );
  assert.throws(() => onlineBenchmarkFixture(qualification, `${bytes} `));
  for (const changed of [
    { status: 'failed' },
    { mode: 'offline' },
    { workspaceBackend: 'native-local' },
    { architecture: 'x64' },
    { sourceCommit: 'c'.repeat(40) },
    { retainedFixtureDirectory: '/private/enrolled-runner' },
  ]) {
    const altered = JSON.stringify({ ...online, ...changed });
    assert.throws(() =>
      onlineBenchmarkFixture(
        {
          ...qualification,
          scenarios: { online: { reportSHA256: sha(altered) } },
        },
        altered,
      ),
    );
  }
});

async function runtimeFixture(t) {
  const base = await realpath(
    await mkdtemp(path.join(tmpdir(), 'camp-release-benchmark-')),
  );
  t.after(() => rm(base, { recursive: true, force: true }));
  const runtime = path.join(base, 'runtime');
  await mkdir(runtime, { mode: 0o700 });
  const worker = {
    image: `sha256:${hash}`,
    sdkCommit: 'b'.repeat(40),
    sdkVersion: '1.1.0',
    buildId: 'c'.repeat(64),
  };
  const files = {},
    controllerFiles = {};
  const put = async (name, content, mode = 0o600) => {
    const file = path.join(runtime, name);
    await mkdir(path.dirname(file), { recursive: true, mode: 0o700 });
    await writeFile(file, content, { mode });
    files[name] = { sha256: sha(content), bytes: Buffer.byteLength(content) };
    if (
      name.startsWith('runtime/layerfs-worker/') ||
      name === 'runtime/layerfs-runtime/pin.json'
    )
      controllerFiles[name] = files[name].sha256;
  };
  for (const name of [
    'client.mjs',
    'checkpoint.mjs',
    'tree.mjs',
    'transfer.mjs',
    'protocol.mjs',
    'state.mjs',
  ])
    await put(
      `runtime/layerfs-worker/${name}`,
      'export const fixture = true;\n',
    );
  await put(
    'runtime/layerfs-worker/installed.json',
    JSON.stringify({
      image: worker.image,
      commit: worker.sdkCommit,
      version: worker.sdkVersion,
      protocol: 1,
      platform: 'linux/arm64',
    }),
  );
  await put(
    'runtime/layerfs-worker/pin.json',
    JSON.stringify({ protocol: 1, platform: 'linux/arm64' }),
  );
  await put(
    'runtime/layerfs-runtime/pin.json',
    JSON.stringify({ commit: worker.sdkCommit, version: worker.sdkVersion }),
  );
  for (const name of ['bin/node', 'bin/camp-sandbox'])
    await put(name, 'synthetic executable fixture\n', 0o700);
  await put(
    'runtime/dsh-runtime/sdk/package.json',
    JSON.stringify({ version: '0.2.1-rc.3' }),
  );
  for (const name of [
    'coordination',
    'task-inputs',
    'discovery',
    'filesystem',
    'shell',
    'transport',
  ])
    await put(
      `runtime/dsh-runtime/camp-tools/${name}.mjs`,
      'export const nativeFixture = true;\n',
    );
  const alias = 'runtime/dsh-runtime/node_modules/@deepseek-ai/dsh';
  await mkdir(path.dirname(path.join(runtime, alias)), {
    recursive: true,
    mode: 0o700,
  });
  await symlink('../../sdk', path.join(runtime, alias));
  const manifest = {
    schema: 2,
    role: 'runner-runtime',
    platform: 'darwin/arm64',
    node: '24.16.0',
    dsh: '0.2.1-rc.3',
    symlinks: { [alias]: '../../sdk' },
    checkpointWorker: {
      image: worker.image,
      commit: worker.sdkCommit,
      version: worker.sdkVersion,
    },
    files,
  };
  const body = JSON.stringify(manifest);
  await writeFile(path.join(runtime, 'manifest.json'), body, { mode: 0o600 });
  return {
    runtime,
    worker,
    manifest,
    identity: { worker, controllerFiles, runtimeManifestSHA256: sha(body) },
  };
}

test('package runtime verification binds controller, SDK pin, platform and used executable hashes without reading enrolled configuration', async (t) => {
  const f = await runtimeFixture(t);
  const selected = await verifyBenchmarkRuntime(
    f.runtime,
    f.identity,
    '24.16.0',
  );
  assert.equal(
    selected.installation,
    path.join(f.runtime, 'runtime/layerfs-worker/installed.json'),
  );
  assert.equal(selected.node, path.join(f.runtime, 'bin/node'));
  await assert.rejects(
    verifyBenchmarkRuntime(
      f.runtime,
      { ...f.identity, runtimeManifestSHA256: 'f'.repeat(64) },
      '24.16.0',
    ),
  );
  await assert.rejects(
    verifyBenchmarkRuntime(f.runtime, f.identity, 'wrong-node'),
  );
  await writeFile(
    path.join(f.runtime, 'runtime/layerfs-worker/client.mjs'),
    'tampered',
  );
  await assert.rejects(
    verifyBenchmarkRuntime(f.runtime, f.identity, '24.16.0'),
  );
});

test('runtime verification refuses redirected or writable inputs and foreign worker identity', async (t) => {
  const f = await runtimeFixture(t);
  const file = path.join(f.runtime, 'runtime/layerfs-worker/tree.mjs');
  await chmod(file, 0o666);
  await assert.rejects(
    verifyBenchmarkRuntime(f.runtime, f.identity, '24.16.0'),
  );
  await chmod(file, 0o600);
  const saved = await readFile(file);
  await rm(file);
  const outside = path.join(path.dirname(f.runtime), 'outside.mjs');
  await writeFile(outside, saved);
  await symlink(outside, file);
  await assert.rejects(
    verifyBenchmarkRuntime(f.runtime, f.identity, '24.16.0'),
  );
  await rm(file);
  await writeFile(file, saved, { mode: 0o600 });
  await assert.rejects(
    verifyBenchmarkRuntime(
      f.runtime,
      {
        ...f.identity,
        worker: { ...f.worker, image: `sha256:${'e'.repeat(64)}` },
      },
      '24.16.0',
    ),
  );
});

test('package child refuses partial envelopes, alternate controllers and socket/default Docker fallback', async (t) => {
  const f = await runtimeFixture(t);
  const runtime = await verifyBenchmarkRuntime(
    f.runtime,
    f.identity,
    '24.16.0',
  );
  const executable = path.join(path.dirname(f.runtime), 'multicall');
  const selected = path.join(path.dirname(f.runtime), 'docker');
  await writeFile(executable, 'synthetic client', { mode: 0o700 });
  await symlink(executable, selected);
  const docker = await verifiedDockerBinary(selected);
  const input = {
    ...runtime,
    ...docker,
    identity: f.identity,
    socket: '/private/synthetic.sock',
  };
  assert.equal(
    (await verifyBenchmarkEnvelope(input, input.socket)).controller,
    runtime.controller,
  );
  for (const key of [
    'controller',
    'installation',
    'dockerBinary',
    'dockerBinarySHA256',
    'node',
  ]) {
    const partial = { ...input };
    delete partial[key];
    await assert.rejects(verifyBenchmarkEnvelope(partial, input.socket));
  }
  await assert.rejects(
    verifyBenchmarkEnvelope(
      { ...input, controller: '/source/controller' },
      input.socket,
    ),
  );
  await assert.rejects(
    verifyBenchmarkEnvelope(input, '/private/other-engine.sock'),
  );
});

test('native Go fingerprint inputs and their shipped dependency alias remain checksum-bound', async (t) => {
  const f = await runtimeFixture(t);
  await writeFile(
    path.join(f.runtime, 'runtime/dsh-runtime/camp-tools/coordination.mjs'),
    'changed native helper',
  );
  await assert.rejects(
    verifyBenchmarkRuntime(f.runtime, f.identity, '24.16.0'),
  );
  const g = await runtimeFixture(t);
  const alias = path.join(
    g.runtime,
    'runtime/dsh-runtime/node_modules/@deepseek-ai/dsh',
  );
  await rm(alias);
  await symlink('../../../other-sdk', alias);
  await assert.rejects(
    verifyBenchmarkRuntime(g.runtime, g.identity, '24.16.0'),
  );
});

test('actual selected engine and worker cannot drift from exact qualified image, SDK, build and profile', () => {
  const info = {
    ID: 'synthetic-engine',
    Architecture: 'arm64',
    KernelVersion: 'test-kernel',
    OperatingSystem: 'test-engine',
    Driver: 'overlay2',
    ServerVersion: '29.4.0',
    NCPU: 4,
    MemTotal: 2 ** 30,
  };
  const engine = {
    identitySHA256: sha(info.ID),
    architecture: info.Architecture,
    kernel: info.KernelVersion,
    operatingSystem: info.OperatingSystem,
    storageDriver: info.Driver,
    dockerVersion: info.ServerVersion,
    cpus: info.NCPU,
    memoryBytes: info.MemTotal,
  };
  const expected = {
    image: `sha256:${hash}`,
    sdkCommit: 'b'.repeat(40),
    sdkVersion: '1.1.0',
    buildId: 'c'.repeat(64),
  };
  const inputs = { identity: { worker: expected }, engine };
  verifyPackageEngine(info, inputs);
  const worker = {
    status: {
      image: expected.image,
      sourceCommit: expected.sdkCommit,
      sdkVersion: expected.sdkVersion,
      buildId: expected.buildId,
    },
    engine: { id: info.ID, ...engine },
  };
  verifyPackageWorker(worker, inputs);
  for (const changed of [
    { ID: 'another-engine' },
    { Architecture: 'x86_64' },
    { KernelVersion: 'changed' },
    { MemTotal: 1 },
  ])
    assert.throws(() => verifyPackageEngine({ ...info, ...changed }, inputs));
  assert.throws(() =>
    verifyPackageWorker(
      { ...worker, status: { ...worker.status, buildId: 'f'.repeat(64) } },
      inputs,
    ),
  );
});

test('Docker integrity verification preserves the selected multicall invocation alias', async (t) => {
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), 'camp-benchmark-client-')),
  );
  t.after(() => rm(root, { recursive: true, force: true }));
  const executable = path.join(root, 'multicall'),
    selected = path.join(root, 'docker');
  await writeFile(executable, 'synthetic executable', { mode: 0o700 });
  await symlink(executable, selected);
  const checked = await verifiedDockerBinary(selected);
  assert.equal(checked.dockerBinary, selected);
  assert.equal(checked.resolvedDockerBinary, executable);
  assert.equal(checked.dockerBinarySHA256, sha('synthetic executable'));
  await chmod(executable, 0o600);
  await assert.rejects(verifiedDockerBinary(selected));
});

test('comparison coverage requires every exact tuple and all three complete same-input fixture rounds', () => {
  const packageRound = round(1);
  assert.equal(packageRound.observations.length, 68);
  assert.equal(round(1, false).observations.length, 92);
  benchmarkRoundCoverage(round(1, false), { packaged: false, root });
  assert.equal(
    benchmarkRoundsComplete([round(1), round(2), round(3)], {
      packaged: true,
      root,
    }).length,
    48,
  );
  assert.throws(() =>
    benchmarkRoundsComplete([round(1)], { packaged: true, root }),
  );
  for (const change of [
    (r) => {
      r.observations.pop();
    },
    (r) => {
      r.observations[1] = r.observations[2];
    },
    (r) => {
      r.observations[1].samplingError = {};
    },
    (r) => {
      r.observations[1].result = undefined;
    },
    (r) => {
      r.nativeFixtures[0].source = '/private/enrolled/fixture';
    },
    (r) => {
      r.nativeFixtures[0].digest = 'wrong';
    },
    (r) => {
      r.nativeFixtures[1].digest = 'c'.repeat(64);
    },
    (r) => {
      r.nativeFixtures.pop();
    },
    (r) => {
      r.excludedArms = [];
    },
    (r) => {
      r.closeError = {};
    },
    (r) => {
      r.observations.find(
        (item) => item.arm === 'streamed' && item.phase === 'unchanged',
      ).result.binding.digest = 'f'.repeat(64);
    },
  ]) {
    const altered = globalThis.structuredClone(packageRound);
    change(altered);
    assert.throws(() =>
      benchmarkRoundCoverage(altered, { packaged: true, root }),
    );
  }
});

test('late diagnostic failures retain the completed row and original action failure', async () => {
  const input = round(1);
  const row = input.observations.find((item) => item.arm === 'streamed');
  const original = {
    code: 'CommitUnconfirmed',
    message: 'Retain the original operation.',
  };
  row.error = original;
  await sampleBenchmarkDiagnostic(row, 'dockerSocketWire', async () => {
    throw new Error('Owned wire counter closed.');
  });
  await sampleBenchmarkDiagnostic(row, 'afterResources', async () => {
    throw new Error('Worker sampling became unavailable.');
  });
  assert.equal(row.error, original);
  assert.equal(row.result.state, 'confirmed');
  assert.deepEqual(
    row.samplingErrors.map(({ field }) => field),
    ['dockerSocketWire', 'afterResources'],
  );
  assert.equal(row.samplingError, row.samplingErrors[0]);
  assert.throws(() =>
    benchmarkRoundCoverage(input, { packaged: true, smoke: false, root }),
  );
  delete row.error;
  assert.throws(() =>
    benchmarkRoundCoverage(input, { packaged: true, smoke: false, root }),
  );
  const healthy = {};
  await sampleBenchmarkDiagnostic(healthy, 'dockerSocketWire', async () => ({
    received: 23,
  }));
  assert.deepEqual(healthy, { dockerSocketWire: { received: 23 } });
});

test('native aggregate requires an actual Go PASS and exact results, refusing skips and blanket unsupported errors', () => {
  const fixtures = round(1).nativeFixtures;
  const report = {
    schema: 1,
    passed: true,
    observations: fixtures.map((fixture) => ({
      fixture,
      prepareMs: 1,
      captureMs: 1,
      archiveDigest: hash,
    })),
  };
  const events = [
    { Test: 'TestNativeLocalComparisonMeasurements', Action: 'pass' },
  ];
  verifyNativeComparison(report, events, fixtures);
  assert.throws(() => verifyNativeComparison(report, [], fixtures));
  assert.throws(() =>
    verifyNativeComparison(
      report,
      [
        ...events,
        { Test: 'TestNativeLocalComparisonMeasurements', Action: 'skip' },
      ],
      fixtures,
    ),
  );
  const unsupported = globalThis.structuredClone(report);
  const row = unsupported.observations.find(
    (item) => item.fixture.workload === 'dependencies-links',
  );
  Object.assign(row, { error: 'permission failed', unsupported: true });
  assert.throws(() => verifyNativeComparison(unsupported, events, fixtures));
  Object.assign(row, {
    error: 'Prepared dependency link escapes the project',
    unsupportedCode: 'native-prepared-link-outside-project',
  });
  verifyNativeComparison(unsupported, events, fixtures);
  const partial = { ...report, observations: report.observations.slice(1) };
  assert.throws(() => verifyNativeComparison(partial, events, fixtures));
});

test('offline setup-only has a separate zero-model scope and cannot pass before confirmed cleanup', () => {
  assert.equal(offlineSetupOnly('offline', '1'), true);
  assert.equal(offlineSetupOnly('online', undefined), false);
  for (const [mode, value] of [
    ['online', '1'],
    ['previous-upgrade', '1'],
    ['offline', '0'],
    [undefined, '1'],
  ])
    assert.throws(() => offlineSetupOnly(mode, value));
  const identity = {
    version: 'v0.1.0-alpha.11',
    sourceCommit: 'b'.repeat(40),
    packages: { offline: { sha256: hash }, runtime: { sha256: hash } },
    runtimeManifestSHA256: hash,
  };
  const inputs = { identity, offlineEntrypointBinarySHA256: hash };
  const initial = {
    schema: 1,
    mode: 'offline-setup-only',
    setupVerified: true,
    checks: offlineSetupChecks,
    version: identity.version,
    sourceCommit: identity.sourceCommit,
    archiveSHA256: hash,
    runtimeSHA256: hash,
    runtimeManifestSHA256: hash,
    entrypointBinarySHA256: hash,
    workspaceBackend: 'native-local',
    timings: {
      setupMs: 1,
      repeatSetupMs: 1,
      setupPhases: [{ phase: 'Runner self-test', status: 'complete' }],
    },
  };
  const complete = completeOfflineSetupReport(initial, {
    cleanupConfirmed: true,
    modelRequests: 0,
  });
  verifySetupOnlyReport(complete, inputs, 'native-local');
  for (const change of [
    { cleanupConfirmed: false, modelRequests: 0 },
    { cleanupConfirmed: undefined, modelRequests: 0 },
    { cleanupConfirmed: true, modelRequests: 1 },
  ])
    assert.equal(completeOfflineSetupReport(initial, change).status, 'failed');
  assert.equal(
    completeOfflineSetupReport(
      { ...initial, checks: [] },
      { cleanupConfirmed: true, modelRequests: 0 },
    ).status,
    'failed',
  );
  assert.throws(() =>
    verifySetupOnlyReport(
      { ...complete, mode: 'offline' },
      inputs,
      'native-local',
    ),
  );
  assert.throws(() =>
    verifySetupOnlyReport(complete, inputs, 'layerfs-worker'),
  );
  const samples = ['native-local', 'layerfs-worker'].flatMap((backend) =>
    [1, 2, 3].map((iteration) => ({
      backend,
      iteration,
      status: 'passed',
      modelRequests: 0,
      reportSHA256: hash,
    })),
  );
  benchmarkSetupComplete(samples, { setupSeries: 'both', rounds: 3 });
  assert.throws(() =>
    benchmarkSetupComplete(samples.slice(1), {
      setupSeries: 'both',
      rounds: 3,
    }),
  );
  assert.throws(() =>
    benchmarkSetupComplete([...samples.slice(0, -1), samples[0]], {
      setupSeries: 'both',
      rounds: 3,
    }),
  );
  assert.throws(() =>
    benchmarkSetupComplete([{ ...samples[0], status: 'running' }], {
      setupSeries: 'native-local',
      rounds: 1,
    }),
  );
});

test('setup attempts retain private failure output and duration across explicit reconstruction setup', async (t) => {
  const directory = await mkdtemp(path.join(tmpdir(), 'camp-setup-attempts-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const output =
    'private synthetic registration value\nRunner self-test: failed; elapsed 2s\n';
  const initial = await retainSetupAttempt(directory, 'initial', {
    exit: 1,
    elapsedMs: 2075,
    output,
  });
  const next = await retainSetupAttempt(directory, 'post-reconstruction', {
    exit: 0,
    elapsedMs: 400,
    output: 'Runner self-test: complete; elapsed 0.4s\n',
  });
  assert.equal(initial.elapsedMs, 2075);
  assert.equal(initial.phases[0].status, 'failed');
  assert.equal(next.phases[0].status, 'complete');
  assert.equal(
    JSON.stringify(initial).includes('private synthetic registration value'),
    false,
  );
  assert.equal(
    await readFile(path.join(directory, initial.privateLog), 'utf8'),
    output,
  );
  assert.equal(
    (await stat(path.join(directory, initial.privateLog))).mode & 0o777,
    0o600,
  );
  await assert.rejects(
    retainSetupAttempt(directory, 'initial', {
      exit: 0,
      elapsedMs: 1,
      output: 'replacement',
    }),
    { code: 'EEXIST' },
  );
  assert.equal(
    await readFile(path.join(directory, initial.privateLog), 'utf8'),
    output,
  );
  assert.equal(
    JSON.parse(
      await readFile(path.join(directory, 'initial-setup.json'), 'utf8'),
    ).exit,
    1,
  );
});
