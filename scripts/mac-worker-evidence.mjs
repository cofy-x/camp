import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { lstat, readFile, realpath } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { promisify } from 'node:util';
import { validVersion } from './release.mjs';

export const macWorkerScope = 'actual-mac-packaged-layerfs-worker';
const sha = /^[a-f0-9]{64}$/;
const commit = /^[a-f0-9]{40}$/;
export const packageScenarioNames = [
  'online',
  'offline',
  'defaultUpgrade',
  'workerUpgrade',
];
export const packageScenarioDrivers = [
  '.node-version',
  'package.json',
  'go.mod',
  'go.sum',
  'scripts/smoke-bundle.mjs',
  'scripts/journal-evidence.mjs',
  'scripts/setup-progress.mjs',
  'scripts/lib.mjs',
  'scripts/release.mjs',
  'scripts/upgrade.mjs',
  'scripts/upgrade-proxy.mjs',
  'tools/layerfs-worker/tree.mjs',
  'tools/layerfs-worker/contracts.mjs',
  'tools/layerfs-worker/state.mjs',
];
export const qualificationDrivers = [
  '.node-version',
  'package.json',
  'go.mod',
  'go.sum',
  'scripts/qualify-mac-worker.mjs',
  'scripts/mac-worker-evidence.mjs',
  'scripts/smoke-bundle.mjs',
  'scripts/journal-evidence.mjs',
  'scripts/upgrade.mjs',
  'scripts/upgrade-proxy.mjs',
  'scripts/lib.mjs',
  'tools/layerfs-worker/qualify-runner-restart.mjs',
  'tools/layerfs-worker/qualify-recovery.mjs',
  'tools/layerfs-worker/qualify-export-release.mjs',
  'tools/layerfs-worker/tree.mjs',
  'tools/layerfs-worker/contracts.mjs',
  'tools/layerfs-worker/state.mjs',
  'scripts/release.mjs',
  'scripts/setup-progress.mjs',
  'scripts/runner-restart-resource.mjs',
];

export const digestJSON = (value) =>
  createHash('sha256').update(JSON.stringify(value)).digest('hex');

export function macWorkerServiceCleanupAction(status) {
  const service = status?.service;
  if (
    status?.schema !== 1 ||
    status.platform !== 'darwin/arm64' ||
    typeof status.runnerId !== 'string' ||
    status.runnerId.length === 0 ||
    service?.supported !== true ||
    ['installed', 'loaded', 'running', 'loginEnabled'].some(
      (field) => typeof service[field] !== 'boolean',
    )
  )
    return 'uncertain';
  if (
    service.state === 'absent' &&
    !service.installed &&
    !service.loaded &&
    !service.running &&
    !service.loginEnabled
  )
    return 'absent';
  if (
    service.installed &&
    ((service.state === 'stopped' && !service.running) ||
      (service.state === 'running' && service.loaded && service.running))
  )
    return 'remove';
  return 'uncertain';
}

export async function validateMacWorkerReuseFixture(directory, name, retained) {
  assert.ok(packageScenarioNames.includes(name));
  const fixture = join(directory, `camp-release-${name}`);
  assert.equal(
    retained,
    fixture,
    'Use the original canonical synthetic fixture path.',
  );
  for (const selected of [
    directory,
    fixture,
    join(fixture, "Runner's private data"),
    join(fixture, 'data'),
  ]) {
    const info = await lstat(selected);
    assert.ok(
      info.isDirectory() &&
        info.uid === process.getuid() &&
        !(info.mode & 0o077),
      'Reuse fixture roots must be private owned real directories.',
    );
    assert.equal(
      await realpath(selected),
      selected,
      'Reuse fixture roots cannot redirect to another installation.',
    );
  }
  return fixture;
}

