import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  serverService,
  assertServerServiceReceipt,
} from './server-service.mjs';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'camp-server-service-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const data = join(root, 'private-data');
  await mkdir(data, { mode: 0o700 });
  return { root, data };
}

function receiptFixture() {
  return {
    schema: 1,
    version: 'v0.1.0-alpha.10',
    sourceCommit: 'a'.repeat(40),
    platform: 'darwin/arm64',
    status: 'passed',
    mode: 'service',
    cleanupConfirmed: true,
    retainedDataPreserved: true,
    modelRequests: 0,
    sourceModified: false,
    binarySHA256: 'b'.repeat(64),
    checks: [
      'markdown-and-plaintext',
      'original-only-attachments-preserved',
      'retired-document-api-absent',
      'allowlisted-server-directory-import-preserves-source',
      'restart-preserves-files',
      'packaged-server-launchd-lifecycle',
      'service-removal-preserves-private-data',
      'owned-service-absent',
    ],
    service: ['install', 'start', 'stop', 'start', 'stop', 'remove'].map(
      (action) => ({
        action,
        supported: true,
        installed: action !== 'remove',
        loaded: action === 'start',
        running: action === 'start',
        loginEnabled: false,
        state:
          action === 'remove'
            ? 'absent'
            : action === 'start'
              ? 'running'
              : 'stopped',
      }),
    ),
  };
}

test('Server transport uses explicit service actions and confirms stop/removal without enabling login', async (t) => {
  const { root, data } = await fixture(t);
  const calls = [];
  const state = {
    supported: true,
    state: 'absent',
    installed: false,
    loaded: false,
    running: false,
    loginEnabled: false,
  };
  const adapter = serverService({
    binary: '/fixed/camp',
    data,
    cwd: root,
    environment: { PATH: '/usr/bin:/bin' },
    address: '127.0.0.1:3001',
    run: async (binary, args, options) => {
      assert.equal(binary, '/fixed/camp');
      assert.equal(options.env.PATH, '/usr/bin:/bin');
      assert.deepEqual(args.slice(-2), ['--data-dir', data]);
      const action = args[1];
      calls.push(action);
      if (action === 'install')
        Object.assign(state, { state: 'stopped', installed: true });
      if (action === 'start') {
        Object.assign(state, { state: 'running', loaded: true, running: true });
        await mkdir(join(data, 'logs'));
        await writeFile(
          join(data, 'logs/server.log'),
          'Open Camp: http://127.0.0.1:3001\n',
        );
      }
      if (action === 'stop')
        Object.assign(state, {
          state: 'stopped',
          loaded: false,
          running: false,
        });
      if (action === 'remove')
        Object.assign(state, { state: 'absent', installed: false });
      return { stdout: JSON.stringify(state) };
    },
  });
  await adapter.install();
  await adapter.start(async () => true);
  await adapter.stop();
  await adapter.remove();
  assert.deepEqual(
    calls.filter((value) => value !== 'status'),
    ['install', 'start', 'stop', 'remove'],
  );
  assert.deepEqual(
    adapter.observations.map((value) => value.action),
    ['install', 'start', 'stop', 'remove'],
  );
});

test('uncertain service inspection preserves a failed fixture without replay or removal', async (t) => {
  const { root, data } = await fixture(t);
  const retained = join(data, 'private-marker');
  await writeFile(retained, 'keep exact bytes');
  const calls = [];
  const adapter = serverService({
    binary: '/fixed/camp',
    data,
    cwd: root,
    environment: {},
    address: '127.0.0.1:3001',
    run: async (_binary, args) => {
      calls.push(args[1]);
      if (args[1] === 'install') throw new Error('installation response lost');
      return {
        stdout: JSON.stringify({
          supported: true,
          state: 'unknown',
          installed: true,
          loginEnabled: false,
        }),
      };
    },
  });
  await assert.rejects(adapter.install(), /response lost/);
  await assert.rejects(adapter.remove(), /ownership is uncertain/);
  assert.deepEqual(calls, ['install', 'status']);
  assert.equal(await readFile(retained, 'utf8'), 'keep exact bytes');
});

