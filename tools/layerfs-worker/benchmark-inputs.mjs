import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { lstat, readFile, readlink, realpath } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { hashFile } from '../../scripts/release.mjs';
import {
  readMacWorkerIdentity,
  validateMacWorkerReport,
} from '../../scripts/mac-worker-evidence.mjs';
import { offlineSetupChecks } from '../../scripts/setup-progress.mjs';

export const workloads = [
  'text',
  '10000-files',
  '128-MiB-binary',
  'dependencies-links',
];
export const checkpointPhases = ['full', 'hot', 'incremental', 'unchanged'];
export const packageArms = ['native-local-full-tree', 'streamed'];
export const excludedPackageArms = [
  {
    arm: 'upstream',
    reason:
      'The host upstream SDK/build context is not shipped in the qualified runtime; source-only comparison is excluded.',
  },
];
const sha = /^[a-f0-9]{64}$/;
const digest = (bytes) => createHash('sha256').update(bytes).digest('hex');
const inside = (parent, child) => child.startsWith(`${parent}${path.sep}`);

function manifestTarget(name, symlinks) {
  let target = name;
  const links = new Map();
  for (let count = 0; count < 40; count++) {
    const parts = target.split('/');
    const prefix = parts
      .map((_, index) => parts.slice(0, index + 1).join('/'))
      .find((value) => Object.hasOwn(symlinks, value));
    if (!prefix) return { target, links };
    const spelling = symlinks[prefix];
    assert.ok(
      typeof spelling === 'string' &&
        !path.posix.isAbsolute(spelling) &&
        !spelling.includes('\\'),
    );
    links.set(prefix, spelling);
    target = path.posix.normalize(
      path.posix.join(
        path.posix.dirname(prefix),
        spelling,
        ...parts.slice(prefix.split('/').length),
      ),
    );
    assert.ok(
      !path.posix.isAbsolute(target) &&
        target !== '..' &&
        !target.startsWith('../'),
    );
  }
  throw new Error('Runtime manifest contains a cyclic dependency alias.');
}

export function benchmarkOptions(values) {
  const packaged = Boolean(
    values.qualification ||
    values['online-report'] ||
    values.packages ||
    values['setup-series'],
  );
  for (const key of [
    'output-base',
    'qualification',
    'online-report',
    'packages',
    'docker-binary',
    'docker-socket',
  ])
    if (values[key])
      assert.ok(path.isAbsolute(values[key]), `--${key} must be absolute.`);
  const explicitEngine = Boolean(
    values['docker-binary'] ||
    values['docker-socket'] ||
    values['engine-profile'],
  );
  if (packaged || explicitEngine) {
    assert.ok(
      values['docker-binary'] && values['docker-socket'],
      'Choose an explicit Docker binary and socket.',
    );
    assert.match(
      values['engine-profile'] ?? '',
      /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$/,
    );
  }
  if (packaged)
    assert.ok(
      values.qualification && values['online-report'],
      'Package mode requires the complete qualification and its exact online report.',
    );
  if (packaged)
    assert.ok(
      values['output-base'],
      'Package mode requires an explicit private --output-base outside application ancestry.',
    );
  if (explicitEngine)
    assert.ok(
      packaged,
      'Explicit engine options require package mode; source mode retains its existing configuration.',
    );
  const setupSeries = values['setup-series'] || 'none';
  assert.ok(
    ['none', 'native-local', 'layerfs-worker', 'both'].includes(setupSeries),
  );
  if (setupSeries !== 'none')
    assert.ok(
      values.packages,
      'Offline setup series requires --packages from the qualifier campaign.',
    );
  return {
    packaged,
    explicitEngine,
    rounds: values.smoke ? 1 : 3,
    setupSeries,
  };
}

export async function boundedJSON(file, maximum = 32 << 20) {
  const info = await lstat(file);
  assert.ok(
    info.isFile() && info.size > 0 && info.size <= maximum,
    'Expected bounded regular JSON input.',
  );
  const bytes = await readFile(file);
  return { value: JSON.parse(bytes), sha256: digest(bytes) };
}

