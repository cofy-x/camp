import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import {
  digestJSON,
  macWorkerIdentity,
  macWorkerRecoverySummary,
  macWorkerScope,
  optionalMacWorkerEvidence,
  qualificationDrivers,
  readMacWorkerIdentity,
  validateMacWorkerReport,
} from './mac-worker-evidence.mjs';

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
    qualificationDrivers.map((name) => [name, sha]),
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
        checks: [
          'native-task',
          'single-publication',
          'stopped-runner-backup-relocation',
          'complete-capture-native-and-worker-restore',
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
    },
    baseline: 'v0.1.0-alpha.10',
    scenarios,
    model: { paid: false, requests: 16 },
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
  validateMacWorkerReport(f.report, f.identity);
  u.reconstruction.resultDigest = 'e'.repeat(64);
  assert.throws(() => validateMacWorkerReport(f.report, f.identity));
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
