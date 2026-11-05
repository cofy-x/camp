import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { verifyRelease, verifyRunnerInstallationReport } from './release.mjs';
import {
  upgradeBaseline,
  protocolTransitionBaseline,
  previousRelease,
  runnerUpgradeProfiles,
  verifyRunnerUpgrade,
} from './upgrade.mjs';
import { verifyWorkerArtifact } from './worker-release.mjs';
import { assertServerServiceReceipt } from './server-service.mjs';
import { root } from './lib.mjs';
import {
  driverFiles,
  optionalMacWorkerEvidence,
  readMacWorkerIdentity,
} from './mac-worker-evidence.mjs';

export async function collectReleaseEvidence(
  directory,
  identity,
  validationOnly,
) {
  const { runner, server, artifacts } = await verifyRelease(
    directory,
    identity.version,
    identity.sourceCommit,
  );
  const checkpointWorker = await verifyWorkerArtifact(
    directory,
    identity.sourceCommit,
  );
  const acceptance = JSON.parse(
    await readFile(join(directory, 'acceptance.json'), 'utf8'),
  );
  assert.equal(acceptance.version, identity.version);
  assert.equal(acceptance.sourceCommit, identity.sourceCommit);
  assert.equal(acceptance.sourceModified, false);
  assert.equal(acceptance.native.status, 'passed');
  assert.equal(acceptance.validationOnly, validationOnly);
  for (const [field, baseline] of [
    ['serverUpgrade', upgradeBaseline],
    ['serverTransitionUpgrade', protocolTransitionBaseline],
    ['serverPreviousUpgrade', previousRelease],
  ]) {
    const report = acceptance[field];
    assert.equal(report?.status, 'passed');
    assert.equal(report.version, identity.version);
    assert.equal(report.sourceCommit, identity.sourceCommit);
    assert.equal(report.sourceModified, false);
    assert.equal(report.fromVersion, baseline);
  }
  for (const mode of Object.keys(runnerUpgradeProfiles)) {
    const reports = acceptance.installations.filter(
      (report) => report.mode === mode,
    );
    assert.equal(reports.length, 1, `Retain one Linux ${mode} report.`);
    verifyRunnerUpgrade(reports[0], runner, 'linux-amd64', mode);
  }
  assert.match(
    acceptance.ci.sourceBrowserFixture,
    /^https:\/\/github\.com\/cofy-x\/camp\/actions\/runs\/\d+$/,
  );
  const macReports = [];
  for (const platform of ['darwin-arm64', 'darwin-amd64']) {
    const report = JSON.parse(
      await readFile(
        join(directory, `server-verification-${platform}.json`),
        'utf8',
      ),
    );
    assert.equal(report.status, 'passed');
    assert.equal(report.platform, platform);
    assert.equal(report.version, identity.version);
    assert.equal(report.sourceCommit, identity.sourceCommit);
    assert.equal(report.archiveSHA256, server.servers[platform].sha256);
    assert.equal(report.upgrade?.fromVersion, upgradeBaseline);
    assert.equal(
      report.transitionUpgrade?.fromVersion,
      protocolTransitionBaseline,
    );
    assert.equal(report.previousUpgrade?.fromVersion, previousRelease);
    assertServerServiceReceipt(report.service, {
      version: identity.version,
      sourceCommit: identity.sourceCommit,
      platform: platform === 'darwin-arm64' ? 'darwin/arm64' : 'darwin/x64',
    });
    assert.equal(report.service.archiveSHA256, report.archiveSHA256);
    macReports.push(report);
  }
  acceptance.macServers = macReports;
  acceptance.macRunners = [];
  acceptance.macQualification = [];
  acceptance.macServerServices = [];
  for (const host of ['macos-15', 'macos-26']) {
    const service = JSON.parse(
      await readFile(
        join(directory, `server-service-verification-${host}.json`),
        'utf8',
      ),
    );
    assertServerServiceReceipt(service, {
      version: identity.version,
      sourceCommit: identity.sourceCommit,
      platform: 'darwin/arm64',
    });
    assert.equal(service.archiveSHA256, server.servers['darwin-arm64'].sha256);
    acceptance.macServerServices.push({ host, ...service });
    const boundary = JSON.parse(
      await readFile(join(directory, `runner-boundary-${host}.json`), 'utf8'),
    );
    assert.equal(boundary.status, 'passed');
    assert.equal(boundary.sourceCommit, identity.sourceCommit);
    assert.equal(boundary.modified, false);
    assert.equal(boundary.host.platform, 'darwin/arm64');
    const checkpoints = JSON.parse(
      await readFile(join(directory, `runner-checkpoint-${host}.json`), 'utf8'),
    );
    assert.equal(checkpoints.platform, 'darwin/arm64');
    assert.equal(checkpoints.modelCalled, false);
    assert.equal(checkpoints.linuxRequired, false);
    assert.equal(checkpoints.observations.length, 9);
    for (const workload of ['text', '10000-files', '128-MiB-binary'])
      for (const iteration of [0, 1, 2]) {
        const observation = checkpoints.observations.find(
          (item) => item.workload === workload && item.iteration === iteration,
        );
        assert.ok(observation);
        assert.ok(observation.prepareMs >= 0 && observation.captureMs >= 0);
        assert.ok(observation.archiveBytes > 0 && observation.rssKiB > 0);
      }
    acceptance.macQualification.push({ host, boundary, checkpoints });
    for (const mode of ['online', 'offline', 'server-entrypoint']) {
      const report = JSON.parse(
        await readFile(
          join(directory, `runner-verification-${host}-${mode}.json`),
          'utf8',
        ),
      );
      verifyRunnerInstallationReport(report, runner, 'darwin-arm64', mode);
      acceptance.macRunners.push({ host, ...report });
    }
    for (const mode of Object.keys(runnerUpgradeProfiles)) {
      const report = JSON.parse(
        await readFile(
          join(directory, `runner-verification-${host}-${mode}.json`),
          'utf8',
        ),
      );
      verifyRunnerUpgrade(report, runner, 'darwin-arm64', mode);
      acceptance.macRunners.push({ host, ...report });
    }
  }

  const actualMac = await optionalMacWorkerEvidence(
    directory,
    () => readMacWorkerIdentity(directory),
    () => driverFiles(root),
  );
  acceptance.optionalCheckpointWorker = {
    ...acceptance.optionalCheckpointWorker,
    macQualification: actualMac,
  };
  return { runner, server, artifacts, checkpointWorker, acceptance };
}
