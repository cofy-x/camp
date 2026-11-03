import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  lstat,
  mkdir,
  readFile,
  readdir,
  realpath,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import {
  binary as builtBinary,
  dispose,
  root,
  scratch,
  stop,
  waitFor,
} from './lib.mjs';
import { hashFile, releaseIdentity } from './release.mjs';
import { serverService } from './server-service.mjs';

const { values } = parseArgs({
  options: { service: { type: 'boolean', default: false } },
});
if (values.service) {
  assert.equal(
    process.platform,
    'darwin',
    'Server service acceptance requires an actual Mac.',
  );
}
const binary = resolve(process.env.CAMP_SMOKE_BINARY || builtBinary);
const temporary = await scratch();
const data = join(temporary, 'server');
const source = join(temporary, 'import-source');
await mkdir(source, { mode: 0o700 });
await writeFile(
  join(source, 'native.txt'),
  'Immutable Mac directory snapshot\n',
);
const listener = createServer();
await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
const address = `127.0.0.1:${listener.address().port}`;
await new Promise((resolve) => listener.close(resolve));
const base = `http://${address}`;
const identity = await releaseIdentity();
const report = {
  schema: 1,
  status: 'failed',
  sourceCommit: identity.sourceCommit,
  sourceModified: identity.modified,
  version: identity.version,
  platform: `${process.platform}/${process.arch}`,
  binarySHA256: await hashFile(binary),
  ...(process.env.CAMP_SERVER_ARCHIVE
    ? { archiveSHA256: await hashFile(process.env.CAMP_SERVER_ARCHIVE) }
    : {}),
  mode: values.service ? 'service' : 'foreground',
  cleanupConfirmed: false,
  untested: ['physical sleep and wake', 'login startup', 'paid model provider'],
  checks: [],
  modelRequests: 0,
};
let child;
let cookie = '';
const environment = {
  PATH: '/usr/bin:/bin',
  HOME: temporary,
  LANG: 'C.UTF-8',
  CAMP_IMPORT_ROOTS: await realpath(source),
  ...(process.env.XDG_CACHE_HOME
    ? { XDG_CACHE_HOME: process.env.XDG_CACHE_HOME }
    : {}),
};
const interrupted = new AbortController();
const interrupt = () =>
  interrupted.abort(
    new Error('Server acceptance was interrupted; preserve failed fixtures.'),
  );
process.on('SIGINT', interrupt);
process.on('SIGTERM', interrupt);
const service = values.service
  ? serverService({
      binary,
      data,
      cwd: temporary,
      environment,
      address,
      signal: interrupted.signal,
    })
  : null;
if (service) report.service = service.observations;

