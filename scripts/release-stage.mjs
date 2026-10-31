import assert from 'node:assert/strict';
import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { hashFile, releaseIdentity, verifyRelease } from './release.mjs';
import { upgradeBaseline } from './upgrade.mjs';
import { verifyWorkerArtifact } from './worker-release.mjs';

const identity = await releaseIdentity();
const directory = join(root, 'dist');
const { runner, artifacts } = await verifyRelease(
  directory,
  identity.version,
  identity.sourceCommit,
);
const reports = [];
const upgrade = JSON.parse(
  await readFile(join(directory, 'server-upgrade-verification.json'), 'utf8'),
);
assert.equal(upgrade.status, 'passed');
assert.equal(upgrade.version, identity.version);
assert.equal(upgrade.sourceCommit, identity.sourceCommit);
assert.equal(upgrade.sourceModified, false);
assert.equal(upgrade.fromVersion, upgradeBaseline);
assert.equal(
  upgrade.binarySHA256,
  await hashFile(join(directory, 'camp-server-linux-amd64/camp')),
);
for (const mode of ['online', 'offline', 'upgrade']) {
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
  if (mode === 'upgrade') {
    assert.equal(report.fromVersion, upgradeBaseline);
    for (const field of [
      'runnerIdentityPreserved',
      'durableSourceReceiptPreserved',
      'journalPreserved',
    ])
      assert.equal(report[field], true);
  }
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
  publicDistribution:
    process.env.CAMP_RELEASE_VALIDATE_ONLY === 'true'
      ? 'not published; this run validates Native packages only'
      : 'pending; see appended distribution-verification evidence',
  untested: [
    'paid model provider',
    'upgrades from releases other than the named baseline',
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