export function macWorkerStorageSummary(storage) {
  return {
    complete: storage?.complete,
    retention: {
      previewAllowed: storage?.retention?.previewAllowed,
      deletionSupported: storage?.retention?.deletionSupported,
    },
    lifecycle: Object.fromEntries(
      [
        'liveOverview',
        'policyConfigured',
        'advisoryWarning',
        'protectedHistoryPreserved',
        'quarantinePreservesBytes',
        'restoredExactly',
        'permanentlyPurged',
        'recoveryGuardPreserved',
        'cleanupPlanBlocked',
        'cleanupPlanError',
      ]
        .filter((field) => storage?.lifecycle?.[field] !== undefined)
        .map((field) => [field, storage.lifecycle[field]]),
    ),
  };
}

const packageGoSource = (name) =>
  /^[^/]+\.go$/.test(name) || /^internal\/launcher\/.+\.go$/.test(name);

export async function packageGoSources(root, revision = 'HEAD') {
  assert.ok(revision === 'HEAD' || /^[a-f0-9]{40}$/.test(revision));
  const execute = promisify(execFile);
  const options = {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 1 << 20,
    timeout: 10_000,
    killSignal: 'SIGKILL',
  };
  const outputs = await Promise.all([
    execute('git', ['ls-tree', '-z', '--name-only', revision], options),
    execute(
      'git',
      [
        'ls-tree',
        '-r',
        '-z',
        '--name-only',
        revision,
        '--',
        'internal/launcher',
      ],
      options,
    ),
  ]);
  return outputs
    .flatMap(({ stdout }) => stdout.split('\0'))
    .filter(packageGoSource)
    .sort();
}

export async function driverFiles(root) {
  const names = [...qualificationDrivers, ...(await packageGoSources(root))];
  return Object.fromEntries(
    await Promise.all(
      names.map(async (name) => [
        name,
        createHash('sha256')
          .update(await readFile(join(root, name)))
          .digest('hex'),
      ]),
    ),
  );
}

export async function readMacWorkerIdentity(directory) {
  const read = async (name) =>
    JSON.parse(await readFile(join(directory, name), 'utf8'));
  const runner = await read('runner-release.json'),
    server = await read('server-release.json'),
    worker = await read('checkpoint-worker-release.json');
  const runtime = runner.runners?.['darwin-arm64']?.runtime;
  artifact(runtime, `camp-runtime-darwin-arm64-${runtime?.sha256}.tar.gz`);
  const { stdout } = await promisify(execFile)(
    'tar',
    [
      '-xOf',
      join(directory, runtime.filename),
      'camp-runtime-darwin-arm64/manifest.json',
    ],
    { encoding: 'buffer', maxBuffer: 32 << 20 },
  );
  assert.equal(
    createHash('sha256').update(stdout).digest('hex'),
    runtime.manifestSHA256,
  );
  return macWorkerIdentity(
    runner,
    server,
    worker,
    JSON.parse(stdout.toString('utf8')),
  );
}

function artifact(value, filename) {
  assert.equal(value?.filename, filename);
  assert.equal(basename(value.filename), value.filename);
  assert.match(value.sha256, sha);
  assert.ok(
    Number.isSafeInteger(value.bytes) &&
      value.bytes > 0 &&
      value.bytes <= 2 ** 31,
  );
  return { filename: value.filename, sha256: value.sha256, bytes: value.bytes };
}

export function macWorkerArtifacts(runner, server, worker) {
  assert.equal(runner.schema, 2);
  assert.equal(server.schema, 1);
  assert.ok(validVersion(runner.version));
  assert.match(runner.sourceCommit, commit);
  assert.equal(server.version, runner.version);
  assert.equal(server.sourceCommit, runner.sourceCommit);
  assert.equal(runner.protocolVersion, 2);
  assert.equal(worker.schema, 1);
  assert.equal(worker.platform, 'linux/arm64');
  assert.equal(worker.campSourceCommit, runner.sourceCommit);
  assert.match(worker.sourceCommit, commit);
  assert.match(worker.image, /^sha256:[a-f0-9]{64}$/);
  assert.match(worker.buildId, sha);
  const mac = runner.runners?.['darwin-arm64'];
  assert.match(mac?.runtime?.manifestSHA256, sha);
  return {
    installer: artifact(mac.installer, 'camp-runner-darwin-arm64.tar.gz'),
    offline: artifact(mac.offline, 'camp-runner-offline-darwin-arm64.tar.gz'),
    runtime: artifact(
      mac.runtime,
      `camp-runtime-darwin-arm64-${mac.runtime.sha256}.tar.gz`,
    ),
    server: artifact(
      server.servers?.['darwin-arm64'],
      'camp-server-darwin-arm64.tar.gz',
    ),
    worker: artifact(
      worker.artifact,
      `camp-layerfs-worker-linux-arm64-${worker.artifact.sha256}.tar.gz`,
    ),
  };
}

