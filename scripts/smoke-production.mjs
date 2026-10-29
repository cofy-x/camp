import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile, readdir, stat } from 'node:fs/promises';
import { request as httpRequest } from 'node:http';
import { createServer } from 'node:net';
import { join } from 'node:path';
import {
  binary as builtBinary,
  dispose,
  project,
  scratch,
  stop,
  waitFor,
} from './lib.mjs';

// Release acceptance also runs this lifecycle against a relocated archive or
// the public Go entrypoint, without rebuilding a different executable.
const binary = process.env.CAMP_SMOKE_BINARY || builtBinary;

const dir = await scratch();
const children = [];
const listener = createServer();
await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const base = `http://127.0.0.1:${port}`;
const addr = `127.0.0.1:${port}`;
const dataDir = join(dir, 'data');
const ignoredDataDir = join(dir, 'ignored-environment-data');

function statusWithHost(url, host) {
  return new Promise((resolve, reject) => {
    // Fetch controls Host itself; use the HTTP client to exercise the actual
    // server boundary with a different authority on this loopback connection.
    const request = httpRequest(
      url,
      { headers: { Host: host }, timeout: 5000 },
      (response) => {
        response.on('error', reject);
        response.on('end', () => resolve(response.statusCode));
        response.resume();
      },
    );
    request.on('error', reject);
    request.on('timeout', () => request.destroy(new Error('HTTP timed out.')));
    request.end();
  });
}

function invoke(args, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, {
      cwd: dir,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: {
        ...process.env,
        ALLOWED_HOSTS: '',
        TRUSTED_ORIGINS: '',
        CAMP_PUBLIC_ORIGIN: '',
        ...env,
      },
    });
    children.push(child);
    let stdout = '';
    let stderr = '';
    let timedOut = false;
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (text) => {
      stdout += text;
    });
    child.stderr.on('data', (text) => {
      stderr += text;
    });
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGKILL');
    }, 5000);
    child.once('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once('close', (code, signal) => {
      clearTimeout(timer);
      resolve({ code, signal, stdout, stderr, timedOut });
    });
  });
}

function start(args = [], env = {}) {
  const child = spawn(binary, args, {
    cwd: dir,
    stdio: ['inherit', 'inherit', 'pipe'],
    env: {
      ...process.env,
      ADDR: addr,
      DATA_DIR: dataDir,
      ALLOWED_HOSTS: '',
      TRUSTED_ORIGINS: '',
      CAMP_PUBLIC_ORIGIN: '',
      ...env,
    },
  });
  children.push(child);
  return child;
}