test('Server release evidence rejects foreground, unknown cleanup, changed source and failed preservation', () => {
  const identity = {
    version: 'v0.1.0-alpha.10',
    sourceCommit: 'a'.repeat(40),
    platform: 'darwin/arm64',
  };
  const report = receiptFixture();
  assertServerServiceReceipt(report, identity);
  for (const [key, value] of Object.entries({
    mode: 'foreground',
    cleanupConfirmed: false,
    retainedDataPreserved: false,
    modelRequests: 1,
    sourceModified: true,
    sourceCommit: 'c'.repeat(40),
    platform: 'darwin/x64',
    service: [],
  }))
    assert.throws(
      () => assertServerServiceReceipt({ ...report, [key]: value }, identity),
      key,
    );
});

test('Server acceptance rejects missing schema/checks and every incomplete restart lifecycle', () => {
  const report = receiptFixture();
  assertServerServiceReceipt(report);
  for (const schema of [undefined, 0, 2])
    assert.throws(() => assertServerServiceReceipt({ ...report, schema }));
  assert.throws(() =>
    assertServerServiceReceipt({ ...report, checks: undefined }),
  );
  for (const check of report.checks)
    assert.throws(
      () =>
        assertServerServiceReceipt({
          ...report,
          checks: report.checks.filter((value) => value !== check),
        }),
      check,
    );
  for (let index = 0; index < report.service.length; index++) {
    assert.throws(
      () =>
        assertServerServiceReceipt({
          ...report,
          service: report.service.filter(
            (_value, position) => position !== index,
          ),
        }),
      `missing observation ${index}`,
    );
    for (const key of [
      'supported',
      'installed',
      'loaded',
      'running',
      'loginEnabled',
      'state',
    ]) {
      const service = structuredClone(report.service);
      delete service[index][key];
      assert.throws(
        () => assertServerServiceReceipt({ ...report, service }),
        `missing ${key} in observation ${index}`,
      );
    }
  }
  const reordered = structuredClone(report.service);
  [reordered[1], reordered[2]] = [reordered[2], reordered[1]];
  assert.throws(() =>
    assertServerServiceReceipt({ ...report, service: reordered }),
  );
});

test('operator qualification excludes both source launchd fixtures while preserving native command tests', async () => {
  const source = await readFile(
    new URL('./qualify-native-mac.mjs', import.meta.url),
    'utf8',
  );
  const pattern = new RegExp(source.match(/'-skip=([^']+)'/)[1]);
  for (const name of [
    'TestMacBackgroundServiceActualLaunchd',
    'TestMacServerServiceActualLaunchdAndRetainedData',
  ])
    assert.ok(pattern.test(name));
  assert.equal(pattern.test('TestOwnedJobCleansAfterParentDeath'), false);
});

test('both ARM64 release hosts verify the complete Server archive and public Go uses the same service mode', async () => {
  const workflow = await readFile(
    new URL('../.github/workflows/release.yml', import.meta.url),
    'utf8',
  );
  const runner = workflow.slice(
    workflow.indexOf('  mac_runner:'),
    workflow.indexOf('  candidate_evidence:'),
  );
  assert.match(runner, /host: \[macos-15, macos-26\]/);
  assert.match(
    runner,
    /CAMP_SMOKE_BINARY: .*\/dist\/camp-server-darwin-arm64\/camp/,
  );
  assert.match(
    runner,
    /CAMP_SERVER_SERVICE_REPORT: .*server-service-verification-/,
  );
  assert.match(
    runner,
    /run: node scripts\/smoke-server-service\.mjs --service/,
  );
  const go = workflow.slice(workflow.indexOf('  go_install:'));
  assert.match(go, /host: macos-15-intel/);
  assert.match(go, /run: node scripts\/smoke-server-service\.mjs --service/);
});
