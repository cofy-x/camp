import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { promisify } from 'node:util';
import { root } from './lib.mjs';

const execute = promisify(execFile);
const reference = process.argv[2];
assert.ok(
  reference && process.argv.length === 3,
  'Choose the exact Server image reference.',
);
const release = JSON.parse(
  await readFile(join(root, 'dist/runner-release.json'), 'utf8'),
);
const docker = async (...args) =>
  (await execute('docker', args, { maxBuffer: 1 << 20 })).stdout.trim();
const [image] = JSON.parse(await docker('image', 'inspect', reference));
assert.equal(
  image.Config.Labels['org.opencontainers.image.version'],
  release.version,
);
assert.equal(
  image.Config.Labels['org.opencontainers.image.revision'],
  release.sourceCommit,
);
assert.equal(
  image.Config.Labels['org.opencontainers.image.source'],
  'https://github.com/cofy-x/camp',
);
const version = await docker(
  'run',
  '--rm',
  '--entrypoint',
  '/bin/sh',
  reference,
  '-ec',
  'test -s /opt/camp/licenses/Camp-LICENSE; test -s /opt/camp/runner-release.json; test ! -e /usr/bin/docker; test ! -e /usr/local/bin/node; test ! -e /opt/camp/runtime; camp version',
);
assert.ok(
  version.includes(release.version),
  'Image binary version differs from its label.',
);
const directory = await mkdtemp(join(tmpdir(), 'camp-image-'));
const data = join(directory, 'data');
await mkdir(data, { mode: 0o700 });
let container;
try {
  container = await docker(
    'run',
    '--detach',
    '--rm',
    '--publish',
    '127.0.0.1::3443',
    '--mount',
    `type=bind,src=${data},dst=/data`,
    reference,
  );
  assert.match(container, /^[a-f0-9]{64}$/);
  const port = (await docker('port', container, '3443/tcp')).match(
    /^127\.0\.0\.1:(\d+)$/,
  )?.[1];
  assert.ok(port);
  const origin = `http://127.0.0.1:${port}`;
  let ready = false;
  for (let attempt = 0; attempt < 120; attempt++) {
    assert.equal(
      await docker('inspect', '--format', '{{.State.Running}}', container),
      'true',
    );
    try {
      ready = (
        await fetch(`${origin}/api/health`, {
          signal: AbortSignal.timeout(1000),
        })
      ).ok;
    } catch {
      /* The new container may still be opening SQLite. */
    }
    if (ready) break;
    await pause(250);
  }
  assert.ok(ready, 'Server image did not become healthy.');
  const setup = await fetch(`${origin}/api/auth/setup`, {
    method: 'POST',
    headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      token: (await readFile(join(data, 'team-setup.token'), 'utf8')).trim(),
      username: 'image-admin',
      name: 'Image Admin',
      password: 'Disposable image acceptance password',
    }),
  });
  assert.equal(setup.status, 200);
  const cookie = setup.headers.get('set-cookie').split(';')[0];
  const metadata = await fetch(`${origin}/api/runner-installation`, {
    headers: { Cookie: cookie },
  });
  assert.equal(metadata.status, 200);
  const installation = await metadata.json();
  assert.equal(installation.source, 'github');
  assert.equal(installation.version, release.version);
  assert.equal(installation.available, true);
  assert.equal(installation.sha256, release.installer.sha256);
  assert.equal(
    installation.downloadUrl,
    `https://github.com/cofy-x/camp/releases/download/${release.version}/${release.installer.filename}`,
  );
  const page = await fetch(origin);
  const html = await page.text();
  assert.equal(page.status, 200);
  const script = html.match(/<script[^>]*src="([^"]+)"/)?.[1];
  assert.ok(script, 'Dashboard is missing from the Server image.');
  assert.equal((await fetch(new URL(script, origin))).status, 200);
  await writeFile(
    join(root, 'dist/smoke-server-image.json'),
    `${JSON.stringify(
      {
        schema: 1,
        status: 'passed',
        version: release.version,
        sourceCommit: release.sourceCommit,
        imageID: image.Id,
        completedAt: new Date().toISOString(),
        checks: [
          'binary-version',
          'OCI-labels',
          'licenses',
          'server-without-native-runtime',
          'fresh-data-bootstrap',
          'embedded-dashboard',
          'fixed-GitHub-installation-without-staged-packages',
        ],
      },
      null,
      2,
    )}\n`,
  );
  console.log(`Server image smoke passed: ${reference}`);
} finally {
  if (container) await docker('stop', '--time', '10', container);
  await rm(directory, { recursive: true, force: true });
}
