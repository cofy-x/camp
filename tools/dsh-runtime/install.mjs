import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import process from 'node:process';
import { fileURLToPath, URL } from 'node:url';

const directory = fileURLToPath(new URL('.', import.meta.url));
const pin = JSON.parse(
  readFileSync(new URL('package.json', import.meta.url), 'utf8'),
).dependencies['@deepseek-ai/dsh'];

if (process.version !== 'v24.16.0') {
  throw new Error(
    `Camp DSH runtime requires Node 24.16.0; found ${process.version}`,
  );
}

const install = spawnSync(
  'npx',
  ['--yes', 'pnpm@12.11.2', 'install', '--frozen-lockfile'],
  {
    cwd: directory,
    stdio: 'inherit',
  },
);
if (install.error) throw install.error;
if (install.status !== 0) process.exit(install.status ?? 1);
const version = spawnSync(
  process.execPath,
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
