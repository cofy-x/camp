import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { createReadStream } from 'node:fs';
import {
  lstat,
  readFile,
  readdir,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import { basename, join } from 'node:path';
import { isDeepStrictEqual, promisify } from 'node:util';
import { command, go, root } from './lib.mjs';

const execute = promisify(execFile);
export const repository = 'cofy-x/camp';
export const serverPlatforms = ['linux-amd64', 'darwin-arm64', 'darwin-amd64'];
export const versionPattern =
  /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$/;
export const runnerPlatforms = ['linux-amd64', 'darwin-arm64'];

export function verifyRunnerInstallationReport(
  report,
  descriptor,
  platform,
  scenario,
) {
  assert.ok(runnerPlatforms.includes(platform));
  assert.ok(['online', 'offline', 'server-entrypoint'].includes(scenario));
  const [os, arch] = platform.split('-');
  assert.equal(report.status, 'passed');
  assert.equal(report.platform, os);
  assert.equal(report.architecture, arch === 'amd64' ? 'x64' : arch);
  assert.equal(report.version, descriptor.version);
  assert.equal(report.sourceCommit, descriptor.sourceCommit);
  assert.equal(report.mode, scenario === 'offline' ? 'offline' : 'online');
  assert.equal(
    report.entrypoint,
    scenario === 'server-entrypoint' ? 'provided-binary' : 'runner-installer',
  );
  assert.match(report.entrypointBinarySHA256, /^[a-f0-9]{64}$/);
  assert.equal(
    report.archiveSHA256,
    descriptor.runners[platform][
      scenario === 'offline' ? 'offline' : 'installer'
    ].sha256,
  );
}

export function validVersion(version) {
  return (
    typeof version === 'string' &&
    version === version.trim() &&
    versionPattern.test(version)
  );
}

export async function releaseIdentity() {
  const version = process.env.VERSION ?? 'dev';
  if (version !== 'dev' && !validVersion(version))
    throw new Error('VERSION must be dev or a release such as v0.1.0-alpha.1.');
  const { stdout: sourceCommit } = await execute('git', ['rev-parse', 'HEAD'], {
    cwd: root,
  });
  const { stdout: changes } = await execute('git', ['status', '--porcelain'], {
    cwd: root,
  });
  if (version !== 'dev' && changes.trim())
    throw new Error('Release packages require a clean source checkout.');
  return {
    version,
    sourceCommit: sourceCommit.trim(),
    published: version !== 'dev',
    modified: Boolean(changes.trim()),
  };
}

export async function serverBuildFlags(identity) {
  let flags = `-X main.version=${identity.version}`;
  if (identity.published) {
    const data = await readFile(join(root, 'dist/runner-release.json'));
    const runner = JSON.parse(data);
    if (
      runner.version !== identity.version ||
      runner.sourceCommit !== identity.sourceCommit
    )
      throw new Error(
        'Build matching Runner packages before the release Server.',
      );
    runnerEntries(runner);
    flags += ` -X github.com/cofy-x/camp/apps/server/internal/releaseinfo.manifestBase64=${data.toString('base64')}`;
  }
  return flags;
}

function runnerEntries(descriptor, { partial = false } = {}) {
  const platforms = Object.keys(descriptor.runners ?? {});
  if (
    descriptor.schema !== 2 ||
    platforms.length === 0 ||
    platforms.some((platform) => !runnerPlatforms.includes(platform)) ||
    (!partial && platforms.length !== runnerPlatforms.length)
  )
    throw new Error(
      'The release requires the complete supported Runner platform set.',
    );
  return platforms.flatMap((platform) => {
    const runner = descriptor.runners[platform];
    if (!runner || !/^[a-f0-9]{64}$/.test(runner.runtime?.manifestSHA256))
      throw new Error('Invalid runtime manifest identity.');
    return [
      [runner.installer, `camp-runner-${platform}.tar.gz`],
      [
        runner.runtime,
        `camp-runtime-${platform}-${runner.runtime.sha256}.tar.gz`,
      ],
      [runner.offline, `camp-runner-offline-${platform}.tar.gz`],
    ];
  });
}

// Native builders contribute one platform each. The Server embeds the combined
// catalog only after both contributions agree on source, version and protocol.
export async function mergeRunnerReleases(directory, identity, options = {}) {
  let combined;
  for (const platform of runnerPlatforms) {
    let descriptor;
    try {
      descriptor = JSON.parse(
        await readFile(
          join(directory, `runner-release-${platform}.json`),
          'utf8',
        ),
      );
    } catch (error) {
      if (options.partial && error.code === 'ENOENT') continue;
      throw error;
    }
    runnerEntries(descriptor, { partial: true });
    if (
      Object.keys(descriptor.runners).join(',') !== platform ||
      descriptor.version !==
        (identity.published ? identity.version : undefined) ||
      descriptor.sourceCommit !==
        (identity.published ? identity.sourceCommit : undefined)
    )
      throw new Error(
        'Runner contribution identity differs from its native builder.',
      );
    if (!combined) combined = { ...descriptor, runners: {} };
    if (descriptor.protocolVersion !== combined.protocolVersion)
      throw new Error('Runner contributions use different protocols.');
    combined.runners[platform] = descriptor.runners[platform];
  }
  runnerEntries(combined ?? {}, options);
  const destination = join(directory, 'runner-release.json');
  await writeFile(
    `${destination}.pending`,
    `${JSON.stringify(combined, null, 2)}\n`,
  );
  await rename(`${destination}.pending`, destination);
  return combined;
}

export function installationNotes(identity, role) {
  const ref = identity.published ? identity.version : 'main';
  return `# Camp ${role === 'server' ? 'Server' : 'Runner'} ${identity.version}

Follow the [installation guide](https://github.com/${repository}/blob/${ref}/docs/install.md) for host requirements, setup, explicit service operation, upgrades and backup.

Run \`./camp ${role === 'server' ? 'serve --help' : 'runner --help'}\` for command options. No service starts automatically. Keep private data outside this installation directory.
`;
}

export async function hashFile(filename) {
  const digest = createHash('sha256');
  for await (const chunk of createReadStream(filename)) digest.update(chunk);
  return digest.digest('hex');
}

export async function treeBytes(directory) {
  let bytes = 0;
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filename = join(directory, entry.name);
    if (entry.isDirectory()) bytes += await treeBytes(filename);
    else if (entry.isFile()) bytes += (await lstat(filename)).size;
  }
  return bytes;
}

