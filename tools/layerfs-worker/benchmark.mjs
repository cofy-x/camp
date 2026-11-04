import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import {
  lstat,
  mkdir,
  mkdtemp,
  open,
  readFile,
  realpath,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { command, root as sourceRoot } from '../../scripts/lib.mjs';
import { hashFile } from '../../scripts/release.mjs';
import { saveState } from './state.mjs';
import {
  benchmarkOptions,
  benchmarkSetupComplete,
  benchmarkRoundsComplete,
  boundedJSON,
  excludedPackageArms,
  excludedCheckpointArms,
  packageBenchmarkInputs,
  verifyBenchmarkArchives,
  verifyBenchmarkRuntime,
  verifyNativeComparison,
  verifyPackageEngine,
  verifySetupOnlyReport,
} from './benchmark-inputs.mjs';
import { selectSourceEngine, requireSourceEngine } from './source-engine.mjs';

const { values } = parseArgs({
  options: {
    smoke: { type: 'boolean', default: false },
    help: { type: 'boolean', default: false },
    'output-base': { type: 'string' },
    qualification: { type: 'string' },
    'online-report': { type: 'string' },
    packages: { type: 'string' },
    'docker-binary': { type: 'string' },
    'docker-socket': { type: 'string' },
    'engine-profile': { type: 'string' },
    'setup-series': { type: 'string' },
    'checkpoint-only': { type: 'boolean', default: false },
    'sync-helper': { type: 'string' },
  },
  strict: true,
});
if (values.help) {
  process.stdout
    .write(`Compare three retained same-input Mac checkpoint rounds without model calls.
node tools/layerfs-worker/benchmark.mjs --output-base ABSOLUTE_PRIVATE_PARENT
Package mode: --qualification ABSOLUTE_JSON --online-report ABSOLUTE_JSON
  --docker-binary ABSOLUTE_FILE --docker-socket ABSOLUTE_SOCKET --engine-profile NAME
Optional offline setup series: --packages ABSOLUTE_DIR --setup-series native-local|layerfs-worker|both
Source focus: --checkpoint-only --sync-helper ABSOLUTE_HELPER with explicit Docker binary/socket/profile
--smoke is one text round only; source mode retains the existing upstream SDK comparison.\n`);
  process.exit(0);
}
const options = benchmarkOptions(values);
assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
if (options.packaged) assert.notEqual(process.getuid(), 0);
const inputs = options.packaged
  ? await packageBenchmarkInputs(values)
  : undefined;
const parent = path.resolve(values['output-base'] ?? '.data');
await mkdir(parent, { recursive: true, mode: 0o700 });
const root = await realpath(
  await mkdtemp(path.join(parent, 'mac-worker-benchmark-')),
);
if (inputs || options.checkpointOnly)
  for (
    let directory = path.dirname(root);
    ;
    directory = path.dirname(directory)
  ) {
    const scope = await lstat(path.join(directory, 'package.json')).catch(
      (error) => {
        if (error.code === 'ENOENT') return null;
        throw error;
      },
    );
    assert.equal(
      scope,
      null,
      'Keep private native fixture homes outside application npm ancestry.',
    );
    if (path.dirname(directory) === directory) break;
  }
const git = (args) =>
  execFileSync('git', args, { cwd: sourceRoot, encoding: 'utf8' }).trim();
const driverNames = [
  'tools/layerfs-worker/benchmark.mjs',
  'tools/layerfs-worker/benchmark-run.mjs',
  'tools/layerfs-worker/benchmark-inputs.mjs',
  'tools/layerfs-worker/tree.mjs',
  'tools/layerfs-worker/sync.mjs',
  'tools/layerfs-worker/checkpoint.mjs',
  'tools/layerfs-worker/client.mjs',
  'tools/layerfs-worker/contracts.mjs',
  'tools/layerfs-worker/home.mjs',
  'tools/layerfs-worker/references.mjs',
  'tools/layerfs-worker/transfer.mjs',
  'tools/layerfs-worker/source-engine.mjs',
  'tools/layerfs-worker/state.mjs',
  'tools/layerfs-worker/wire-proxy.mjs',
  'tools/layerfs-worker/upstream-comparison.mjs',
  'scripts/smoke-bundle.mjs',
  'scripts/setup-progress.mjs',
  'scripts/journal-evidence.mjs',
  'scripts/lib.mjs',
  'apps/server/internal/nativeexecutor/comparison_darwin_test.go',
];
const driverFiles = async () =>
  Object.fromEntries(
    await Promise.all(
      driverNames.map(async (name) => [
        name,
        await hashFile(path.join(sourceRoot, name)),
      ]),
    ),
  );
const report = {
  schema: 1,
  root,
  passed: false,
  repetitions: [],
  modelRequests: 0,
  scope: values.smoke
    ? 'one-round text harness preflight only'
    : options.packaged
      ? 'three-round exact packaged worker and full-tree local comparison; separately verified source native-local Prepare/publication'
      : options.checkpointOnly
        ? 'three-round source checkpoint/full-tree comparison; no upstream SDK or archived-package acceptance'
        : 'three-round source Mac four-workload comparison',
  ...(options.checkpointOnly ? { excludedArms: excludedCheckpointArms } : {}),
  policy:
    'All samples retained; OS/image caches not flushed, existing VM/services unchanged. Fresh client/worker/owned Store per round. Shared seal is added to each arm explicitly; source native production has its separately recorded process scope.',
  ...(inputs
    ? {
        packageIdentity: inputs.identity,
        qualificationSHA256: inputs.qualificationSHA256,
        onlineReportSHA256: inputs.onlineReportSHA256,
        excludedArms: excludedPackageArms,
        retainedSetupEvidence: inputs.setupEvidence,
      }
    : {}),
  setupSeries: [],
  untested: [
    'cold VM/physical startup',
    'network acquisition cost',
    'paid models',
  ],
};
let packageFile, packageFileSHA256;
const environment = { PATH: process.env.PATH, HOME: root, LANG: 'C.UTF-8' };
const node = inputs?.node || process.execPath;
let socket = inputs?.socket;
let sourceEngine, sourceEngineID;

async function verifyFixtures(fixtures) {
  const { scanTree } = await import(
    pathToFileURL(path.join(inputs.controller, 'tree.mjs'))
  );
  for (const item of fixtures) {
    const source = await realpath(item.source);
    assert.equal(source, item.source);
    assert.ok(source.startsWith(`${root}/round-${item.iteration}/`));
    assert.equal(
      (await scanTree(source)).digest,
      item.digest,
      'Retained input changed before or after native comparison.',
    );
  }
}

async function nativeRound(round) {
  const plan = path.join(root, `native-plan-round-${round.iteration}.json`);
  await writeFile(
    plan,
    `${JSON.stringify({ schema: 1, fixtures: round.nativeFixtures })}\n`,
    { flag: 'wx', mode: 0o600 },
  );
  const output = path.join(
    root,
    `native-production-round-${round.iteration}.json`,
  );
  const trace = path.join(root, `native-go-round-${round.iteration}.jsonl`);
  const stderr = path.join(root, `native-go-round-${round.iteration}.stderr`);
  const home = path.join(root, `native-go-home-${round.iteration}`);
  await mkdir(home, { mode: 0o700 });
  const temporary = path.join(home, 'tmp');
  await mkdir(temporary, { mode: 0o700 });
  const cache = execFileSync('go', ['env', 'GOCACHE', 'GOMODCACHE'], {
    encoding: 'utf8',
  })
    .trim()
    .split('\n');
  await verifyFixtures(round.nativeFixtures);
  const out = await open(trace, 'wx', 0o600),
    err = await open(stderr, 'wx', 0o600);
  try {
    await command(
      'go',
      [
        'test',
        '-json',
        '-run',
        '^TestNativeLocalComparisonMeasurements$',
        '-count=1',
        '-timeout',
        '20m',
        './apps/server/internal/nativeexecutor',
      ],
      {
        cwd: sourceRoot,
        stdio: ['ignore', out.fd, err.fd],
        env: {
          PATH: `${path.join(inputs.runtime, 'bin')}:${process.env.PATH}`,
          HOME: home,
          TMPDIR: temporary,
          LANG: 'C.UTF-8',
          GOCACHE: cache[0],
          GOMODCACHE: cache[1],
          GOPROXY: 'off',
          GOTOOLCHAIN: 'local',
          CAMP_TEST_DSH_RUNTIME: path.join(
            inputs.runtime,
            'runtime/dsh-runtime',
          ),
          CAMP_TEST_SANDBOX_HELPER: path.join(
            inputs.runtime,
            'bin/camp-sandbox',
          ),
          CAMP_MAC_COMPARISON_PLAN: plan,
          CAMP_MAC_COMPARISON_OUTPUT: output,
        },
      },
    );
  } finally {
    await out.close();
    await err.close();
  }
  await verifyFixtures(round.nativeFixtures);
  const native = (await boundedJSON(output)).value;
  const events = (await readFile(trace, 'utf8'))
    .trim()
    .split('\n')
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  verifyNativeComparison(native, events, round.nativeFixtures);
  return {
    iteration: round.iteration,
    status: 'passed',
    report: output,
    reportSHA256: await hashFile(output),
    traceSHA256: await hashFile(trace),
    scope:
      'Source Go native-local Prepare/publication with exact packaged helper/Node/DSH and identical retained inputs; not an archived Runner binary measurement.',
  };
}

async function setupSeries() {
  if (options.setupSeries === 'none') return;
  await verifyBenchmarkArchives(values.packages, inputs.identity);
  const server = path.join(root, 'setup-server');
  const bytes = execFileSync(
    'tar',
    [
      '-xOf',
      path.join(values.packages, inputs.identity.packages.server.filename),
      'camp-server-darwin-arm64/camp',
    ],
    { encoding: 'buffer', maxBuffer: 128 << 20 },
  );
  await writeFile(server, bytes, { flag: 'wx', mode: 0o700 });
  assert.equal(await hashFile(server), inputs.serverBinarySHA256);
  const backends =
    options.setupSeries === 'both'
      ? ['native-local', 'layerfs-worker']
      : [options.setupSeries];
  for (const backend of backends)
    for (let iteration = 1; iteration <= options.rounds; iteration++) {
      const output = path.join(root, `setup-${backend}-${iteration}.json`);
      const attempt = {
        iteration,
        backend,
        status: 'running',
        report: output,
        modelRequests: 'unknown',
      };
      report.setupSeries.push(attempt);
      await saveState(path.join(root, 'report.json'), report);
      try {
        await command(
          node,
          [path.join(sourceRoot, 'scripts/smoke-bundle.mjs'), 'offline'],
          {
            cwd: sourceRoot,
            env: {
              ...environment,
              CAMP_SMOKE_SETUP_ONLY: '1',
              CAMP_SMOKE_PACKAGE_DIRECTORY: values.packages,
              CAMP_SMOKE_SERVER_BINARY: server,
              CAMP_SMOKE_REPORT: output,
              CAMP_SMOKE_FIXTURE_DIRECTORY: path.join(
                root,
                `camp-release-setup-${backend}-${iteration}`,
              ),
              ...(backend === 'layerfs-worker'
                ? {
                    CAMP_TEST_WORKSPACE_BACKEND: backend,
                    CAMP_TEST_DOCKER_BINARY: inputs.dockerBinary,
                    CAMP_TEST_DOCKER_SOCKET: inputs.socket,
                    CAMP_TEST_WORKER_IMAGE: path.join(
                      values.packages,
                      inputs.identity.packages.worker.filename,
                    ),
                  }
                : {}),
            },
          },
        );
        const sample = (await boundedJSON(output)).value;
        attempt.modelRequests = sample.model?.requests ?? 'unknown';
        verifySetupOnlyReport(sample, inputs, backend);
        Object.assign(attempt, {
          status: 'passed',
          reportSHA256: await hashFile(output),
          timings: sample.timings,
          scope: sample.scope,
          modelRequests: 0,
        });
      } catch (error) {
        attempt.status = 'failed';
        attempt.error = { message: error.message };
        const failed = await boundedJSON(output).catch(() => null);
        if (failed) {
          attempt.reportSHA256 = failed.sha256;
          attempt.modelRequests = failed.value.model?.requests ?? 'unknown';
        }
        report.modelRequests =
          typeof attempt.modelRequests === 'number'
            ? attempt.modelRequests
            : 'unknown';
        throw error;
      } finally {
        await saveState(path.join(root, 'report.json'), report);
      }
    }
  await verifyBenchmarkArchives(values.packages, inputs.identity);
  benchmarkSetupComplete(report.setupSeries, options);
}

try {
  if (options.checkpointOnly) {
    sourceEngine = await selectSourceEngine({
      dockerBinary: values['docker-binary'],
      dockerSocket: values['docker-socket'],
    });
    const observed = requireSourceEngine(sourceEngine);
    sourceEngineID = observed.ID;
    socket = sourceEngine.proof.socket;
    report.sourceEngine = {
      id: sourceEngineID,
      profile: values['engine-profile'],
      proof: sourceEngine.proof,
    };
    report.sourceInstallationSHA256 = await hashFile(
      path.join(import.meta.dirname, 'installed.json'),
    );
  } else if (!inputs)
    socket = execFileSync(
      'docker',
      ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
      { encoding: 'utf8' },
    )
      .trim()
      .slice(7);
  report.driver = {
    sourceCommit: git(['rev-parse', 'HEAD']),
    sourceTree: git(['rev-parse', 'HEAD^{tree}']),
    modified: git(['status', '--porcelain']) !== '',
    files: await driverFiles(),
    node: process.versions.node,
  };
  if (inputs || options.checkpointOnly) {
    assert.equal(
      report.driver.modified,
      false,
      'Package measurements require a clean committed driver.',
    );
    assert.equal(
      process.versions.node,
      (await readFile(path.join(sourceRoot, '.node-version'), 'utf8')).trim(),
    );
    report.driver.go = execFileSync('go', ['version'], {
      encoding: 'utf8',
    }).trim();
    assert.ok(
      report.driver.go.startsWith(
        `go version go${(await readFile(path.join(sourceRoot, 'go.mod'), 'utf8')).match(/^go (\S+)$/m)[1]} `,
      ),
    );
  }
  if (inputs) {
    const dockerHome = path.join(root, 'docker-client');
    await mkdir(dockerHome, { mode: 0o700 });
    await writeFile(path.join(dockerHome, 'config.json'), '{}\n', {
      mode: 0o600,
    });
    const info = JSON.parse(
      execFileSync(inputs.dockerBinary, ['info', '--format', '{{json .}}'], {
        encoding: 'utf8',
        timeout: 20000,
        env: {
          ...environment,
          DOCKER_HOST: `unix://${inputs.socket}`,
          DOCKER_CONFIG: dockerHome,
        },
      }),
    );
    verifyPackageEngine(info, inputs);
    packageFile = path.join(root, 'package-inputs.json');
    await writeFile(packageFile, `${JSON.stringify(inputs)}\n`, {
      flag: 'wx',
      mode: 0o600,
    });
    packageFileSHA256 = await hashFile(packageFile);
    await setupSeries();
  }
  for (let iteration = 1; iteration <= options.rounds; iteration++) {
    let exit = 0;
    try {
      await command(
        node,
        [
          path.join(import.meta.dirname, 'benchmark-run.mjs'),
          root,
          socket,
          String(iteration),
          ...(values.smoke ? ['smoke'] : []),
        ],
        {
          cwd: sourceRoot,
          env: {
            ...environment,
            ...(!inputs &&
            (values['sync-helper'] || process.env.CAMP_TEST_SANDBOX_HELPER)
              ? {
                  CAMP_TEST_SANDBOX_HELPER:
                    values['sync-helper'] ||
                    process.env.CAMP_TEST_SANDBOX_HELPER,
                }
              : {}),
            ...(options.checkpointOnly
              ? {
                  CAMP_MAC_CHECKPOINT_ONLY: '1',
                  CAMP_MAC_CHECKPOINT_DOCKER_BINARY: values['docker-binary'],
                }
              : {}),
            ...(inputs
              ? {
                  CAMP_MAC_BENCHMARK_PACKAGE_INPUTS: packageFile,
                  CAMP_MAC_BENCHMARK_PACKAGE_INPUTS_SHA256: packageFileSHA256,
                }
              : {}),
          },
        },
      );
    } catch (error) {
      exit = 1;
      report.lastRoundError = { iteration, message: error.message };
    }
    report.repetitions.push({
      iteration,
      exit,
      report: path.join(root, `round-${iteration}/report.json`),
    });
    await saveState(path.join(root, 'report.json'), report);
    if (exit !== 0)
      throw new Error(
        'A comparison round failed; its original fixture is retained.',
      );
  }
  const rounds = await Promise.all(
    report.repetitions.map(
      async (item) => (await boundedJSON(item.report)).value,
    ),
  );
  const fixtures = benchmarkRoundsComplete(rounds, {
    packaged: options.packaged,
    checkpointOnly: options.checkpointOnly,
    smoke: values.smoke,
    root,
  });
  if (inputs)
    for (const round of rounds)
      assert.deepEqual(round.packageIdentity, inputs.identity);
  await saveState(path.join(root, 'native-plan.json'), {
    schema: 1,
    scope:
      'Identical retained input plan only; source native-local Prepare/publication is qualified separately from complete-tree local/worker restore.',
    fixtures,
  });
  if (inputs && !values.smoke) {
    report.nativeProduction = [];
    for (const round of rounds)
      report.nativeProduction.push(await nativeRound(round));
  } else
    report.nativeProduction = {
      status: 'not-run',
      scope: values.smoke
        ? 'Text preflight does not establish three-round native production comparison.'
        : 'Source mode retains its separate native-plan command.',
    };
  report.passed = true;
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  if (options.checkpointOnly && report.driver) {
    try {
      requireSourceEngine(sourceEngine, sourceEngineID);
      assert.equal(
        await hashFile(path.join(import.meta.dirname, 'installed.json')),
        report.sourceInstallationSHA256,
      );
      assert.equal(git(['rev-parse', 'HEAD']), report.driver.sourceCommit);
      assert.equal(git(['status', '--porcelain']), '');
      assert.deepEqual(await driverFiles(), report.driver.files);
    } catch (error) {
      report.inputError = { message: error.message };
      report.passed = false;
    }
  }
  try {
    if (inputs) {
      await verifyBenchmarkRuntime(
        inputs.runtime,
        inputs.identity,
        inputs.nodeVersion,
      );
      assert.equal(
        await hashFile(inputs.dockerBinary),
        inputs.dockerBinarySHA256,
      );
      assert.equal(git(['rev-parse', 'HEAD']), report.driver?.sourceCommit);
      assert.equal(git(['status', '--porcelain']), '');
      assert.deepEqual(await driverFiles(), report.driver.files);
    }
  } catch (error) {
    report.passed = false;
    report.inputError = { message: error.message };
  }
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Actual Mac ${report.scope} ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
