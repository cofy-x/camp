import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { DatabaseSync } from 'node:sqlite';
import { runnerReplyProxy } from './upgrade-proxy.mjs';
import {
  assertDurableJournalEqual,
  assertJournalCopy,
  stoppedJournalEvidence,
} from './journal-evidence.mjs';
import { createHash, randomUUID } from 'node:crypto';
import {
  chmod,
  lstat,
  copyFile,
  cp,
  readFile,
  mkdtemp,
  mkdir,
  readdir,
  realpath,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { command, root, stop, waitFor } from './lib.mjs';
import {
  baselineLauncher,
  httpProxyEnvironment,
  upgradeBaseline,
  previousRelease,
} from './upgrade.mjs';
import { hashFile, validVersion } from './release.mjs';
import {
  completeOfflineSetupReport,
  offlineSetupChecks,
  offlineSetupOnly,
  retainSetupAttempt,
} from './setup-progress.mjs';
import { captureTree, scanTree } from '../tools/layerfs-worker/tree.mjs';

const mode = process.argv[2];
const setupOnly = offlineSetupOnly(mode, process.env.CAMP_SMOKE_SETUP_ONLY);
if (!mode) {
  for (const variant of ['online', 'offline'])
    await command(process.execPath, [
      path.join(root, 'scripts/smoke-bundle.mjs'),
      variant,
    ]);
  process.exit(0);
}
assert.ok(
  [
    'online',
    'offline',
    'upgrade',
    'github',
    'github-offline',
    'github-upgrade',
    'previous-upgrade',
  ].includes(mode),
);
const publicDownload = mode.startsWith('github');
const offline = mode.endsWith('offline');
const previousUpgrade = mode === 'previous-upgrade';
const upgrading =
  previousUpgrade || mode === 'upgrade' || mode === 'github-upgrade';
const baseline = previousUpgrade
  ? process.env.CAMP_TEST_PREVIOUS_RELEASE || previousRelease
  : upgradeBaseline;
assert.ok(validVersion(baseline), 'Select an exact public upgrade baseline.');
const baselineBackend = process.env.CAMP_TEST_BASELINE_BACKEND;
assert.ok(
  !baselineBackend ||
    (previousUpgrade &&
      ['native-local', 'layerfs-worker'].includes(baselineBackend)),
);
const packageDirectory = path.resolve(
  process.env.CAMP_SMOKE_PACKAGE_DIRECTORY || path.join(root, 'dist'),
);
const serverBinary = path.resolve(
  process.env.CAMP_SMOKE_SERVER_BINARY || path.join(packageDirectory, 'camp'),
);
const execute = promisify(execFile);
const mac = process.platform === 'darwin' && process.arch === 'arm64';
assert.ok(
  !setupOnly || mac,
  'Offline setup-only measurements require an ARM64 Mac.',
);
const target = mac ? 'darwin-arm64' : 'linux-amd64';
const checkpointWorker =
  process.env.CAMP_TEST_WORKSPACE_BACKEND === 'layerfs-worker';
assert.ok(
  !checkpointWorker || mac,
  'The native checkpoint worker fixture requires an ARM64 Mac.',
);
if (upgrading && mac && !previousUpgrade)
  throw new Error('The public upgrade baseline has no Mac Runner.');
const reportFile = process.env.CAMP_SMOKE_REPORT
  ? path.resolve(process.env.CAMP_SMOKE_REPORT)
  : path.join(
      root,
      `dist/smoke-${checkpointWorker ? 'layerfs-worker-' : ''}${mode}.json`,
    );
if (setupOnly)
  await writeFile(
    reportFile,
    '{"schema":1,"status":"running","mode":"offline-setup-only"}\n',
    { flag: 'wx', mode: 0o600 },
  );
else await rm(reportFile, { force: true });
const release = JSON.parse(
  await readFile(path.join(packageDirectory, 'runner-release.json'), 'utf8'),
);
const artifact = offline
  ? release.runners[target].offline
  : release.runners[target].installer;

// A real native Run, with deterministic local provider responses and no key.
const dir = process.env.CAMP_SMOKE_FIXTURE_DIRECTORY
  ? path.resolve(process.env.CAMP_SMOKE_FIXTURE_DIRECTORY)
  : await mkdtemp(path.join(tmpdir(), 'camp-release-'));
if (process.env.CAMP_SMOKE_FIXTURE_DIRECTORY) await mkdir(dir, { mode: 0o700 });
const bundle = path.join(dir, "relocated Runner's installation");
const runnerBinary = process.env.CAMP_SMOKE_RUNNER_BINARY
  ? path.resolve(process.env.CAMP_SMOKE_RUNNER_BINARY)
  : path.join(bundle, 'camp');
assert.ok(
  !process.env.CAMP_SMOKE_RUNNER_BINARY || (!offline && !upgrading),
  'A provided Runner entrypoint requires an online scenario.',
);
const data = path.join(dir, 'data');
const cwd = path.join(dir, 'unrelated-working-directory');
let child;
let runner;
let macServiceInstalled = false;
let serviceCleanupError;
let cleanupFailure;
let calls = 0;
let previousConfig;
let previousImport;
let previousJournal;
let upgradedJournal;
let backupJournalEvidence;
let previousImportRequest;
let replyProxy;
let legacyOperation;
let previousCompletion;
let protocolTask;
let firstNativeRunId = '';
let compatibilityRequests = 0;
let passed = false;
let legacyInputProtocolGate = true;
let workerUpgrade;
let previousWorkerOperation;
let previousWorkerFiles;

const timings = {};
const setupAttempts = [];
const setupOnlyReport = setupOnly
  ? {
      schema: 1,
      status: 'failed',
      mode: 'offline-setup-only',
      scope:
        'Fresh offline private runtime/cache installation and repeat setup; excludes network acquisition, cold VM startup and Agent work.',
      version: release.version,
      sourceCommit: release.sourceCommit,
      archiveSHA256: artifact.sha256,
      runtimeSHA256: release.runners[target].runtime.sha256,
      runtimeManifestSHA256: release.runners[target].runtime.manifestSHA256,
      workspaceBackend: checkpointWorker ? 'layerfs-worker' : 'native-local',
      retainedFixtureDirectory: dir,
      timings,
      setupVerified: false,
      cleanupConfirmed: false,
      model: { paid: false, requests: 0 },
      untested: [
        'Agent execution and business acceptance',
        'public network download',
        'cold VM or physical boot',
        'cross-version upgrade',
        'background/login service',
      ],
    }
  : undefined;

async function retainedWorkerFiles(home, includeRecovery = false) {
  const files = {};
  let bytes = 0;

  async function record(file) {
    const info = await lstat(file);
    assert.ok(
      info.isFile(),
      'Retained checkpoint evidence must contain only owned files.',
    );
    bytes += info.size;
    assert.ok(Object.keys(files).length < 100_000 && bytes <= 16 * 2 ** 30);
    files[path.relative(home, file)] = {
      sha256: await hashFile(file),
      mode: info.mode & 0o777,
    };
  }

  async function walk(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(file);
      else await record(file);
    }
  }

  for (const name of includeRecovery
    ? ['checkpoints', 'epochs', 'recoveries']
    : ['checkpoints/captures', 'checkpoints/operations'])
    await walk(path.join(home, name));
  if (includeRecovery) await record(path.join(home, 'current.json'));
  return files;
}

async function until(label, read, accept) {
  const deadline = Date.now() + 180000;
  do {
    const value = await read();
    if (accept(value)) return value;
    await pause(250);
  } while (Date.now() < deadline);
  throw new Error(`${label} timed out`);
}

