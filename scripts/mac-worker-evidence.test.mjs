import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import {
  chmod,
  link,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, relative } from 'node:path';
import { test } from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import {
  digestJSON,
  copyMacWorkerBaseline,
  verifyMacWorkerArchive,
  macWorkerIdentity,
  macWorkerRecoverySummary,
  macWorkerScope,
  macWorkerScenarioSummary,
  macWorkerServiceCleanupAction,
  macWorkerStorageSummary,
  optionalMacWorkerEvidence,
  qualificationDrivers,
  packageScenarioNames,
  packageScenarioDrivers,
  packageScenarioFiles,
  readMacWorkerIdentity,
  validateMacWorkerReport,
  validateMacWorkerReuse,
  validateMacWorkerReuseFixture,
} from './mac-worker-evidence.mjs';

async function baselineCacheFixture(t) {
  const root = await realpath(
    await mkdtemp(join(tmpdir(), 'camp-baseline-cache-')),
  );
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const cache = join(root, 'cache'),
    destination = join(root, 'destination');
  await mkdir(cache, { mode: 0o700 });
  await mkdir(destination, { mode: 0o700 });
  const content = Buffer.from('fixed baseline bytes');
  const sha256 = createHash('sha256').update(content).digest('hex');
  const item = {
    filename: `camp-layerfs-worker-linux-arm64-${sha256}.tar.gz`,
    sha256,
    bytes: content.length,
  };
  await writeFile(join(cache, item.filename), content, { mode: 0o600 });
  await writeFile(
    join(cache, item.filename + '.sha256'),
    `${sha256}  ${item.filename}\n`,
    { mode: 0o600 },
  );
  return { root, cache, destination, content, item };
}

test('explicit baseline cache supplies only fully verified immutable artifact bytes', async (t) => {
  const f = await baselineCacheFixture(t);
  await copyMacWorkerBaseline(f.cache, f.destination, f.item);
  await verifyMacWorkerArchive(f.destination, f.item);
  assert.deepEqual(
    await readFile(join(f.destination, f.item.filename)),
    f.content,
  );
  assert.deepEqual(await readFile(join(f.cache, f.item.filename)), f.content);
  await assert.rejects(copyMacWorkerBaseline(f.cache, f.destination, f.item), {
    code: 'EEXIST',
  });
});

test('baseline cache rejects corruption, aliases, shared files and incomplete downloads', async (t) => {
  for (const kind of ['corrupt', 'short', 'symlink', 'hardlink', 'public'])
    await t.test(kind, async (t) => {
      const f = await baselineCacheFixture(t),
        source = join(f.cache, f.item.filename);
      if (kind === 'corrupt')
        await writeFile(source, Buffer.alloc(f.content.length, 0x78));
      if (kind === 'short') await writeFile(source, 'partial');
      if (kind === 'symlink') {
        await rm(source);
        await writeFile(join(f.root, 'outside'), f.content);
        await symlink(join(f.root, 'outside'), source);
      }
      if (kind === 'hardlink') await link(source, join(f.root, 'shared'));
      if (kind === 'public') await chmod(source, 0o644);
      await assert.rejects(
        copyMacWorkerBaseline(f.cache, f.destination, f.item),
      );
    });
});

test('the package campaign requires an explicit baseline before host or engine activity', async () => {
  await assert.rejects(
    promisify(execFile)(
      process.execPath,
      [
        fileURLToPath(new URL('./qualify-mac-worker.mjs', import.meta.url)),
        '--packages',
        '/unused/candidate',
        '--docker-binary',
        '/unused/docker',
        '--docker-socket',
        '/unused/socket',
        '--engine-profile',
        'fixture',
      ],
      { env: {}, timeout: 10000 },
    ),
    (error) => {
      assert.match(error.stderr, /Select an explicit fixed public --baseline/);
      return true;
    },
  );
});

test('reuse requires both explicit original report and campaign paths before host activity', async () => {
  for (const option of ['--reuse-report', '--reuse-directory'])
    await assert.rejects(
      promisify(execFile)(
        process.execPath,
        [
          fileURLToPath(new URL('./qualify-mac-worker.mjs', import.meta.url)),
          '--packages',
          '/unused/candidate',
          '--docker-binary',
          '/unused/docker',
          '--docker-socket',
          '/unused/socket',
          '--engine-profile',
          'fixture',
          option,
          '/unused/original',
        ],
        { env: {}, timeout: 10000 },
      ),
      (error) =>
        /Use --reuse-report and --reuse-directory together/.test(error.stderr),
    );
});

