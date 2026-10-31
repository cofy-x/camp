import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import {
  chmod,
  cp,
  lstat,
  mkdir,
  readFile,
  readdir,
  readlink,
  realpath,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { command, go, root } from './lib.mjs';
import { collectLicenses } from './licenses.mjs';
import {
  archiveTree,
  releaseIdentity,
  installationNotes,
  mergeRunnerReleases,
} from './release.mjs';

const linux = process.platform === 'linux' && process.arch === 'x64';
const mac = process.platform === 'darwin' && process.arch === 'arm64';
if (!linux && !mac)
  throw new Error('Build Runner packages on Linux amd64 or macOS ARM64.');
const target = linux ? 'linux-amd64' : 'darwin-arm64';
const runtimePlatform = `${process.platform}/${process.arch}`;
const prebuild = `${process.platform}-${process.arch}`;
if (process.argv.slice(2).some((arg) => arg !== '--checkpoint-worker'))
  throw new Error(
    'Usage: node scripts/package-runner.mjs [--checkpoint-worker]',
  );
if (process.versions.node !== '24.16.0') throw new Error('Use Node 24.16.0.');
const destination = path.join(root, `dist/camp-runtime-${target}`);
const staging = `${destination}.staging`;
await rm(staging, { recursive: true, force: true });
await mkdir(staging, { recursive: true });
const copy = async (from, to) => {
  await mkdir(path.dirname(path.join(staging, to)), { recursive: true });
  await cp(from, path.join(staging, to), {
    recursive: true,
    verbatimSymlinks: true,
  });
};
const json = async (name, value) =>
  writeFile(path.join(staging, name), `${JSON.stringify(value, null, 2)}\n`);

async function hash(filename) {
  const digest = createHash('sha256');
  for await (const chunk of createReadStream(filename)) digest.update(chunk);
  return digest.digest('hex');
}

try {
  const identity = await releaseIdentity();
  const licenses = await collectLicenses(staging);
  await copy(path.join(root, 'dist/camp-sandbox'), 'bin/camp-sandbox');
  await copy(await realpath(process.execPath), 'bin/node');
  await copy(
    path.join(path.dirname(await realpath(process.execPath)), '../LICENSE'),
    'licenses/Node-LICENSE',
  );
  for (const name of ['dsh-runtime', 'project-runtime']) {
    const source = path.join(root, 'tools', name);
    for (const entry of await readdir(source, { withFileTypes: true })) {
      if (
        entry.name === 'node_modules' ||
        entry.name === 'camp-tools' ||
        entry.name === 'extract.mjs' ||
        [
          'package.json',
          'pnpm-lock.yaml',
          'pnpm-workspace.yaml',
          'README.md',
        ].includes(entry.name)
      )
        await copy(
          path.join(source, entry.name),
          `runtime/${name}/${entry.name}`,
        );
    }
  }
  // Only the current native prebuild can be selected by node-pty's loader.
  const ptyPrebuilds = path.join(
    staging,
    'runtime/dsh-runtime/node_modules/.pnpm/node-pty@1.2.0-beta.15/node_modules/node-pty/prebuilds',
  );
  await lstat(path.join(ptyPrebuilds, prebuild));
  for (const entry of await readdir(ptyPrebuilds, { withFileTypes: true })) {
    if (entry.isDirectory() && entry.name !== prebuild)
      await rm(path.join(ptyPrebuilds, entry.name), { recursive: true });
  }
  for (const runtime of ['dsh-runtime', 'project-runtime']) {
    for (const metadata of ['.modules.yaml', '.pnpm-workspace-state-v1.json'])
      await rm(
        path.join(staging, 'runtime', runtime, 'node_modules', metadata),
        { force: true },
      );
  }
  let linuxMetadata = {};
  if (mac && process.argv.includes('--checkpoint-worker')) {
    const worker = path.join(root, 'tools/layerfs-worker');
    const installed = JSON.parse(
      await readFile(path.join(worker, 'installed.json'), 'utf8'),
    );
    const descriptor = JSON.parse(
      await readFile(
        path.join(root, 'dist/checkpoint-worker-release.json'),
        'utf8',
      ),
    );
    if (
      descriptor.image !== installed.image ||
      descriptor.sourceCommit !== installed.commit ||
      descriptor.platform !== 'linux/arm64' ||
      (await hash(path.join(root, 'dist', descriptor.artifact.filename))) !==
        descriptor.artifact.sha256
    )
      throw new Error(
        'Package the matching fixed optional ARM64 worker artifact first.',
      );
    for (const name of [
      'bridge.mjs',
      'client.mjs',
      'checkpoint.mjs',
      'home.mjs',
      'administration.mjs',
      'manage.mjs',
      'contracts.mjs',
      'tree.mjs',
      'transfer.mjs',
      'protocol.mjs',
      'state.mjs',
      'pin.json',
      'README.md',
    ])
      await copy(path.join(worker, name), `runtime/layerfs-worker/${name}`);
    const publicInstallation = { ...installed };
    delete publicInstallation.buildContext;
    await json('runtime/layerfs-worker/installed.json', publicInstallation);
    await copy(
      path.join(root, 'tools/layerfs-runtime/pin.json'),
      'runtime/layerfs-runtime/pin.json',
    );
    linuxMetadata = {
      checkpointWorker: {
        version: descriptor.version,
        commit: descriptor.sourceCommit,
        image: descriptor.image,
        filename: descriptor.artifact.filename,
        archiveSHA256: descriptor.artifact.sha256,
        archiveBytes: descriptor.artifact.bytes,
      },
    };
  }
  if (linux) {
    const layer = path.join(root, 'tools/layerfs-runtime');
    const installed = JSON.parse(
      await readFile(path.join(layer, 'installed.json'), 'utf8'),
    );
    for (const field of ['source', 'daemon', 'native'])
      installed[field] = path.resolve(layer, installed[field]);
    const rustLicenses = path.join(
      layer,
      'node_modules/.camp-layerfs/licenses',
    );
    const rustModules = JSON.parse(
      await readFile(path.join(rustLicenses, 'modules.json'), 'utf8'),
    );
    await copy(rustLicenses, 'licenses/rust');
    for (const [filename, expected] of [
      [installed.daemon, installed.daemonSha256],
      [installed.native, installed.nativeSha256],
      [await realpath(process.execPath), installed.nodeSha256],
    ]) {
      if ((await hash(filename)) !== expected)
        throw new Error(
          'Installed runtime integrity differs from its recorded build. Reinstall before packaging.',
        );
    }
    for (const name of [
      'bridge.mjs',
      'runtime.mjs',
      'docker.mjs',
      'state.mjs',
      'validation.mjs',
      'pin.json',
      'package.json',
      'README.md',
    ])
      await copy(path.join(layer, name), `runtime/layerfs-runtime/${name}`);
    await copy(
      path.join(installed.source, 'api'),
      'runtime/layerfs-runtime/vendor/api',
    );
    await copy(
      path.join(installed.source, 'LICENSE'),
      'runtime/layerfs-runtime/vendor/LICENSE',
    );
    await copy(
      installed.daemon,
      'runtime/layerfs-runtime/vendor/layerfs-daemon',
    );
    await copy(installed.native, 'runtime/layerfs-runtime/vendor/layerfs.node');
    await json('runtime/layerfs-runtime/installed.json', {
      ...installed,
      source: 'vendor',
      daemon: 'vendor/layerfs-daemon',
      native: 'vendor/layerfs.node',
    });
    await mkdir(path.join(staging, 'images'));
    await command('docker', [
      'image',
      'save',
      '--output',
      path.join(staging, 'images/layerfs.tar'),
      installed.image,
    ]);
    linuxMetadata = {
      rust: {
        toolchain: JSON.parse(
          await readFile(path.join(layer, 'pin.json'), 'utf8'),
        ).rust,
        modules: rustModules,
      },
      layerfs: {
        version: installed.version,
        commit: installed.commit,
        image: installed.image,
        baseImage: installed.baseImage,
      },
    };
  }
  const manifest = {
    schema: 2,
    role: 'runner-runtime',
    platform: runtimePlatform,
    target: linux ? 'Ubuntu 24.04 amd64' : 'macOS 15+ Apple Silicon',
    node: process.versions.node,
    pnpm: '12.11.2',
    ...licenses,
    ...linuxMetadata,
    dsh: '0.2.1-rc.3',
    prerequisites: linux
      ? [
          'Docker Engine with local Unix API and volumes',
          'FUSE /dev/fuse',
          'Bubblewrap with working user and PID namespaces',
          'Git',
          'Bash',
        ]
      : [
          'macOS 15 or later on Apple Silicon',
          'Unprivileged account with a launchd session',
          'Git',
        ],
    platformSelection: {
      'node-pty@1.2.0-beta.15/prebuilds': [prebuild],
    },
    files: {},
    symlinks: {},
  };
  const checksums = [];

  async function inventory(dir) {
    for (const entry of (await readdir(dir, { withFileTypes: true })).sort(
      (a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0),
    )) {
      const filename = path.join(dir, entry.name);
      const relative = path.relative(staging, filename);
      if (entry.isSymbolicLink()) {
        const target = await readlink(filename);
        const resolved = path.resolve(path.dirname(filename), target);
        if (path.isAbsolute(target) || !resolved.startsWith(`${staging}/`))
          throw new Error(`Release symlink escapes the bundle: ${relative}`);
        await lstat(resolved);
        manifest.symlinks[relative] = target;
      } else if (entry.isDirectory()) await inventory(filename);
      else if (entry.isFile()) {
        const digest = await hash(filename);
        manifest.files[relative] = {
          sha256: digest,
          bytes: (await lstat(filename)).size,
        };
        checksums.push(`${digest}  ${relative}`);
      } else throw new Error(`Unsupported release entry: ${relative}`);
    }
  }

  await inventory(staging);
  await json('manifest.json', manifest);
  checksums.push(
    `${await hash(path.join(staging, 'manifest.json'))}  manifest.json`,
  );
  await writeFile(
    path.join(staging, 'SHA256SUMS'),
    `${checksums.join('\n')}\n`,
  );
  await rm(destination, { recursive: true, force: true });
  await rename(staging, destination);
  await archiveTree(destination);
  const archive = `${destination}.tar.gz`;
  const runtimeSHA = await hash(archive);
  const runtimeName = `camp-runtime-${target}-${runtimeSHA}.tar.gz`;
  const runtimeArchive = path.join(root, 'dist', runtimeName);
  await rename(archive, runtimeArchive);

  async function treeBytes(directory) {
    let bytes = 0;
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const filename = path.join(directory, entry.name);
      if (entry.isDirectory()) bytes += await treeBytes(filename);
      else if (entry.isFile()) bytes += (await lstat(filename)).size;
    }
    return bytes;
  }

  const protocol = await readFile(
    path.join(root, 'apps/server/internal/runnerprotocol/protocol.go'),
    'utf8',
  );
  const protocolVersion = Number(protocol.match(/const Version = (\d+)/)?.[1]);
  if (!protocolVersion)
    throw new Error('Cannot determine Runner protocol version.');
  const pin = {
    schema: 1,
    platform: runtimePlatform,
    protocolVersion,
    ...(identity.published ? { releaseVersion: identity.version } : {}),
    sha256: runtimeSHA,
    manifestSHA256: await hash(path.join(destination, 'manifest.json')),
    bytes: (await lstat(runtimeArchive)).size,
    unpackedBytes: await treeBytes(destination),
  };
  const installer = path.join(root, `dist/camp-runner-${target}`);
  const installerStage = `${installer}.staging`;
  await rm(installerStage, { recursive: true, force: true });
  await mkdir(installerStage);
  const version = identity.version;
  if (!/^[A-Za-z0-9][A-Za-z0-9._+-]*$/.test(version))
    throw new Error('Use a valid release VERSION.');
  await command(
    go,
    [
      'build',
      '-trimpath',
      '-ldflags',
      `-s -w -X main.version=${version} -X github.com/cofy-x/camp/apps/server/internal/cli.runtimePinJSON=${JSON.stringify(pin)}`,
      '-o',
      path.join(installerStage, 'camp'),
      './apps/server/cmd/server',
    ],
    { env: { ...process.env, CGO_ENABLED: '0' } },
  );
  await chmod(path.join(installerStage, 'camp'), 0o755);
  await writeFile(
    path.join(installerStage, 'INSTALL.md'),
    installationNotes(identity, 'runner'),
  );
  await cp(
    path.join(destination, 'licenses'),
    path.join(installerStage, 'licenses'),
    { recursive: true },
  );
  await writeFile(
    path.join(installerStage, 'runtime-pin.json'),
    `${JSON.stringify(pin, null, 2)}\n`,
  );
  await rm(installer, { recursive: true, force: true });
  await rename(installerStage, installer);

  const installerInfo = await archiveTree(installer);
  const offline = path.join(root, `dist/camp-runner-offline-${target}`);
  await rm(offline, { recursive: true, force: true });
  await cp(installer, offline, { recursive: true });
  await cp(runtimeArchive, path.join(offline, runtimeName));
  const offlineInfo = await archiveTree(offline);
  const runtimeInfo = {
    filename: runtimeName,
    sha256: pin.sha256,
    bytes: pin.bytes,
    unpackedBytes: pin.unpackedBytes,
    manifestSHA256: pin.manifestSHA256,
  };
  for (const artifact of [installerInfo, runtimeInfo, offlineInfo]) {
    await writeFile(
      path.join(root, 'dist', `${artifact.filename}.sha256`),
      `${artifact.sha256}  ${artifact.filename}\n`,
    );
    console.log(`Packaged ${artifact.filename}: ${artifact.bytes} bytes`);
  }
  const descriptorPath = path.join(root, `dist/runner-release-${target}.json`);
  await writeFile(
    `${descriptorPath}.pending`,
    `${JSON.stringify({ schema: 2, ...(identity.published ? { version: identity.version, sourceCommit: identity.sourceCommit, protocolVersion } : {}), runners: { [target]: { installer: installerInfo, runtime: runtimeInfo, offline: offlineInfo } } }, null, 2)}\n`,
  );
  await rename(`${descriptorPath}.pending`, descriptorPath);
  await mergeRunnerReleases(path.join(root, 'dist'), identity, {
    partial: true,
  });
} catch (error) {
  await rm(staging, { recursive: true, force: true });
  throw error;
}
