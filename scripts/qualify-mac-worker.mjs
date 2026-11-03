import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { constants, createWriteStream } from 'node:fs';
import {
  chmod,
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  open,
  readFile,
  realpath,
  writeFile,
} from 'node:fs/promises';
import { release as osRelease, tmpdir } from 'node:os';
import path from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { promisify } from 'node:util';
import { command, root } from './lib.mjs';
import { hashFile, validVersion } from './release.mjs';
import { httpProxyEnvironment } from './upgrade.mjs';
import {
  digestJSON,
  driverFiles,
  macWorkerArtifacts,
  macWorkerRecoverySummary,
  macWorkerScope,
  macWorkerServiceCleanupAction,
  macWorkerStorageSummary,
  readMacWorkerIdentity,
  validateMacWorkerReport,
} from './mac-worker-evidence.mjs';

const args = process.argv.slice(2);
if (args.includes('--help')) {
  console.log(`Qualify exact Mac LayerFS worker packages with fresh deterministic fixtures.
node scripts/qualify-mac-worker.mjs --packages ABSOLUTE_DIR | --version vX.Y.Z-alpha.N
  --baseline vX.Y.Z-alpha.N --docker-binary ABSOLUTE_FILE --docker-socket ABSOLUTE_SOCKET
  --engine-profile NAME [--output ABSOLUTE_NEW_JSON]
No enrolled configuration is read. Existing services/VMs and the global Docker context are unchanged.
Package installation, native-local and worker upgrades, checkpoint interruption and Store reconstruction are separate recorded scenarios.`);
  process.exit(0);
}
const options = {};
for (let i = 0; i < args.length; i += 2) {
  assert.ok(
    [
      '--packages',
      '--version',
      '--baseline',
      '--docker-binary',
      '--docker-socket',
      '--engine-profile',
      '--output',
    ].includes(args[i]) &&
      args[i + 1] &&
      !args[i + 1].startsWith('--'),
    'Use --help for qualification options.',
  );
  assert.ok(!options[args[i]], 'Do not repeat qualification options.');
  options[args[i]] = args[i + 1];
}
assert.notEqual(
  Boolean(options['--packages']),
  Boolean(options['--version']),
  'Choose an exact package directory or public version.',
);
for (const name of [
  '--packages',
  '--docker-binary',
  '--docker-socket',
  '--output',
])
  if (options[name])
    assert.ok(path.isAbsolute(options[name]), `${name} must be absolute.`);
assert.ok(options['--docker-binary'] && options['--docker-socket']);
assert.match(
  options['--engine-profile'] ?? '',
  /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$/,
);
const baseline = options['--baseline'];
assert.ok(
  validVersion(baseline),
  'Select an explicit fixed public --baseline.',
);
assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
assert.notEqual(process.getuid(), 0);
assert.ok(
  Number(osRelease().split('.')[0]) >= 24,
  'Qualification requires macOS 15+.',
);
assert.notEqual(
  process.env.NODE_TLS_REJECT_UNAUTHORIZED,
  '0',
  'Retain HTTPS certificate verification.',
);
if (options['--version']) assert.ok(validVersion(options['--version']));
const execute = promisify(execFile);
assert.equal(
  process.versions.node,
  (await readFile(path.join(root, '.node-version'), 'utf8')).trim(),
  'Use the pinned Node toolchain.',
);
const goVersion = (await execute('go', ['version'])).stdout.trim();
const pinnedGo = (await readFile(path.join(root, 'go.mod'), 'utf8')).match(
  /^go (\S+)$/m,
)?.[1];
assert.ok(
  goVersion.startsWith(`go version go${pinnedGo} `),
  'Use the pinned Go toolchain.',
);
const git = async (args) =>
  (await execute('git', args, { cwd: root })).stdout.trim();
assert.equal(
  await git(['status', '--porcelain']),
  '',
  'Use a clean committed qualification driver.',
);
// DSH fixture homes must remain outside the application's npm ancestry.
const campaign = await realpath(
  await mkdtemp(path.join(tmpdir(), 'camp-mac-worker-qualification-')),
);
await chmod(campaign, 0o700);
for (
  let directory = path.dirname(campaign);
  ;
  directory = path.dirname(directory)
) {
  const packageScope = await lstat(path.join(directory, 'package.json')).catch(
    (error) => {
      if (error.code === 'ENOENT') return null;
      throw error;
    },
  );
  assert.equal(
    packageScope,
    null,
    'Select a temporary directory outside npm package ancestry for private DSH fixtures.',
  );
  if (path.dirname(directory) === directory) break;
}
const packages = path.join(campaign, 'packages');
await mkdir(packages, { mode: 0o700 });
const output =
  options['--output'] || path.join(campaign, 'mac-worker-qualification.json');
