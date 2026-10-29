import { cp, mkdir, readFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { binary, command, go, root } from './lib.mjs';
import { releaseIdentity } from './release.mjs';

const identity = await releaseIdentity();
const args = process.argv.slice(2);
if (args.length > 1 || (args.length && args[0] !== '--skip-dashboard'))
  throw new Error('Usage: node scripts/build.mjs [--skip-dashboard]');
let flags = `-X main.version=${identity.version}`;
if (identity.published) {
  const data = await readFile(join(root, 'dist/runner-release.json'));
  const release = JSON.parse(data);
  if (
    release.version !== identity.version ||
    release.sourceCommit !== identity.sourceCommit
  )
    throw new Error(
      'Build matching Runner packages before the release Server.',
    );
  flags += ` -X github.com/cofy-x/camp/apps/server/internal/releaseinfo.manifestBase64=${data.toString('base64')}`;
}

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
    '-o',
    join(root, 'dist/camp-sandbox'),
    './apps/server/cmd/camp-sandbox',
  ],
  { env: { ...process.env, CGO_ENABLED: '0' } },
);
console.log('Built the isolated command helper.');