export function macWorkerIdentity(runner, server, worker, manifest) {
  const packages = macWorkerArtifacts(runner, server, worker);
  assert.equal(manifest.schema, 2);
  assert.equal(manifest.platform, 'darwin/arm64');
  assert.equal(manifest.role, 'runner-runtime');
  assert.equal(manifest.checkpointWorker?.image, worker.image);
  assert.equal(manifest.checkpointWorker?.commit, worker.sourceCommit);
  assert.equal(manifest.checkpointWorker?.version, worker.version);
  assert.equal(
    manifest.checkpointWorker?.archiveSHA256,
    worker.artifact.sha256,
  );
  const mac = runner.runners['darwin-arm64'];
  const files = Object.fromEntries(
    Object.entries(manifest.files ?? {})
      .filter(
        ([name]) =>
          name.startsWith('runtime/layerfs-worker/') ||
          name === 'runtime/layerfs-runtime/pin.json',
      )
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, value]) => {
        assert.ok(!name.includes('..') && !name.includes('\\'));
        assert.match(value.sha256, sha);
        return [name, value.sha256];
      }),
  );
  for (const name of [
    'installed.json',
    'client.mjs',
    'checkpoint.mjs',
    'tree.mjs',
    'administration.mjs',
    'home.mjs',
    'state.mjs',
    'protocol.mjs',
  ])
    assert.ok(
      files[`runtime/layerfs-worker/${name}`],
      `Missing packaged worker controller ${name}.`,
    );
  return {
    version: runner.version,
    sourceCommit: runner.sourceCommit,
    protocolVersion: runner.protocolVersion,
    packages,
    runtimeManifestSHA256: mac.runtime.manifestSHA256,
    controllerFiles: files,
    controllerSHA256: digestJSON(files),
    worker: {
      image: worker.image,
      sdkVersion: worker.version,
      sdkCommit: worker.sourceCommit,
      buildId: worker.buildId,
    },
  };
}

export function macWorkerRecoverySummary(recovery, reportSHA256) {
  return {
    passed: recovery.passed,
    modelRequests: recovery.modelRequests,
    workerDrainConfirmed: recovery.workerDrainConfirmed,
    exportRelease:
      recovery.exportRelease &&
      Object.fromEntries(
        [
          'passed',
          'modelRequests',
          'delayConfirmed',
          'unknownRejected',
          'receivedCaptureRetained',
          'releaseJournalRetained',
          'reopenRefusedWithoutReplay',
          'controlledSandboxStopped',
        ].map((name) => [name, recovery.exportRelease[name]]),
      ),
    worker: Object.fromEntries(
      [
        'protocol',
        'image',
        'sourceCommit',
        'sdkVersion',
        'buildId',
        'platform',
        'kernel',
        'storeProfile',
        'crashDurable',
      ].map((name) => [name, recovery.worker?.[name]]),
    ),
    initial: {
      storeId: recovery.initial?.storeId,
      digest: recovery.initial?.digest,
    },
    reconstruction: {
      result: {
        storeId: recovery.reconstruction?.result?.storeId,
        digest: recovery.reconstruction?.result?.digest,
      },
    },
    reportSHA256,
  };
}

