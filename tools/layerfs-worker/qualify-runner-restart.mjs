// Supplement the installed-package fixture with an actual Go Runner death
// after checkpoint dispatch. Only a stopped synthetic package fixture is read.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFile, spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { createServer as createListener } from 'node:net';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  stat,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { promisify } from 'node:util';
import { pathToFileURL, URL } from 'node:url';
import { command, root, stop, waitFor } from '../../scripts/lib.mjs';
import { saveState } from './state.mjs';
import { manifestDigest } from './tree.mjs';

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
assert.equal(process.argv.length, 3, 'Supply a passed hybrid smoke report.');
const baseline = JSON.parse(await readFile(process.argv[2], 'utf8'));
assert.equal(baseline.status, 'passed');
assert.equal(baseline.workspaceBackend, 'layerfs-worker');
assert.ok(baseline.checks.includes('stopped-runner-backup-relocation'));
const original = baseline.retainedFixtureDirectory;
assert.ok(path.isAbsolute(original));
const runnerBinary = path.join(
  original,
  "relocated Runner's installation/camp",
);
assert.equal(
  createHash('sha256')
    .update(await readFile(runnerBinary))
    .digest('hex'),
  baseline.entrypointBinarySHA256,
);
await mkdir(path.join(root, '.data'), { recursive: true, mode: 0o700 });
const directory = await mkdtemp(
  path.join(root, '.data/packaged-worker-restart-'),
);
const runnerData = path.join(directory, 'runner');
const serverData = path.join(directory, 'server');
await mkdir(runnerData, { mode: 0o700 });
await mkdir(serverData, { mode: 0o700 });
// APFS clone preserves the stopped fixture without downloading/extracting a
// second package or writing to the source fixture. Symlinks remain symlinks.
for (const [source, target] of [
  [path.join(original, "Runner's private data"), runnerData],
  [path.join(original, 'data'), serverData],
])
  await command('/bin/cp', ['-cRp', `${source}/.`, target]);
const configFile = path.join(runnerData, 'daemon.json');
const before = JSON.parse(await readFile(configFile, 'utf8'));
assert.equal(before.name, 'Installed Camp Runner');
assert.equal(before.workspaceBackend, 'layerfs-worker');
const listener = createListener();
await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const origin = `http://127.0.0.1:${port}`;
const config = {
  ...before,
  serverUrl: origin,
  bundleDir: path.join(
    runnerData,
    'runtime/cache',
    path.basename(before.bundleDir),
  ),
};
await writeFile(configFile, `${JSON.stringify(config)}\n`, { mode: 0o600 });
const environment = { PATH: '/usr/bin:/bin', HOME: directory, LANG: 'C.UTF-8' };
const execute = promisify(execFile);
const journal = async () => {
  const database = path.join(runnerData, 'journal.sqlite');
  const wal = await stat(`${database}-wal`).catch((error) => {
    if (error.code === 'ENOENT') return null;
    throw error;
  });
  assert.ok(
    !wal?.size,
    'Read immutable journal rows only after a stopped checkpoint.',
  );
  const { stdout } = await execute(
    '/usr/bin/sqlite3',
    [
      '-readonly',
      '-json',
      `${pathToFileURL(database).href}?immutable=1`,
      'SELECT id, operation_id, digest, hex(body) AS frozen_body FROM commands ORDER BY id',
    ],
    { env: environment, maxBuffer: 8 << 20 },
  );
  return JSON.parse(stdout || '[]');
};
const report = {
  schema: 1,
  passed: false,
  scope:
    'actual packaged Go Runner SIGKILL after complete-checkpoint dispatch, then observation/restart without model replay',
  root: directory,
  modelRequests: 0,
  checks: [],
  entrypointBinarySHA256: baseline.entrypointBinarySHA256,
  acceptanceDriverSHA256: createHash('sha256')
    .update(await readFile(new URL(import.meta.url)))
    .digest('hex'),
};
let server,
  runner,
  calls = 0,
  cookie = '';
const provider = createServer(async (request, response) => {
  for await (const chunk of request) void chunk;
  calls++;
  const content =
    calls === 1
      ? {
          type: 'tool_use',
          id: 'inflight-native-write',
          name: 'bash',
          input: {
            description:
              'Write a controlled native result before checkpoint interruption',
            command: `node -e 'require("fs").writeFileSync("inflight.bin",Buffer.alloc(16<<20,44))'`,
          },
        }
      : {
          type: 'text',
          text: 'The controlled native bytes are ready for checks.',
        };
  if (calls > 2) {
    response.writeHead(500).end('Replay refused');
    return;
  }
  const events = [
    {
      type: 'message_start',
      message: {
        id: 'inflight-provider',
        model: 'deepseek-v4-pro',
        usage: { input_tokens: 1, output_tokens: 0 },
      },
    },
    { type: 'content_block_start', index: 0, content_block: content },
    { type: 'content_block_stop', index: 0 },
    {
      type: 'message_delta',
      delta: { stop_reason: calls === 1 ? 'tool_use' : 'end_turn' },
      usage: { output_tokens: 1 },
    },
    { type: 'message_stop' },
  ];
  response.writeHead(200, { 'Content-Type': 'text/event-stream' });
  for (const event of events)
    response.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  response.end();
});

