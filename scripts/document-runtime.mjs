import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import {
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rm,
  stat,
  writeFile,
} from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { command, go, root } from './lib.mjs';

async function digest(filename) {
  const hash = createHash('sha256');
  for await (const data of createReadStream(filename)) hash.update(data);
  return hash.digest('hex');
}

// Build flat, link-free reader packages so standalone, Homebrew and the Go
// launcher retain identical private runtime layout without npm resolution.
export async function packageDocumentRuntime(destination, platform) {
  if (!['darwin-arm64', 'darwin-amd64'].includes(platform))
    throw new Error('Unsupported document package platform.');
  try {
    if ((await readdir(destination)).length)
      throw new Error(
        'Build document packages into a fresh empty staging directory.',
      );
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  const arch = platform === 'darwin-amd64' ? 'x64' : 'arm64';
  const pin = JSON.parse(
    await readFile(join(root, 'tools/document-runtime/node-pin.json'), 'utf8'),
  );
  if (
    pin.version !== (await readFile(join(root, '.node-version'), 'utf8')).trim()
  )
    throw new Error('Document Node pin must match the repository toolchain.');
  const temporary = await mkdtemp(join(root, 'dist/document-build-'));
  try {
    const source = join(root, 'tools/document-runtime');
    const install = join(temporary, 'install');
    await mkdir(install);
    for (const name of ['package.json', 'pnpm-lock.yaml'])
      await cp(join(source, name), join(install, name));
    await writeFile(
      join(install, 'pnpm-workspace.yaml'),
      `packages: []\nsupportedArchitectures:\n  os: [darwin]\n  cpu: [${arch}]\n`,
    );
    await command(
      'npx',
      [
        '--yes',
        'pnpm@12.11.2',
        'install',
        '--frozen-lockfile',
        '--ignore-scripts',
      ],
      { cwd: install },
    );
    const runtime = join(destination, 'runtime');
    await mkdir(join(runtime, 'node_modules/@napi-rs'), { recursive: true });
    await cp(join(source, 'extract.mjs'), join(runtime, 'extract.mjs'));
    const modules = join(install, 'node_modules/.pnpm');
    const pdf = join(modules, 'pdfjs-dist@6.4.299/node_modules/pdfjs-dist');
    const target = join(runtime, 'node_modules/pdfjs-dist');
    await mkdir(join(target, 'legacy/build'), { recursive: true });
    for (const name of [
      'package.json',
      'LICENSE',
      'cmaps',
      'standard_fonts',
      'legacy/build/pdf.mjs',
      'legacy/build/pdf.worker.mjs',
    ]) {
      await cp(join(pdf, name), join(target, name), {
        recursive: true,
        dereference: true,
      });
    }
    const canvas = join(
      modules,
      '@napi-rs+canvas@1.0.13/node_modules/@napi-rs',
    );
    for (const name of ['canvas', `canvas-darwin-${arch}`]) {
      // Resolve through the locked optional dependency, never a host module.
      const candidate =
        name === 'canvas'
          ? join(canvas, name)
          : join(
              modules,
              `@napi-rs+${name}@1.0.13/node_modules/@napi-rs`,
              name,
            );
      await cp(candidate, join(runtime, 'node_modules/@napi-rs', name), {
        recursive: true,
        dereference: true,
      });
    }
    await mkdir(join(destination, 'bin'), { recursive: true });
    await mkdir(join(destination, 'licenses'), { recursive: true });
    if (
      process.platform === 'darwin' &&
      process.arch === arch &&
      process.versions.node === pin.version
    ) {
      const node = await realpath(process.execPath);
      await cp(node, join(destination, 'bin/node'));
      await cp(
        join(dirname(node), '../LICENSE'),
        join(destination, 'licenses/Node-LICENSE'),
      );
    } else {
      const archiveName = `node-v${pin.version}-darwin-${arch}`;
      const archive = join(temporary, 'node.tar.gz');
      await command('curl', [
        '--fail',
        '--location',
        '--proto',
        '=https',
        '--max-time',
        '300',
        '--max-filesize',
        String(128 << 20),
        '--output',
        archive,
        `https://nodejs.org/dist/v${pin.version}/${archiveName}.tar.gz`,
      ]);
      if ((await digest(archive)) !== pin.archives[`darwin-${arch}`])
        throw new Error('The fixed Node checksum differs.');
      await command('tar', [
        '-xzf',
        archive,
        '-C',
        temporary,
        `${archiveName}/bin/node`,
        `${archiveName}/LICENSE`,
      ]);
      await cp(
        join(temporary, archiveName, 'bin/node'),
        join(destination, 'bin/node'),
      );
      await cp(
        join(temporary, archiveName, 'LICENSE'),
        join(destination, 'licenses/Node-LICENSE'),
      );
    }
    await command(
      go,
      [
        'build',
        '-trimpath',
        '-o',
        join(destination, 'bin/camp-sandbox'),
        './apps/server/cmd/camp-sandbox',
      ],
      {
        env: {
          ...process.env,
          CGO_ENABLED: '0',
          GOOS: 'darwin',
          GOARCH: arch === 'x64' ? 'amd64' : arch,
        },
      },
    );
    const files = {};

    async function inventory(directory) {
      for (const entry of (
        await readdir(join(destination, directory), { withFileTypes: true })
      ).sort((left, right) => left.name.localeCompare(right.name, 'en'))) {
        const name = directory ? `${directory}/${entry.name}` : entry.name;
        if (entry.isDirectory()) await inventory(name);
        else if (entry.isFile()) {
          const info = await stat(join(destination, name));
          files[name] = {
            sha256: await digest(join(destination, name)),
            bytes: info.size,
            executable: (info.mode & 0o111) !== 0,
          };
        } else
          throw new Error(
            'A bundled document runtime must contain only directories and regular files.',
          );
      }
    }

    await inventory('');
    const manifest = {
      schema: 1,
      platform: `darwin/${arch === 'x64' ? 'amd64' : arch}`,
      nodeVersion: pin.version,
      pdfjsVersion: '6.4.299',
      files,
    };
    await writeFile(
      join(destination, 'manifest.json'),
      `${JSON.stringify(manifest, null, 2)}\n`,
    );
    return digest(join(destination, 'manifest.json'));
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  const [platform, directory] = process.argv.slice(2);
  if (!directory)
    throw new Error(
      'Usage: node scripts/document-runtime.mjs darwin-arm64|darwin-amd64 OUTPUT',
    );
  await mkdir(dirname(resolve(directory)), { recursive: true });
  console.log(await packageDocumentRuntime(resolve(directory), platform));
}
