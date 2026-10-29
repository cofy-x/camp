import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { appendFile, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { command, root } from './lib.mjs';
import {
  hashFile,
  releaseIdentity,
  repository,
  verifyRelease,
} from './release.mjs';

const execute = promisify(execFile);
const identity = await releaseIdentity();
const directory = join(root, 'dist/release');
const { runner, server, artifacts } = await verifyRelease(
  directory,
  identity.version,
  identity.sourceCommit,
);
const acceptance = JSON.parse(
  await readFile(join(directory, 'acceptance.json'), 'utf8'),
);
assert.equal(acceptance.sourceCommit, identity.sourceCommit);
assert.equal(acceptance.sourceModified, false);
assert.equal(acceptance.native.status, 'passed');
const existingResponse = await fetch(
  `https://api.github.com/repos/${repository}/releases/tags/${identity.version}`,
  {
    headers: {
      Authorization: `Bearer ${process.env.GH_TOKEN}`,
      Accept: 'application/vnd.github+json',
    },
    signal: AbortSignal.timeout(30000),
  },
);
assert.ok(
  [200, 404].includes(existingResponse.status),
  'Could not check existing release state.',
);
const existing =
  existingResponse.status === 200 ? await existingResponse.json() : null;
if (existing) {
  assert.equal(
    existing.draft,
    true,
    'A published version can never be replaced; choose a new version.',
  );
  assert.equal(
    existing.target_commitish,
    identity.sourceCommit,
    'The draft belongs to different source.',
  );
}
const imageTag = `ghcr.io/${repository}:${identity.version}`;
await command('docker', ['tag', `camp-server:${identity.version}`, imageTag]);
await command('docker', ['push', imageTag]);
const { stdout } = await execute('docker', [
  'image',
  'inspect',
  '--format',
  '{{json .RepoDigests}}',
  imageTag,
]);
const image = JSON.parse(stdout).find((value) =>
  new RegExp(`^ghcr.io/${repository}@sha256:[a-f0-9]{64}$`).test(value),
);
assert.ok(
  image,
  'The registry did not return an immutable Server image digest.',
);
await writeFile(
  join(directory, 'release.json'),
  `${JSON.stringify(
    {
      schema: 1,
      version: identity.version,
      sourceCommit: identity.sourceCommit,
      protocolVersion: runner.protocolVersion,
      platform: 'linux/amd64',
      installer: runner.installer,
      runtime: runner.runtime,
      offline: runner.offline,
      server: server.server,
      image,
      imageTag,
    },
    null,
    2,
  )}\n`,
);
const filenames = [
  'runner-release.json',
  'server-release.json',
  'release.json',
  'acceptance.json',
  ...artifacts.flatMap((artifact) => [
    artifact.filename,
    `${artifact.filename}.sha256`,
  ]),
];
await writeFile(
  join(directory, 'SHA256SUMS'),
  `${(await Promise.all(filenames.map(async (name) => `${await hashFile(join(directory, name))}  ${name}`))).join('\n')}\n`,
);
filenames.push('SHA256SUMS');
const notes = join(root, 'dist/release-notes.md');
await writeFile(
  notes,
  `Camp ${identity.version} publishes a Linux amd64 Server, the small Camp Runner installer, its fixed runtime and an offline installer containing that same runtime. Native Runner support is Ubuntu 24.04 amd64.

Install using the [versioned guide](https://github.com/${repository}/blob/${identity.version}/docs/install.md). Verify SHA256SUMS before extraction. Server image: \`${image}\`.

This is early development software. Alpha/beta versions may introduce breaking changes. Back up stopped Server and Runner data before an explicit upgrade; never delete data to resolve an incompatibility. Runner protocol: ${runner.protocolVersion}. No automatic runtime installation or upgrade occurs during \`run\`.

[Source, Browser and Fixture CI](${acceptance.ci.sourceBrowserFixture}) and the [Native build and installation checks](${acceptance.ci.native}) passed for source \`${identity.sourceCommit}\`. See acceptance.json for artifact and runtime evidence. Public GitHub downloads, online/offline installation and anonymous GHCR pull are checked after publication; the workflow appends a distribution-verification report with their actual outcomes. A failed distribution check leaves this prerelease available for diagnosis and must be resolved before recommending it.

The deterministic local provider exercises real DSH and LayerFS without paid model calls. Paid-provider acceptance, systemd installation and upgrades from an earlier public release are not established by these checks.
`,
);
if (!existing)
  await command('gh', [
    'release',
    'create',
    identity.version,
    '--repo',
    repository,
    '--target',
    identity.sourceCommit,
    '--title',
    `Camp ${identity.version}`,
    '--notes-file',
    notes,
    '--draft',
    ...(identity.version.includes('-') ? ['--prerelease'] : []),
  ]);
const uploaded = existing?.assets || [];
for (const filename of filenames) {
  const asset = uploaded.find((item) => item.name === filename);
  if (asset) {
    assert.equal(
      asset.digest,
      `sha256:${await hashFile(join(directory, filename))}`,
      'Draft asset bytes differ; choose a new version.',
    );
    continue;
  }
  await command('gh', [
    'release',
    'upload',
    identity.version,
    join(directory, filename),
    '--repo',
    repository,
  ]);
}
await command('gh', [
  'release',
  'edit',
  identity.version,
  '--repo',
  repository,
  '--draft=false',
  '--latest=false',
]);
await appendFile(
  process.env.GITHUB_OUTPUT,
  `image=${image}\nurl=https://github.com/${repository}/releases/tag/${identity.version}\n`,
);