try {
  const invalidEnvironment = {
    ADDR: 'invalid-address',
    PORT: 'invalid-port',
    DATA_DIR: ignoredDataDir,
  };
  let versionOutput;
  for (const args of [
    ['help'],
    ['--help'],
    ['serve', '--help'],
    ['version'],
    ['--version'],
  ]) {
    const result = await invoke(args, invalidEnvironment);
    const label = `${project} ${args.join(' ')}`;
    assert.equal(result.timedOut, false, `${label} must exit promptly.`);
    assert.equal(result.code, 0, `${label}: ${result.stderr}`);
    assert.equal(result.signal, null, `${label} must exit normally.`);
    assert.equal(result.stderr, '', `${label} must not report an error.`);
    if (args.includes('version') || args.includes('--version')) {
      assert.ok(result.stdout.startsWith(`${project} `), label);
      assert.match(result.stdout.slice(project.length + 1), /^\S+\n$/, label);
      versionOutput ??= result.stdout;
      assert.equal(result.stdout, versionOutput, label);
    } else {
      assert.match(result.stdout, /Usage/, label);
    }
    assert.deepEqual(
      await readdir(dir),
      [],
      `${label} must not initialize a data directory or database.`,
    );
  }

  for (const args of [
    ['unknown-command'],
    ['--unknown-flag'],
    ['serve', '--unknown-flag'],
    ['serve', '--addr'],
    ['serve', '--data-dir'],
    ['serve', '--addr='],
    ['serve', '--data-dir='],
    ['serve', '--allowed-hosts='],
    ['serve', '--trusted-origins='],
    ['serve', '--addr', '127.0.0.1:65536'],
    ['serve', 'unexpected-argument'],
    ['version', 'unexpected-argument'],
    ['--addr', addr],
  ]) {
    // Valid environment settings make an accidentally started server hang,
    // rather than disguising an ignored CLI error as a startup failure.
    const result = await invoke(args, {
      ADDR: addr,
      PORT: String(port),
      DATA_DIR: ignoredDataDir,
    });
    const label = `${project} ${args.join(' ')}`;
    assert.equal(result.timedOut, false, `${label} must reject invalid input.`);
    assert.equal(result.signal, null, `${label} must exit normally.`);
    assert.notEqual(result.code, 0, `${label} must exit unsuccessfully.`);
    assert.ok(result.stderr.trim(), `${label} must explain the error.`);
    assert.equal(result.stdout, '', `${label} must not print success output.`);
    assert.deepEqual(
      await readdir(dir),
      [],
      `${label} must reject invalid input before initializing storage.`,
    );
  }

  const child = start();
  await waitFor(`${base}/api/health`, child);
  const html = await (await fetch(base)).text();
  assert.match(html, /<div id="root">/);
  const asset = html.match(/src="([^"]+\.js)"/)?.[1];
  assert.ok(asset, 'Production HTML must reference a bundled script.');
  assert.equal((await fetch(`${base}${asset}`)).status, 200);
  assert.equal((await fetch(`${base}/desktop/documents`)).status, 200);
  assert.equal(
    await statusWithHost(`${base}/api/rooms`, 'untrusted.invalid'),
    403,
  );
  let cookie = '';

  async function api(path, options = {}) {
    const { body, headers, ...rest } = options;
    return fetch(`${base}${path}`, {
      ...rest,
      headers: {
        Origin: base,
        Cookie: cookie,
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...headers,
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  }

  for (const path of [
    '/api/bootstrap',
    '/api/rooms',
    '/api/library',
    '/api/workspace/events',
    '/api/resources/missing/file?path=secret',
    '/api/resources/missing/patch?from=private&to=current',
  ]) {
    assert.equal((await api(path)).status, 401, `Unauthenticated ${path}`);
  }
  const initial = await (await api('/api/auth/status')).json();
  assert.equal(initial.setupRequired, true);
  assert.equal(initial.user, null);
  const setupPath = join(dataDir, 'team-setup.token');
  assert.equal((await stat(setupPath)).mode & 0o777, 0o600);
  const token = (await readFile(setupPath, 'utf8')).trim();
  const setup = await api('/api/auth/setup', {
    method: 'POST',
    body: {
      token,
      username: 'owner',
      name: 'Smoke Owner',
      password: 'A private smoke-test password',
    },
  });
  assert.equal(setup.status, 200);
  const session = setup.headers.getSetCookie()[0];
  assert.match(session, /HttpOnly/);
  assert.match(session, /SameSite=Strict/);
  cookie = session.split(';')[0];
  const { user } = await setup.json();
  assert.equal(user.role, 'admin');
  assert.equal(
    (
      await api('/api/auth/setup', {
        method: 'POST',
        body: {
          token,
          username: 'second',
          name: 'Second',
          password: 'Another smoke-test password',
        },
      })
    ).status,
    409,
  );
  assert.equal(
    (
      await api('/api/preferences', {
        method: 'PUT',
        headers: { Origin: 'http://localhost:49999' },
        body: { wallpaper: 'solid' },
      })
    ).status,
    403,
  );
  assert.equal(
    (
      await api('/api/preferences', {
        method: 'PUT',
        headers: { Origin: '' },
        body: { wallpaper: 'solid' },
      })
    ).status,
    403,
  );
  for (const path of [
    '/api/terminal',
    '/api/terminal/info',
    '/api/runs/missing/preview',
    '/api/entries',
    '/api/trash',
  ]) {
    assert.equal((await api(path)).status, 404, `Removed endpoint ${path}`);
  }
  const { rooms } = await (await api('/api/rooms')).json();
  assert.equal(rooms[0].name, 'general');
  const room = rooms[0];
  const message = await api(`/api/rooms/${room.id}/messages`, {
    method: 'POST',
    body: {
      requestId: 'smoke-discussion',
      content: 'A discussion without model execution.',
    },
  });
  assert.equal(message.status, 200);
  const turn = await message.json();
  assert.equal(turn.message.authorUserId, user.id);
  assert.equal(turn.runs.length, 0);
  const { resources } = await (await api('/api/resources')).json();
  const resource = resources.find((item) => item.id === room.resourceId);
  assert.ok(resource);
  assert.equal(Object.hasOwn(resource, 'sourcePath'), false);
  const edit = {
    requestId: 'smoke-save',
    expectedCommit: resource.currentCommit,
    path: 'notes/welcome.md',
    content: 'Persisted through a restart.\n',
  };
  const savedResponse = await api(`/api/resources/${resource.id}/files`, {
    method: 'PUT',
    body: edit,
  });
  assert.equal(savedResponse.status, 200);
  const saved = await savedResponse.json();
  assert.equal(saved.publication.authorUserId, user.id);
  assert.equal(saved.publication.checksStatus, 'not_run');
  const repeated = await api(`/api/resources/${resource.id}/files`, {
    method: 'PUT',
    body: edit,
  });
  assert.equal(repeated.status, 200);
  assert.equal((await repeated.json()).publication.id, saved.publication.id);
  const patchPath = `/api/resources/${resource.id}/patch?from=${resource.currentCommit}&to=${saved.resource.currentCommit}`;
  const patch = await api(patchPath);
  assert.equal(patch.status, 200);
  assert.equal(
    patch.headers.get('Content-Type'),
    'text/x-patch; charset=utf-8',
  );
  assert.match(patch.headers.get('Content-Disposition'), /attachment/);
  assert.match(await patch.text(), /\+Persisted through a restart\./);
  assert.equal((await api(`/api/resources/${resource.id}/patch`)).status, 400);
  assert.equal(
    (await api(`${patchPath}&from=${resource.currentCommit}`)).status,
    400,
  );
  assert.equal(
    (
      await api(`/api/resources/${resource.id}/files`, {
        method: 'PUT',
        body: null,
      })
    ).status,
    400,
  );
  const draft = await api('/api/me/drafts/document', {
    method: 'PUT',
    body: {
      key: `${resource.id}:notes/welcome.md`,
      revision: 0,
      value: {
        resourceId: resource.id,
        path: 'notes/welcome.md',
        content: 'Private unsaved draft',
      },
    },
  });
  assert.equal(draft.status, 200);
  await stop(child);
  const restarted = start(
    ['serve', '--addr', addr, '--data-dir', dataDir],
    invalidEnvironment,
  );
  await waitFor(`${base}/api/health`, restarted);
  assert.equal((await (await api('/api/auth/status')).json()).user.id, user.id);
  const download = await api(
    `/api/resources/${resource.id}/file?path=notes%2Fwelcome.md&commit=${saved.resource.currentCommit}`,
  );
  assert.equal(download.status, 200);
  assert.equal(await download.text(), edit.content);
  const history = await (
    await api(`/api/resources/${resource.id}/history`)
  ).json();
  assert.equal(history.publications.length, 1);
  const persistedDraft = await (
    await api(
      `/api/me/drafts/document?key=${encodeURIComponent(`${resource.id}:notes/welcome.md`)}`,
    )
  ).json();
  assert.equal(persistedDraft.draft.value.content, 'Private unsaved draft');
  assert.equal(
    (await api('/api/auth/logout', { method: 'POST', body: {} })).status,
    200,
  );
  assert.equal((await api('/api/library')).status, 401);
  assert.equal((await api(patchPath)).status, 401);
  assert.deepEqual(
    await readdir(dir),
    ['data'],
    'Explicit serve flags must override ADDR, PORT, and DATA_DIR.',
  );
  console.log(
    'Production smoke passed: CLI, independent working directory, embedded UI, protected setup, authentication, Origin/Host boundaries, attributed discussion, Library save/retry/download/history/patch, private drafts, restart persistence, logout and removed host/VFS endpoints.',
  );
} finally {
  await dispose(dir, children);
}
