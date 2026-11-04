import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  rmdir,
  writeFile,
} from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { command, root } from './lib.mjs';
import { verifyRunnerInstallationReport } from './release.mjs';
import { offlineSetupChecks } from './setup-progress.mjs';

export const nativePackageChecks = [
  ...offlineSetupChecks,
  'native-task',
  'configured-checks',
  'binary-transfer',
  'single-publication',
  'human-approval-and-delivery',
  'stopped-backup-and-restore',
  'cleanup',
  'launchd-background-lifecycle',
  'offline-checkpoint-storage-without-model-replay',
  'live-advisory-storage-heartbeat',
  'explicit-checkpoint-budget-quarantine-restore-purge',
];

export async function checkNativePackages({
  packageDirectory = join(root, 'dist'),
  temporaryDirectory = process.env.RUNNER_TEMP || join(root, '.data'),
  outputDirectory = join(root, 'dist/native-mac'),
  platform = 'darwin-arm64',
  sourceCommit,
  execute = command,
} = {}) {
  await mkdir(temporaryDirectory, { recursive: true });
  await mkdir(outputDirectory, { recursive: true });
  const fixtures = await mkdtemp(
    join(temporaryDirectory, 'camp-native-acceptance-'),
  );
  const descriptor = JSON.parse(
    await readFile(join(packageDirectory, 'runner-release.json'), 'utf8'),
  );
  const identity = {
    ...descriptor,
    version: descriptor.version || 'dev',
    sourceCommit: descriptor.sourceCommit || '',
  };
  const report = {
    schema: 1,
    sourceCommit,
    execution: 'sequential',
    status: 'running',
    scenarios: [],
  };
  const output = join(outputDirectory, 'package-timings.json');
  await writeFile(output, `${JSON.stringify(report, null, 2)}\n`);
  const started = performance.now();
  const runScenario = async (mode) => {
    const filename = join(packageDirectory, `smoke-${mode}.json`);
    await rm(filename, { force: true });
    const scenario = { mode, status: 'failed', elapsedMs: 0 };
    report.scenarios.push(scenario);
    const begin = performance.now();
    console.log(`Native package ${mode}: started.`);
    try {
      await execute(
        process.execPath,
        [join(root, 'scripts/smoke-bundle.mjs'), mode],
        {
          env: {
            ...process.env,
            CAMP_SMOKE_SETUP_ONLY: '',
            CAMP_TEST_WORKSPACE_BACKEND: '',
            CAMP_SMOKE_RUNNER_BINARY: '',
            CAMP_SMOKE_SERVER_BINARY: '',
            CAMP_SMOKE_PACKAGE_DIRECTORY: resolve(packageDirectory),
            CAMP_SMOKE_FIXTURE_DIRECTORY: join(fixtures, mode),
            CAMP_SMOKE_REPORT: filename,
          },
        },
      );
      const evidence = JSON.parse(await readFile(filename, 'utf8'));
      verifyRunnerInstallationReport(evidence, identity, platform, mode);
      assert.ok(Array.isArray(evidence.checks));
      for (const check of nativePackageChecks)
        assert.ok(
          evidence.checks.includes(check),
          `Missing ${mode} evidence: ${check}`,
        );
      assert.notEqual(evidence.cleanupConfirmed, false);
      scenario.status = 'passed';
    } finally {
      scenario.elapsedMs = performance.now() - begin;
      console.log(
        `Native package ${mode}: ${scenario.status} in ${(scenario.elapsedMs / 1000).toFixed(3)}s.`,
      );
    }
  };
  const results = [];
  for (const mode of ['online', 'offline']) {
    try {
      await runScenario(mode);
      results.push({ status: 'fulfilled' });
    } catch (reason) {
      results.push({ status: 'rejected', reason });
    }
  }
  const errors = results
    .filter((result) => result.status === 'rejected')
    .map((result) => result.reason);
  if (!errors.length) {
    try {
      // Each smoke owns and removes its fixture. Never recursively erase an
      // installation whose cleanup or ownership has not been established.
      await rmdir(fixtures);
    } catch (error) {
      errors.push(error);
    }
  }
  report.elapsedMs = performance.now() - started;
  report.status = errors.length ? 'failed' : 'passed';
  await writeFile(output, `${JSON.stringify(report, null, 2)}\n`);
  if (errors.length) {
    console.error(`Retained native package fixtures: ${fixtures}`);
    throw new AggregateError(
      errors,
      'Both complete native package scenarios must pass with matching evidence and confirmed cleanup.',
    );
  }
  console.log(
    'Both complete native package scenarios and their evidence passed.',
  );
  return report;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  assert.equal(process.platform, 'darwin');
  assert.equal(process.arch, 'arm64');
  await checkNativePackages({
    sourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], {
      cwd: root,
      encoding: 'utf8',
    }).trim(),
  });
}
