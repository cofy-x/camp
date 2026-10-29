import { cp, mkdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { binary, command, go, root } from './lib.mjs';

const version = process.env.VERSION ?? 'dev';
if (!/^[A-Za-z0-9]/.test(version) || /[^A-Za-z0-9._+-]/.test(version)) {
  throw new Error(
    'VERSION must start with a letter or digit and contain only letters, digits, dots, underscores, plus signs, or hyphens.',
  );
}

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
    `-X main.version=${version}`,
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
    '-o',
    join(root, 'dist/camp-sandbox'),
    './apps/server/cmd/camp-sandbox',
  ],
  { env: { ...process.env, CGO_ENABLED: '0' } },
);
console.log('Built the isolated command helper.');
