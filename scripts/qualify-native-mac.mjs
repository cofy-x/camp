import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, open, readFile, rm } from 'node:fs/promises';
import { cpus, release, tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { command, go, root } from './lib.mjs';

const { values } = parseArgs({
  options: {
    command: { type: 'string' },
    'tool-root': { type: 'string', multiple: true, default: [] },
    output: { type: 'string' },
  },
});
assert.equal(process.platform, 'darwin', 'Run on an actual Apple Silicon Mac.');
assert.equal(process.arch, 'arm64', 'Rosetta and Intel do not qualify ARM64.');
assert.equal(
  process.versions.node,
  (await readFile(join(root, '.node-version'), 'utf8')).trim(),
  'Use the pinned Node toolchain.',
);
const capture = (binary, args) =>
  execFileSync(binary, args, { cwd: root, encoding: 'utf8' }).trim();
const goVersion = capture(go, ['version']);
assert.ok(
  goVersion.includes(
    ` go${(await readFile(join(root, '.go-version'), 'utf8')).trim()} `,
  ),
  'Use the pinned Go toolchain.',
);
const output = resolve(
  values.output ??
    join('dist/native-mac', `operator-${Date.now()}-${process.pid}.json`),
);
await mkdir(dirname(output), { recursive: true });
// Reserve a new private report before running; never replace existing evidence.
const reportFile = await open(output, 'wx', 0o600);
const scratch = await mkdtemp(join(tmpdir(), 'camp-mac-qualification-'));
const log = await open(join(scratch, 'go-tests.jsonl'), 'wx', 0o600);
const report = {
  schema: 1,
  sourceCommit: capture('git', ['rev-parse', 'HEAD']),
  modified: capture('git', ['status', '--porcelain']).length > 0,
  host: {
    platform: 'darwin/arm64',
    osVersion: capture('/usr/bin/sw_vers', ['-productVersion']),
    kernel: release(),
    cpu: cpus()[0]?.model,
  },
  toolchains: { node: process.versions.node, go: goVersion },
  runtimes: {
    dsh: JSON.parse(
      await readFile(join(root, 'tools/dsh-runtime/package.json'), 'utf8'),
    ).dependencies,
    project: JSON.parse(
      await readFile(join(root, 'tools/project-runtime/package.json'), 'utf8'),
    ).dependencies,
  },
  operatorCommand: values.command
    ? {
        sha256: createHash('sha256').update(values.command).digest('hex'),
        toolGrants: values['tool-root'].length,
      }
    : null,
  status: 'failed',
  tests: [],
  untested: [
    'public package installation',
    'background service lifecycle (qualified separately with packages)',
    'existing Runner registration and journal upgrades',
    'Mac to Linux LayerFS worker transport',
    'paid model providers',
    'tool credentials, signing, GUI automation and network access',
  ],
};
let failed;
try {
  await command('make', ['build-sandbox']);
  await command(
    go,
    [
      'test',
      '-race',
      '-json',
      '-p=1',
      '-count=1',
      '-timeout=240s',
      // An interrupted diagnostic must not leave a standalone service fixture.
      // Actual packaged launchd lifecycle has independent CI/release acceptance.
      '-skip=^TestMacBackgroundServiceActualLaunchd$',
      ...[
        'processowner',
        'seatbelt',
        'nativejob',
        'resources',
        'runnertools',
        'sandboxexec',
        'cli',
        'daemon',
        'projectenv',
        'nativeexecutor',
      ].map((name) => `./apps/server/internal/${name}`),
    ],
    {
      stdio: ['ignore', log.fd, 'inherit'],
      env: {
        ...process.env,
        CAMP_TEST_MAC_NATIVE: '1',
        CAMP_TEST_MAC_PACKAGE: '1',
        CAMP_TEST_DSH_RUNTIME: join(root, 'tools/dsh-runtime'),
        CAMP_MAC_HELPER: join(root, 'dist/camp-sandbox'),
        CAMP_MAC_ACCEPTANCE_COMMAND: values.command ?? '',
        CAMP_MAC_ACCEPTANCE_TOOL_ROOTS: JSON.stringify(values['tool-root']),
      },
    },
  );
} catch (error) {
  failed = error;
} finally {
  await log.close();
  const events = (await readFile(join(scratch, 'go-tests.jsonl'), 'utf8'))
    .split('\n')
    .filter(Boolean)
    .flatMap((line) => {
      try {
        return [JSON.parse(line)];
      } catch {
        failed ??= new Error('The native test stream was interrupted.');
        return [];
      }
    });
  report.tests = events
    .filter((event) => ['pass', 'fail', 'skip'].includes(event.Action))
    .map(({ Package, Test, Action, Elapsed }) => ({
      package: Package,
      test: Test ?? null,
      status: Action,
      seconds: Elapsed,
    }));
  const required = [
    'TestApprovedMacGoToolchainBuildsAndRunsNativeMachO',
    'TestApprovedMacToolchainFindsUsrBinTools',
    'TestActualNativeReadWriteAndControlPorts',
    'TestNativeMaterializationNeverMergesPathAliases',
    'TestLaunchdCoalitionOwnsDetachedChildren',
    'TestOwnedJobCleansAfterParentDeath',
    'TestNativeCommandReadWriteNetworkAndReceipts',
    'TestNativeNormalExitAndTimeoutRemoveDetachedWriters',
    'TestNativeClientDeathCleansBeforeAnyRecoveryReceipt',
    'TestNativeLockedPnpmInstallAndCacheWithoutAmbientConfiguration',
    'TestNativeUnknownPreparationRetainsStagingAndNeverPublishesReady',
  ];
  if (values.command) required.push('TestMacOperatorToolAcceptance');
  if (
    !failed &&
    required.every((name) =>
      report.tests.some((test) => test.test === name && test.status === 'pass'),
    )
  )
    report.status = 'passed';
  else {
    failed ??= new Error('A required native tool test did not pass.');
    // Command output stays out of the retained report; show local diagnostics.
    process.stderr.write(
      events
        .filter((event) => event.Output)
        .map((event) => event.Output)
        .join('')
        .slice(-32768),
    );
  }
  report.completedAt = new Date().toISOString();
  await reportFile.writeFile(`${JSON.stringify(report, null, 2)}\n`);
  await reportFile.close();
  await rm(scratch, { recursive: true, force: true });
  console.log(`Native Mac qualification ${report.status}: ${output}`);
}
if (failed) throw failed;
