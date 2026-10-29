import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
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
import { createRequire } from 'node:module';
import { promisify } from 'node:util';
import { command, go, root } from './lib.mjs';

if (process.platform !== 'linux' || process.arch !== 'x64')
  throw new Error('Build the first Camp release on Linux x86_64.');
if (process.versions.node !== '24.16.0') throw new Error('Use Node 24.16.0.');
const destination = path.join(root, 'dist/camp-runtime-linux-amd64');
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
const execute = promisify(execFile);
// Runtime identity follows content, not the installer build date or host uid.
// GNU format has no PAX atime/ctime headers that could vary between copies.
const deterministicTar = [
  '--sort=name',
  '--mtime=@0',
  '--owner=0',
  '--group=0',
  '--numeric-owner',
  '--format=gnu',
];

async function copyLicenses(source, destination) {
  for (const entry of await readdir(source, { withFileTypes: true })) {
    if (
      entry.isFile() &&
      /^(license|licence|notice|copying)(\.|-|$)/i.test(entry.name)
    )
      await copy(path.join(source, entry.name), `${destination}/${entry.name}`);
  }
}

async function collectLicenses() {
  await copy(path.join(root, 'LICENSE'), 'licenses/Camp-LICENSE');
  const { stdout: goRoot } = await execute('go', ['env', 'GOROOT'], {
    cwd: root,
  });
  await copy(path.join(goRoot.trim(), 'LICENSE'), 'licenses/Go-LICENSE');
  const { stdout } = await execute(
    'go',
    [
      'list',
      '-deps',
      '-f',
      '{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}',
      './apps/server/cmd/server',
      './apps/server/cmd/camp-sandbox',
    ],
    { cwd: root },
  );
  const modules = new Map();
  for (const line of stdout.trim().split('\n')) {
    const [name, version, source] = line.split('\t');
    if (!version || modules.has(name)) continue;
    modules.set(name, version);
    await copyLicenses(source, `licenses/go/${name}@${version}`);
  }
  const webModules = {};
  const visited = new Set();

  async function visitWeb(packageFile) {
    packageFile = await realpath(packageFile);
    if (visited.has(packageFile)) return;
    visited.add(packageFile);
    const metadata = JSON.parse(await readFile(packageFile, 'utf8'));
    if (!metadata.private) {
      webModules[metadata.name] = metadata.version;
      await copyLicenses(
        path.dirname(packageFile),
        `licenses/web/${metadata.name}@${metadata.version}`,
      );
    }
    const resolver = createRequire(packageFile);
    for (const name of Object.keys(metadata.dependencies ?? {})) {
      let dependency;
      for (const directory of resolver.resolve.paths(name) ?? []) {
        const candidate = path.join(directory, name, 'package.json');
        try {
          dependency = await realpath(candidate);
          break;
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      }
      if (!dependency)
        throw new Error(`Cannot find the installed license for ${name}.`);
      await visitWeb(dependency);
    }
  }

  await visitWeb(path.join(root, 'apps/dashboard/package.json'));
  for (const name of [
    'Inter-LICENSE.txt',
    'ChicagoKare-LICENSE.txt',
    'README.md',
  ])
    await copy(
      path.join(root, 'packages/platinum/src/fonts', name),
      `licenses/fonts/${name}`,
    );
  return { goModules: Object.fromEntries(modules), webModules };
}

async function hash(filename) {
  const digest = createHash('sha256');
  for await (const chunk of createReadStream(filename)) digest.update(chunk);
  return digest.digest('hex');
}

try {
  const licenses = await collectLicenses();
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
  // node-pty's published loader selects prebuilds/${process.platform}-${process.arch}.
  // The Linux amd64 release retains its native build and Linux x64 prebuild;
  // other platforms are never selected. Keep package source, maps and licenses.
  const ptyPrebuilds = path.join(
    staging,
    'runtime/dsh-runtime/node_modules/.pnpm/node-pty@1.2.0-beta.15/node_modules/node-pty/prebuilds',
  );
  await lstat(path.join(ptyPrebuilds, 'linux-x64'));
  for (const entry of await readdir(ptyPrebuilds, { withFileTypes: true })) {
    if (entry.isDirectory() && entry.name !== 'linux-x64')
      await rm(path.join(ptyPrebuilds, entry.name), { recursive: true });
  }
  const layer = path.join(root, 'tools/layerfs-runtime');
  const installed = JSON.parse(
    await readFile(path.join(layer, 'installed.json'), 'utf8'),
  );
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
  await copy(installed.daemon, 'runtime/layerfs-runtime/vendor/layerfs-daemon');
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
  const manifest = {
    schema: 2,
    role: 'runner-runtime',
    platform: 'linux/x64',
    target: 'Ubuntu 24.04',
    node: process.versions.node,
    pnpm: '12.11.2',
    ...licenses,
    dsh: '0.2.1-rc.3',
    layerfs: {
      version: installed.version,
      commit: installed.commit,
      image: installed.image,
      baseImage: installed.baseImage,
    },
    prerequisites: [
      'Docker Engine with local Unix API and volumes',
      'FUSE /dev/fuse',
      'Bubblewrap with working user and PID namespaces',
      'Git',
      'Bash',
    ],
    platformSelection: {
      'node-pty@1.2.0-beta.15/prebuilds': ['linux-x64'],
    },
    files: {},
    symlinks: {},
  };
  const checksums = [];

  async function inventory(dir) {
    for (const entry of (await readdir(dir, { withFileTypes: true })).sort(
      (a, b) => a.name.localeCompare(b.name),
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
  const archive = `${destination}.tar.gz`;
  await command('tar', [
    ...deterministicTar,
    '-czf',
    archive,
    '-C',
    path.dirname(destination),
    path.basename(destination),
  ]);
  const runtimeSHA = await hash(archive);
  const runtimeName = `camp-runtime-linux-amd64-${runtimeSHA}.tar.gz`;
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
    platform: 'linux/x64',
    protocolVersion,
    sha256: runtimeSHA,
    manifestSHA256: await hash(path.join(destination, 'manifest.json')),
    bytes: (await lstat(runtimeArchive)).size,
    unpackedBytes: await treeBytes(destination),
  };
  const installer = path.join(root, 'dist/camp-runner-linux-amd64');
  const installerStage = `${installer}.staging`;
  await rm(installerStage, { recursive: true, force: true });
  await mkdir(installerStage);
  const version = process.env.VERSION ?? 'dev';
  if (!/^[A-Za-z0-9][A-Za-z0-9._+-]*$/.test(version))
    throw new Error('Use a valid release VERSION.');
  await command(
    go,
    [
      'build',
      '-trimpath',
      '-tags',
      'production',
      '-ldflags',
      `-s -w -X main.version=${version} -X github.com/cofy-x/camp/apps/server/internal/cli.runtimePinJSON=${JSON.stringify(pin)}`,
      '-o',
      path.join(installerStage, 'camp'),
      './apps/server/cmd/server',
    ],
    { env: { ...process.env, CGO_ENABLED: '0' } },
  );
  await chmod(path.join(installerStage, 'camp'), 0o755);
  await cp(
    path.join(root, 'docs/install.md'),
    path.join(installerStage, 'INSTALL.md'),
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

  async function packageTree(directory) {
    const target = `${directory}.tar.gz`;
    await command('tar', [
      ...deterministicTar,
      '-czf',
      target,
      '-C',
      path.dirname(directory),
      path.basename(directory),
    ]);
    return {
      filename: path.basename(target),
      sha256: await hash(target),
      bytes: (await lstat(target)).size,
      unpackedBytes: await treeBytes(directory),
    };
  }

  const installerInfo = await packageTree(installer);
  const offline = path.join(root, 'dist/camp-runner-offline-linux-amd64');
  await rm(offline, { recursive: true, force: true });
  await cp(installer, offline, { recursive: true });
  await cp(runtimeArchive, path.join(offline, runtimeName));
  const offlineInfo = await packageTree(offline);
  const runtimeInfo = {
    filename: runtimeName,
    sha256: pin.sha256,
    bytes: pin.bytes,
    unpackedBytes: pin.unpackedBytes,
  };
  for (const artifact of [installerInfo, runtimeInfo, offlineInfo]) {
    await writeFile(
      path.join(root, 'dist', `${artifact.filename}.sha256`),
      `${artifact.sha256}  ${artifact.filename}\n`,
    );
    console.log(`Packaged ${artifact.filename}: ${artifact.bytes} bytes`);
  }
  const descriptorPath = path.join(root, 'dist/runner-release.json');
  await writeFile(
    `${descriptorPath}.pending`,
    `${JSON.stringify({ schema: 1, installer: installerInfo, runtime: runtimeInfo, offline: offlineInfo }, null, 2)}\n`,
  );
  await rename(`${descriptorPath}.pending`, descriptorPath);
} catch (error) {
  await rm(staging, { recursive: true, force: true });
  throw error;
}