export function onlineBenchmarkFixture(qualification, bytes) {
  assert.equal(
    digest(bytes),
    qualification.scenarios.online.reportSHA256,
    'Online fixture report differs from its qualification.',
  );
  const online = JSON.parse(bytes);
  assert.equal(online.status, 'passed');
  assert.equal(online.mode, 'online');
  assert.equal(online.workspaceBackend, 'layerfs-worker');
  assert.equal(online.platform, 'darwin');
  assert.equal(online.architecture, 'arm64');
  assert.equal(online.version, qualification.identity.version);
  assert.equal(online.sourceCommit, qualification.identity.sourceCommit);
  assert.equal(
    online.archiveSHA256,
    qualification.identity.packages.installer.sha256,
  );
  assert.ok(path.isAbsolute(online.retainedFixtureDirectory));
  assert.ok(
    path.basename(online.retainedFixtureDirectory).startsWith('camp-release-'),
  );
  return online.retainedFixtureDirectory;
}

export async function verifyBenchmarkRuntime(runtime, identity, nodeVersion) {
  runtime = await realpath(runtime);
  const owner = await lstat(runtime);
  assert.ok(
    owner.isDirectory() &&
      owner.uid === process.getuid() &&
      !(owner.mode & 0o077),
    'Use the retained private fixture runtime.',
  );
  const { value: manifest, sha256 } = await boundedJSON(
    path.join(runtime, 'manifest.json'),
  );
  assert.equal(sha256, identity.runtimeManifestSHA256);
  assert.equal(manifest.schema, 2);
  assert.equal(manifest.role, 'runner-runtime');
  assert.equal(manifest.platform, 'darwin/arm64');
  assert.equal(manifest.node, nodeVersion);
  assert.equal(manifest.checkpointWorker.image, identity.worker.image);
  assert.equal(manifest.checkpointWorker.commit, identity.worker.sdkCommit);
  assert.equal(manifest.checkpointWorker.version, identity.worker.sdkVersion);
  for (const name of [
    'client.mjs',
    'checkpoint.mjs',
    'tree.mjs',
    'transfer.mjs',
    'protocol.mjs',
    'state.mjs',
    'installed.json',
    'pin.json',
  ])
    assert.ok(
      identity.controllerFiles[`runtime/layerfs-worker/${name}`],
      `Missing qualified controller ${name}.`,
    );
  assert.ok(
    identity.controllerFiles['runtime/layerfs-runtime/pin.json'],
    'Missing qualified SDK pin.',
  );
  const files = { ...identity.controllerFiles };
  for (const name of ['bin/node', 'bin/camp-sandbox'])
    files[name] = manifest.files[name]?.sha256;
  const dshPackage =
    'runtime/dsh-runtime/node_modules/@deepseek-ai/dsh/package.json';
  const nativeInputs = [
    dshPackage,
    ...[
      'coordination',
      'task-inputs',
      'discovery',
      'filesystem',
      'shell',
      'transport',
    ].map((name) => `runtime/dsh-runtime/camp-tools/${name}.mjs`),
  ];
  for (const name of nativeInputs) {
    const resolved = manifestTarget(name, manifest.symlinks || {});
    for (const [alias, spelling] of resolved.links)
      assert.equal(await readlink(path.join(runtime, alias)), spelling);
    assert.equal(
      await realpath(path.join(runtime, name)),
      path.join(runtime, resolved.target),
    );
    files[resolved.target] = manifest.files[resolved.target]?.sha256;
  }
  for (const [name, expected] of Object.entries(files)) {
    assert.ok(!path.isAbsolute(name) && !name.split(/[\\/]/).includes('..'));
    assert.match(expected ?? '', sha);
    const file = path.join(runtime, name),
      info = await lstat(file);
    assert.ok(
      info.isFile() && info.uid === process.getuid() && !(info.mode & 0o022),
      'Runtime inputs must remain owned regular files.',
    );
    assert.equal(await realpath(file), file);
    assert.equal(info.size, manifest.files[name].bytes);
    assert.equal(manifest.files[name].sha256, expected);
    assert.equal(
      await hashFile(file),
      expected,
      `Changed runtime input ${name}.`,
    );
    if (name.startsWith('bin/')) assert.ok(info.mode & 0o100);
  }
  const controller = path.join(runtime, 'runtime/layerfs-worker');
  const installed = (await boundedJSON(path.join(controller, 'installed.json')))
    .value;
  const sdk = (
    await boundedJSON(path.join(runtime, 'runtime/layerfs-runtime/pin.json'))
  ).value;
  assert.equal(installed.image, identity.worker.image);
  assert.equal(installed.commit, identity.worker.sdkCommit);
  assert.equal(installed.version, identity.worker.sdkVersion);
  const pin = (await boundedJSON(path.join(controller, 'pin.json'))).value;
  assert.equal(installed.platform, 'linux/arm64');
  assert.equal(installed.protocol, pin.protocol);
  assert.equal(pin.platform, 'linux/arm64');
  assert.equal(sdk.commit, identity.worker.sdkCommit);
  assert.equal(sdk.version, identity.worker.sdkVersion);
  assert.equal(
    (await boundedJSON(path.join(runtime, dshPackage))).value.version,
    manifest.dsh,
  );
  return {
    runtime,
    controller,
    installation: path.join(controller, 'installed.json'),
    node: path.join(runtime, 'bin/node'),
    nodeVersion,
    verifiedRuntimeFiles: files,
  };
}