export function macWorkerScenarioSummary(value, reportSHA256) {
  const fields = [
    'status',
    'mode',
    'workspaceBackend',
    'platform',
    'architecture',
    'version',
    'sourceCommit',
    'sdkVersion',
    'buildId',
    'archiveSHA256',
    'entrypointBinarySHA256',
    'model',
    'checks',
    'fromVersion',
    'baselineWorkspaceBackend',
    'runnerIdentityPreserved',
    'journalPreserved',
    'durableSourceReceiptPreserved',
    'lostCompletionReplyRecovered',
    'workerUpgrade',
    'timings',
  ];
  return {
    ...Object.fromEntries(
      fields
        .filter((key) => value[key] !== undefined)
        .map((key) => [key, value[key]]),
    ),
    checkpointStorage: macWorkerStorageSummary(value.checkpointStorage),
    reportSHA256,
  };
}

export function validateMacWorkerPackageScenario(
  name,
  item,
  identity,
  baseline,
) {
  assert.ok(
    packageScenarioNames.includes(name),
    'Select a known package scenario.',
  );
  const mode = ['online', 'offline'].includes(name) ? name : 'previous-upgrade';
  const backend =
    name === 'defaultUpgrade'
      ? 'native-local'
      : name === 'workerUpgrade'
        ? 'layerfs-worker'
        : undefined;
  assert.equal(
    item?.status,
    'passed',
    `Missing passing ${name} package scenario.`,
  );
  assert.equal(item.platform, 'darwin');
  assert.equal(item.architecture, 'arm64');
  assert.equal(item.workspaceBackend, 'layerfs-worker');
  assert.equal(item.version, identity.version);
  assert.equal(item.sourceCommit, identity.sourceCommit);
  assert.equal(item.mode, mode);
  assert.equal(
    item.archiveSHA256,
    identity.packages[name === 'offline' ? 'offline' : 'installer'].sha256,
  );
  assert.match(item.entrypointBinarySHA256, sha);
  assert.match(item.reportSHA256, sha);
  assert.equal(item.model?.paid, false);
  assert.equal(item.model?.kind, 'deterministic-local-provider');
  assert.equal(item.model?.requests, backend ? 5 : 2);
  for (const check of [
    'native-task',
    'single-publication',
    'stopped-runner-backup-relocation',
    'complete-capture-native-and-worker-restore',
  ])
    assert.ok(item.checks?.includes(check));
  const storage = item.checkpointStorage;
  assert.equal(storage?.complete, true);
  assert.equal(storage.retention?.deletionSupported, false);
  for (const field of [
    'liveOverview',
    'policyConfigured',
    'advisoryWarning',
    'protectedHistoryPreserved',
  ])
    assert.equal(storage.lifecycle?.[field], true);
  const guarded =
    name === 'workerUpgrade' &&
    item.workerUpgrade?.reconstructionRequired === true;
  const fullCleanup = 'explicit-checkpoint-budget-quarantine-restore-purge';
  const recoveryGuard = 'checkpoint-reconstruction-history-blocks-cleanup';
  assert.equal(storage.retention.previewAllowed, !guarded);
  assert.ok(item.checks.includes(guarded ? recoveryGuard : fullCleanup));
  assert.ok(!item.checks.includes(guarded ? fullCleanup : recoveryGuard));
  for (const field of [
    'quarantinePreservesBytes',
    'restoredExactly',
    'permanentlyPurged',
  ])
    assert.equal(storage.lifecycle[field], guarded ? undefined : true);
  for (const field of ['recoveryGuardPreserved', 'cleanupPlanBlocked'])
    assert.equal(storage.lifecycle[field], guarded ? true : undefined);
  assert.equal(
    storage.lifecycle.cleanupPlanError,
    guarded ? 'RecoveryRequired' : undefined,
  );
  if (backend) {
    assert.equal(item.fromVersion, baseline);
    assert.equal(item.baselineWorkspaceBackend, backend);
    for (const field of [
      'runnerIdentityPreserved',
      'journalPreserved',
      'durableSourceReceiptPreserved',
      'lostCompletionReplyRecovered',
    ])
      assert.equal(item[field], true);
  }
  if (name === 'workerUpgrade') {
    const upgrade = item.workerUpgrade;
    assert.equal(upgrade?.fromVersion, baseline);
    assert.equal(upgrade?.baselineBackend, 'layerfs-worker');
    assert.equal(upgrade?.currentImage, identity.worker.image);
    assert.match(upgrade?.baselineImage, /^sha256:[a-f0-9]{64}$/);
    for (const field of [
      'stoppedBackupPreserved',
      'previousCapturesAndReceiptsPreserved',
      'checkpointReadbackVerified',
    ])
      assert.equal(upgrade[field], true);
    assert.ok(
      Number.isSafeInteger(upgrade.retainedCaptureFiles) &&
        upgrade.retainedCaptureFiles > 0,
    );
    assert.equal(typeof upgrade.reconstructionRequired, 'boolean');
    if (upgrade.baselineImage !== upgrade.currentImage)
      assert.equal(
        upgrade.reconstructionRequired,
        true,
        'A changed worker image requires explicit reconstruction evidence.',
      );
    if (upgrade.reconstructionRequired) {
      assert.match(upgrade.reconstruction?.sourceDigest, sha);
      assert.equal(
        upgrade.reconstruction.sourceDigest,
        upgrade.reconstruction.resultDigest,
      );
      assert.notEqual(
        upgrade.reconstruction.oldStore,
        upgrade.reconstruction.newStore,
      );
      assert.notEqual(
        upgrade.reconstruction.oldEpoch,
        upgrade.reconstruction.newEpoch,
      );
      for (const field of ['oldStore', 'newStore', 'oldEpoch', 'newEpoch'])
        assert.ok(
          typeof upgrade.reconstruction[field] === 'string' &&
            upgrade.reconstruction[field].length > 0,
        );
    }
  }
  return item;
}

