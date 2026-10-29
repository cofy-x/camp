import { spawnSync } from 'node:child_process';
import process from 'node:process';

if (process.version !== 'v24.16.0')
  throw new Error(
    `Camp document runtime requires Node 24.16.0; found ${process.version}`,
  );

const install = spawnSync(
  'npx',
  ['--yes', 'pnpm@12.11.2', 'install', '--frozen-lockfile', '--ignore-scripts'],
  {
    cwd: import.meta.dirname,
    stdio: 'inherit',
  },
);

if (install.error) throw install.error;
if (install.status !== 0) process.exit(install.status ?? 1);