export async function verifyBenchmarkEnvelope(inputs, engineSocket) {
  const runtime = await verifyBenchmarkRuntime(
    inputs.runtime,
    inputs.identity,
    inputs.nodeVersion,
  );
  for (const key of ['runtime', 'controller', 'installation', 'node'])
    assert.equal(
      inputs[key],
      runtime[key],
      `Package input lost its exact ${key} binding.`,
    );
  assert.ok(path.isAbsolute(inputs.socket));
  assert.equal(
    engineSocket,
    inputs.socket,
    'Child socket differs from its package input.',
  );
  const docker = await verifiedDockerBinary(inputs.dockerBinary);
  assert.equal(inputs.resolvedDockerBinary, docker.resolvedDockerBinary);
  assert.equal(inputs.dockerBinarySHA256, docker.dockerBinarySHA256);
  return { ...inputs, ...runtime, ...docker };
}

export async function packageBenchmarkInputs(values) {
  const qualified = await boundedJSON(values.qualification, 16 << 20);
  const qualification = validateMacWorkerReport(qualified.value, {
    identity: qualified.value.identity,
  });
  assert.equal(values['engine-profile'], qualification.engine.profile);
  const onlineInfo = await lstat(values['online-report']);
  assert.ok(
    onlineInfo.isFile() && onlineInfo.size > 0 && onlineInfo.size <= 16 << 20,
  );
  const fixture = await realpath(
    onlineBenchmarkFixture(
      qualification,
      await readFile(values['online-report']),
    ),
  );
  const owner = await lstat(fixture);
  assert.ok(
    path.basename(fixture).startsWith('camp-release-') &&
      owner.isDirectory() &&
      owner.uid === process.getuid() &&
      !(owner.mode & 0o077),
  );
  const runtime = await realpath(
    path.join(
      fixture,
      "Runner's private data/runtime/cache",
      qualification.identity.packages.runtime.sha256,
    ),
  );
  assert.ok(
    inside(fixture, runtime),
    'The runtime must belong to this retained controlled fixture.',
  );
  const verified = await verifyBenchmarkRuntime(
    runtime,
    qualification.identity,
    qualification.driver.tools.node,
  );
  const docker = await verifiedDockerBinary(values['docker-binary']);
  const socket = await realpath(values['docker-socket']);
  assert.ok((await lstat(socket)).isSocket());
  return {
    ...verified,
    identity: qualification.identity,
    serverBinarySHA256: qualification.serverBinarySHA256,
    offlineEntrypointBinarySHA256:
      qualification.scenarios.offline.entrypointBinarySHA256,
    qualificationSHA256: qualified.sha256,
    onlineReportSHA256: qualification.scenarios.online.reportSHA256,
    engine: qualification.engine,
    ...docker,
    socket,
    engineProfile: values['engine-profile'],
    fixture,
    setupEvidence: Object.fromEntries(
      ['online', 'offline'].map((name) => [
        name,
        {
          samples: 1,
          scope:
            'Retained qualification sample; different installation modes are not pooled into three repetitions.',
          reportSHA256: qualification.scenarios[name].reportSHA256,
          timings: qualification.scenarios[name].timings,
        },
      ]),
    ),
  };
}