const provider = createServer(async (request, response) => {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const body = Buffer.concat(chunks).toString('utf8');
  const compatibility = body.includes('Camp previous-package compatibility');
  if (compatibility) {
    compatibilityRequests++;
    if (previousUpgrade && compatibilityRequests === 1)
      replyProxy.holdReplies();
  } else calls++;
  if ((!compatibility && calls > 2) || compatibilityRequests > 4) {
    response.writeHead(500).end('Unexpected extra model request');
    return;
  }

  function historyResultIn(value) {
    if (!value || typeof value !== 'object') return false;
    if (
      value.type === 'tool_result' &&
      value.tool_use_id === 'upgrade-history-read'
    )
      return true;
    return Object.values(value).some(historyResultIn);
  }

  const historyResult = compatibility && historyResultIn(JSON.parse(body));
  if (historyResult)
    assert.ok(body.includes('previous-package-original-input'));
  const content = compatibility
    ? body.includes('Read original history') && !historyResult
      ? {
          type: 'tool_use',
          id: 'upgrade-history-read',
          name: 'camp_read_task_inputs',
          input: { after: 0, limit: 1 },
        }
      : {
          type: 'tool_use',
          id: 'upgrade-finish',
          name: 'camp_finish',
          input: { summary: 'Previous package compatibility verified.' },
        }
    : calls === 1
      ? {
          type: 'tool_use',
          id: 'bundle-write',
          name: 'bash',
          input: {
            description: 'Verify relocated native execution',
            command: `test -z "$DEEPSEEK_API_KEY" || exit 91
printf 'export const greet = (name = "friend") => "Hello, " + name + "!";\\n' > greet.mjs
printf 'import {test} from "node:test"; import assert from "node:assert/strict"; import {greet} from "./greet.mjs"; test("default",()=>assert.equal(greet(),"Hello, friend!"));\\n' > greet.test.mjs
printf '\\000\\001\\377\\200' > asset.bin
echo stream-before-test
node --test`,
          },
        }
      : {
          type: 'text',
          text: 'The relocated bundle generated and tested greet.',
        };
  const events = [
    {
      type: 'message_start',
      message: {
        id: 'bundle-check',
        model: 'deepseek-v4-pro',
        usage: { input_tokens: 3, output_tokens: 0 },
      },
    },
    { type: 'content_block_start', index: 0, content_block: content },
    { type: 'content_block_stop', index: 0 },
    {
      type: 'message_delta',
      delta: {
        stop_reason: compatibility || calls === 1 ? 'tool_use' : 'end_turn',
      },
      usage: { output_tokens: 1 },
    },
    { type: 'message_stop' },
  ];
  response.writeHead(200, { 'Content-Type': 'text/event-stream' });
  for (const event of events)
    response.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  response.end();
});
acceptance: try {
  await command(
    mac ? 'shasum' : 'sha256sum',
    [
      ...(mac ? ['-a', '256'] : []),
      '--check',
      '--quiet',
      `${artifact.filename}.sha256`,
    ],
    { cwd: packageDirectory },
  );
  await mkdir(bundle);
  await command('tar', [
    '-xzf',
    path.join(packageDirectory, artifact.filename),
    '--strip-components=1',
    '-C',
    bundle,
  ]);
  await mkdir(cwd);
  await mkdir(data, { mode: 0o700 });
  const archiveName = release.runners[target].installer.filename;
  if (!publicDownload) {
    await mkdir(path.join(data, 'packages'), { mode: 0o700 });
    for (const name of [
      release.runners[target].installer,
      release.runners[target].runtime,
      release.runners[target].offline,
    ].flatMap((item) => [item.filename, `${item.filename}.sha256`]))
      await copyFile(
        path.join(packageDirectory, name),
        path.join(data, 'packages', name),
      );
    await copyFile(
      path.join(packageDirectory, 'runner-release.json'),
      path.join(data, 'packages/runner-release.json'),
    );
  }
  await new Promise((resolve) => provider.listen(0, '127.0.0.1', resolve));
  const socket = createServer();
  await new Promise((resolve) => socket.listen(0, '127.0.0.1', resolve));
  const port = socket.address().port;
  await new Promise((resolve) => socket.close(resolve));
  const origin = `http://127.0.0.1:${port}`;
  if (previousUpgrade) replyProxy = await runnerReplyProxy(port);
  child = spawn(
    serverBinary,
    ['serve', '--addr', `127.0.0.1:${port}`, '--data-dir', data],
    {
      cwd,
      stdio: ['ignore', 'ignore', 'pipe'],
      env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' },
    },
  );
  await waitFor(`${origin}/api/health`, child, 90000);
  let cookie = '';
  const api = async (url, method = 'GET', body) => {
    const response = await fetch(`${origin}${url}`, {
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
  };

  function recordedInputProtocol(id) {
    // The browser DTO intentionally omits internal admission feature versions.
    const database = new DatabaseSync(path.join(data, 'desktop.sqlite'), {
      readOnly: true,
    });
    try {
      return (
        database
          .prepare(
            "SELECT json_extract(capabilities,'$.taskInputsVersion') AS version FROM runners WHERE id=?",
          )
          .get(id)?.version ?? 0
      );
    } finally {
      database.close();
    }
  }

  await api('/api/auth/setup', 'POST', {
    token: (await readFile(path.join(data, 'team-setup.token'), 'utf8')).trim(),
    username: 'release-admin',
    name: 'Release Admin',
    password: 'Native bundle smoke password',
  });
  const installation = await api(`/api/runner-installation?platform=${target}`);
  const checksumText = await readFile(
    path.join(packageDirectory, `${archiveName}.sha256`),
    'utf8',
  );
  assert.equal(installation.available, true);
  assert.equal(installation.filename, archiveName);
  assert.equal(installation.bytes, release.runners[target].installer.bytes);
  assert.equal(
    installation.runtime.bytes,
    release.runners[target].runtime.bytes,
  );
  assert.equal(
    installation.runtime.unpackedBytes,
    release.runners[target].runtime.unpackedBytes,
  );
  assert.equal(
    installation.offline.bytes,
    release.runners[target].offline.bytes,
  );
  assert.equal(installation.sha256, checksumText.split(' ')[0]);
  assert.equal(installation.source, publicDownload ? 'github' : 'server');
  if (publicDownload) assert.equal(installation.version, release.version);
  const checksum = await fetch(new URL(installation.checksumUrl, origin), {
    headers: publicDownload ? {} : { Cookie: cookie },
  });
  assert.equal(checksum.status, 200);
  assert.equal((await checksum.text()).trim(), checksumText.trim());
  const anonymous = await fetch(new URL(installation.downloadUrl, origin), {
    headers: { Range: 'bytes=0-15' },
  });
  assert.equal(anonymous.status, publicDownload ? 206 : 401);
  await anonymous.body.cancel();
  const download = await fetch(new URL(installation.downloadUrl, origin), {
    headers: {
      ...(publicDownload ? {} : { Cookie: cookie }),
      Range: 'bytes=0-15',
    },
  });
  assert.equal(download.status, 206);
  const prefix = new Uint8Array(await download.arrayBuffer());
  assert.equal(prefix.length, 16);
  assert.deepEqual(prefix.slice(0, 2), Uint8Array.of(0x1f, 0x8b));
  await api('/api/settings/models', 'PATCH', {
    apiKey: 'keyless-bundle-fixture',
    baseUrl: `http://127.0.0.1:${provider.address().port}`,
    maxTokens: 1024,
    maxSteps: 4,
    timeoutSeconds: 60,
  });
  const { token } = await api('/api/runner-enrollments', 'POST', {
    name: 'Installed Camp Runner',
  });
  const runnerData = path.join(dir, "Runner's private data");
  const sourceDirectory = path.join(dir, 'original upgrade source');
  if (upgrading) {
    await mkdir(sourceDirectory);
    await writeFile(
      path.join(sourceDirectory, 'before-upgrade.txt'),
      'Preserve the original source.\n',
    );
  }
  const stagedRuntime = path.join(
    data,
    'packages',
    release.runners[target].runtime.filename,
  );
  if (offline && !publicDownload)
    await rename(stagedRuntime, `${stagedRuntime}.held`);
  const setupArgs = [
    'runner',
    'setup',
    '--server',
    replyProxy?.origin ?? origin,
    '--name',
    'Installed Camp Runner',
    '--data-dir',
    runnerData,
    '--code-file',
    '-',
    '--docker-socket',
    process.env.CAMP_TEST_DOCKER_SOCKET || '/var/run/docker.sock',
    ...(checkpointWorker
      ? [
          '--workspace-backend',
          'layerfs-worker',
          '--docker-binary',
          process.env.CAMP_TEST_DOCKER_BINARY || '/usr/local/bin/docker',
          ...(process.env.CAMP_TEST_WORKER_IMAGE
            ? [
                '--worker-image',
                path.resolve(process.env.CAMP_TEST_WORKER_IMAGE),
              ]
            : []),
        ]
      : []),
    ...(upgrading ? ['--source', `upgrade-notes=${sourceDirectory}`] : []),
  ];
  const runnerEnv = { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' };

  async function setupWith(executable, code, legacy = false, extra = []) {
    const started = performance.now();
    const source = legacy
      ? ['--runtime-source', 'github']
      : !publicDownload
        ? ['--runtime-source', 'server']
        : [];
    const baselineSelection =
      legacy && checkpointWorker && baselineBackend
        ? [
            '--workspace-backend',
            baselineBackend,
            '--worker-image',
            baselineBackend === 'layerfs-worker'
              ? process.env.CAMP_TEST_BASELINE_WORKER_IMAGE || ''
              : '',
          ]
        : [];
    const setup = spawn(
      executable,
      [...setupArgs, ...source, ...baselineSelection, ...extra],
      {
        cwd,
        stdio: ['pipe', 'pipe', 'pipe'],
        env: { ...runnerEnv, ...httpProxyEnvironment() },
      },
    );
    let output = '';
    for (const stream of [setup.stdout, setup.stderr])
      stream.on('data', (chunk) => {
        output += chunk.toString();
      });
    setup.stdin.end(code);
    const exit = await new Promise((resolve, reject) => {
      setup.once('error', reject);
      setup.once('close', resolve);
    });
    return { output, exit, elapsedMs: performance.now() - started };
  }

  async function workerCheckpoint(action, extra = []) {
    const { stdout } = await execute(
      runnerBinary,
      ['runner', 'checkpoint', action, '--data-dir', runnerData, ...extra],
      { cwd, env: runnerEnv, maxBuffer: 16 << 20 },
    );
    return JSON.parse(stdout);
  }

  if (upgrading) {
    // Optional local reruns reuse only already verified immutable runtime files,
    // never another installation's identity, credentials or durable journal.
    if (previousUpgrade && process.env.CAMP_TEST_UPGRADE_RUNTIME_CACHE) {
      await mkdir(runnerData, { mode: 0o700 });
      await cp(
        path.resolve(process.env.CAMP_TEST_UPGRADE_RUNTIME_CACHE),
        path.join(runnerData, 'runtime/cache'),
        {
          recursive: true,
          errorOnExist: true,
          force: false,
          verbatimSymlinks: true,
        },
      );
    }
    const previous = await baselineLauncher(dir, 'runner', baseline);
    runnerEnv.XDG_CACHE_HOME = previous.cache;
    const setup = await setupWith(previous.binary, token, true);
    setupAttempts.push(await retainSetupAttempt(dir, 'baseline', setup));
    assert.equal(setup.exit, 0, 'The fixed baseline Runner setup failed');
    previousConfig = JSON.parse(
      await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
    );
    runner = spawn(
      previous.binary,
      ['runner', 'run', '--data-dir', runnerData],
      { cwd, stdio: 'ignore', env: runnerEnv },
    );
    await until(
      'Baseline Runner connection',
      () => api('/api/runners'),
      (value) =>
        value.runners.some(
          (item) =>
            item.id === previousConfig.runnerId &&
            item.online &&
            item.capabilities.ready,
        ),
    );
    const { sources } = await until(
      'Baseline source availability',
      () => api(`/api/runners/${previousConfig.runnerId}/sources`),
      (value) =>
        value.sources.some(
          (item) => item.alias === 'upgrade-notes' && item.available,
        ),
    );
    const source = sources.find((item) => item.alias === 'upgrade-notes');
    previousImportRequest = {
      requestId: randomUUID(),
      runnerId: previousConfig.runnerId,
      sourceId: source.id,
      expectedSourceRevision: source.revision,
      roomName: 'Before Runner upgrade',
    };
    const imported = await api(
      '/api/source-imports',
      'POST',
      previousImportRequest,
    );
    const receipt = await until(
      'Baseline durable source import',
      () => api(`/api/source-imports/${imported.sourceImport.id}`),
      (value) =>
        ['succeeded', 'failed', 'interrupted'].includes(
          value.sourceImport.status,
        ),
    );
    assert.equal(
      receipt.sourceImport.status,
      'succeeded',
      'Baseline source import failed',
    );
    previousImport = receipt.sourceImport;
    if (previousUpgrade) {
      const oldRunner = (await api('/api/runners')).runners.find(
        (item) => item.id === previousConfig.runnerId,
      );
      legacyInputProtocolGate = recordedInputProtocol(oldRunner.id) !== 1;
      let compatRoom = (await api('/api/rooms')).rooms.find(
        (room) => room.id === previousImport.roomId,
      );
      assert.ok(compatRoom);
      const { agent: compatAgent } = await api(
        `/api/rooms/${compatRoom.id}/agents`,
        'POST',
        {
          name: 'Package compatibility writer',
          handle: 'package-compatibility-writer',
          access: 'write',
          enabled: true,
          preferredRunnerId: oldRunner.id,
          instructions:
            'Use only the requested Camp tool to verify package compatibility.',
        },
      );
      await api(
        `/api/runners/${oldRunner.id}/agents/${compatAgent.id}`,
        'PUT',
        {},
      );
      ({ room: compatRoom } = await api(
        `/api/rooms/${compatRoom.id}/runner`,
        'PUT',
        { runnerId: oldRunner.id, revision: compatRoom.runnerRevision },
      ));
      const legacy = await api(`/api/rooms/${compatRoom.id}/tasks`, 'POST', {
        requestId: randomUUID(),
        goal: 'Camp previous-package compatibility: finish legacy work.',
        participantAgentIds: [compatAgent.id],
        firstAgentId: compatAgent.id,
      });
      const legacyResult = await until(
        'Previous package legacy task',
        () => api(`/api/tasks/${legacy.task.id}`),
        (value) =>
          !['queued', 'running', 'publishing'].includes(value.task.status),
      );
      assert.equal(legacyResult.task.status, 'completed');
      assert.equal(legacyResult.runs.length, 1);
      assert.equal(legacyResult.runs[0].status, 'succeeded');
      firstNativeRunId = legacyResult.runs[0].id;
      legacyOperation = legacyResult.runs[0].attemptId;
      if (!legacyInputProtocolGate) {
        await stop(runner);
        runner = undefined;
        await until(
          'Stopped previous package Runner observed offline',
          () => api('/api/runners'),
          (value) =>
            value.runners.some(
              (item) => item.id === oldRunner.id && !item.online,
            ),
        );
      }
      ({ task: protocolTask } = await api(
        `/api/rooms/${compatRoom.id}/tasks?view=summary`,
        'POST',
        {
          inputProtocol: 1,
          requestId: randomUUID(),
          goal: 'Camp previous-package compatibility: Read original history and finish.',
          participantAgentIds: [compatAgent.id],
          firstAgentId: compatAgent.id,
        },
      ));
      await api(`/api/tasks/${protocolTask.id}/inputs?view=summary`, 'POST', {
        inputProtocol: 1,
        requestId: randomUUID(),
        content: 'previous-package-original-input',
      });
      if (legacyInputProtocolGate)
        await until(
          'Previous package scheduling heartbeat',
          () => api('/api/runners'),
          (value) =>
            value.runners.some(
              (item) =>
                item.id === oldRunner.id &&
                item.lastSeenAt !== oldRunner.lastSeenAt,
            ),
        );
      const queued = await api(`/api/tasks/${protocolTask.id}`);
      assert.equal(queued.task.status, 'queued');
      assert.equal(queued.task.turnsUsed, 0);
      assert.equal(queued.task.modelRequestsUsed, 0);
      assert.equal(queued.task.draftFence, protocolTask.draftFence);
      assert.equal(queued.task.activeAttemptId, '');
      assert.ok(
        queued.runs.every((run) => run.status === 'queued' && !run.attemptId),
      );
      assert.equal(compatibilityRequests, 1);
    }
    if (runner) await stop(runner);
    runner = undefined;
    if (previousUpgrade) {
      const journal = new DatabaseSync(
        path.join(runnerData, 'journal.sqlite'),
        { readOnly: true },
      );
      try {
        const operation = journal
          .prepare('SELECT state,result FROM commands WHERE operation_id=?')
          .get(legacyOperation);
        assert.equal(operation.state, 'uploading');
        previousCompletion = JSON.parse(
          Buffer.from(operation.result).toString('utf8'),
        ).completionId;
        assert.ok(previousCompletion);
      } finally {
        journal.close();
      }
      replyProxy.releaseReplies();
    }
    previousJournal = await stoppedJournalEvidence(runnerData);
    if (checkpointWorker && baselineBackend === 'layerfs-worker') {
      assert.ok(process.env.CAMP_TEST_BASELINE_WORKER_IMAGE);
      const backup = path.join(dir, 'stopped previous worker backup');
      await mkdir(backup, { mode: 0o700 });
      await command('/bin/cp', ['-cRp', `${runnerData}/.`, backup]);
      assertJournalCopy(previousJournal, await stoppedJournalEvidence(backup));
      assertJournalCopy(
        previousJournal,
        await stoppedJournalEvidence(runnerData),
      );
      const previousManifest = JSON.parse(
        await readFile(
          path.join(previousConfig.bundleDir, 'manifest.json'),
          'utf8',
        ),
      );
      const currentManifest = JSON.parse(
        (
          await execute(
            'tar',
            [
              '-xOf',
              path.join(
                packageDirectory,
                release.runners[target].runtime.filename,
              ),
              'camp-runtime-darwin-arm64/manifest.json',
            ],
            { maxBuffer: 32 << 20 },
          )
        ).stdout,
      );
      const { stdout } = await execute(
        previous.binary,
        ['runner', 'checkpoint', 'status', '--data-dir', runnerData],
        { cwd, env: runnerEnv, maxBuffer: 16 << 20 },
      );
      const status = JSON.parse(stdout);
      previousWorkerOperation = status.operations.find(
        (operation) =>
          operation.state === 'confirmed' && operation.selectedEpoch,
      );
      assert.ok(
        previousWorkerOperation,
        'The prior worker must retain a confirmed checkpoint.',
      );
      previousWorkerFiles = await retainedWorkerFiles(
        path.join(runnerData, 'layerfs-worker'),
      );
      workerUpgrade = {
        fromVersion: baseline,
        baselineBackend,
        baselineImage: previousManifest.checkpointWorker.image,
        currentImage: currentManifest.checkpointWorker.image,
        stoppedBackupPreserved: true,
        reconstructionRequired: false,
      };
    }
  }
  let setup = await setupWith(runnerBinary, token);
  setupAttempts.push(await retainSetupAttempt(dir, 'initial', setup));
  let { output: setupOutput, exit: setupCode } = setup;
  if (workerUpgrade && setupCode !== 0) {
    assert.notEqual(
      workerUpgrade.baselineImage,
      workerUpgrade.currentImage,
      'A setup failure without an image change is not evidence authorizing Store reconstruction.',
    );
    const current = JSON.parse(
      await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
    );
    assert.equal(
      path.basename(current.bundleDir),
      release.runners[target].runtime.sha256,
    );
    const rebuilding = performance.now();
    const rebuilt = await workerCheckpoint('reconstruct', [
      '--operation',
      previousWorkerOperation.operationId,
    ]);
    assert.equal(rebuilt.kind, 'new-store-reconstruction');
    assert.equal(rebuilt.result.digest, previousWorkerOperation.digest);
    assert.notEqual(rebuilt.result.storeId, previousWorkerOperation.storeId);
    assert.equal(rebuilt.modelRequests, 0);
    previousWorkerOperation = rebuilt.result;
    workerUpgrade.reconstructionRequired = true;
    workerUpgrade.reconstruction = {
      elapsedMs: performance.now() - rebuilding,
      sourceDigest: rebuilt.source.digest,
      resultDigest: rebuilt.result.digest,
      oldStore: rebuilt.source.storeId,
      newStore: rebuilt.result.storeId,
      oldEpoch: rebuilt.previousEpoch,
      newEpoch: rebuilt.selectedEpoch,
    };
    setup = await setupWith(runnerBinary, '');
    setupAttempts.push(
      await retainSetupAttempt(dir, 'post-reconstruction', setup),
    );
    ({ output: setupOutput, exit: setupCode } = setup);
  }
  if (workerUpgrade && setupCode === 0) {
    const observed = await workerCheckpoint('observe', [
      '--operation',
      previousWorkerOperation.operationId,
    ]);
    assert.equal(observed.state, 'confirmed');
    assert.equal(observed.digest, previousWorkerOperation.digest);
    const parent = path.join(dir, 'prior worker checkpoint restore');
    await mkdir(parent, { mode: 0o700 });
    const restored = await workerCheckpoint('restore', [
      '--operation',
      previousWorkerOperation.operationId,
      '--parent',
      parent,
    ]);
    assert.equal(restored.kind, 'verified-layerfs-restoration');
    assert.equal(
      (await scanTree(restored.root)).digest,
      previousWorkerOperation.digest,
    );
    assert.equal(restored.modelRequests, 0);
    const remaining = await retainedWorkerFiles(
      path.join(runnerData, 'layerfs-worker'),
    );
    for (const [file, binding] of Object.entries(previousWorkerFiles))
      assert.deepEqual(remaining[file], binding);
    workerUpgrade.retainedCaptureFiles =
      Object.keys(previousWorkerFiles).length;
    workerUpgrade.previousCapturesAndReceiptsPreserved = true;
    workerUpgrade.checkpointReadbackVerified = true;
  }
  timings.setupAttempts = setupAttempts;
  timings.setupMs = setupAttempts
    .filter(({ stage }) => stage !== 'baseline')
    .reduce((sum, attempt) => sum + attempt.elapsedMs, 0);
  timings.setupPhases = setupAttempts
    .filter(({ stage }) => stage !== 'baseline')
    .flatMap(({ stage, phases }) =>
      phases.map((phase) => ({ ...phase, attempt: stage })),
    );
  assert.equal(
    setupCode,
    0,
    'Runner setup failed; inspect this retained campaign’s private setup attempt logs.',
  );
  assert.ok(
    timings.setupPhases.some(
      ({ phase, status }) =>
        phase === 'Runner self-test' && status === 'complete',
    ),
    'Setup did not record the completed self-test phase',
  );
  if (publicDownload && !offline && !upgrading)
    assert.ok(
      setupOutput.includes(`Downloading GitHub release ${release.version}`),
      'Setup did not download the fixed public release',
    );
  const savedConfig = JSON.parse(
    await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
  );
  if (upgrading) {
    for (const field of [
      'runnerId',
      'storageId',
      'registrationId',
      'credential',
    ])
      assert.ok(
        savedConfig[field] === previousConfig[field],
        `Runner upgrade replaced ${field}`,
      );
    upgradedJournal = await stoppedJournalEvidence(runnerData);
    assertDurableJournalEqual(previousJournal, upgradedJournal);
    assert.equal(
      (await api('/api/runners')).runners.length,
      1,
      'Upgrade enrolled another identity',
    );
  }
  const runtime = path.join(
    runnerData,
    'runtime/cache',
    release.runners[target].runtime.sha256,
  );
  assert.equal(savedConfig.bundleDir, runtime);
  await command(
    mac ? 'shasum' : 'sha256sum',
    [...(mac ? ['-a', '256'] : []), '--check', '--quiet', 'SHA256SUMS'],
    {
      cwd: runtime,
    },
  );
  const prebuilds = path.join(
    runtime,
    'runtime/dsh-runtime/node_modules/.pnpm/node-pty@1.2.0-beta.15/node_modules/node-pty/prebuilds',
  );
  assert.deepEqual(await readdir(prebuilds), [
    mac ? 'darwin-arm64' : 'linux-x64',
  ]);
  await command(path.join(runtime, 'bin/node'), [
    '-e',
    'require(process.argv[1]);',
    path.dirname(prebuilds),
  ]);
  // Repeating setup must not redownload the runtime or enroll another identity.
  // Hide the Server artifact in both modes, and the included archive offline.
  if (!offline && !publicDownload)
    await rename(stagedRuntime, `${stagedRuntime}.held`);
  if (offline)
    await rename(
      path.join(bundle, release.runners[target].runtime.filename),
      path.join(bundle, `${release.runners[target].runtime.filename}.held`),
    );
  const repeatSetupStarted = performance.now();
  await command(
    runnerBinary,
    [
      'runner',
      'setup',
      '--server',
      replyProxy?.origin ?? origin,
      '--name',
      'Installed Camp Runner',
      '--data-dir',
      runnerData,
    ],
    { cwd, env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' } },
  );
  timings.repeatSetupMs = performance.now() - repeatSetupStarted;
  const repeatedConfig = JSON.parse(
    await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
  );
  for (const key of [
    'runnerId',
    'storageId',
    'registrationId',
    'credential',
    'workspaceBackend',
  ])
    assert.equal(
      repeatedConfig[key],
      savedConfig[key],
      `Repeat setup changed ${key}.`,
    );
  if (!publicDownload) await rename(`${stagedRuntime}.held`, stagedRuntime);
  const readiness = JSON.parse(
    await readFile(path.join(runnerData, 'readiness.json'), 'utf8'),
  );
  assert.equal(readiness.ready, true, JSON.stringify(readiness));
  assert.ok(
    readiness.checks.some(
      (check) => check.id === 'artifact' && check.status === 'passed',
    ),
  );
  assert.ok(
    readiness.checks.some(
      (check) => check.id === 'cleanup' && check.status === 'passed',
    ),
  );
  assert.equal(
    calls,
    0,
    'Installation or native self-test made a model request',
  );
  if (setupOnly) {
    assert.equal(compatibilityRequests, 0);
    assert.equal(
      await hashFile(path.join(runtime, 'manifest.json')),
      setupOnlyReport.runtimeManifestSHA256,
    );
    setupOnlyReport.entrypointBinarySHA256 = await hashFile(runnerBinary);
    setupOnlyReport.checks = offlineSetupChecks;
    setupOnlyReport.setupVerified = true;
    break acceptance;
  }
  const foregroundCommand = setupOutput
    .split('\n')
    .find((line) => line.startsWith('Run in foreground: '))
    ?.slice('Run in foreground: '.length);
  assert.ok(foregroundCommand, 'Setup did not provide a foreground command');
  runner = spawn(
    process.env.CAMP_SMOKE_RUNNER_BINARY ? runnerBinary : '/bin/sh',
    process.env.CAMP_SMOKE_RUNNER_BINARY
      ? ['runner', 'run', '--data-dir', runnerData]
      : ['-c', `exec ${foregroundCommand}`],
    {
      cwd,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' },
    },
  );
  let runnerOutput = '';
  for (const stream of [runner.stdout, runner.stderr])
    stream.on('data', (chunk) => {
      runnerOutput += chunk.toString();
    });
  let assigned;
  const connectedDeadline = Date.now() + 180000;
  do {
    const { runners } = await api('/api/runners');
    assigned = runners.find((item) => item.name === 'Installed Camp Runner');
    if (
      assigned?.online &&
      assigned.capabilities?.ready &&
      (!previousUpgrade || recordedInputProtocol(assigned.id) === 1)
    )
      break;
    if (runner.exitCode !== null)
      throw new Error(`Runner stopped: ${runnerOutput}`);
    await pause(250);
  } while (Date.now() < connectedDeadline);
  assert.equal(assigned?.online, true, runnerOutput);
  assert.equal(
    assigned?.capabilities?.ready,
    true,
    JSON.stringify(assigned?.capabilities),
  );
  assert.equal(
    assigned.capabilities.platform,
    `${process.platform}/${process.arch === 'x64' ? 'amd64' : process.arch}`,
  );
  assert.equal(
    assigned.capabilities.workspace,
    checkpointWorker
      ? 'native-layerfs-worker'
      : mac
        ? 'native-local'
        : 'layerfs',
  );
  assert.equal(
    assigned.capabilities.sandbox,
    mac ? 'seatbelt' : 'linux-container',
  );
  assert.equal(
    assigned.capabilities.checkpoint,
    checkpointWorker
      ? 'layerfs-disposable+native-capture'
      : mac
        ? 'local-capture'
        : 'layerfs',
  );
  if (checkpointWorker)
    assert.equal(assigned.capabilities.layerfsVersion, '1.1.0');
  else if (mac) assert.equal(assigned.capabilities.layerfsVersion, undefined);
  assert.equal(assigned.firstTaskRunId, firstNativeRunId);
  if (!previousUpgrade) assert.equal(assigned.firstTaskSucceededAt, '');
  assert.equal(assigned.modelConfigured, true);
  if (upgrading) {
    const retained = (await api(`/api/source-imports/${previousImport.id}`))
      .sourceImport;
    assert.equal(retained.status, 'succeeded');
    assert.equal(retained.roomId, previousImport.roomId);
    assert.equal(
      await readFile(path.join(sourceDirectory, 'before-upgrade.txt'), 'utf8'),
      'Preserve the original source.\n',
    );
  }
  if (previousUpgrade) {
    assert.equal(recordedInputProtocol(assigned.id), 1);
    const recovered = await api(
      '/api/source-imports',
      'POST',
      previousImportRequest,
    );
    assert.equal(recovered.sourceImport.id, previousImport.id);
    assert.equal(recovered.sourceImport.status, 'succeeded');
    const result = await until(
      'Upgraded package bounded-input task',
      () => api(`/api/tasks/${protocolTask.id}`),
      (value) =>
        !['queued', 'running', 'publishing'].includes(value.task.status),
    );
    assert.equal(result.task.status, 'completed');
    assert.equal(result.runs.length, 1);
    assert.equal(result.runs[0].taskInputProtocol, 1);
    assert.equal(result.task.processedInputRevision, 1);
    assert.equal(compatibilityRequests, 3);
    const journal = new DatabaseSync(path.join(runnerData, 'journal.sqlite'), {
      readOnly: true,
    });
    try {
      const operation = journal
        .prepare('SELECT state,receipt FROM commands WHERE operation_id=?')
        .get(legacyOperation);
      assert.equal(operation.state, 'terminal');
      const receipt = JSON.parse(
        Buffer.from(operation.receipt).toString('utf8'),
      );
      assert.equal(receipt.disposition, 'committed');
      assert.equal(receipt.runId, firstNativeRunId);
    } finally {
      journal.close();
    }
    const serverDatabase = new DatabaseSync(path.join(data, 'desktop.sqlite'), {
      readOnly: true,
    });
    try {
      assert.equal(
        serverDatabase
          .prepare('SELECT result_id FROM run_attempts WHERE id=?')
          .get(legacyOperation).result_id,
        previousCompletion,
      );
    } finally {
      serverDatabase.close();
    }
  }
  const { rooms } = await api('/api/rooms');
  let room =
    rooms.find((item) => item.id !== previousImport?.roomId) ?? rooms[0];
  await api(
    `/api/runners/${assigned.id}/agents/${room.defaultAgentId}`,
    'PUT',
    {},
  );
  ({ room } = await api(`/api/rooms/${room.id}/runner`, 'PUT', {
    runnerId: assigned.id,
    revision: room.runnerRevision,
  }));
  const discussion = await api(`/api/rooms/${room.id}/messages`, 'POST', {
    requestId: 'discussion',
    content: 'This is team discussion.',
  });
  assert.equal(discussion.runs.length, 0);
  await api(`/api/resources/${room.resourceId}`, 'PATCH', {
    expectedRevision: (await api(`/api/resources/${room.resourceId}`)).resource
      .revision,
    environment: {
      kind: 'command',
      checks: [{ name: 'test', arguments: ['node', '--test'] }],
    },
  });
  const receipt = await api(`/api/rooms/${room.id}/messages`, 'POST', {
    requestId: 'native-bundle',
    content: 'Implement and test greet.',
    recipientAgentIds: [room.defaultAgentId],
    runnerRevision: room.runnerRevision,
  });
  assert.equal(receipt.runs.length, 1);
  const deadline = Date.now() + 90000;
  let run;
  do {
    ({ run } = await api(`/api/runs/${receipt.runs[0].id}`));
    if (
      ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(run.status)
    )
      break;
    await pause(250);
  } while (Date.now() < deadline);
  assert.equal(run.status, 'succeeded', JSON.stringify(run));
  assert.equal(run.runnerId, assigned.id);
  assert.ok(run.attemptId);
  assert.equal(run.published, true);
  assert.equal(run.testExitCode, 0);
  assert.equal(calls, 2);
  const completedRunners = await api('/api/runners');
  const completedRunner = completedRunners.runners.find(
    (item) => item.id === assigned.id,
  );
  assert.equal(completedRunner.firstTaskRunId, firstNativeRunId || run.id);
  assert.ok(completedRunner.firstTaskSucceededAt);
  assert.equal(completedRunner.earlierTaskRunId, undefined);
  const response = await fetch(
    `${origin}/api/resources/${room.resourceId}/file?commit=${run.resultCommit}&path=asset.bin&download=1`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(response.status, 200);
  assert.deepEqual(
    new Uint8Array(await response.arrayBuffer()),
    Uint8Array.of(0, 1, 255, 128),
  );
  const history = await api(`/api/resources/${room.resourceId}/history`);
  assert.equal(history.publications.length, 1);
  assert.equal(history.publications[0].checksStatus, 'passed');
  let { review } = await api(`/api/rooms/${room.id}/reviews`, 'POST', {
    requestId: 'release-review',
    title: 'Release acceptance output',
    resourceId: room.resourceId,
    path: 'greet.mjs',
    commit: run.resultCommit,
    reviewerUserId: assigned.ownerUserId,
  });
  ({ review } = await api(`/api/reviews/${review.id}/actions`, 'POST', {
    requestId: 'release-approve',
    revision: review.revision,
    action: 'approve',
  }));
  ({ review } = await api(`/api/reviews/${review.id}/actions`, 'POST', {
    requestId: 'release-deliver',
    revision: review.revision,
    action: 'deliver',
  }));
  assert.equal(review.status, 'delivered');
  const checkpointCommandAt = async (dataRoot, action, extra = []) => {
    const child = spawn(
      runnerBinary,
      [
        'runner',
        'checkpoint',
        ...(Array.isArray(action) ? action : [action]),
        '--data-dir',
        dataRoot,
        ...extra,
      ],
      { cwd, env: runnerEnv, stdio: ['ignore', 'pipe', 'pipe'] },
    );
    let output = '',
      diagnostic = '';
    child.stdout.on('data', (chunk) => {
      output += chunk;
    });
    child.stderr.on('data', (chunk) => {
      diagnostic = `${diagnostic}${chunk}`.slice(-8192);
    });
    const exit = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', resolve);
    });
    return { exit, output, diagnostic };
  };
  const checkpointAt = async (dataRoot, action, extra = []) => {
    const result = await checkpointCommandAt(dataRoot, action, extra);
    assert.equal(result.exit, 0, result.diagnostic);
    return JSON.parse(result.output);
  };
  let liveStorage;
  if (mac) {
    liveStorage = await checkpointAt(runnerData, 'storage', ['--json']);
    assert.equal(liveStorage.quiescent, false);
    assert.equal(liveStorage.retention.previewAllowed, false);
    const observed = await until(
      'live storage heartbeat',
      () => api('/api/runners'),
      (value) =>
        value.runners.some(
          (item) =>
            item.id === savedConfig.runnerId && item.capabilities.storage,
        ),
    );
    const reported = observed.runners.find(
      (item) => item.id === savedConfig.runnerId,
    );
    assert.equal(reported.capabilities.ready, true);
    assert.equal(reported.capabilities.storage.schema, 1);
    assert.notEqual(reported.capabilities.storage.logicalBytes, null);
  }
  if (checkpointWorker) {
    const ended = new Promise((resolve) => runner.once('close', resolve));
    runner.kill('SIGKILL');
    await ended;
    timings.runnerRestartKind =
      'actual packaged Runner SIGKILL after confirmed result; uncertain coordinator/worker faults qualified separately';
  } else await stop(runner);
  runner = undefined;
  let checkpointStorage;
  if (mac) {
    const config = await readFile(path.join(runnerData, 'daemon.json'));
    const journal = await stoppedJournalEvidence(runnerData);
    checkpointStorage = await checkpointAt(runnerData, 'storage', ['--json']);
    assert.equal(checkpointStorage.complete, true);
    assert.equal(checkpointStorage.retention.deletionSupported, false);
    assert.equal(checkpointStorage.workerStore.observed, false);
    if (checkpointWorker) assert(checkpointStorage.retention.protected > 0);
    // Fresh unreferenced physical capture: no worker or enrolled user's data.
    const cleanupSource = path.join(dir, 'cleanup-source');
    await mkdir(cleanupSource, { mode: 0o700 });
    await writeFile(
      path.join(cleanupSource, 'note.txt'),
      'synthetic retained cleanup bytes',
    );
    const orphan = await captureTree(
      cleanupSource,
      path.join(runnerData, 'layerfs-worker/checkpoints/captures'),
    );
    await chmod(path.join(runnerData, 'layerfs-worker'), 0o700);
    const manifest = await readFile(path.join(orphan.root, 'manifest.json'));
    const receipt = await readFile(path.join(orphan.root, 'receipt.json'));
    const policy = await checkpointAt(runnerData, 'budget', [
      '--retained-budget',
      '1B',
      '--min-free',
      '0',
      '--json',
    ]);
    assert.equal(policy.retainedBudgetBytes, 1);
    const beforeCleanup = await checkpointAt(runnerData, 'storage', ['--json']);
    assert.equal(beforeCleanup.budget.observation.level, 'warning');
    const cleanup = (verb, flags) =>
      checkpointAt(runnerData, ['cleanup', verb], [...flags, '--json']);
    if (workerUpgrade?.reconstructionRequired) {
      // Reconstruction lineage remains retained even after a successful new
      // epoch selection. Its cleanup guard must not be bypassed for acceptance.
      assert.equal(beforeCleanup.complete, true);
      assert.equal(checkpointStorage.retention.previewAllowed, false);
      assert.equal(beforeCleanup.retention.previewAllowed, false);
      assert.ok(beforeCleanup.recoveryIntents > 0);
      assert.equal(
        beforeCleanup.selectedEpoch,
        workerUpgrade.reconstruction.newEpoch,
      );
      assert.equal(
        beforeCleanup.captures.find(
          (item) => item.path === path.relative(runnerData, orphan.root),
        )?.disposition,
        'blocked',
      );
      const workerHome = path.join(runnerData, 'layerfs-worker');
      const retained = await retainedWorkerFiles(workerHome, true);
      const guardedJournal = await stoppedJournalEvidence(runnerData);
      const modelCounts = { calls, compatibilityRequests };
      const blocked = await checkpointCommandAt(
        runnerData,
        ['cleanup', 'plan'],
        ['--json'],
      );
      assert.equal(blocked.exit, 1, blocked.diagnostic);
      assert.match(
        blocked.diagnostic,
        /^error: Storage administration RecoveryRequired: Incomplete metadata or reconstruction history blocks cleanup\.\s*$/,
      );
      assert.equal(blocked.output.trim(), '');
      const after = await checkpointAt(runnerData, 'storage', ['--json']);
      assert.equal(after.complete, true);
      assert.equal(after.retention.previewAllowed, false);
      assert.equal(after.recoveryIntents, beforeCleanup.recoveryIntents);
      assert.equal(after.selectedEpoch, beforeCleanup.selectedEpoch);
      assert.equal(
        after.retention.protected,
        checkpointStorage.retention.protected,
      );
      assert.deepEqual(await retainedWorkerFiles(workerHome, true), retained);
      assert.deepEqual(
        await readFile(path.join(runnerData, 'daemon.json')),
        config,
      );
      assertDurableJournalEqual(
        guardedJournal,
        await stoppedJournalEvidence(runnerData),
        // The refused plan still opens the journal through read-only inspection.
        { allowEmptyWALLifecycle: true },
      );
      assert.deepEqual({ calls, compatibilityRequests }, modelCounts);
      assert.deepEqual(
        await readFile(path.join(orphan.root, 'manifest.json')),
        manifest,
      );
      assert.deepEqual(
        await readFile(path.join(orphan.root, 'receipt.json')),
        receipt,
      );
      checkpointStorage.lifecycle = {
        liveOverview: liveStorage.quiescent === false,
        policyConfigured: true,
        advisoryWarning: true,
        recoveryGuardPreserved: true,
        cleanupPlanBlocked: true,
        cleanupPlanError: 'RecoveryRequired',
        protectedHistoryPreserved: true,
      };
    } else {
      const q = await cleanup('plan', []);
      assert.equal(q.action, 'quarantine');
      assert.equal(
        (await cleanup('apply', ['--plan', q.planId])).state,
        'complete',
      );
      assert.equal(
        (await cleanup('status', ['--plan', q.planId])).state,
        'complete',
      );
      const quarantined = await checkpointAt(runnerData, 'storage', ['--json']);
      assert.equal(
        quarantined.totals.logicalBytes,
        beforeCleanup.totals.logicalBytes,
      );
      const restore = await cleanup('plan', [
        '--action',
        'restore',
        '--batch',
        q.batchId,
      ]);
      assert.equal(
        (await cleanup('apply', ['--plan', restore.planId])).state,
        'complete',
      );
      assert.deepEqual(
        await readFile(path.join(orphan.root, 'manifest.json')),
        manifest,
      );
      assert.deepEqual(
        await readFile(path.join(orphan.root, 'receipt.json')),
        receipt,
      );
      const again = await cleanup('plan', []);
      await cleanup('apply', ['--plan', again.planId]);
      const purge = await cleanup('plan', [
        '--action',
        'purge',
        '--batch',
        again.batchId,
      ]);
      assert.equal(
        (
          await cleanup('apply', [
            '--plan',
            purge.planId,
            '--confirm-permanent',
          ])
        ).state,
        'complete',
      );
      const purged = await checkpointAt(runnerData, 'storage', ['--json']);
      assert.equal(purged.quarantined.logicalBytes, 0);
      assert.equal(
        purged.retention.protected,
        checkpointStorage.retention.protected,
      );
      checkpointStorage.lifecycle = {
        liveOverview: liveStorage.quiescent === false,
        policyConfigured: true,
        advisoryWarning: true,
        quarantinePreservesBytes: true,
        restoredExactly: true,
        permanentlyPurged: true,
        protectedHistoryPreserved: true,
      };
    }
    await checkpointAt(runnerData, 'budget', [
      '--retained-budget',
      '10GiB',
      '--min-free',
      '2GiB',
      '--json',
    ]);
    assert.deepEqual(
      await readFile(path.join(runnerData, 'daemon.json')),
      config,
    );
    assertDurableJournalEqual(
      journal,
      await stoppedJournalEvidence(runnerData),
      // Read-only cleanup inspection may materialize an empty SQLite WAL.
      { allowEmptyWALLifecycle: true },
    );
    assert.equal(
      calls,
      2,
      'Offline storage inspection replayed a model request',
    );
  }
  if (checkpointWorker) {
    const journal = await stoppedJournalEvidence(runnerData);
    for (const backend of ['native-local', 'layerfs-worker']) {
      const switched = await setupWith(runnerBinary, '', false, [
        '--workspace-backend',
        backend,
        ...(backend === 'native-local' ? ['--worker-image', ''] : []),
      ]);
      assert.equal(switched.exit, 0, switched.output);
      const current = JSON.parse(
        await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
      );
      for (const field of [
        'runnerId',
        'storageId',
        'registrationId',
        'credential',
      ])
        assert.equal(current[field], savedConfig[field]);
      assertDurableJournalEqual(
        journal,
        await stoppedJournalEvidence(runnerData),
      );
    }
    assert.equal(
      calls,
      2,
      'Stopped backend selection replayed a model request',
    );
  }
  if (mac) {
    const service = async (action) =>
      command(
        runnerBinary,
        ['runner', 'service', action, '--data-dir', runnerData],
        { cwd, env: runnerEnv },
      );
    await service('install');
    macServiceInstalled = true;
    const serviceStarted = performance.now();
    await service('start');
    await until(
      'Background Runner reconnect',
      () => api('/api/runners'),
      ({ runners }) =>
        runners.some(
          (item) =>
            item.id === assigned.id && item.online && item.capabilities.ready,
        ),
    );
    timings.serviceReadyMs = performance.now() - serviceStarted;
    await service('status');
    await service('stop');
    await until(
      'Background Runner stopped',
      () => api('/api/runners'),
      ({ runners }) =>
        runners.some((item) => item.id === assigned.id && !item.online),
    );
    const serviceLabel = `dev.cofy.camp.runner.${createHash('sha256')
      .update(await realpath(runnerData))
      .digest('hex')
      .slice(0, 16)}`;
    // The control connection can close before launchd finishes deregistration.
    // Observe the owned job disappearing before asking the CLI to remove it.
    await until(
      'Stopped launchd Runner deregistered',
      () =>
        Promise.all(
          ['gui', 'user'].map(
            (domain) =>
              new Promise((resolve, reject) => {
                const probe = spawn(
                  '/bin/launchctl',
                  ['print', `${domain}/${process.getuid()}/${serviceLabel}`],
                  { env: runnerEnv, stdio: 'ignore' },
                );
                probe.once('error', reject);
                probe.once('close', (code) => resolve(code === 0));
              }),
          ),
        ),
      (loaded) => loaded.every((value) => !value),
    );
    await service('remove');
    macServiceInstalled = false;
    const retained = JSON.parse(
      await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
    );
    for (const field of [
      'runnerId',
      'storageId',
      'registrationId',
      'credential',
    ])
      assert.ok(
        retained[field] === savedConfig[field],
        `Service lifecycle replaced ${field}`,
      );
    assert.ok(
      (await readFile(path.join(runnerData, 'journal.sqlite'))).length > 0,
    );
    assert.equal(calls, 2, 'Service restart replayed a model request');
  }
  if (checkpointWorker) {
    const restoredRunner = path.join(dir, "Relocated Runner's stopped backup");
    const backing = performance.now();
    await cp(runnerData, restoredRunner, {
      recursive: true,
      verbatimSymlinks: true,
    });
    assert.deepEqual(
      await readFile(path.join(restoredRunner, 'daemon.json')),
      await readFile(path.join(runnerData, 'daemon.json')),
    );
    backupJournalEvidence = await stoppedJournalEvidence(runnerData);
    assertJournalCopy(
      backupJournalEvidence,
      await stoppedJournalEvidence(restoredRunner),
    );
    const checkpoint = (action, extra = []) =>
      checkpointAt(restoredRunner, action, extra);
    const retained = await checkpoint('status');
    assert.equal(
      retained.workerObserved,
      false,
      'Metadata status fabricated live readiness',
    );
    let resultOperation;
    const restoredRoot = await realpath(restoredRunner);
    for (const operation of retained.operations) {
      assert.ok(operation.captureRoot.startsWith(`${restoredRoot}/`));
      const manifest = JSON.parse(
        await readFile(
          path.join(operation.captureRoot, 'manifest.json'),
          'utf8',
        ),
      );
      if (manifest.entries.some((entry) => entry.path === 'greet.mjs'))
        resultOperation = operation.operationId;
    }
    assert.ok(
      resultOperation,
      'Stopped backup lost the complete native result capture',
    );
    const destination = path.join(restoredRunner, 'explicit-restores');
    await mkdir(destination, { mode: 0o700 });
    for (const fromNative of [true, false]) {
      const restored = await checkpoint('restore', [
        '--operation',
        resultOperation,
        '--parent',
        destination,
        ...(fromNative ? ['--from-native'] : []),
      ]);
      assert.deepEqual(
        await readFile(path.join(restored.root, 'asset.bin')),
        Buffer.from([0, 1, 255, 128]),
      );
      assert.equal(restored.published, false);
      if (fromNative) assert.equal(restored.currentWorkerVerified, false);
    }
    timings.runnerBackupAndCompleteRestoreMs = performance.now() - backing;
    runner = spawn(
      runnerBinary,
      ['runner', 'run', '--data-dir', restoredRunner],
      { cwd, env: runnerEnv, stdio: ['ignore', 'ignore', 'pipe'] },
    );
    runner.stderr.resume();
    await until(
      'Runner reconnect from stopped relocated backup',
      () => api('/api/runners'),
      ({ runners }) =>
        runners.some(
          (item) =>
            item.id === assigned.id && item.online && item.capabilities.ready,
        ),
    );
    await stop(runner);
    runner = undefined;
    const copiedConfig = JSON.parse(
      await readFile(path.join(restoredRunner, 'daemon.json'), 'utf8'),
    );
    for (const field of [
      'runnerId',
      'storageId',
      'registrationId',
      'credential',
    ])
      assert.equal(copiedConfig[field], savedConfig[field]);
    assert.equal(
      calls,
      2,
      'Restoring the stopped Runner backup replayed a model request',
    );
  }
  await stop(child);
  child = undefined;
  // Distribution caches are rebuildable. Back up all remaining business state,
  // then restart the exact release from that stopped, private copy.
  await rm(path.join(data, 'packages'), { recursive: true, force: true });
  const restored = path.join(dir, 'restored-data');
  await cp(data, restored, { recursive: true, verbatimSymlinks: true });
  child = spawn(
    serverBinary,
    ['serve', '--addr', `127.0.0.1:${port}`, '--data-dir', restored],
    {
      cwd,
      stdio: ['ignore', 'ignore', 'pipe'],
      env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' },
    },
  );
  await waitFor(`${origin}/api/health`, child, 90000);
  const restoredRun = (await api(`/api/runs/${run.id}`)).run;
  assert.equal(restoredRun.resultCommit, run.resultCommit);
  assert.equal(restoredRun.status, 'succeeded');
  const restoredHistory = await api(
    `/api/resources/${room.resourceId}/history`,
  );
  assert.deepEqual(restoredHistory, history);
  const restoredReview = (await api(`/api/reviews/${review.id}`)).review;
  assert.equal(restoredReview.status, 'delivered');
  assert.equal(restoredReview.deliveredCommit, run.resultCommit);
  assert.equal(restoredReview.submittedCommit, run.resultCommit);
  const restoredFile = await fetch(
    `${origin}/api/resources/${room.resourceId}/file?commit=${restoredReview.deliveredCommit}&path=asset.bin&download=1`,
    { headers: { Cookie: cookie } },
  );
  assert.equal(restoredFile.status, 200);
  assert.deepEqual(
    new Uint8Array(await restoredFile.arrayBuffer()),
    Uint8Array.of(0, 1, 255, 128),
  );
  assert.equal(
    (await api('/api/runners')).runners.find((item) => item.id === assigned.id)
      .firstTaskRunId,
    firstNativeRunId || run.id,
  );
  await stop(child);
  child = undefined;
  const manifest = JSON.parse(
    await readFile(path.join(runtime, 'manifest.json'), 'utf8'),
  );
  const digest = createHash('sha256')
    .update(
      JSON.stringify(
        manifest.layerfs ?? {
          workspace: 'native-local',
          sandbox: 'seatbelt',
          platform: manifest.platform,
        },
      ),
    )
    .digest('hex')
    .slice(0, 12);
  console.log(
    `Runner ${mode} install smoke passed: separate Server, ${publicDownload ? 'anonymous GitHub' : 'authenticated mirror'} download, relocated verified runtime, repeat setup, native DSH task, checks, binary bytes, one publication, human approval/delivery, stopped backup/restore and cleanup (${digest}). No paid model endpoint used.`,
  );
  await writeFile(
    reportFile,
    `${JSON.stringify(
      {
        schema: 1,
        status: 'passed',
        mode,
        workspaceBackend: checkpointWorker
          ? 'layerfs-worker'
          : mac
            ? 'native-local'
            : 'layerfs',
        ...(checkpointWorker ? { retainedFixtureDirectory: dir } : {}),
        entrypoint: process.env.CAMP_SMOKE_RUNNER_BINARY
          ? 'provided-binary'
          : 'runner-installer',
        entrypointBinarySHA256: createHash('sha256')
          .update(await readFile(runnerBinary))
          .digest('hex'),
        version: release.version || 'dev',
        sourceCommit: release.sourceCommit || '',
        archiveSHA256: artifact.sha256,
        ...(upgrading
          ? {
              fromVersion: baseline,
              runnerIdentityPreserved: true,
              durableSourceReceiptPreserved: true,
              journalPreserved: true,
              journalEvidence: {
                schema: 1,
                before: previousJournal.durableFiles,
                after: upgradedJournal.durableFiles,
                scope:
                  'Stopped main database and present WAL bytes; SHM is volatile coordination and excluded from durable equality.',
              },
              ...(previousUpgrade
                ? {
                    baselineRuntimeCacheProvided: Boolean(
                      process.env.CAMP_TEST_UPGRADE_RUNTIME_CACHE,
                    ),
                    legacyProtocolAccepted: true,
                    unsupportedInputProtocolQueued: legacyInputProtocolGate,
                    upgradedInputHistoryVerified: true,
                    sourceReceiptRetryPreserved: true,
                    lostCompletionReplyRecovered: true,
                  }
                : {}),
            }
          : {}),
        completedAt: new Date().toISOString(),
        platform: process.platform,
        architecture: process.arch,
        timings,
        ...(checkpointStorage ? { checkpointStorage } : {}),
        ...(workerUpgrade ? { workerUpgrade } : {}),
        ...(backupJournalEvidence ? { backupJournalEvidence } : {}),
        ...(previousUpgrade
          ? {
              baselineWorkspaceBackend:
                baselineBackend || (mac ? 'native-local' : 'layerfs'),
              baselineInputProtocol: legacyInputProtocolGate ? 0 : 1,
            }
          : {}),
        model: {
          kind: 'deterministic-local-provider',
          requests: calls + compatibilityRequests,
          paid: false,
        },
        checks: [
          'archive-checksum',
          'fixed-runtime-installation',
          'relocation',
          'repeat-setup-preserves-identity',
          'native-self-test-without-model',
          'native-task',
          'configured-checks',
          'binary-transfer',
          'single-publication',
          'human-approval-and-delivery',
          'stopped-backup-and-restore',
          'cleanup',
          ...(mac
            ? [
                'launchd-background-lifecycle',
                'offline-checkpoint-storage-without-model-replay',
                'live-advisory-storage-heartbeat',
                workerUpgrade?.reconstructionRequired
                  ? 'checkpoint-reconstruction-history-blocks-cleanup'
                  : 'explicit-checkpoint-budget-quarantine-restore-purge',
              ]
            : []),
          ...(checkpointWorker
            ? [
                'actual-runner-kill-and-restart',
                'stopped-backend-switch-preserves-identity-and-journal',
                'stopped-runner-backup-relocation',
                'complete-capture-native-and-worker-restore',
                'runner-backup-reconnect-without-model-replay',
              ]
            : []),
          ...(upgrading
            ? [
                'cross-version-runner-upgrade',
                'durable-source-receipt-preserved',
                'journal-preserved',
              ]
            : []),
        ],
        untested: [
          'paid-model-provider',
          upgrading
            ? 'upgrades-other-than-named-baseline'
            : 'cross-version-upgrade',
          mac ? 'login-autostart' : 'systemd-service-installation',
        ],
      },
      null,
      2,
    )}\n`,
  );
  passed = true;
} finally {
  const cleanupErrors = [];
  if (macServiceInstalled) {
    try {
      await command(
        runnerBinary,
        [
          'runner',
          'service',
          'remove',
          '--data-dir',
          path.join(dir, "Runner's private data"),
        ],
        { cwd, env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' } },
      );
    } catch (error) {
      serviceCleanupError = error;
      cleanupErrors.push({ owner: 'service', error });
    }
  }
  for (const [owner, action] of [
    [
      'runner',
      async () => {
        if (runner) await stop(runner);
      },
    ],
    [
      'reply-proxy',
      async () => {
        if (replyProxy) await replyProxy.close();
      },
    ],
    [
      'server',
      async () => {
        if (child) await stop(child);
      },
    ],
    [
      'provider',
      async () => {
        provider.closeAllConnections();
        if (provider.listening)
          await new Promise((resolve, reject) =>
            provider.close((error) => (error ? reject(error) : resolve())),
          );
      },
    ],
  ]) {
    try {
      await action();
    } catch (error) {
      cleanupErrors.push({ owner, error });
    }
  }
  if (setupOnly) {
    const completed = completeOfflineSetupReport(setupOnlyReport, {
      cleanupConfirmed: cleanupErrors.length === 0,
      modelRequests: calls + compatibilityRequests,
    });
    passed = completed.status === 'passed';
    if (cleanupErrors.length)
      completed.cleanupErrors = cleanupErrors.map(({ owner }) => ({
        owner,
        code: 'CleanupUnconfirmed',
      }));
    await writeFile(reportFile, `${JSON.stringify(completed, null, 2)}\n`, {
      mode: 0o600,
    });
    if (!passed) process.exitCode = 1;
  }
  if (cleanupErrors.length) {
    passed = false;
    if (!setupOnly) {
      const saved = await readFile(reportFile, 'utf8')
        .then(JSON.parse)
        .catch(() => null);
      if (saved)
        await writeFile(
          reportFile,
          `${JSON.stringify({ ...saved, status: 'failed', cleanupConfirmed: false }, null, 2)}\n`,
        );
    }
  }
  if (serviceCleanupError)
    console.error(
      `Service cleanup failed; retained installation and Runner data at ${dir}`,
    );
  else if (setupOnly || checkpointWorker || !passed)
    console.log(`Retained private installation fixture: ${dir}`);
  else await rm(dir, { recursive: true, force: true });
  if (cleanupErrors.length)
    cleanupFailure = new AggregateError(
      cleanupErrors.map(({ error }) => error),
      'Owned package fixture cleanup was not confirmed; original data retained.',
    );
}
if (cleanupFailure) throw cleanupFailure;
if (serviceCleanupError)
  throw new Error(
    'The Mac service could not be removed after package verification',
    { cause: serviceCleanupError },
  );