const handle = await open(output, 'wx', 0o600);
const env = { PATH: process.env.PATH, HOME: process.env.HOME, LANG: 'C.UTF-8' };
const report = {
  schema: 1,
  scope: macWorkerScope,
  status: 'failed',
  startedAt: new Date().toISOString(),
  host: { platform: 'darwin/arm64', release: osRelease() },
  baseline,
  serverHarness: {
    scope: 'exact-packaged-server-executable-only',
  },
  scenarios: {},
  untested: [
    'paid model provider',
    'physical sleep/wake and login/boot startup',
    'other engine profiles or baseline versions',
    'physical host power loss and crash durability',
    'complete Runner execution inside a Linux VM',
    'Server directory import and background service (qualified separately with complete Server archives)',
  ],
};
const sha = /^[a-f0-9]{64}$/;

async function download(version, name, directory, maximum = 8 << 20) {
  assert.equal(path.basename(name), name);
  assert.ok(validVersion(version));
  const response = await fetch(
    `https://github.com/cofy-x/camp/releases/download/${version}/${encodeURIComponent(name)}`,
    { signal: AbortSignal.timeout(300_000) },
  );
  assert.equal(
    response.status,
    200,
    `Could not download fixed public asset ${name}.`,
  );
  assert.equal(new URL(response.url).protocol, 'https:');
  let bytes = 0;
  const bound = new Transform({
    transform(chunk, encoding, done) {
      bytes += chunk.length;
      done(
        bytes > maximum
          ? new Error('Public asset exceeds its declared bound.')
          : null,
        chunk,
      );
    },
  });
  await pipeline(
    Readable.fromWeb(response.body),
    bound,
    createWriteStream(path.join(directory, name), { flags: 'wx', mode: 0o600 }),
  );
}

async function copyInput(name, directory = packages, maximum = 2 ** 31) {
  assert.equal(path.basename(name), name);
  const source = path.join(options['--packages'], name);
  const info = await lstat(source);
  assert.ok(
    info.isFile() && info.size > 0 && info.size <= maximum,
    `Package input exceeds its regular-file bound: ${name}`,
  );
  await copyFile(
    source,
    path.join(directory, name),
    constants.COPYFILE_EXCL | constants.COPYFILE_FICLONE,
  );
}

const read = async (file) => {
  const info = await lstat(file);
  assert.ok(
    info.isFile() && info.size > 0 && info.size <= 16 << 20,
    'JSON evidence must be a bounded regular file.',
  );
  return JSON.parse(await readFile(file, 'utf8'));
};

async function metadata(version, directory, names) {
  for (const name of names) await download(version, name, directory);
  const checksums = await readFile(path.join(directory, 'SHA256SUMS'), 'utf8');
  for (const name of names.filter((name) => name !== 'SHA256SUMS')) {
    const lines = checksums
      .split('\n')
      .filter((line) => line.endsWith(`  ${name}`));
    assert.equal(
      lines.length,
      1,
      `Missing unique public checksum for ${name}.`,
    );
    assert.equal(
      await hashFile(path.join(directory, name)),
      lines[0].slice(0, 64),
    );
  }
}

async function verifyFile(directory, item) {
  assert.equal(path.basename(item.filename), item.filename);
  assert.match(item.sha256, sha);
  const file = path.join(directory, item.filename),
    info = await lstat(file);
  assert.ok(
    info.isFile() &&
      info.size === item.bytes &&
      info.size > 0 &&
      info.size <= 2 ** 31,
  );
  assert.equal(await hashFile(file), item.sha256);
  assert.equal(
    await readFile(`${file}.sha256`, 'utf8'),
    `${item.sha256}  ${item.filename}\n`,
  );
}

