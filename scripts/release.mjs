import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { lstat, readFile, readdir } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { promisify } from 'node:util';
import { command, root } from './lib.mjs';

const execute = promisify(execFile);
export const repository = 'cofy-x/camp';
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
  const artifacts = [
    runner.installer,
    runner.runtime,
    runner.offline,
    server.server,
  ];
  const expected = [
    'camp-runner-linux-amd64.tar.gz',
    `camp-runtime-linux-amd64-${runner.runtime?.sha256}.tar.gz`,
    'camp-runner-offline-linux-amd64.tar.gz',
    'camp-server-linux-amd64.tar.gz',
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
