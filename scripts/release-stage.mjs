import assert from 'node:assert/strict';
import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { releaseIdentity, verifyRelease } from './release.mjs';

const identity = await releaseIdentity();
const directory = join(root, 'dist');
const { runner, artifacts } = await verifyRelease(
  directory,
  identity.version,
  identity.sourceCommit,
);
const reports = [];
for (const mode of ['online', 'offline']) {
  const report = JSON.parse(
    await readFile(join(directory, `smoke-${mode}.json`), 'utf8'),
  );
  assert.equal(report.status, 'passed');
  assert.equal(report.version, identity.version);
  reports.push(report);
}
const image = JSON.parse(
  await readFile(join(directory, 'smoke-server-image.json'), 'utf8'),
);
assert.equal(image.status, 'passed');
assert.equal(image.version, identity.version);
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
const evidence = {
  schema: 1,
  version: identity.version,
  sourceCommit: identity.sourceCommit,
  sourceModified: identity.modified,
  protocolVersion: runner.protocolVersion,
  completedAt: new Date().toISOString(),
  host: {
    os: (await readFile('/etc/os-release', 'utf8')).match(
      /^PRETTY_NAME="(.*)"$/m,
    )?.[1],
    architecture: process.arch,
  },
  ci: {
    sourceBrowserFixture: process.env.CAMP_RELEASE_CI_URL,
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
  installations: reports,
  serverImage: image,
  publicDistribution:
    'pending; see appended distribution-verification evidence',
  untested: [
    'paid model provider',
    'cross-version upgrade from an earlier public release',
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