export async function verifiedDockerBinary(selected) {
  assert.ok(path.isAbsolute(selected));
  const resolvedDockerBinary = await realpath(selected);
  const binary = await lstat(resolvedDockerBinary);
  assert.ok(binary.isFile() && binary.mode & 0o111);
  // OrbStack and other multicall clients use the selected entry's basename.
  return {
    dockerBinary: selected,
    resolvedDockerBinary,
    dockerBinarySHA256: await hashFile(resolvedDockerBinary),
  };
}

export function verifyPackageWorker(worker, inputs) {
  const expected = inputs.identity.worker;
  assert.equal(worker.status.image, expected.image);
  assert.equal(worker.status.sourceCommit, expected.sdkCommit);
  assert.equal(worker.status.sdkVersion, expected.sdkVersion);
  assert.equal(worker.status.buildId, expected.buildId);
  assert.equal(digest(worker.engine.id), inputs.engine.identitySHA256);
  for (const key of [
    'architecture',
    'kernel',
    'operatingSystem',
    'dockerVersion',
    'cpus',
    'memoryBytes',
  ])
    assert.equal(
      worker.engine[key],
      inputs.engine[key],
      `Selected engine changed ${key}.`,
    );
}

export function verifyPackageEngine(info, inputs) {
  assert.equal(digest(info.ID), inputs.engine.identitySHA256);
  for (const [key, observed] of Object.entries({
    architecture: info.Architecture,
    kernel: info.KernelVersion,
    operatingSystem: info.OperatingSystem,
    storageDriver: info.Driver,
    dockerVersion: info.ServerVersion,
    cpus: info.NCPU,
    memoryBytes: info.MemTotal,
  }))
    assert.equal(
      observed,
      inputs.engine[key],
      `Selected engine changed ${key}.`,
    );
}

export async function verifyBenchmarkArchives(directory, identity) {
  for (const item of Object.values(identity.packages)) {
    const filename = path.join(directory, item.filename),
      info = await lstat(filename);
    assert.ok(info.isFile());
    assert.equal(info.size, item.bytes);
    assert.equal(await hashFile(filename), item.sha256);
    assert.equal(
      await readFile(`${filename}.sha256`, 'utf8'),
      `${item.sha256}  ${item.filename}\n`,
    );
  }
  assert.deepEqual(await readMacWorkerIdentity(directory), identity);
}

export function verifySetupOnlyReport(report, inputs, backend) {
  assert.equal(report.schema, 1);
  assert.equal(report.status, 'passed');
  assert.equal(report.mode, 'offline-setup-only');
  assert.equal(report.workspaceBackend, backend);
  assert.equal(report.version, inputs.identity.version);
  assert.equal(report.sourceCommit, inputs.identity.sourceCommit);
  assert.equal(report.archiveSHA256, inputs.identity.packages.offline.sha256);
  assert.equal(report.runtimeSHA256, inputs.identity.packages.runtime.sha256);
  assert.equal(
    report.runtimeManifestSHA256,
    inputs.identity.runtimeManifestSHA256,
  );
  assert.equal(report.cleanupConfirmed, true);
  assert.equal(report.setupVerified, true);
  assert.deepEqual(report.model, { paid: false, requests: 0 });
  assert.equal(
    report.entrypointBinarySHA256,
    inputs.offlineEntrypointBinarySHA256,
  );
  assert.deepEqual(report.checks, offlineSetupChecks);
  for (const key of ['setupMs', 'repeatSetupMs'])
    assert.ok(Number.isFinite(report.timings[key]) && report.timings[key] >= 0);
  assert.ok(
    report.timings.setupPhases.some(
      (row) => row.phase === 'Runner self-test' && row.status === 'complete',
    ),
  );
}

