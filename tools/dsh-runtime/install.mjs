import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
} from 'node:fs';
import { delimiter, join } from 'node:path';
import process from 'node:process';
import { fileURLToPath, URL } from 'node:url';

const directory = fileURLToPath(new URL('.', import.meta.url));
const manifest = JSON.parse(
  readFileSync(new URL('package.json', import.meta.url), 'utf8'),
);
const pin = manifest.dependencies['@deepseek-ai/dsh'];
const nodeVersion = `v${manifest.engines.node}`;
const platform = `${process.platform}-${process.arch}`;
// Official Node release archive checksums for the supported Runner platforms.
const checksums = {
  'linux-x64':
    '4f2b175a6d53a5c617a80dba2676238a0688cef28c6da4eba35c7d844e66fcfb',
  'darwin-arm64':
    '1d64136c8a43d648d1b3bad10797a4c995b1ca032d2bde8799de43b37f058fe5',
};
const checksum = checksums[platform];
if (!checksum) throw new Error('DSH requires Linux x64 or macOS ARM64.');
const nodeRoot = join(directory, 'node');
const bin = join(nodeRoot, 'bin');
const node = join(bin, 'node');
const installed = spawnSync(node, ['--version'], { encoding: 'utf8' });
if (installed.status !== 0 || installed.stdout.trim() !== nodeVersion) {
  mkdirSync(bin, { recursive: true });
  const staging = mkdtempSync(join(nodeRoot, 'install-'));
  try {
    const artifact = `node-${nodeVersion}-${platform}`;
    const archive = join(staging, `${artifact}.tar.xz`);
    execFileSync('curl', [
      '--fail',
      '--silent',
      '--show-error',
      '--location',
      '--proto',
      '=https',
      '--proto-redir',
      '=https',
      '--max-time',
      '120',
      '--retry',
      '2',
      '--output',
      archive,
      `https://nodejs.org/dist/${nodeVersion}/${artifact}.tar.xz`,
    ]);
    const actual = createHash('sha256')
      .update(readFileSync(archive))
      .digest('hex');
    if (actual !== checksum)
      throw new Error('DSH Node archive checksum mismatch.');
    execFileSync('tar', [
      '--no-same-owner',
      '-xJf',
      archive,
      '-C',
      staging,
      `${artifact}/bin/node`,
      `${artifact}/LICENSE`,
    ]);
    renameSync(join(staging, artifact, 'LICENSE'), join(nodeRoot, 'LICENSE'));
    renameSync(join(staging, artifact, 'bin/node'), node);
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

const install = spawnSync(
  'npx',
  ['--yes', 'pnpm@12.11.2', 'install', '--frozen-lockfile'],
  {
    cwd: directory,
    env: {
      ...process.env,
      PATH: `${bin}${delimiter}${process.env.PATH ?? ''}`,
    },
    stdio: 'inherit',
  },
);
if (install.error) throw install.error;
if (install.status !== 0) process.exit(install.status ?? 1);
const version = spawnSync(
  node,
  ['node_modules/@deepseek-ai/dsh/lib/bin.js', '--version'],
  {
    cwd: directory,
    encoding: 'utf8',
  },
);
if (version.error) throw version.error;
if (version.status !== 0 || version.stdout.trim() !== pin) {
  throw new Error(
    `Installed DSH runtime did not report the pinned version ${pin}`,
  );
}
process.stdout.write(`Camp DSH runtime ${pin} is ready.\n`);
