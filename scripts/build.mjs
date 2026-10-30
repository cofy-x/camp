import { cp, mkdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { binary, command, go, root } from './lib.mjs';
import { releaseIdentity, serverBuildFlags } from './release.mjs';

const identity = await releaseIdentity();
const args = process.argv.slice(2);
if (args.length > 1 || (args.length && args[0] !== '--skip-dashboard'))
  throw new Error('Usage: node scripts/build.mjs [--skip-dashboard]');
const flags = await serverBuildFlags(identity);

if (!args.length)
  await command(
    process.execPath,
    [join(root, 'node_modules/vite/bin/vite.js'), 'build'],
    { cwd: join(root, 'apps/dashboard') },
  );
const destination = join(root, 'apps/server/internal/webui/dist');
await rm(destination, { recursive: true, force: true });
await cp(join(root, 'apps/dashboard/dist'), destination, { recursive: true });
await mkdir(join(root, 'dist'), { recursive: true });
await command(
  go,
  [
    'build',
    '-trimpath',
    '-tags',
    'production',
    '-ldflags',
    flags,
    '-o',
    binary,
    './apps/server/cmd/server',
  ],
  { env: { ...process.env, CGO_ENABLED: '0' } },
);
console.log(`Built ${binary}`);
await command(
  go,
  [
    'build',
    '-trimpath',
    '-buildvcs=false',
    '-o',
    join(root, 'dist/camp-sandbox'),
    './apps/server/cmd/camp-sandbox',
  ],
  { env: { ...process.env, CGO_ENABLED: '0' } },
);
console.log('Built the isolated command helper.');
