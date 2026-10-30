import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { request as httpsRequest } from 'node:https';
import { join } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { promisify } from 'node:util';
import { root } from './lib.mjs';

const execute = promisify(execFile);
const reference = process.argv[2];
const reportFile = join(root, 'dist/smoke-server-image.json');
await rm(reportFile, { force: true });
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
const tls = join(directory, 'tls');
await mkdir(tls, { mode: 0o700 });
await execute('openssl', [
  'req',
  '-x509',
  '-newkey',
  'rsa:2048',
  '-noenc',
  '-days',
  '1',
  '-subj',
  '/CN=localhost',
  '-addext',
  'subjectAltName=IP:127.0.0.1,DNS:localhost',
  '-keyout',
  join(tls, 'server.key'),
  '-out',
  join(tls, 'server.crt'),
]);
const ca = await readFile(join(tls, 'server.crt'));

function request(url, options = {}) {
  return new Promise((resolve, reject) => {
    const pending = httpsRequest(
      url,
      { ca, method: options.method, headers: options.headers, timeout: 5000 },
      (response) => {
        const chunks = [];
        response.on('data', (chunk) => chunks.push(chunk));
        response.once('error', reject);
        response.once('end', () =>
          resolve({
            status: response.statusCode,
            headers: response.headers,
            text: Buffer.concat(chunks).toString('utf8'),
          }),
        );
      },
    );
    pending.once('error', reject);
    pending.once('timeout', () =>
      pending.destroy(new Error('Image HTTPS request timed out.')),
    );
    pending.end(options.body);
  });
}

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
    '--mount',
    `type=bind,src=${tls},dst=/tls,readonly`,
    reference,
    'serve',
    '--addr',
    '0.0.0.0:3443',
    '--data-dir',
    '/data',
    '--tls-cert',
    '/tls/server.crt',
    '--tls-key',
    '/tls/server.key',
    '--allowed-hosts',
    '127.0.0.1',
  );
  assert.match(container, /^[a-f0-9]{64}$/);
  const port = (await docker('port', container, '3443/tcp')).match(
    /^127\.0\.0\.1:(\d+)$/,
  )?.[1];
  assert.ok(port);
  const origin = `https://127.0.0.1:${port}`;
  let ready = false;
  for (let attempt = 0; attempt < 120; attempt++) {
    assert.equal(
      await docker('inspect', '--format', '{{.State.Running}}', container),
      'true',
    );
    try {
      ready = (await request(`${origin}/api/health`)).status === 200;
    } catch {
      /* The new container may still be opening SQLite. */
    }
    if (ready) break;
    await pause(250);
  }
  assert.ok(ready, 'Server image did not become healthy.');
  const setup = await request(`${origin}/api/auth/setup`, {
    method: 'POST',
    headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      token: (await readFile(join(data, 'team-setup.token'), 'utf8')).trim(),
      username: 'image-admin',
      name: 'Image Admin',
      password: 'Disposable image acceptance password',
    }),
  });
  assert.equal(setup.status, 200, setup.text);
  const cookie = setup.headers['set-cookie'][0].split(';')[0];
  const metadata = await request(`${origin}/api/runner-installation`, {
    headers: { Cookie: cookie },
  });
  assert.equal(metadata.status, 200);
  const installation = JSON.parse(metadata.text);
  assert.equal(installation.source, 'github');
  assert.equal(installation.version, release.version);
  assert.equal(installation.available, true);
  assert.equal(
    installation.sha256,
    release.runners['linux-amd64'].installer.sha256,
  );
  assert.equal(
    installation.downloadUrl,
    `https://github.com/cofy-x/camp/releases/download/${release.version}/${release.runners['linux-amd64'].installer.filename}`,
  );
  const page = await request(origin);
  const html = page.text;
  assert.equal(page.status, 200);
  const script = html.match(/<script[^>]*src="([^"]+)"/)?.[1];
  assert.ok(script, 'Dashboard is missing from the Server image.');
  assert.equal((await request(new URL(script, origin))).status, 200);
  await writeFile(
    reportFile,
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
          'verified-HTTPS',
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