export function packageScenarioFiles(files) {
  const goFiles = Object.keys(files).filter(packageGoSource).sort();
  assert.ok(
    goFiles.some((name) => !name.includes('/')) &&
      goFiles.some((name) => name.startsWith('internal/launcher/')),
    'Missing baseline launcher Go source scope.',
  );
  return Object.fromEntries(
    [...packageScenarioDrivers, ...goFiles].map((name) => {
      assert.match(
        files?.[name] ?? '',
        sha,
        `Missing package-scope input ${name}.`,
      );
      return [name, files[name]];
    }),
  );
}

export function validateMacWorkerReuse(source, current, inputs) {
  assert.equal(source.schema, 1);
  assert.equal(source.scope, macWorkerScope);
  assert.ok(['failed', 'passed'].includes(source.status));
  if (source.status === 'failed')
    assert.ok(
      typeof source.error?.message === 'string' &&
        source.error.message.length > 0,
    );
  assert.equal(
    source.serverHarness?.scope,
    'exact-packaged-server-executable-only',
  );
  for (const field of [
    'identity',
    'baseline',
    'host',
    'engine',
    'serverBinarySHA256',
    'baselineWorkerImage',
  ])
    assert.deepEqual(source[field], current[field], `Reuse changed ${field}.`);
  assert.match(source.engine?.identitySHA256 ?? '', sha);
  assert.deepEqual(source.driver?.tools, current.driver?.tools);
  assert.equal(source.driver?.modified, false);
  assert.match(source.driver?.sourceCommit ?? '', commit);
  assert.match(source.driver?.sourceTree ?? '', commit);
  assert.equal(source.driver.sha256, digestJSON(source.driver.files));
  assert.deepEqual(
    source.driver.files,
    inputs.sourceDriverFiles,
    'Original producer files differ from their Git source.',
  );
  for (const name of [
    'scripts/qualify-mac-worker.mjs',
    'scripts/mac-worker-evidence.mjs',
    ...packageScenarioDrivers.filter(
      (file) => !['package.json', 'go.sum'].includes(file),
    ),
  ])
    assert.match(source.driver.files[name] ?? '', sha);
  assert.deepEqual(
    packageScenarioFiles(inputs.scopeFiles),
    packageScenarioFiles(current.driver.files),
    'Package-scope inputs changed.',
  );
  assert.match(inputs.sourceQualificationSHA256, sha);
  const reused = {};
  for (const name of packageScenarioNames) {
    const item = source.scenarios?.[name];
    validateMacWorkerPackageScenario(
      name,
      item,
      current.identity,
      current.baseline,
    );
    assert.equal(
      item.reuse,
      undefined,
      'Reuse only original package observations, without chaining receipts.',
    );
    const raw = inputs.rawReports?.[name];
    assert.equal(
      raw?.sha256,
      item.reportSHA256,
      `Changed original ${name} report bytes.`,
    );
    assert.deepEqual(
      macWorkerScenarioSummary(raw.value, raw.sha256),
      item,
      `Changed original ${name} report fields.`,
    );
    reused[name] = {
      ...item,
      reuse: {
        scope: 'passed-package-scenario',
        sourceQualificationSHA256: inputs.sourceQualificationSHA256,
        sourceQualificationStatus: source.status,
        sourceQualificationError: source.error ?? null,
        producer: source.driver,
        driverScope: {
          files: inputs.scopeFiles,
          sha256: digestJSON(inputs.scopeFiles),
        },
      },
    };
  }
  return reused;
}

