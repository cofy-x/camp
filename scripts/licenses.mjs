import { execFile } from 'node:child_process';
import { cp, mkdir, readFile, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { promisify } from 'node:util';
import { root } from './lib.mjs';

const execute = promisify(execFile);

async function copyLicenses(source, destination, copy) {
  for (const entry of await readdir(source, { withFileTypes: true })) {
    if (
      entry.isFile() &&
      /^(license|licence|notice|copying)(\.|-|$)/i.test(entry.name)
    )
      await copy(path.join(source, entry.name), `${destination}/${entry.name}`);
  }
}

export async function collectLicenses(staging) {
  const copy = async (from, to) => {
    await mkdir(path.dirname(path.join(staging, to)), { recursive: true });
    await cp(from, path.join(staging, to), {
      recursive: true,
      verbatimSymlinks: true,
    });
  };
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
    await copyLicenses(source, `licenses/go/${name}@${version}`, copy);
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
        copy,
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