test('package driver scope covers every literal source import before its helper can change independently', async () => {
  const imports = (source) =>
    [
      ...source.matchAll(
        /^\s*(?:import|export)\s+(?:[^;'"`]*?\sfrom\s*)?(['"])([^'"]+)\1/gm,
      ),
      ...source.matchAll(/\bimport\(\s*(['"])([^'"]+)\1/g),
    ].map((match) => match[2]);
  assert.deepEqual(
    imports(
      "import x from './a.mjs';\nimport './b.mjs';\nconst c = import('./c.mjs');",
    ),
    ['./a.mjs', './b.mjs', './c.mjs'],
  );
  const directory = fileURLToPath(new URL('../', import.meta.url));
  for (const file of packageScenarioDrivers.filter((name) =>
    name.endsWith('.mjs'),
  )) {
    const source = new URL('../' + file, import.meta.url);
    for (const specifier of imports(await readFile(source, 'utf8'))) {
      if (specifier.startsWith('node:')) continue;
      assert.ok(
        specifier.startsWith('.'),
        `Declare the pinned dependency scope for ${file}: ${specifier}`,
      );
      const dependency = relative(
        directory,
        fileURLToPath(new URL(specifier, source)),
      );
      assert.ok(
        packageScenarioDrivers.includes(dependency),
        `Add ${dependency} to the package driver scope imported by ${file}.`,
      );
    }
  }
});

test('failed campaign cleanup uses complete structured service ownership and never guesses from installation alone', () => {
  const status = {
    schema: 1,
    platform: 'darwin/arm64',
    runnerId: 'fixture-runner',
    service: {
      supported: true,
      installed: false,
      loaded: false,
      running: false,
      loginEnabled: false,
      state: 'absent',
    },
  };
  assert.equal(macWorkerServiceCleanupAction(status), 'absent');
  for (const service of [
    { installed: true, state: 'stopped' },
    { installed: true, loaded: true, state: 'stopped' },
    { installed: true, loaded: true, running: true, state: 'running' },
  ])
    assert.equal(
      macWorkerServiceCleanupAction({
        ...status,
        service: { ...status.service, ...service },
      }),
      'remove',
    );
  for (const value of [
    undefined,
    {},
    { ...status, schema: 2 },
    { ...status, platform: 'linux/arm64' },
    { ...status, runnerId: '' },
    ...[
      { supported: false },
      { installed: undefined },
      { loaded: undefined },
      { loginEnabled: undefined },
      { state: 'unknown', installed: true },
      { state: 'ambiguous', installed: true, loaded: true },
      { installed: true },
      { state: 'stopped', loaded: true },
      { state: 'running', loaded: true, running: true },
      { state: 'running', installed: true, loaded: false, running: true },
      { state: 'stopped', installed: true, running: true },
      { loginEnabled: true },
    ].map((service) => ({
      ...status,
      service: { ...status.service, ...service },
    })),
  ])
    assert.equal(macWorkerServiceCleanupAction(value), 'uncertain');
});

function storageFixture(guarded = false) {
  return {
    complete: true,
    retention: { previewAllowed: !guarded, deletionSupported: false },
    lifecycle: {
      liveOverview: true,
      policyConfigured: true,
      advisoryWarning: true,
      protectedHistoryPreserved: true,
      ...(guarded
        ? {
            recoveryGuardPreserved: true,
            cleanupPlanBlocked: true,
            cleanupPlanError: 'RecoveryRequired',
          }
        : {
            quarantinePreservesBytes: true,
            restoredExactly: true,
            permanentlyPurged: true,
          }),
    },
  };
}

test('storage qualification summary retains outcome proof without private capture paths', () => {
  const storage = storageFixture();
  assert.deepEqual(
    macWorkerStorageSummary({
      ...storage,
      captures: [{ path: '/private/fixture/capture' }],
      recoveryIntent: '/private/fixture/recovery',
      retention: { ...storage.retention, privatePath: '/private/fixture' },
      lifecycle: { ...storage.lifecycle, path: '/private/fixture' },
    }),
    storage,
  );
});

test('reuse rejects aliased, redirected or public fixture data roots before any Runner command', async (t) => {
  const directory = await realpath(
    await mkdtemp(join(tmpdir(), 'camp-reuse-boundary-')),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  const fixtureRoot = join(directory, 'camp-release-online');
  const runner = join(fixtureRoot, "Runner's private data");
  const server = join(fixtureRoot, 'data');
  await mkdir(fixtureRoot, { mode: 0o700 });
  for (const child of [runner, server]) await mkdir(child, { mode: 0o700 });
  assert.equal(
    await validateMacWorkerReuseFixture(directory, 'online', fixtureRoot),
    fixtureRoot,
  );
  await assert.rejects(
    validateMacWorkerReuseFixture(
      directory,
      'online',
      directory + '/./camp-release-online',
    ),
    /canonical/,
  );
  const other = join(directory, 'another-installation');
  await mkdir(other, { mode: 0o700 });
  for (const child of [runner, server]) {
    await rm(child, { recursive: true });
    await symlink(other, child);
    await assert.rejects(
      validateMacWorkerReuseFixture(directory, 'online', fixtureRoot),
      /real directories/,
    );
    await rm(child);
    await mkdir(child, { mode: 0o700 });
    await chmod(child, 0o755);
    await assert.rejects(
      validateMacWorkerReuseFixture(directory, 'online', fixtureRoot),
      /private owned/,
    );
    await chmod(child, 0o700);
  }
  await rm(fixtureRoot, { recursive: true });
  await symlink(other, fixtureRoot);
  await assert.rejects(
    validateMacWorkerReuseFixture(directory, 'online', fixtureRoot),
    /real directories/,
  );
});

function fixture() {
  const sha = 'a'.repeat(64),
    source = 'b'.repeat(40),
    image = `sha256:${sha}`;
  const file = (name) => ({ filename: name, sha256: sha, bytes: 100 });
  const runner = {
    schema: 2,
    version: 'v0.1.0-alpha.11',
    sourceCommit: source,
    protocolVersion: 2,
    runners: {
      'darwin-arm64': {
        installer: file('camp-runner-darwin-arm64.tar.gz'),
        offline: file('camp-runner-offline-darwin-arm64.tar.gz'),
        runtime: {
          ...file(`camp-runtime-darwin-arm64-${sha}.tar.gz`),
          manifestSHA256: sha,
        },
      },
    },
  };
  const server = {
    schema: 1,
    version: runner.version,
    sourceCommit: source,
    servers: { 'darwin-arm64': file('camp-server-darwin-arm64.tar.gz') },
  };
  const worker = {
    schema: 1,
    platform: 'linux/arm64',
    version: '1.1.0',
    sourceCommit: 'c'.repeat(40),
    campSourceCommit: source,
    image,
    buildId: sha,
    artifact: file(`camp-layerfs-worker-linux-arm64-${sha}.tar.gz`),
  };
  const manifest = {
    schema: 2,
    platform: 'darwin/arm64',
    role: 'runner-runtime',
    checkpointWorker: {
      image,
      commit: worker.sourceCommit,
      version: worker.version,
      archiveSHA256: sha,
    },
    files: Object.fromEntries(
      [
        'installed.json',
        'client.mjs',
        'checkpoint.mjs',
        'tree.mjs',
        'sync.mjs',
        'administration.mjs',
        'home.mjs',
        'state.mjs',
        'protocol.mjs',
      ].map((name) => [
        `runtime/layerfs-worker/${name}`,
        { sha256: sha, bytes: 10 },
      ]),
    ),
  };
  const identity = macWorkerIdentity(runner, server, worker, manifest);
  const drivers = Object.fromEntries(
    [
      ...qualificationDrivers,
      'main.go',
      'internal/launcher/entrypoint.go',
      'internal/launcher/launcher.go',
      'internal/launcher/execute_unix.go',
    ].map((name) => [name, sha]),
  );
  const scenarios = Object.fromEntries(
    [
      ['online', 'online', undefined],
      ['offline', 'offline', undefined],
      ['defaultUpgrade', 'previous-upgrade', 'native-local'],
      ['workerUpgrade', 'previous-upgrade', 'layerfs-worker'],
    ].map(([name, mode, backend]) => [
      name,
      {
        status: 'passed',
        platform: 'darwin',
        architecture: 'arm64',
        workspaceBackend: 'layerfs-worker',
        version: identity.version,
        sourceCommit: source,
        mode,
        archiveSHA256: sha,
        entrypointBinarySHA256: sha,
        reportSHA256: sha,
        model: {
          paid: false,
          kind: 'deterministic-local-provider',
          requests: backend ? 5 : 2,
        },
        checkpointStorage: storageFixture(),
        checks: [
          'native-task',
          'single-publication',
          'stopped-runner-backup-relocation',
          'complete-capture-native-and-worker-restore',
          'explicit-checkpoint-budget-quarantine-restore-purge',
        ],
        ...(backend
          ? {
              fromVersion: 'v0.1.0-alpha.10',
              baselineWorkspaceBackend: backend,
              runnerIdentityPreserved: true,
              journalPreserved: true,
              durableSourceReceiptPreserved: true,
              lostCompletionReplyRecovered: true,
            }
          : {}),
      },
    ]),
  );
  scenarios.workerUpgrade.workerUpgrade = {
    fromVersion: 'v0.1.0-alpha.10',
    baselineBackend: 'layerfs-worker',
    baselineImage: image,
    currentImage: image,
    stoppedBackupPreserved: true,
    previousCapturesAndReceiptsPreserved: true,
    checkpointReadbackVerified: true,
    retainedCaptureFiles: 3,
    reconstructionRequired: false,
  };
  scenarios.restart = {
    passed: true,
    published: false,
    entrypointBinarySHA256: sha,
    nativeCaptureRetained: true,
    modelRequests: 2,
    checkpointAfterObservation: { state: 'confirmed', verifiedDigest: sha },
  };
  const recovery = {
    passed: true,
    modelRequests: 0,
    workerDrainConfirmed: true,
    exportRelease: {
      passed: true,
      modelRequests: 0,
      delayConfirmed: true,
      unknownRejected: true,
      receivedCaptureRetained: true,
      releaseJournalRetained: true,
      reopenRefusedWithoutReplay: true,
      controlledSandboxStopped: true,
      recoveredNewEpoch: true,
    },
    worker: {
      protocol: 1,
      image,
      sourceCommit: worker.sourceCommit,
      sdkVersion: worker.version,
      buildId: worker.buildId,
      platform: 'linux/arm64',
      kernel: '6.1-test',
      storeProfile: 'Disposable',
      crashDurable: false,
    },
    initial: { storeId: 'old', digest: sha },
    reconstruction: { result: { storeId: 'new', digest: sha } },
  };
  scenarios.recovery = macWorkerRecoverySummary(recovery, sha);
  const report = {
    schema: 1,
    scope: macWorkerScope,
    status: 'passed',
    identity,
    serverBinarySHA256: sha,
    serverHarness: {
      scope: 'exact-packaged-server-executable-only',
    },
    host: { platform: 'darwin/arm64' },
    driver: {
      sourceCommit: source,
      sourceTree: source,
      modified: false,
      tools: { node: '24.16.0', go: 'go version go1.27.3 darwin/arm64' },
      files: drivers,
      sha256: digestJSON(drivers),
    },
    engine: {
      profile: 'orbstack-test',
      architecture: 'arm64',
      kernel: '6.1-test',
      operatingSystem: 'OrbStack',
      storageDriver: 'overlay2',
      dockerVersion: '29.4.0',
      cpus: 4,
      memoryBytes: 2 ** 30,
      identitySHA256: sha,
    },
    baseline: 'v0.1.0-alpha.10',
    scenarios,
    model: {
      paid: false,
      requests: 16,
      executedRequests: 16,
      reusedRequests: 0,
    },
    baselineWorkerImage: {
      archiveSHA256: sha,
      image,
      campSourceCommit: source,
      reusedIdenticalArtifact: false,
    },
    linuxImageQualification: {
      scope: 'native-linux-arm64-image-only',
      passed: true,
      sdkCommit: worker.sourceCommit,
      image,
    },
  };
  return {
    runner,
    server,
    worker,
    manifest,
    identity,
    drivers,
    report,
    recovery,
  };
}

function reuseFixture() {
  const f = fixture();
  const source = structuredClone(f.report);
  source.status = 'failed';
  source.error = {
    code: 'QualificationFailed',
    message: 'Original restart failed before a model request.',
  };
  source.driver.sourceCommit = 'd'.repeat(40);
  source.driver.sourceTree = 'e'.repeat(40);
  source.driver.files['scripts/qualify-mac-worker.mjs'] = 'f'.repeat(64);
  source.driver.files['scripts/mac-worker-evidence.mjs'] = 'f'.repeat(64);
  for (const name of [
    'package.json',
    'go.sum',
    'scripts/runner-restart-resource.mjs',
  ])
    delete source.driver.files[name];
  source.driver.sha256 = digestJSON(source.driver.files);
  delete source.scenarios.restart;
  delete source.scenarios.recovery;
  delete source.model;
  const inputs = {
    sourceQualificationSHA256: 'f'.repeat(64),
    sourceDriverFiles: structuredClone(source.driver.files),
    scopeFiles: packageScenarioFiles(f.report.driver.files),
    rawReports: {},
  };
  for (const name of packageScenarioNames) {
    const value = {
      ...structuredClone(source.scenarios[name]),
      retainedFixtureDirectory: `/owned/original/camp-release-${name}`,
    };
    delete value.reportSHA256;
    const sha256 = digestJSON(value);
    source.scenarios[name].reportSHA256 = sha256;
    inputs.rawReports[name] = { value, sha256 };
    assert.deepEqual(
      macWorkerScenarioSummary(value, sha256),
      source.scenarios[name],
    );
  }
  return { ...f, source, inputs };
}

test('explicit reuse accepts only four original passing scopes while retaining their failed parent and producer provenance', () => {
  const f = reuseFixture();
  const reused = validateMacWorkerReuse(f.source, f.report, f.inputs);
  assert.deepEqual(Object.keys(reused), packageScenarioNames);
  for (const name of packageScenarioNames) {
    const proof = reused[name].reuse;
    assert.equal(proof.sourceQualificationStatus, 'failed');
    assert.deepEqual(proof.sourceQualificationError, f.source.error);
    assert.equal(
      proof.sourceQualificationSHA256,
      f.inputs.sourceQualificationSHA256,
    );
    assert.deepEqual(proof.producer, f.source.driver);
    assert.deepEqual(proof.driverScope.files, f.inputs.scopeFiles);
    assert.equal(proof.driverScope.sha256, digestJSON(f.inputs.scopeFiles));
    assert.equal(reused[name].reportSHA256, f.inputs.rawReports[name].sha256);
  }
  Object.assign(f.report.scenarios, reused);
  f.report.model.executedRequests = 2;
  f.report.model.reusedRequests = 14;
  validateMacWorkerReport(f.report, f.identity);
  assert.equal(f.source.status, 'failed');
  assert.equal(f.report.driver.sourceCommit, f.identity.sourceCommit);
  assert.notEqual(
    f.report.driver.sourceCommit,
    reused.online.reuse.producer.sourceCommit,
  );
  const partial = structuredClone(f.report);
  delete partial.scenarios.online.reuse;
  assert.throws(() => validateMacWorkerReport(partial, f.identity));
  const wrongCount = structuredClone(f.report);
  wrongCount.model.executedRequests = 16;
  assert.throws(() => validateMacWorkerReport(wrongCount, f.identity));
});

test('reuse rejects changed identities, inputs, raw hashes, acceptance outcomes and unknown or chained states', () => {
  const changes = [
    (f) => {
      f.source.status = 'unknown';
    },
    (f) => {
      f.source.identity.packages.worker.sha256 = '0'.repeat(64);
    },
    (f) => {
      f.source.baseline = 'v0.1.0-alpha.9';
    },
    (f) => {
      f.source.host.release = 'different-host';
    },
    (f) => {
      f.source.engine.identitySHA256 = '0'.repeat(64);
    },
    (f) => {
      f.source.serverBinarySHA256 = '0'.repeat(64);
    },
    (f) => {
      f.source.baselineWorkerImage.archiveSHA256 = '0'.repeat(64);
    },
    (f) => {
      f.source.driver.tools.node = '99.0.0';
    },
    (f) => {
      f.source.driver.modified = true;
    },
    (f) => {
      f.inputs.sourceDriverFiles['scripts/lib.mjs'] = '0'.repeat(64);
    },
    (f) => {
      f.inputs.scopeFiles['scripts/journal-evidence.mjs'] = '0'.repeat(64);
    },
    (f) => {
      delete f.inputs.scopeFiles['go.sum'];
    },
    (f) => {
      f.inputs.scopeFiles['internal/launcher/new.go'] = 'a'.repeat(64);
    },
    (f) => {
      delete f.inputs.scopeFiles['internal/launcher/entrypoint.go'];
    },
    (f) => {
      f.inputs.scopeFiles['renamed.go'] = f.inputs.scopeFiles['main.go'];
      delete f.inputs.scopeFiles['main.go'];
    },
    (f) => {
      f.inputs.rawReports.online.sha256 = '0'.repeat(64);
    },
    (f) => {
      f.inputs.rawReports.online.value.mode = 'offline';
    },
    (f) => {
      f.inputs.rawReports.online.value.status = 'failed';
    },
    (f) => {
      f.source.scenarios.online.status = 'unknown';
    },
    (f) => {
      f.source.scenarios.online.status = 'failed';
    },
    (f) => {
      delete f.source.scenarios.workerUpgrade;
    },
    (f) => {
      f.source.scenarios.offline.checkpointStorage.lifecycle.permanentlyPurged = false;
    },
    (f) => {
      f.source.scenarios.online.reuse = {};
    },
  ];
  for (const change of changes) {
    const f = reuseFixture();
    change(f);
    assert.throws(() => validateMacWorkerReuse(f.source, f.report, f.inputs));
  }
});

test('the actual worker recovery projection retains SDK identity and protocol independently from Runner protocol', () => {
  const f = fixture();
  // This is the WorkerClient.status shape returned by the Linux SDK process;
  // private paths and low-level binary details are unnecessary release data.
  const actual = {
    ...f.recovery,
    worker: {
      ...f.recovery.worker,
      project: 'owned-fixture-project',
      volume: 'owned-fixture-store',
      nativeSha256: 'd'.repeat(64),
      daemonSha256: 'e'.repeat(64),
    },
  };
  f.report.scenarios.recovery = macWorkerRecoverySummary(
    actual,
    'a'.repeat(64),
  );
  validateMacWorkerReport(f.report, f.identity);
  assert.equal(f.report.identity.protocolVersion, 2);
  assert.equal(f.report.scenarios.recovery.worker.protocol, 1);
  assert.equal(f.report.scenarios.recovery.worker.sdkVersion, f.worker.version);
  assert.equal(f.report.scenarios.recovery.worker.buildId, f.worker.buildId);
  assert.equal(
    f.report.scenarios.recovery.worker.sourceCommit,
    f.worker.sourceCommit,
  );
  assert.equal('volume' in f.report.scenarios.recovery.worker, false);
  for (const name of ['protocol', 'sdkVersion', 'buildId']) {
    const missing = structuredClone(actual);
    delete missing.worker[name];
    f.report.scenarios.recovery = macWorkerRecoverySummary(
      missing,
      'a'.repeat(64),
    );
    assert.throws(() => validateMacWorkerReport(f.report, f.identity));
  }
});

test('identity binds the actual Mac runtime, controller and separately built Linux worker', () => {
  const f = fixture();
  assert.equal(f.identity.worker.sdkCommit, f.worker.sourceCommit);
  assert.equal(f.identity.sourceCommit, f.worker.campSourceCommit);
  assert.notEqual(f.identity.sourceCommit, f.identity.worker.sdkCommit);
  for (const change of [
    () => {
      f.worker.campSourceCommit = 'd'.repeat(40);
    },
    () => {
      f.worker.image = `sha256:${'d'.repeat(64)}`;
    },
    () => {
      f.runner.protocolVersion = 99;
    },
    () => {
      f.runner.runners['darwin-arm64'].runtime.filename = '../escape.tar.gz';
    },
    () => {
      delete f.manifest.files['runtime/layerfs-worker/checkpoint.mjs'];
    },
  ]) {
    const fresh = fixture();
    change();
    assert.throws(() =>
      macWorkerIdentity(f.runner, f.server, f.worker, f.manifest),
    );
    Object.assign(f, fresh);
  }
});

test('passing qualification requires package scenarios, real Mac provenance and independent recovery', () => {
  const f = fixture();
  assert.equal(
    validateMacWorkerReport(f.report, {
      identity: f.identity,
      drivers: f.drivers,
      baseline: 'v0.1.0-alpha.10',
    }),
    f.report,
  );
  for (const change of [
    (r) => {
      r.scope = 'native-linux-arm64-image-only';
    },
    (r) => {
      r.status = 'failed';
    },
    (r) => {
      delete r.serverBinarySHA256;
    },
    (r) => {
      r.serverHarness.scope = 'complete-server-archive';
    },
    (r) => {
      r.host.platform = 'linux/arm64';
    },
    (r) => {
      r.driver.modified = true;
    },
    (r) => {
      delete r.driver.files['scripts/smoke-bundle.mjs'];
    },
    (r) => {
      r.engine.architecture = 'x86_64';
    },
    (r) => {
      delete r.scenarios.offline;
    },
    (r) => {
      r.scenarios.online.sourceCommit = 'd'.repeat(40);
    },
    (r) => {
      r.scenarios.online.workspaceBackend = 'native-local';
    },
    (r) => {
      r.scenarios.workerUpgrade.baselineWorkspaceBackend = 'native-local';
    },
    (r) => {
      r.scenarios.workerUpgrade.workerUpgrade.previousCapturesAndReceiptsPreserved = false;
    },
    (r) => {
      r.scenarios.workerUpgrade.workerUpgrade.reconstructionRequired = true;
    },
    (r) => {
      r.scenarios.restart.published = true;
    },
    (r) => {
      r.scenarios.recovery.worker.image = `sha256:${'d'.repeat(64)}`;
    },
    (r) => {
      r.scenarios.recovery.worker.kernel = 'another-engine';
    },
    (r) => {
      r.scenarios.recovery.reconstruction.result.storeId = 'old';
    },
    (r) => {
      r.model.paid = true;
    },
    (r) => {
      r.scenarios.online.model.requests = 3;
    },
  ]) {
    const copy = structuredClone(f.report);
    change(copy);
    assert.throws(() =>
      validateMacWorkerReport(copy, {
        identity: f.identity,
        drivers: f.drivers,
      }),
    );
  }
  assert.throws(() =>
    validateMacWorkerReport(f.report, {
      identity: { ...f.identity, sourceCommit: 'd'.repeat(40) },
    }),
  );
});

test('Server harness scope remains authoritative while unrelated retained metadata is ignored', () => {
  const f = fixture();
  f.report.serverHarness.additionalObservation = { historical: true };
  validateMacWorkerReport(f.report, f.identity);
  for (const serverHarness of [
    undefined,
    null,
    {},
    { additionalObservation: { historical: true } },
    { ...f.report.serverHarness, scope: 'complete-server-archive' },
  ])
    assert.throws(() =>
      validateMacWorkerReport({ ...f.report, serverHarness }, f.identity),
    );
});

test('a changed worker image requires retained explicit reconstruction evidence', () => {
  const f = fixture(),
    u = f.report.scenarios.workerUpgrade.workerUpgrade;
  u.baselineImage = `sha256:${'d'.repeat(64)}`;
  u.reconstructionRequired = true;
  u.reconstruction = {
    sourceDigest: 'a'.repeat(64),
    resultDigest: 'a'.repeat(64),
    oldStore: 'old',
    newStore: 'new',
    oldEpoch: 'initial',
    newEpoch: 'replacement',
  };
  const scenario = f.report.scenarios.workerUpgrade;
  scenario.checkpointStorage = storageFixture(true);
  scenario.checks = scenario.checks
    .filter(
      (check) =>
        check !== 'explicit-checkpoint-budget-quarantine-restore-purge',
    )
    .concat('checkpoint-reconstruction-history-blocks-cleanup');
  validateMacWorkerReport(f.report, f.identity);
  u.reconstruction.resultDigest = 'e'.repeat(64);
  assert.throws(() => validateMacWorkerReport(f.report, f.identity));
});

test('exact worker acceptance requires the delayed and failed export release proof', () => {
  for (const key of [
    'passed',
    'delayConfirmed',
    'unknownRejected',
    'receivedCaptureRetained',
    'releaseJournalRetained',
    'reopenRefusedWithoutReplay',
    'controlledSandboxStopped',
    'recoveredNewEpoch',
  ]) {
    const f = fixture();
    delete f.report.scenarios.recovery.exportRelease[key];
    assert.throws(() => validateMacWorkerReport(f.report, f.identity));
  }
  const f = fixture();
  f.report.scenarios.recovery.exportRelease.modelRequests = 1;
  assert.throws(() => validateMacWorkerReport(f.report, f.identity));
});

test('reconstructed history requires a guarded cleanup proof while fresh scenarios require the full lifecycle', () => {
  const f = fixture();
  const scenario = f.report.scenarios.workerUpgrade;
  Object.assign(scenario.workerUpgrade, {
    baselineImage: `sha256:${'d'.repeat(64)}`,
    reconstructionRequired: true,
    reconstruction: {
      sourceDigest: 'a'.repeat(64),
      resultDigest: 'a'.repeat(64),
      oldStore: 'old',
      newStore: 'new',
      oldEpoch: 'initial',
      newEpoch: 'replacement',
    },
  });
  scenario.checkpointStorage = storageFixture(true);
  scenario.checks = scenario.checks
    .filter(
      (check) =>
        check !== 'explicit-checkpoint-budget-quarantine-restore-purge',
    )
    .concat('checkpoint-reconstruction-history-blocks-cleanup');
  validateMacWorkerReport(f.report, f.identity);
  for (const change of [
    (r) => {
      r.scenarios.workerUpgrade.checkpointStorage.complete = false;
    },
    (r) => {
      r.scenarios.workerUpgrade.checkpointStorage.retention.previewAllowed = true;
    },
    (r) => {
      delete r.scenarios.workerUpgrade.checkpointStorage.lifecycle
        .cleanupPlanBlocked;
    },
    (r) => {
      delete r.scenarios.workerUpgrade.checkpointStorage.lifecycle
        .recoveryGuardPreserved;
    },
    (r) => {
      r.scenarios.workerUpgrade.checkpointStorage.lifecycle.cleanupPlanError =
        'AnotherError';
    },
    (r) => {
      r.scenarios.workerUpgrade.checkpointStorage.lifecycle.permanentlyPurged = true;
    },
    (r) => {
      r.scenarios.workerUpgrade.checks.push(
        'explicit-checkpoint-budget-quarantine-restore-purge',
      );
    },
    (r) => {
      r.scenarios.online.checkpointStorage = storageFixture(true);
    },
    (r) => {
      delete r.scenarios.offline.checkpointStorage.lifecycle
        .quarantinePreservesBytes;
    },
    (r) => {
      r.scenarios.defaultUpgrade.checks =
        r.scenarios.defaultUpgrade.checks.filter(
          (check) =>
            check !== 'explicit-checkpoint-budget-quarantine-restore-purge',
        );
    },
  ]) {
    const changed = structuredClone(f.report);
    change(changed);
    assert.throws(() => validateMacWorkerReport(changed, f.identity));
  }
});

test('runtime identity reads only the exact packaged manifest and rejects a mismatched manifest hash', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-mac-worker-manifest-'));
  try {
    const f = fixture(),
      payload = JSON.stringify(f.manifest);
    f.runner.runners['darwin-arm64'].runtime.manifestSHA256 = createHash(
      'sha256',
    )
      .update(payload)
      .digest('hex');
    for (const [name, value] of [
      ['runner-release.json', f.runner],
      ['server-release.json', f.server],
      ['checkpoint-worker-release.json', f.worker],
    ])
      await writeFile(join(directory, name), JSON.stringify(value));
    await mkdir(join(directory, 'camp-runtime-darwin-arm64'));
    await writeFile(
      join(directory, 'camp-runtime-darwin-arm64/manifest.json'),
      payload,
    );
    await promisify(execFile)('tar', [
      '-czf',
      join(directory, f.runner.runners['darwin-arm64'].runtime.filename),
      '-C',
      directory,
      'camp-runtime-darwin-arm64/manifest.json',
    ]);
    assert.equal(
      (await readMacWorkerIdentity(directory)).worker.image,
      f.worker.image,
    );
    f.runner.runners['darwin-arm64'].runtime.manifestSHA256 = 'f'.repeat(64);
    await writeFile(
      join(directory, 'runner-release.json'),
      JSON.stringify(f.runner),
    );
    await assert.rejects(readMacWorkerIdentity(directory));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('optional pending evidence never turns Linux image proof into Mac worker support', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-mac-worker-evidence-'));
  try {
    const f = fixture();
    assert.equal(
      (await optionalMacWorkerEvidence(directory, f.identity, f.drivers))
        .status,
      'pending',
    );
    assert.equal(
      (
        await optionalMacWorkerEvidence(directory, () =>
          assert.fail(
            'Pending optional evidence must not unpack or inspect Mac runtimes.',
          ),
        )
      ).status,
      'pending',
    );
    await writeFile(
      join(directory, 'mac-worker-qualification.json'),
      JSON.stringify(f.report),
    );
    assert.equal(
      (await optionalMacWorkerEvidence(directory, f.identity, f.drivers))
        .status,
      'passed',
    );
    f.report.status = 'failed';
    await writeFile(
      join(directory, 'mac-worker-qualification.json'),
      JSON.stringify(f.report),
    );
    await assert.rejects(
      optionalMacWorkerEvidence(directory, f.identity, f.drivers),
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