export function benchmarkSetupComplete(samples, { setupSeries, rounds }) {
  const backends =
    setupSeries === 'both'
      ? ['native-local', 'layerfs-worker']
      : setupSeries === 'none'
        ? []
        : [setupSeries];
  const expected = new Set(
    backends.flatMap((backend) =>
      Array.from({ length: rounds }, (_, index) => `${backend}:${index + 1}`),
    ),
  );
  for (const sample of samples) {
    assert.ok(
      expected.delete(`${sample.backend}:${sample.iteration}`),
      'Repeated or unexpected installation sample.',
    );
    assert.equal(sample.status, 'passed');
    assert.equal(sample.modelRequests, 0);
    assert.match(sample.reportSHA256, sha);
  }
  assert.equal(expected.size, 0, 'Missing independent installation samples.');
}

export function verifyNativeComparison(report, events, fixtures) {
  const test = 'TestNativeLocalComparisonMeasurements';
  assert.ok(
    events.some((event) => event.Test === test && event.Action === 'pass'),
    'Native comparison must actually PASS.',
  );
  assert.ok(
    !events.some(
      (event) =>
        event.Action === 'fail' ||
        (event.Action === 'skip' && event.Test?.startsWith(test)),
    ),
    'Failed or skipped native comparison cannot qualify.',
  );
  assert.equal(report.schema, 1);
  assert.equal(report.passed, true);
  assert.equal(report.observations.length, fixtures.length);
  const expected = new Map(
    fixtures.map((item) => [`${item.workload}:${item.phase}`, item]),
  );
  assert.equal(expected.size, fixtures.length);
  for (const row of report.observations) {
    const key = `${row.fixture.workload}:${row.fixture.phase}`,
      fixture = expected.get(key);
    assert.ok(
      fixture && expected.delete(key),
      'Unexpected or repeated native result.',
    );
    for (const name of ['workload', 'iteration', 'phase', 'source', 'digest'])
      assert.equal(row.fixture[name], fixture[name]);
    if (row.error) {
      assert.equal(fixture.workload, 'dependencies-links');
      assert.equal(row.unsupported, true);
      assert.equal(row.unsupportedCode, 'native-prepared-link-outside-project');
      assert.equal(row.error, 'Prepared dependency link escapes the project');
    } else {
      assert.equal(Boolean(row.unsupported), false);
      assert.match(row.archiveDigest ?? '', sha);
      for (const name of ['prepareMs', 'captureMs'])
        assert.ok(Number.isFinite(row[name]) && row[name] >= 0);
    }
  }
  assert.equal(expected.size, 0);
}

// Late diagnostic failures must not erase a completed phase or its original
// action error. They remain qualification failures alongside the retained row.
export async function sampleBenchmarkDiagnostic(row, field, sample) {
  try {
    row[field] = await sample();
  } catch (error) {
    const failure = {
      field,
      code: error?.code || 'ObservationUnavailable',
      message: error instanceof Error ? error.message : String(error),
    };
    row.samplingError ??= failure;
    (row.samplingErrors ??= []).push(failure);
  }
}

