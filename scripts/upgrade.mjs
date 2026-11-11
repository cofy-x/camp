import assert from 'node:assert/strict';
import { lstat, readFile, realpath } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { command, go } from './lib.mjs';
import {
  hashFile,
  runnerPlatforms,
  serverPlatforms,
  validVersion,
} from './release.mjs';

// Keep the oldest supported package, protocol transition and current public
// predecessor distinct. The launcher verifies immutable archives.
export const upgradeBaseline = 'v0.1.0-alpha.2';
export const protocolTransitionBaseline = 'v0.1.0-alpha.8';
export const previousRelease = 'v0.1.0-alpha.12';
export const serverUpgradeBaselines = [
  upgradeBaseline,
  protocolTransitionBaseline,
  previousRelease,
];
export const runnerUpgradeProfiles = {
  'transition-upgrade': {
    fromVersion: protocolTransitionBaseline,
    baselineInputProtocol: 0,
  },
  'previous-upgrade': {
    fromVersion: previousRelease,
    baselineInputProtocol: 1,
  },
};

export function serverUpgradeReportFile(baseline) {
  assert.ok(serverUpgradeBaselines.includes(baseline));
  return baseline === upgradeBaseline
    ? 'server-upgrade-verification.json'
    : baseline === protocolTransitionBaseline
      ? 'server-transition-upgrade-verification.json'
      : 'server-previous-upgrade-verification.json';
}

export function baselineInputProtocolGate(
  mode,
  protocol,
  customBaseline = false,
) {
  const profile = runnerUpgradeProfiles[mode];
  assert.ok(profile, 'Select a full Runner upgrade profile.');
  assert.ok(
    [0, 1].includes(protocol),
    'Read a supported baseline input capability.',
  );
  if (!customBaseline)
    assert.equal(
      protocol,
      profile.baselineInputProtocol,
      'The fixed public baseline must retain its expected task input capability.',
    );
  return protocol === 0;
}

export async function readBaselineInstallation(
  directory,
  role,
  version,
  launcher,
  platform = process.platform,
  architecture = process.arch,
) {
  assert.ok(isAbsolute(directory));
  directory = await realpath(directory);
  assert.ok(validVersion(version));
  assert.ok(['server', 'runner'].includes(role));
  const target = `${platform}-${architecture === 'x64' ? 'amd64' : architecture}`;
  assert.ok(
    (role === 'runner' ? runnerPlatforms : serverPlatforms).includes(target),
  );
  const cache =
    platform === 'darwin'
      ? join(directory, 'Library/Caches')
      : join(directory, 'baseline-cache');
  const archive = `camp-${role}-${target}.tar.gz`;
  const installed = join(cache, 'camp/releases', version, archive.slice(0, -7));
  assert.equal(
    await realpath(installed),
    installed,
    'Baseline installation must not redirect through symlinks.',
  );
  const binary = join(installed, 'camp');
  const receipt = join(installed, '.camp-install.json');
  for (const file of [binary, receipt]) {
    const info = await lstat(file);
    assert.ok(
      info.isFile() && !(info.mode & 0o022),
      'Baseline files must be regular and private.',
    );
    if (file === receipt) assert.ok(info.size > 0 && info.size <= 4 << 20);
    else assert.ok(info.mode & 0o100);
  }
  const saved = JSON.parse(await readFile(receipt, 'utf8'));
  assert.equal(saved.version, version);
  assert.equal(saved.archive, archive);
  assert.match(
    saved.archiveSHA256 ?? '',
    /^[a-f0-9]{64}$/,
    'Fresh baseline receipts require verified archive provenance.',
  );
  const binarySHA256 = await hashFile(binary);
  assert.equal(saved.binarySHA256, binarySHA256);
  return {
    binary,
    cache,
    identity: {
      version,
      platform: target,
      archive,
      archiveSHA256: saved.archiveSHA256,
      binarySHA256,
      launcherSHA256: await hashFile(launcher),
    },
  };
}

// Match Go's HTTP ProxyFromEnvironment support without inheriting model keys,
// private Server configuration or environment that could weaken TLS validation.
export function httpProxyEnvironment(environment = process.env) {
  return Object.fromEntries(
    [
      'HTTP_PROXY',
      'HTTPS_PROXY',
      'NO_PROXY',
      'http_proxy',
      'https_proxy',
      'no_proxy',
    ]
      .filter((key) => typeof environment[key] === 'string')
      .map((key) => [key, environment[key]]),
  );
}

export function baselineAcquisitionEnvironment(
  directory,
  cache,
  environment = process.env,
) {
  return {
    PATH: environment.PATH,
    HOME: directory,
    LANG: 'C.UTF-8',
    XDG_CACHE_HOME: cache,
    ...httpProxyEnvironment(environment),
  };
}

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
    env: baselineAcquisitionEnvironment(directory, cache),
  });
  // Invoke the verified package directly afterward. macOS ignores XDG_CACHE_HOME;
  // a second launcher invocation with the operator's HOME could use their cache.
  return readBaselineInstallation(directory, role, version, binary);
}

export function verifyRunnerUpgrade(report, descriptor, platform, mode) {
  const profile = runnerUpgradeProfiles[mode];
  assert.ok(profile, 'Select a fixed full Runner upgrade profile.');
  const [os, arch] = platform.split('-');
  assert.equal(report.status, 'passed');
  assert.equal(report.mode, mode);
  assert.equal(report.platform, os);
  assert.equal(report.architecture, arch === 'amd64' ? 'x64' : arch);
  assert.equal(report.version, descriptor.version);
  assert.equal(report.sourceCommit, descriptor.sourceCommit);
  assert.equal(report.fromVersion, profile.fromVersion);
  assert.equal(report.baselineInputProtocol, profile.baselineInputProtocol);
  assert.equal(
    report.unsupportedInputProtocolQueued,
    profile.baselineInputProtocol === 0,
  );
  assert.equal(report.queuedWhileOffline, profile.baselineInputProtocol === 1);
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
    'upgradedInputHistoryVerified',
    'sourceReceiptRetryPreserved',
    'lostCompletionReplyRecovered',
  ])
    assert.equal(report[field], true);
  assert.equal(report.model.paid, false);
  assert.equal(report.model.requests, 5);
}

export function verifyPreviousRunnerUpgrade(report, descriptor, platform) {
  verifyRunnerUpgrade(report, descriptor, platform, 'previous-upgrade');
}