export function validateMacWorkerReport(report, expected) {
  assert.equal(report.schema, 1);
  assert.equal(report.scope, macWorkerScope);
  assert.equal(report.status, 'passed');
  assert.deepEqual(report.identity, expected.identity ?? expected);
  assert.match(report.serverBinarySHA256 ?? '', sha);
  assert.equal(
    report.serverHarness?.scope,
    'exact-packaged-server-executable-only',
  );
  assert.equal(report.host?.platform, 'darwin/arm64');
  assert.equal(report.driver?.modified, false);
  assert.match(report.driver?.tools?.node, /^\d+\.\d+\.\d+$/);
  assert.match(
    report.driver?.tools?.go,
    /^go version go\d+\.\d+\.\d+ darwin\/arm64$/,
  );
  assert.match(report.driver?.sourceCommit, commit);
  for (const name of qualificationDrivers)
    assert.match(report.driver?.files?.[name], sha);
  assert.equal(report.driver.sha256, digestJSON(report.driver.files));
  if (expected.drivers) assert.deepEqual(report.driver.files, expected.drivers);
  const engine = report.engine;
  assert.match(engine?.profile, /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$/);
  assert.ok(['arm64', 'aarch64'].includes(engine.architecture));
  for (const name of [
    'kernel',
    'operatingSystem',
    'storageDriver',
    'dockerVersion',
  ])
    assert.ok(typeof engine[name] === 'string' && engine[name].length > 0);
  assert.ok(Number.isSafeInteger(engine.cpus) && engine.cpus > 0);
  assert.ok(Number.isSafeInteger(engine.memoryBytes) && engine.memoryBytes > 0);
  assert.ok(validVersion(report.baseline));
  if (expected.baseline) assert.equal(report.baseline, expected.baseline);
  const identity = report.identity;
  assert.notEqual(report.baseline, identity.version);
  for (const name of packageScenarioNames)
    validateMacWorkerPackageScenario(
      name,
      report.scenarios?.[name],
      identity,
      report.baseline,
    );
  let reusedRequests = 0;
  for (const name of packageScenarioNames) {
    const item = report.scenarios[name];
    if (!item.reuse) continue;
    const reuse = item.reuse;
    assert.equal(reuse.scope, 'passed-package-scenario');
    assert.match(reuse.sourceQualificationSHA256, sha);
    assert.ok(['failed', 'passed'].includes(reuse.sourceQualificationStatus));
    assert.equal(reuse.producer?.modified, false);
    assert.match(reuse.producer?.sourceCommit ?? '', commit);
    assert.match(reuse.producer?.sourceTree ?? '', commit);
    assert.equal(reuse.producer.sha256, digestJSON(reuse.producer.files));
    assert.equal(
      reuse.driverScope?.sha256,
      digestJSON(reuse.driverScope?.files),
    );
    assert.deepEqual(
      packageScenarioFiles(reuse.driverScope.files),
      packageScenarioFiles(report.driver.files),
    );
    reusedRequests += item.model.requests;
  }
  assert.ok(
    reusedRequests === 0 || reusedRequests === 14,
    'Reuse all four known package cases together.',
  );
  const restart = report.scenarios?.restart;
  assert.equal(restart?.passed, true);
  assert.equal(restart.published, false);
  assert.equal(
    restart.entrypointBinarySHA256,
    report.scenarios.online.entrypointBinarySHA256,
  );
  assert.equal(restart.nativeCaptureRetained, true);
  assert.equal(restart.modelRequests, 2);
  assert.equal(restart.checkpointAfterObservation?.state, 'confirmed');
  assert.match(restart.checkpointAfterObservation?.verifiedDigest, sha);
  const recovery = report.scenarios?.recovery;
  assert.equal(recovery?.passed, true);
  assert.equal(recovery.modelRequests, 0);
  assert.equal(recovery.workerDrainConfirmed, true);
  assert.equal(recovery.exportRelease?.modelRequests, 0);
  for (const field of [
    'passed',
    'delayConfirmed',
    'unknownRejected',
    'receivedCaptureRetained',
    'releaseJournalRetained',
    'reopenRefusedWithoutReplay',
    'controlledSandboxStopped',
  ])
    assert.equal(
      recovery.exportRelease?.[field],
      true,
      `Missing export release proof: ${field}`,
    );
  assert.equal(recovery.worker?.protocol, 1);
  assert.equal(recovery.worker?.image, identity.worker.image);
  assert.equal(recovery.worker?.sourceCommit, identity.worker.sdkCommit);
  assert.equal(recovery.worker?.sdkVersion, identity.worker.sdkVersion);
  assert.equal(recovery.worker?.buildId, identity.worker.buildId);
  assert.equal(recovery.worker?.platform, 'linux/arm64');
  assert.equal(recovery.worker?.kernel, engine.kernel);
  assert.equal(recovery.worker?.storeProfile, 'Disposable');
  assert.equal(recovery.worker?.crashDurable, false);
  assert.notEqual(
    recovery.initial?.storeId,
    recovery.reconstruction?.result?.storeId,
  );
  assert.equal(
    recovery.initial?.digest,
    recovery.reconstruction?.result?.digest,
  );
  assert.match(recovery.initial?.digest, sha);
  assert.equal(report.model?.paid, false);
  assert.equal(report.model?.requests, 16);
  assert.equal(report.model.executedRequests, 16 - reusedRequests);
  assert.equal(report.model.reusedRequests, reusedRequests);
  assert.equal(
    report.linuxImageQualification?.scope,
    'native-linux-arm64-image-only',
  );
  assert.equal(report.linuxImageQualification?.passed, true);
  assert.equal(
    report.linuxImageQualification?.sdkCommit,
    identity.worker.sdkCommit,
  );
  assert.equal(report.linuxImageQualification?.image, identity.worker.image);
  return report;
}

export async function optionalMacWorkerEvidence(directory, identity, drivers) {
  let report;
  try {
    const file = join(directory, 'mac-worker-qualification.json');
    const info = await lstat(file);
    assert.ok(
      info.isFile() && info.size > 0 && info.size <= 16 << 20,
      'Mac qualification must be bounded regular JSON evidence.',
    );
    report = JSON.parse(await readFile(file, 'utf8'));
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    return {
      status: 'pending',
      scope: macWorkerScope,
      reason:
        'No exact-package actual-Mac worker qualification was supplied. Native-local evidence remains separate.',
    };
  }
  const resolvedIdentity =
    typeof identity === 'function' ? await identity() : identity;
  const resolvedDrivers =
    typeof drivers === 'function' ? await drivers() : drivers;
  validateMacWorkerReport(report, {
    identity: resolvedIdentity,
    drivers: resolvedDrivers,
  });
  return {
    status: 'passed',
    scope: macWorkerScope,
    report: 'mac-worker-qualification.json',
    identity: report.identity,
    engine: report.engine,
  };
}