export function benchmarkRoundCoverage(round, { packaged, smoke, root }) {
  assert.equal(round.schema, 1);
  assert.equal(round.passed, true);
  assert.ok(
    Number.isInteger(round.iteration) &&
      round.iteration >= 1 &&
      round.iteration <= 3,
  );
  assert.ok(!round.error && !round.closeError);
  const names = smoke ? workloads.slice(0, 1) : workloads;
  const arms = packaged ? packageArms : [...packageArms, 'upstream'];
  const expected = new Set();
  for (const workload of names) {
    expected.add(
      JSON.stringify([workload, '', 'synthetic-fixture-generation']),
    );
    for (const phase of checkpointPhases)
      for (const arm of ['native-sealing', ...arms])
        expected.add(JSON.stringify([workload, arm, phase]));
    for (const phase of ['restore', 'fork'])
      for (const arm of arms)
        expected.add(JSON.stringify([workload, arm, phase]));
  }
  for (const row of round.observations) {
    assert.ok(
      !row.error && !row.samplingError,
      'Failed samples cannot qualify a comparison.',
    );
    assert.ok(Number.isFinite(row.elapsedMs) && row.elapsedMs >= 0);
    const key = JSON.stringify([row.workload, row.arm || '', row.phase]);
    assert.ok(expected.delete(key), 'Duplicate or unexpected measured tuple.');
    if (row.phase !== 'synthetic-fixture-generation') assert.ok(row.result);
    if (row.result?.status === 'unsupported')
      assert.ok(
        !packaged &&
          row.arm === 'upstream' &&
          row.workload === 'dependencies-links',
      );
  }
  assert.equal(expected.size, 0, 'Missing required measured tuples.');
  const fixtures = new Map();
  for (const item of round.nativeFixtures) {
    assert.equal(item.iteration, round.iteration);
    assert.ok(
      names.includes(item.workload) && checkpointPhases.includes(item.phase),
    );
    assert.match(item.digest, sha);
    assert.ok(
      path.isAbsolute(item.source) &&
        inside(
          path.join(root, `round-${round.iteration}`),
          path.resolve(item.source),
        ),
    );
    const key = `${item.workload}:${item.phase}`;
    assert.ok(!fixtures.has(key), 'Duplicate native input fixture.');
    fixtures.set(key, item);
  }
  assert.equal(fixtures.size, names.length * checkpointPhases.length);
  for (const workload of names) {
    assert.equal(
      fixtures.get(`${workload}:full`).digest,
      fixtures.get(`${workload}:hot`).digest,
    );
    assert.equal(
      fixtures.get(`${workload}:incremental`).digest,
      fixtures.get(`${workload}:unchanged`).digest,
    );
    assert.notEqual(
      fixtures.get(`${workload}:full`).digest,
      fixtures.get(`${workload}:incremental`).digest,
    );
  }
  for (const row of round.observations) {
    if (checkpointPhases.includes(row.phase)) {
      const expectedDigest = fixtures.get(
        `${row.workload}:${row.phase}`,
      ).digest;
      if (row.arm === 'native-sealing')
        assert.equal(row.result.digest, expectedDigest);
      else {
        assert.equal(row.expectedDigest, expectedDigest);
        if (row.arm === 'streamed') {
          assert.equal(row.result.state, 'confirmed');
          assert.equal(row.result.binding?.digest, expectedDigest);
          assert.equal(
            row.result.workerReceipt?.verifiedDigest,
            expectedDigest,
          );
        } else if (row.result.status !== 'unsupported')
          assert.equal(row.result.digest, expectedDigest);
      }
    } else if (row.phase === 'restore' || row.phase === 'fork') {
      const expectedDigest = fixtures.get(`${row.workload}:unchanged`).digest;
      assert.equal(row.expectedDigest, expectedDigest);
      if (row.arm === 'streamed' && row.phase === 'fork') {
        assert.equal(row.result.state, 'confirmed');
        assert.equal(row.result.binding?.digest, expectedDigest);
      }
    }
  }
  if (packaged) assert.deepEqual(round.excludedArms, excludedPackageArms);
  return round;
}

export function benchmarkRoundsComplete(rounds, options) {
  assert.equal(rounds.length, options.smoke ? 1 : 3);
  rounds.forEach((round, index) => {
    assert.equal(round.iteration, index + 1);
    benchmarkRoundCoverage(round, options);
  });
  return rounds.flatMap((round) => round.nativeFixtures);
}