async function api(url, method = 'GET', body) {
  const response = await globalThis.fetch(`${origin}${url}`, {
    method,
    headers: {
      Origin: origin,
      'Content-Type': 'application/json',
      Cookie: cookie,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (response.headers.has('set-cookie'))
    cookie = response.headers.get('set-cookie').split(';')[0];
  const value = await response.json();
  assert.ok(response.ok, `${method} ${url}: ${JSON.stringify(value)}`);
  return value;
}

async function until(label, action, accept, timeout = 90000) {
  const deadline = Date.now() + timeout;
  do {
    const value = await action();
    if (accept(value)) return value;
    await pause(100);
  } while (Date.now() < deadline);
  throw new Error(`${label} timed out`);
}

function startRunner() {
  const child = spawn(
    runnerBinary,
    ['runner', 'run', '--data-dir', runnerData],
    { cwd: directory, env: environment, stdio: ['ignore', 'ignore', 'pipe'] },
  );
  child.stderr.resume();
  return child;
}

try {
  const originalCommands = await journal();
  assert.ok(originalCommands.length > 0);
  await new Promise((resolve) => provider.listen(0, '127.0.0.1', resolve));
  server = spawn(
    path.join(root, 'dist/camp'),
    ['serve', '--addr', `127.0.0.1:${port}`, '--data-dir', serverData],
    { cwd: directory, env: environment, stdio: ['ignore', 'ignore', 'pipe'] },
  );
  server.stderr.resume();
  await waitFor(`${origin}/api/health`, server, 90000);
  await api('/api/auth/login', 'POST', {
    username: 'release-admin',
    password: 'Native bundle smoke password',
  });
  await api('/api/settings/models', 'PATCH', {
    apiKey: 'keyless-inflight-fixture',
    baseUrl: `http://127.0.0.1:${provider.address().port}`,
    maxTokens: 1024,
    maxSteps: 4,
    timeoutSeconds: 120,
  });
  runner = startRunner();
  await until(
    'Runner readiness',
    () => api('/api/runners'),
    (v) =>
      v.runners.some(
        (x) => x.id === config.runnerId && x.online && x.capabilities.ready,
      ),
  );
  let { room } = await api('/api/rooms', 'POST', {
    name: 'Controlled in-flight checkpoint restart',
    description: 'Synthetic restart acceptance; no delivery requested.',
  });
  await api(
    `/api/runners/${config.runnerId}/agents/${room.defaultAgentId}`,
    'PUT',
    {},
  );
  ({ room } = await api(`/api/rooms/${room.id}/runner`, 'PUT', {
    runnerId: config.runnerId,
    revision: room.runnerRevision,
  }));
  await api(`/api/resources/${room.resourceId}`, 'PATCH', {
    environment: {
      kind: 'command',
      checks: [
        {
          name: 'native-byte-check',
          arguments: [
            'node',
            '-e',
            'if(require("fs").statSync("inflight.bin").size!==(16<<20))process.exit(1)',
          ],
        },
      ],
    },
  });
  const operationDirectory = path.join(
    runnerData,
    'layerfs-worker/checkpoints/operations',
  );
  const previous = new Set(await readdir(operationDirectory));
  const { runs } = await api(`/api/rooms/${room.id}/messages`, 'POST', {
    requestId: randomUUID(),
    content: 'Write the controlled binary and check its length.',
    recipientAgentIds: [room.defaultAgentId],
    runnerRevision: room.runnerRevision,
  });
  assert.equal(runs.length, 1);
  let pending;
  const deadline = Date.now() + 90000;
  while (Date.now() < deadline && !pending) {
    for (const name of await readdir(operationDirectory)) {
      if (previous.has(name) || !name.endsWith('.json')) continue;
      const record = JSON.parse(
        await readFile(path.join(operationDirectory, name), 'utf8'),
      );
      if (
        record.state === 'commit-dispatched' &&
        record.binding.attemptId !== 'worker-selftest'
      ) {
        pending = record;
        break;
      }
    }
    if (!pending) await pause(5);
  }
  assert.ok(
    pending,
    'No unconfirmed checkpoint dispatch was observed; no kill was performed.',
  );
  const ended = new Promise((resolve) => runner.once('close', resolve));
  runner.kill('SIGKILL');
  await ended;
  runner = undefined;
  report.killedCheckpoint = {
    operationId: pending.binding.operationId,
    generation: pending.binding.generation,
    digest: pending.binding.digest,
    parentCheckpoint: pending.binding.parentCheckpoint,
    stateAtKill: pending.state,
  };
  assert.equal(calls, 2);
  const capture = path.join(
    runnerData,
    'layerfs-worker',
    pending.captureRelative,
  );
  const manifest = JSON.parse(
    await readFile(path.join(capture, 'manifest.json'), 'utf8'),
  );
  assert.equal(manifestDigest(manifest), pending.binding.digest);
  const entry = manifest.entries.find((x) => x.path === 'inflight.bin');
  assert.equal(entry?.bytes, 16 << 20);
  assert.equal(
    createHash('sha256')
      .update(await readFile(path.join(capture, 'blobs', entry.sha256)))
      .digest('hex'),
    createHash('sha256')
      .update(Buffer.alloc(16 << 20, 44))
      .digest('hex'),
  );
  report.checks.push(
    'The actual installed Go Runner was SIGKILLed while its result checkpoint was dispatched but not locally confirmed; the native manifest survived.',
  );
  // Allow the independently owned bridge to finish/drain. Its observation path
  // may confirm the checkpoint, but never authorizes the interrupted Run.
  const workerIdentity = JSON.parse(
    await readFile(
      path.join(runnerData, 'layerfs-worker/control/worker.json'),
      'utf8',
    ),
  );
  await until(
    'Owned worker drain',
    async () => {
      const child = spawn(
        config.dockerBinary,
        [
          '--host',
          `unix://${config.dockerSocket}`,
          'ps',
          '--filter',
          `label=cofy.camp.worker=${workerIdentity.id}`,
          '--format',
          '{{.ID}}',
        ],
        { env: environment, stdio: ['ignore', 'pipe', 'ignore'] },
      );
      let output = '';
      child.stdout.on('data', (b) => (output += b));
      const exit = await new Promise((resolve) => child.once('close', resolve));
      assert.equal(exit, 0, 'Docker worker observation failed.');
      return output.trim();
    },
    (x) => x === '',
  );
  runner = startRunner();
  const finished = await until(
    'Interrupted Run reconciliation',
    () => api(`/api/runs/${runs[0].id}`),
    (v) => ['interrupted', 'failed', 'cancelled'].includes(v.run.status),
  );
  assert.equal(finished.run.published, false);
  assert.notEqual(finished.run.status, 'succeeded');
  await until(
    'Reconnected Runner',
    () => api('/api/runners'),
    (v) => v.runners.some((x) => x.id === config.runnerId && x.online),
  );
  await stop(runner);
  runner = undefined;
  const observed = JSON.parse(
    await readFile(
      path.join(operationDirectory, `${pending.binding.operationId}.json`),
      'utf8',
    ),
  );
  assert.equal(observed.state, 'confirmed');
  assert.equal(observed.workerReceipt.verifiedDigest, pending.binding.digest);
  report.checkpointAfterObservation = {
    state: observed.state,
    verifiedDigest: observed.workerReceipt.verifiedDigest,
    readbackMs: observed.workerReceipt.readbackMs,
    durableDelivery: observed.durableDelivery,
  };
  const after = JSON.parse(await readFile(configFile, 'utf8'));
  for (const field of ['runnerId', 'storageId', 'registrationId', 'credential'])
    assert.equal(after[field], before[field]);
  const retainedCommands = new Map(
    (await journal()).map((row) => [row.id, row]),
  );
  for (const row of originalCommands)
    assert.deepEqual(retainedCommands.get(row.id), row);
  report.preexistingJournalCommandsPreserved = originalCommands.length;
  assert.equal(calls, 2, 'Restart replayed a model request.');
  assert.equal(
    (await api(`/api/resources/${room.resourceId}/history`)).publications
      .length,
    0,
  );
  report.runStatus = finished.run.status;
  report.published = false;
  report.nativeCaptureRetained = true;
  report.modelRequests = calls;
  report.checks.push(
    'Restart reconciled the interrupted result without publishing its checkpoint or replaying either model request; controlled identity and all retained data survived.',
  );
  report.passed = true;
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  if (runner) await stop(runner);
  if (server) await stop(server);
  provider.closeAllConnections();
  if (provider.listening)
    await new Promise((resolve) => provider.close(resolve));
  report.modelRequests = calls;
  await saveState(path.join(directory, 'report.json'), report);
  process.stdout.write(
    `Packaged in-flight Runner restart ${report.passed ? 'passed' : 'failed'}: ${path.join(directory, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