async function scenario(name, mode, extra = {}) {
  const file = path.join(campaign, `${name}.json`);
  const fixture = path.join(campaign, `camp-release-${name}`);
  let passed = false;
  try {
    await command(
      process.execPath,
      [path.join(root, 'scripts/smoke-bundle.mjs'), mode],
      {
        env: {
          ...env,
          ...httpProxyEnvironment(),
          ...extra,
          CAMP_TEST_WORKSPACE_BACKEND: 'layerfs-worker',
          CAMP_TEST_DOCKER_BINARY: options['--docker-binary'],
          CAMP_TEST_DOCKER_SOCKET: options['--docker-socket'],
          CAMP_TEST_WORKER_IMAGE: path.join(
            packages,
            report.identity.packages.worker.filename,
          ),
          CAMP_SMOKE_PACKAGE_DIRECTORY: packages,
          CAMP_SMOKE_SERVER_BINARY: path.join(campaign, 'camp-server'),
          CAMP_SMOKE_REPORT: file,
          CAMP_SMOKE_FIXTURE_DIRECTORY: fixture,
          CAMP_TEST_PREVIOUS_RELEASE: baseline,
        },
      },
    );
    passed = true;
  } finally {
    if (!passed) {
      const executable = path.join(
        fixture,
        "relocated Runner's installation/camp",
      );
      const data = path.join(fixture, "Runner's private data");
      const runnerCommand = (args) =>
        execute(executable, ['runner', ...args, '--data-dir', data], {
          cwd: fixture,
          env: { PATH: '/usr/bin:/bin', HOME: fixture, LANG: env.LANG },
          maxBuffer: 1 << 20,
          timeout: 90_000,
          killSignal: 'SIGKILL',
        });
      const serviceAction = async () =>
        macWorkerServiceCleanupAction(
          JSON.parse((await runnerCommand(['status', '--json'])).stdout),
        );
      try {
        const action = await serviceAction();
        assert.notEqual(
          action,
          'uncertain',
          'Runner service ownership was not established by status --json.',
        );
        if (action === 'remove') {
          await runnerCommand(['service', 'remove']);
          assert.equal(
            await serviceAction(),
            'absent',
            'Runner service removal was not confirmed by status --json.',
          );
        }
        report.cleanup = {
          status: 'confirmed',
          scenario: name,
          service: action === 'remove' ? 'removed' : 'not-installed',
        };
      } catch (error) {
        report.cleanup = {
          status: 'uncertain',
          scenario: name,
          reason: error.message,
        };
      }
    }
  }
  const value = await read(file);
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
  report.scenarios[name] = {
    ...Object.fromEntries(
      fields
        .filter((key) => value[key] !== undefined)
        .map((key) => [key, value[key]]),
    ),
    checkpointStorage: macWorkerStorageSummary(value.checkpointStorage),
    reportSHA256: await hashFile(file),
  };
  return value;
}