export async function archiveTree(directory) {
  const filename = `${directory}.tar.gz`;
  await rm(filename, { force: true });
  await command(go, ['run', './tools/release-archive', directory, filename]);
  return {
    filename: basename(filename),
    sha256: await hashFile(filename),
    bytes: (await lstat(filename)).size,
    unpackedBytes: await treeBytes(directory),
  };
}

// Validate the closed set of distributable files again after CI transfer.
// Never glob a builder's directory into a public release.
export async function verifyRelease(directory, version, sourceCommit) {
  if (!validVersion(version) || !/^[a-f0-9]{40}$/.test(sourceCommit))
    throw new Error('Invalid release identity.');
  const read = async (name) =>
    JSON.parse(await readFile(join(directory, name), 'utf8'));
  const runner = await read('runner-release.json');
  const server = await read('server-release.json');
  for (const [descriptor, schema] of [
    [runner, 2],
    [server, 1],
  ])
    if (
      descriptor.schema !== schema ||
      descriptor.version !== version ||
      descriptor.sourceCommit !== sourceCommit
    )
      throw new Error('Release descriptor identity does not match its source.');
  const protocol = await readFile(
    join(root, 'apps/server/internal/runnerprotocol/protocol.go'),
    'utf8',
  );
  if (
    runner.protocolVersion !==
    Number(protocol.match(/const Version = (\d+)/)?.[1])
  )
    throw new Error('Release Runner protocol does not match its source.');
  if (
    !server.servers ||
    Object.keys(server.servers).sort().join(',') !==
      [...serverPlatforms].sort().join(',')
  )
    throw new Error(
      'The release requires the complete supported Server platform set.',
    );
  if (!isDeepStrictEqual(server.server, server.servers['linux-amd64']))
    throw new Error(
      'The container Server must match the Linux Server package.',
    );
  const entries = [
    ...runnerEntries(runner),
    ...serverPlatforms.map((platform) => [
      server.servers[platform],
      `camp-server-${platform}.tar.gz`,
    ]),
  ];
  for (const [artifact, expected] of entries) {
    if (
      !artifact ||
      artifact.filename !== expected ||
      !/^[a-f0-9]{64}$/.test(artifact.sha256) ||
      !Number.isSafeInteger(artifact.bytes) ||
      artifact.bytes <= 0 ||
      artifact.bytes >= 2 ** 31 ||
      !Number.isSafeInteger(artifact.unpackedBytes) ||
      artifact.unpackedBytes <= 0 ||
      artifact.unpackedBytes > 8 * 2 ** 30
    )
      throw new Error('Invalid release artifact.');
    const filename = join(directory, artifact.filename);
    const info = await lstat(filename);
    if (
      !info.isFile() ||
      info.size !== artifact.bytes ||
      (await hashFile(filename)) !== artifact.sha256
    )
      throw new Error(
        `Release artifact integrity failed: ${artifact.filename}`,
      );
    if (
      (await readFile(`${filename}.sha256`, 'utf8')) !==
      `${artifact.sha256}  ${artifact.filename}\n`
    )
      throw new Error(
        `Release checksum differs from its descriptor: ${artifact.filename}`,
      );
  }
  return { runner, server, artifacts: entries.map(([artifact]) => artifact) };
}
