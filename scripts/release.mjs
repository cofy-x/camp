import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { lstat, readFile, readdir } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { isDeepStrictEqual, promisify } from 'node:util';
import { command, root } from './lib.mjs';

const execute = promisify(execFile);
export const repository = 'cofy-x/camp';
export const serverPlatforms = ['linux-amd64', 'darwin-arm64', 'darwin-amd64'];
export const versionPattern =
  /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$/;
export const deterministicTar = [
  '--sort=name',
  '--mtime=@0',
  '--owner=0',
  '--group=0',
  '--numeric-owner',
  '--format=gnu',
];

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
    flags += ` -X github.com/cofy-x/camp/apps/server/internal/releaseinfo.manifestBase64=${data.toString('base64')}`;
  }
  return flags;
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
  await command('tar', [
    ...deterministicTar,
    '-czf',
    filename,
    '-C',
    dirname(directory),
    basename(directory),
  ]);
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
  for (const descriptor of [runner, server])
    if (
      descriptor.schema !== 1 ||
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
  const artifacts = [
    runner.installer,
    runner.runtime,
    runner.offline,
    ...serverPlatforms.map((platform) => server.servers[platform]),
  ];
  const expected = [
    'camp-runner-linux-amd64.tar.gz',
    `camp-runtime-linux-amd64-${runner.runtime?.sha256}.tar.gz`,
    'camp-runner-offline-linux-amd64.tar.gz',
    ...serverPlatforms.map((platform) => `camp-server-${platform}.tar.gz`),
  ];
  for (const [index, artifact] of artifacts.entries()) {
    if (
      !artifact ||
      artifact.filename !== expected[index] ||
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
  return { runner, server, artifacts };
}