try {
  if (options['--version']) {
    await metadata(options['--version'], packages, [
      'runner-release.json',
      'server-release.json',
      'checkpoint-worker-release.json',
      'worker-arm64-qualification.json',
      'SHA256SUMS',
    ]);
    const runner = await read(path.join(packages, 'runner-release.json')),
      server = await read(path.join(packages, 'server-release.json')),
      worker = await read(
        path.join(packages, 'checkpoint-worker-release.json'),
      );
    assert.equal(runner.version, options['--version']);
    const items = Object.values(macWorkerArtifacts(runner, server, worker));
    for (const item of items) {
      assert.ok(
        Number.isSafeInteger(item.bytes) &&
          item.bytes > 0 &&
          item.bytes <= 2 ** 31,
      );
      await download(options['--version'], item.filename, packages, item.bytes);
      await download(
        options['--version'],
        `${item.filename}.sha256`,
        packages,
        4096,
      );
      await verifyFile(packages, item);
    }
  } else {
    for (const name of [
      'runner-release.json',
      'server-release.json',
      'checkpoint-worker-release.json',
      'worker-arm64-qualification.json',
    ])
      await copyInput(name, packages, 8 << 20);
    const runner = await read(path.join(packages, 'runner-release.json')),
      server = await read(path.join(packages, 'server-release.json')),
      worker = await read(
        path.join(packages, 'checkpoint-worker-release.json'),
      );
    for (const item of Object.values(
      macWorkerArtifacts(runner, server, worker),
    )) {
      assert.ok(
        item?.filename &&
          Number.isSafeInteger(item.bytes) &&
          item.bytes > 0 &&
          item.bytes <= 2 ** 31,
      );
      await copyInput(item.filename);
      await copyInput(`${item.filename}.sha256`);
      await verifyFile(packages, item);
    }
  }
  report.identity = await readMacWorkerIdentity(packages);
  assert.notEqual(
    baseline,
    report.identity.version,
    'An upgrade baseline must differ from the target version.',
  );
  const sdk = await read(path.join(root, 'tools/layerfs-runtime/pin.json'));
  assert.equal(
    report.identity.worker.sdkCommit,
    sdk.commit,
    'The qualification driver must support this fixed SDK.',
  );
  assert.equal(report.identity.worker.sdkVersion, sdk.version);
  const linux = await read(
    path.join(packages, 'worker-arm64-qualification.json'),
  );
  assert.equal(linux.passed, true);
  assert.equal(linux.platform, 'linux/arm64');
  assert.equal(linux.sourceCommit, report.identity.worker.sdkCommit);
  assert.equal(linux.image, report.identity.worker.image);
  report.linuxImageQualification = {
    scope: 'native-linux-arm64-image-only',
    passed: true,
    sdkCommit: linux.sourceCommit,
    image: linux.image,
    reportSHA256: await hashFile(
      path.join(packages, 'worker-arm64-qualification.json'),
    ),
  };
  const files = await driverFiles(root);
  report.driver = {
    sourceCommit: await git(['rev-parse', 'HEAD']),
    sourceTree: await git(['rev-parse', 'HEAD^{tree}']),
    modified: false,
    tools: { node: process.versions.node, go: goVersion },
    files,
    sha256: digestJSON(files),
  };
  const { stdout: server } = await execute(
    'tar',
    [
      '-xOf',
      path.join(packages, report.identity.packages.server.filename),
      'camp-server-darwin-arm64/camp',
    ],
    { encoding: 'buffer', maxBuffer: 128 << 20 },
  );
  await writeFile(path.join(campaign, 'camp-server'), server, { mode: 0o700 });
  await chmod(path.join(campaign, 'camp-server'), 0o700);
  report.serverBinarySHA256 = createHash('sha256').update(server).digest('hex');
  const dockerHome = path.join(campaign, 'docker-client');
  await mkdir(dockerHome, { mode: 0o700 });
  await writeFile(path.join(dockerHome, 'config.json'), '{}\n', {
    mode: 0o600,
  });
  const { stdout: info } = await execute(
    options['--docker-binary'],
    ['info', '--format', '{{json .}}'],
    {
      env: {
        PATH: env.PATH,
        HOME: campaign,
        LANG: env.LANG,
        DOCKER_HOST: `unix://${await realpath(options['--docker-socket'])}`,
        DOCKER_CONFIG: dockerHome,
      },
      maxBuffer: 1 << 20,
    },
  );
  const engine = JSON.parse(info);
  report.engine = {
    profile: options['--engine-profile'],
    architecture: engine.Architecture,
    kernel: engine.KernelVersion,
    operatingSystem: engine.OperatingSystem,
    storageDriver: engine.Driver,
    dockerVersion: engine.ServerVersion,
    cpus: engine.NCPU,
    memoryBytes: engine.MemTotal,
    identitySHA256: createHash('sha256').update(engine.ID).digest('hex'),
  };
  assert.ok(['arm64', 'aarch64'].includes(engine.Architecture));
  const baselineDirectory = path.join(campaign, 'baseline');
  await mkdir(baselineDirectory, { mode: 0o700 });
  await metadata(baseline, baselineDirectory, [
    'runner-release.json',
    'checkpoint-worker-release.json',
    'SHA256SUMS',
  ]);
  const priorRunner = await read(
      path.join(baselineDirectory, 'runner-release.json'),
    ),
    priorWorker = await read(
      path.join(baselineDirectory, 'checkpoint-worker-release.json'),
    );
  assert.equal(priorRunner.version, baseline);
  assert.equal(priorWorker.campSourceCommit, priorRunner.sourceCommit);
  assert.equal(priorWorker.platform, 'linux/arm64');
  assert.match(priorWorker.image, /^sha256:[a-f0-9]{64}$/);
  const prior = priorWorker.artifact;
  assert.match(prior.sha256, sha);
  assert.equal(
    prior.filename,
    `camp-layerfs-worker-linux-arm64-${prior.sha256}.tar.gz`,
  );
  assert.ok(
    Number.isSafeInteger(prior.bytes) &&
      prior.bytes > 0 &&
      prior.bytes <= 2 ** 31,
  );
  const reusedImage =
    prior.sha256 === report.identity.packages.worker.sha256 &&
    prior.bytes === report.identity.packages.worker.bytes &&
    priorWorker.image === report.identity.worker.image &&
    priorWorker.sourceCommit === report.identity.worker.sdkCommit &&
    priorWorker.buildId === report.identity.worker.buildId;
  if (reusedImage) {
    for (const name of [prior.filename, `${prior.filename}.sha256`])
      await copyFile(
        path.join(packages, name),
        path.join(baselineDirectory, name),
        constants.COPYFILE_EXCL | constants.COPYFILE_FICLONE,
      );
  } else {
    await download(baseline, prior.filename, baselineDirectory, prior.bytes);
    await download(
      baseline,
      `${prior.filename}.sha256`,
      baselineDirectory,
      4096,
    );
  }
  report.baselineWorkerImage = {
    archiveSHA256: prior.sha256,
    image: priorWorker.image,
    campSourceCommit: priorRunner.sourceCommit,
    reusedIdenticalArtifact: reusedImage,
  };
  await verifyFile(baselineDirectory, prior);
  const online = await scenario('online', 'online');
  await scenario('offline', 'offline');
  const fixture = await realpath(online.retainedFixtureDirectory);
  assert.ok(path.basename(fixture).startsWith('camp-release-'));
  const config = await read(
    path.join(fixture, "Runner's private data/daemon.json"),
  );
  const runtime = await realpath(config.bundleDir);
  assert.ok(
    path.relative(fixture, runtime).split(path.sep)[0] !== '..' &&
      !path.isAbsolute(path.relative(fixture, runtime)),
  );
  assert.equal(
    await hashFile(path.join(runtime, 'manifest.json')),
    report.identity.runtimeManifestSHA256,
  );
  for (const [name, hash] of Object.entries(report.identity.controllerFiles)) {
    assert.ok((await lstat(path.join(runtime, name))).isFile());
    assert.equal(await hashFile(path.join(runtime, name)), hash);
  }
  const upgraded = await scenario('defaultUpgrade', 'previous-upgrade', {
    CAMP_TEST_BASELINE_BACKEND: 'native-local',
  });
  const cache = path.join(
    upgraded.retainedFixtureDirectory,
    "Runner's private data/runtime/cache",
  );
  await scenario('workerUpgrade', 'previous-upgrade', {
    CAMP_TEST_BASELINE_BACKEND: 'layerfs-worker',
    CAMP_TEST_BASELINE_WORKER_IMAGE: path.join(
      baselineDirectory,
      prior.filename,
    ),
    CAMP_TEST_UPGRADE_RUNTIME_CACHE: cache,
  });
  const common = {
    ...env,
    CAMP_SMOKE_SERVER_BINARY: path.join(campaign, 'camp-server'),
    CAMP_TEST_DOCKER_BINARY: options['--docker-binary'],
    CAMP_TEST_DOCKER_SOCKET: options['--docker-socket'],
  };
  const restartFile = path.join(campaign, 'restart.json');
  await command(
    process.execPath,
    [
      path.join(root, 'tools/layerfs-worker/qualify-runner-restart.mjs'),
      path.join(campaign, 'online.json'),
    ],
    { env: { ...common, CAMP_TEST_WORKER_REPORT: restartFile } },
  );
  const restart = await read(restartFile);
  report.scenarios.restart = Object.fromEntries(
    [
      'passed',
      'scope',
      'entrypointBinarySHA256',
      'published',
      'nativeCaptureRetained',
      'modelRequests',
      'checkpointAfterObservation',
    ].map((name) => [name, restart[name]]),
  );
  report.scenarios.restart.reportSHA256 = await hashFile(restartFile);
  const recoveryFile = path.join(campaign, 'recovery.json');
  await command(
    process.execPath,
    [path.join(root, 'tools/layerfs-worker/qualify-recovery.mjs')],
    {
      env: {
        ...common,
        CAMP_TEST_LAYERFS_WORKER_RUNTIME: path.join(
          runtime,
          'runtime/layerfs-worker',
        ),
        CAMP_TEST_WORKER_REPORT: recoveryFile,
      },
    },
  );
  const recovery = await read(recoveryFile);
  report.scenarios.recovery = macWorkerRecoverySummary(
    recovery,
    await hashFile(recoveryFile),
  );
  report.model = {
    paid: false,
    requests: 16,
    kind: 'deterministic-local-provider',
  };
  assert.deepEqual(
    await driverFiles(root),
    files,
    'Qualification drivers changed during this campaign.',
  );
  assert.equal(
    await git(['status', '--porcelain']),
    '',
    'Qualification source changed during the campaign.',
  );
  report.status = 'passed';
  validateMacWorkerReport(report, {
    identity: report.identity,
    drivers: files,
    baseline,
  });
} catch (error) {
  report.status = 'failed';
  report.error = {
    code: error.code || 'QualificationFailed',
    message: error.message,
  };
  process.exitCode = 1;
} finally {
  report.completedAt = new Date().toISOString();
  const body = Buffer.from(`${JSON.stringify(report, null, 2)}\n`);
  await handle.write(body, 0, body.length, 0);
  await handle.sync();
  await handle.close();
  console.log(`Mac worker qualification ${report.status}: ${output}`);
  console.log(
    `Private fixture reports and exact package inputs retained: ${campaign}`,
  );
}
