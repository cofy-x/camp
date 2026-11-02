import assert from 'node:assert/strict';
import { join } from 'node:path';
import { command, go } from './lib.mjs';

// Fixed public packages; retain the oldest supported baseline separately from
// the immediately previous release. The launcher verifies immutable archives.
export const upgradeBaseline = 'v0.1.0-alpha.2';
export const previousRelease = 'v0.1.0-alpha.8';

export async function baselineLauncher(
  directory,
  role,
  version = upgradeBaseline,
) {
  const binary = join(directory, 'baseline-camp');
  const cache = join(directory, 'baseline-cache');
  await command(go, [
    'build',
    '-trimpath',
    '-ldflags',
    `-X main.version=${version}`,
    '-o',
    binary,
    '.',
  ]);
  await command(binary, [role === 'runner' ? 'runner' : 'serve', '--help'], {
    cwd: directory,
    env: {
      PATH: process.env.PATH,
      HOME: directory,
      LANG: 'C.UTF-8',
      XDG_CACHE_HOME: cache,
    },
  });
  return { binary, cache };
}

export function verifyPreviousRunnerUpgrade(report, descriptor, platform) {
  const [os, arch] = platform.split('-');
  assert.equal(report.status, 'passed');
  assert.equal(report.mode, 'previous-upgrade');
  assert.equal(report.platform, os);
  assert.equal(report.architecture, arch === 'amd64' ? 'x64' : arch);
  assert.equal(report.version, descriptor.version);
  assert.equal(report.sourceCommit, descriptor.sourceCommit);
  assert.equal(report.fromVersion, previousRelease);
  assert.equal(report.entrypoint, 'runner-installer');
  assert.match(report.entrypointBinarySHA256, /^[a-f0-9]{64}$/);
  assert.equal(
    report.archiveSHA256,
    descriptor.runners[platform].installer.sha256,
  );
  for (const field of [
    'runnerIdentityPreserved',
    'durableSourceReceiptPreserved',
    'journalPreserved',
    'legacyProtocolAccepted',
    'unsupportedInputProtocolQueued',
    'upgradedInputHistoryVerified',
    'sourceReceiptRetryPreserved',
    'lostCompletionReplyRecovered',
  ])
    assert.equal(report[field], true);
  assert.equal(report.model.paid, false);
  assert.equal(report.model.requests, 5);
}
