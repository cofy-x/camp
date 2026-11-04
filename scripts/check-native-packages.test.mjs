import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  checkNativePackages,
  nativePackageChecks,
} from './check-native-packages.mjs';

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'camp-native-package-check-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const packageDirectory = join(directory, 'packages');
  await mkdir(packageDirectory);
  const descriptor = {
    version: 'v0.1.0-alpha.9',
    sourceCommit: 'a'.repeat(40),
    runners: {
      'darwin-arm64': {
        installer: { sha256: '1'.repeat(64) },
        offline: { sha256: '2'.repeat(64) },
      },
    },
  };
  await writeFile(
    join(packageDirectory, 'runner-release.json'),
    JSON.stringify(descriptor),
  );
  const options = {
    packageDirectory,
    temporaryDirectory: join(directory, 'temporary'),
    outputDirectory: join(directory, 'evidence'),
  };
  const evidence = (mode) => ({
    status: 'passed',
    platform: 'darwin',
    architecture: 'arm64',
    version: descriptor.version,
    sourceCommit: descriptor.sourceCommit,
    mode,
    entrypoint: 'runner-installer',
    entrypointBinarySHA256: '3'.repeat(64),
    archiveSHA256:
      descriptor.runners['darwin-arm64'][
        mode === 'online' ? 'installer' : 'offline'
      ].sha256,
    checks: [...nativePackageChecks],
  });
  return { options, evidence };
}

test(
  'both complete package scenarios start concurrently with isolated fixtures and matching evidence',
  { timeout: 30000 },
  async (t) => {
    const { options, evidence } = await fixture(t);
    const entered = [];
    let bothStarted;
    const barrier = new Promise((resolve) => {
      bothStarted = resolve;
    });
    const report = await checkNativePackages({
      ...options,
      execute: async (binary, args, { env }) => {
        assert.equal(binary, process.execPath);
        assert.ok(args[0].endsWith('smoke-bundle.mjs'));
        const mode = args[1];
        entered.push({ mode, fixture: env.CAMP_SMOKE_FIXTURE_DIRECTORY });
        assert.equal(
          env.CAMP_SMOKE_PACKAGE_DIRECTORY,
          options.packageDirectory,
        );
        assert.equal(env.CAMP_SMOKE_SETUP_ONLY, '');
        assert.equal(env.CAMP_TEST_WORKSPACE_BACKEND, '');
        assert.equal(env.CAMP_SMOKE_RUNNER_BINARY, '');
        if (entered.length === 2) bothStarted();
        await barrier;
        await mkdir(env.CAMP_SMOKE_FIXTURE_DIRECTORY);
        await writeFile(env.CAMP_SMOKE_REPORT, JSON.stringify(evidence(mode)));
        await rm(env.CAMP_SMOKE_FIXTURE_DIRECTORY, { recursive: true });
      },
    });
    assert.equal(report.status, 'passed');
    assert.equal(new Set(entered.map((item) => item.fixture)).size, 2);
    assert.deepEqual(entered.map((item) => item.mode).sort(), [
      'offline',
      'online',
    ]);
    assert.equal(report.scenarios.length, 2);
    assert.ok(report.scenarios.every((item) => item.status === 'passed'));
  },
);

test('one scenario failure never cancels the other complete scenario', async (t) => {
  const { options, evidence } = await fixture(t);
  const executed = [];
  await assert.rejects(
    checkNativePackages({
      ...options,
      execute: async (binary, args, { env }) => {
        const mode = args[1];
        executed.push(mode);
        await mkdir(env.CAMP_SMOKE_FIXTURE_DIRECTORY);
        if (mode === 'online') throw new Error('Controlled child failure');
        await writeFile(env.CAMP_SMOKE_REPORT, JSON.stringify(evidence(mode)));
        await rm(env.CAMP_SMOKE_FIXTURE_DIRECTORY, { recursive: true });
      },
    }),
    /Both complete native package scenarios must pass/,
  );
  assert.deepEqual(executed.sort(), ['offline', 'online']);
  const report = JSON.parse(
    await readFile(join(options.outputDirectory, 'package-timings.json')),
  );
  assert.equal(report.status, 'failed');
  assert.equal(
    report.scenarios.find((item) => item.mode === 'offline').status,
    'passed',
  );
});

test('missing, stale, mismatched and incomplete package evidence fail closed', async (t) => {
  for (const change of [
    null,
    { status: 'failed' },
    { archiveSHA256: '4'.repeat(64) },
    { sourceCommit: 'b'.repeat(40) },
    { mode: 'offline' },
    { cleanupConfirmed: false },
    { checks: [] },
    { checks: nativePackageChecks.filter((check) => check !== 'native-task') },
  ]) {
    const { options, evidence } = await fixture(t);
    await writeFile(
      join(options.packageDirectory, 'smoke-online.json'),
      JSON.stringify(evidence('online')),
    );
    await assert.rejects(
      checkNativePackages({
        ...options,
        execute: async (binary, args, { env }) => {
          const mode = args[1];
          if (mode === 'online' && change === null) return;
          await writeFile(
            env.CAMP_SMOKE_REPORT,
            JSON.stringify({
              ...evidence(mode),
              ...(mode === 'online' ? change : {}),
            }),
          );
        },
      }),
      /Both complete native package scenarios must pass/,
    );
  }
});
