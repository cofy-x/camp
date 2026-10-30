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

import { homebrewFormula } from './homebrew.mjs';
import { upgradeBaseline } from './upgrade.mjs';

const execute = promisify(execFile);
assert.equal(
  process.env.CAMP_RELEASE_VALIDATE_ONLY,
  'false',
  'Validation runs cannot publish.',
);
assert.equal(
  process.env.GITHUB_REF,
  'refs/heads/main',
  'Publish only from reviewed main source.',
);
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
assert.equal(acceptance.validationOnly, false);
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
  macReports.push(report);
}
acceptance.macServers = macReports;
acceptance.macRunners = [];
acceptance.macQualification = [];
for (const host of ['macos-15', 'macos-26']) {
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
    assert.equal(report.status, 'passed');
    assert.equal(report.platform, 'darwin');
    assert.equal(report.architecture, 'arm64');
    assert.equal(report.version, identity.version);
    assert.equal(report.sourceCommit, identity.sourceCommit);
    if (mode === 'server-entrypoint') {
      assert.equal(report.entrypoint, 'provided-binary');
      assert.match(report.entrypointBinarySHA256, /^[a-f0-9]{64}$/);
    }
    assert.equal(
      report.archiveSHA256,
      runner.runners['darwin-arm64'][
        mode === 'online' ? 'installer' : 'offline'
      ].sha256,
    );
    acceptance.macRunners.push({ host, ...report });
  }
}

await writeFile(
  join(directory, 'acceptance.json'),
  `${JSON.stringify(acceptance, null, 2)}\n`,
);
await writeFile(join(directory, 'camp.rb'), homebrewFormula(server));
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
      schema: 2,
      version: identity.version,
      sourceCommit: identity.sourceCommit,
      protocolVersion: runner.protocolVersion,
      runners: runner.runners,
      server: server.server,
      servers: server.servers,
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
  'camp.rb',
  ...macReports.map((report) => `server-verification-${report.platform}.json`),
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
  `Camp ${identity.version} publishes Linux amd64 and macOS arm64/amd64 Servers, a Go-installable entrypoint, the small Camp Runner installer, its fixed runtime and an offline installer containing that same runtime. Native Runner support includes Ubuntu 24.04 amd64 and macOS 15+ Apple Silicon. Mac execution uses a local native workspace and Seatbelt; no Docker, Linux VM, FUSE or root account is required.

Install using the [versioned guide](https://github.com/${repository}/blob/${identity.version}/docs/install.md). Verify SHA256SUMS before extraction. Server image: \`${image}\`.

This is early development software. Alpha/beta versions may introduce breaking changes. Back up stopped Server and Runner data before an explicit upgrade; never delete data to resolve an incompatibility. Runner protocol: ${runner.protocolVersion}. No automatic runtime installation or upgrade occurs during \`run\`.

[Source, Browser and Fixture CI](${acceptance.ci.sourceBrowserFixture}) and the [Native build and installation checks](${acceptance.ci.native}) passed for source \`${identity.sourceCommit}\`. See acceptance.json for artifact and runtime evidence. Public GitHub downloads, online/offline installation and anonymous GHCR pull are checked after publication; the workflow appends a distribution-verification report with their actual outcomes. A failed distribution check leaves this prerelease available for diagnosis and must be resolved before recommending it.

The deterministic local provider exercises real DSH and LayerFS without paid model calls. Server and Runner data upgrade from the named public baseline are recorded in acceptance.json; public Runner upgrade and tagged Go installation are checked again after publication in the appended distribution reports. Paid-provider acceptance, systemd installation and upgrades from other baselines are not established by these checks.
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
