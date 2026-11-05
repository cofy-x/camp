import assert from 'node:assert/strict';
import {
  copyFile,
  lstat,
  mkdir,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { hashFile, releaseIdentity, verifyRelease } from './release.mjs';
import {
  upgradeBaseline,
  serverUpgradeBaselines,
  serverUpgradeReportFile,
  runnerUpgradeProfiles,
  verifyRunnerUpgrade,
} from './upgrade.mjs';
import { verifyWorkerArtifact } from './worker-release.mjs';

const identity = await releaseIdentity();
const directory = join(root, 'dist');
const { runner, artifacts } = await verifyRelease(
  directory,
  identity.version,
  identity.sourceCommit,
);
const reports = [];
const serverUpgrades = [];
const serverBinarySHA256 = await hashFile(
  join(directory, 'camp-server-linux-amd64/camp'),
);
for (const baseline of serverUpgradeBaselines) {
  const report = JSON.parse(
    await readFile(join(directory, serverUpgradeReportFile(baseline)), 'utf8'),
  );
  assert.equal(report.status, 'passed');
  assert.equal(report.version, identity.version);
  assert.equal(report.sourceCommit, identity.sourceCommit);
  assert.equal(report.sourceModified, false);
  assert.equal(report.fromVersion, baseline);
  assert.equal(report.binarySHA256, serverBinarySHA256);
  serverUpgrades.push(report);
}
const [upgrade, transitionUpgrade, previousUpgrade] = serverUpgrades;
for (const mode of [
  'online',
  'offline',
  'upgrade',
  ...Object.keys(runnerUpgradeProfiles),
]) {
  const report = JSON.parse(
    await readFile(join(directory, `smoke-${mode}.json`), 'utf8'),
  );
  assert.equal(report.status, 'passed');
  assert.equal(report.version, identity.version);
  assert.equal(report.sourceCommit, identity.sourceCommit);
  assert.equal(
    report.archiveSHA256,
    mode === 'offline'
      ? runner.runners['linux-amd64'].offline.sha256
      : runner.runners['linux-amd64'].installer.sha256,
  );
  if (mode === 'upgrade' || runnerUpgradeProfiles[mode]) {
    assert.equal(
      report.fromVersion,
      mode === 'upgrade'
        ? upgradeBaseline
        : runnerUpgradeProfiles[mode].fromVersion,
    );
    for (const field of [
      'runnerIdentityPreserved',
      'durableSourceReceiptPreserved',
      'journalPreserved',
    ])
      assert.equal(report[field], true);
  }
  if (runnerUpgradeProfiles[mode])
    verifyRunnerUpgrade(report, runner, 'linux-amd64', mode);
  reports.push(report);
}
const image = JSON.parse(
  await readFile(join(directory, 'smoke-server-image.json'), 'utf8'),
);
assert.equal(image.status, 'passed');
assert.equal(image.version, identity.version);
assert.equal(image.sourceCommit, identity.sourceCommit);
const runtime = JSON.parse(
  await readFile(
    join(directory, 'camp-runtime-linux-amd64/manifest.json'),
    'utf8',
  ),
);
const staging = join(directory, 'release');
await rm(staging, { recursive: true, force: true });
await mkdir(staging);
for (const filename of [
  'runner-release.json',
  'server-release.json',
  ...artifacts.flatMap((artifact) => [
    artifact.filename,
    `${artifact.filename}.sha256`,
  ]),
])
  await copyFile(join(directory, filename), join(staging, filename));
const checkpointWorker = await verifyWorkerArtifact(
  directory,
  identity.sourceCommit,
);
for (const filename of [
  'checkpoint-worker-release.json',
  'worker-arm64-qualification.json',
  checkpointWorker.artifact.filename,
  `${checkpointWorker.artifact.filename}.sha256`,
])
  await copyFile(join(directory, filename), join(staging, filename));
let macWorkerReport = false;
try {
  assert.ok(
    (await lstat(join(directory, 'mac-worker-qualification.json'))).isFile(),
    'Supplied Mac qualification must be a regular report file.',
  );
  macWorkerReport = true;
} catch (error) {
  if (error.code !== 'ENOENT') throw error;
}
if (macWorkerReport)
  await copyFile(
    join(directory, 'mac-worker-qualification.json'),
    join(staging, 'mac-worker-qualification.json'),
  );
const evidence = {
  schema: 1,
  version: identity.version,
  sourceCommit: identity.sourceCommit,
  sourceModified: identity.modified,
  validationOnly: process.env.CAMP_RELEASE_VALIDATE_ONLY === 'true',
  protocolVersion: runner.protocolVersion,
  completedAt: new Date().toISOString(),
  host: {
    os: (await readFile('/etc/os-release', 'utf8')).match(
      /^PRETTY_NAME="(.*)"$/m,
    )?.[1],
    architecture: process.arch,
  },
  ci: {
    sourceBrowserFixture: process.env.CAMP_RELEASE_CI_URL || null,
    native: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
  },
  native: {
    status: 'passed',
    command: 'make test-remote-native',
    model: 'deterministic local provider; no paid requests',
  },
  runtimePins: {
    node: runtime.node,
    pnpm: runtime.pnpm,
    dsh: runtime.dsh,
    rust: runtime.rust,
    layerfs: runtime.layerfs,
  },
  optionalCheckpointWorker: {
    ...checkpointWorker,
    qualification: 'worker-arm64-qualification.json',
    scope:
      'native Linux ARM64 artifact qualification; actual Mac hybrid acceptance is separate',
  },
  installations: reports,
  serverImage: image,
  serverUpgrade: upgrade,
  serverTransitionUpgrade: transitionUpgrade,
  serverPreviousUpgrade: previousUpgrade,
  publicDistribution:
    process.env.CAMP_RELEASE_VALIDATE_ONLY === 'true'
      ? 'not published; this run validates Native packages only'
      : 'pending; see appended distribution-verification evidence',
  untested: [
    'paid model provider',
    'upgrades from releases other than the three named baselines',
    'systemd service installation',
  ],
};
await writeFile(
  join(staging, 'acceptance.json'),
  `${JSON.stringify(evidence, null, 2)}\n`,
);
console.log(
  `Staged ${artifacts.length} verified release archives and sanitized acceptance evidence.`,
);