async function start() {
  if (service)
    return service.start(async () => {
      try {
        return (
          await fetch(`${base}/api/health`, {
            signal: AbortSignal.any([
              interrupted.signal,
              AbortSignal.timeout(1000),
            ]),
          })
        ).ok;
      } catch {
        return false;
      }
    });
  child = spawn(binary, ['serve', '--addr', address, '--data-dir', data], {
    cwd: temporary,
    env: environment,
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  await waitFor(`${base}/api/health`, child);
}

async function stopServer() {
  if (service) return service.stop();
  if (child) await stop(child);
}

async function retainedData() {
  const files = {};
  let bytes = 0;

  async function visit(relative) {
    for (const name of (await readdir(join(data, relative))).sort()) {
      if (!relative && ['service', 'logs'].includes(name)) continue;
      const path = join(relative, name);
      const info = await lstat(join(data, path));
      if (info.isDirectory()) await visit(path);
      else {
        bytes += info.size;
        assert.ok(
          info.isFile() &&
            Object.keys(files).length < 10000 &&
            bytes <= 256 << 20,
          'Unexpected retained data inventory; preserve the fixture.',
        );
        files[path] = {
          sha256: await hashFile(join(data, path)),
          mode: info.mode & 0o777,
        };
      }
    }
  }

  await visit('');
  assert.ok(
    files['desktop.sqlite'],
    'The stopped Server database must remain present.',
  );
  return files;
}

async function api(path, options = {}, status = 200) {
  const { body, ...rest } = options;
  const form = body instanceof FormData;
  const response = await fetch(`${base}${path}`, {
    ...rest,
    headers: {
      Origin: base,
      Cookie: cookie,
      ...(body && !form ? { 'Content-Type': 'application/json' } : {}),
    },
    ...(body ? { body: form ? body : JSON.stringify(body) } : {}),
    signal: AbortSignal.any([interrupted.signal, AbortSignal.timeout(30000)]),
  });
  assert.equal(
    response.status,
    status,
    `${path}: ${await response.clone().text()}`,
  );
  return response;
}

async function assertNoParserJobs() {
  await assert.rejects(lstat(join(data, 'document-jobs')), { code: 'ENOENT' });
}

try {
  if (service) await service.install();
  await start();
  const setup = await api('/api/auth/setup', {
    method: 'POST',
    body: {
      token: (await readFile(join(data, 'team-setup.token'), 'utf8')).trim(),
      username: 'server-owner',
      name: 'Server Owner',
      password: 'Private synthetic Server fixture password',
    },
  });
  cookie = setup.headers.getSetCookie()[0].split(';')[0];
  for (let i = 0; i < 3; i++) {
    const readiness = await (await api('/api/settings/readiness')).json();
    assert.equal(
      readiness.ready,
      false,
      'Plain Server work must not invent a Runner or model.',
    );
    assert.ok(
      readiness.components.every(
        (component) => !component.id.startsWith('document-'),
      ),
    );
  }
  const { runners } = await (await api('/api/runners')).json();
  assert.equal(runners.length, 0);
  const { rooms } = await (await api('/api/rooms')).json();
  const originals = [];
  for (const [name, bytes] of [
    [
      'guide.md',
      Buffer.from('# Team guide\n\nKeep source material in Markdown.\n'),
    ],
    ['notes.txt', Buffer.from('Plain text remains readable.\n')],
    ['original.pdf', Buffer.from('%PDF-opaque attachment fixture\n')],
    ['original.docx', Buffer.from('PK opaque attachment fixture\n')],
  ]) {
    const form = new FormData();
    form.append('requestId', randomUUID());
    form.append('file', new Blob([bytes]), name);
    const { reference } = await (
      await api(`/api/rooms/${rooms[0].id}/uploads`, {
        method: 'POST',
        body: form,
      })
    ).json();
    assert.equal(
      reference.document ?? null,
      null,
      'An upload started document preparation.',
    );
    const endpoint = `/api/resources/${reference.resourceId}/file?path=${encodeURIComponent(reference.path)}&commit=${reference.commit}`;
    assert.deepEqual(
      Buffer.from(await (await api(endpoint)).arrayBuffer()),
      bytes,
    );
    originals.push({ endpoint, bytes });
    if (/\.(pdf|docx)$/.test(name)) {
      await api(
        `/api/resources/${reference.resourceId}/document`,
        {
          method: 'POST',
          body: { commit: reference.commit, path: reference.path },
        },
        410,
      );
    }
  }
  await assertNoParserJobs();
  report.checks.push(
    'markdown-and-plaintext',
    'original-only-attachments-preserved',
    'no-parser-readiness-or-jobs',
  );
  const { resource } = await (
    await api('/api/resources', {
      method: 'POST',
      body: { name: 'Mac source', path: source },
    })
  ).json();
  assert.equal(
    await (
      await api(
        `/api/resources/${resource.id}/file?path=native.txt&commit=${resource.currentCommit}`,
      )
    ).text(),
    'Immutable Mac directory snapshot\n',
  );
  assert.equal(
    await readFile(join(source, 'native.txt'), 'utf8'),
    'Immutable Mac directory snapshot\n',
  );
  await api(
    '/api/resources',
    { method: 'POST', body: { name: 'Denied source', path: temporary } },
    403,
  );
  report.checks.push('allowlisted-server-directory-import-preserves-source');
  await stopServer();
  await start();
  for (const saved of originals) {
    assert.deepEqual(
      Buffer.from(await (await api(saved.endpoint)).arrayBuffer()),
      saved.bytes,
    );
  }
  await assertNoParserJobs();
  report.parserJobsCreated = false;
  report.checks.push('restart-preserves-files');
  if (service) {
    await stopServer();
    const before = await retainedData();
    await service.remove();
    assert.deepEqual(
      await retainedData(),
      before,
      'Removing the service changed private team data.',
    );
    report.retainedDataPreserved = true;
    report.cleanupConfirmed = true;
    report.checks.push(
      'packaged-server-launchd-lifecycle',
      'service-removal-preserves-private-data',
      'owned-service-absent',
    );
  }
  report.status = 'passed';
} finally {
  try {
    if (service && !report.cleanupConfirmed) {
      await service.remove();
      report.cleanupConfirmed = true;
    } else if (child) await stop(child);
    if (!service) report.cleanupConfirmed = true;
    if (report.status === 'passed')
      await dispose(temporary, child ? [child] : []);
    else report.retainedFixture = temporary;
  } catch {
    report.status = 'failed';
    report.retainedFixture = temporary;
    process.exitCode = 1;
  } finally {
    process.removeListener('SIGINT', interrupt);
    process.removeListener('SIGTERM', interrupt);
    report.completedAt = new Date().toISOString();
    await mkdir(join(root, 'dist'), { recursive: true });
    await writeFile(
      process.env.CAMP_SERVER_SERVICE_REPORT ||
        join(
          root,
          `dist/server-service-verification-${process.platform}-${process.arch}.json`,
        ),
      `${JSON.stringify(report, null, 2)}\n`,
    );
  }
}
