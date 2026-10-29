import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFile,
  cp,
  readFile,
  mkdtemp,
  mkdir,
  readdir,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import { command, root, stop, waitFor } from './lib.mjs';

const mode = process.argv[2];
if (!mode) {
  for (const variant of ['online', 'offline'])
    await command(process.execPath, [
      path.join(root, 'scripts/smoke-bundle.mjs'),
      variant,
    ]);
  process.exit(0);
}
assert.ok(['online', 'offline', 'github', 'github-offline'].includes(mode));
const publicDownload = mode.startsWith('github');
const offline = mode.endsWith('offline');
const release = JSON.parse(
  await readFile(path.join(root, 'dist/runner-release.json'), 'utf8'),
);
const artifact = offline ? release.offline : release.installer;

// A real native Run, with deterministic local provider responses and no key.
const dir = await mkdtemp(path.join(tmpdir(), 'camp-release-'));
const bundle = path.join(dir, "relocated Runner's installation");
const data = path.join(dir, 'data');
const cwd = path.join(dir, 'unrelated-working-directory');
let child;
let runner;
let calls = 0;
const provider = createServer(async (request, response) => {
  for await (const chunk of request) void chunk;
  calls++;
  if (calls > 2) {
    response.writeHead(500).end('Unexpected extra model request');
    return;
  }
  const content =
    calls === 1
      ? {
          type: 'tool_use',
          id: 'bundle-write',
          name: 'bash',
          input: {
            description: 'Verify relocated LayerFS execution',
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
try {
  await command(
    'sha256sum',
    ['--check', '--quiet', `${artifact.filename}.sha256`],
    { cwd: path.join(root, 'dist') },
  );
  await mkdir(bundle);
  await command('tar', [
    '-xzf',
    path.join(root, 'dist', artifact.filename),
    '--strip-components=1',
    '-C',
    bundle,
  ]);
  await mkdir(cwd);
  await mkdir(data, { mode: 0o700 });
  const archiveName = release.installer.filename;
  if (!publicDownload) {
    await mkdir(path.join(data, 'packages'), { mode: 0o700 });
    for (const name of [
      release.installer,
      release.runtime,
      release.offline,
    ].flatMap((item) => [item.filename, `${item.filename}.sha256`]))
      await copyFile(
        path.join(root, 'dist', name),
        path.join(data, 'packages', name),
      );
    await copyFile(
      path.join(root, 'dist/runner-release.json'),
      path.join(data, 'packages/runner-release.json'),
    );
  }
  await new Promise((resolve) => provider.listen(0, '127.0.0.1', resolve));
  const socket = createServer();
  await new Promise((resolve) => socket.listen(0, '127.0.0.1', resolve));
  const port = socket.address().port;
  await new Promise((resolve) => socket.close(resolve));
  const origin = `http://127.0.0.1:${port}`;
  child = spawn(
    path.join(root, 'dist/camp'),
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
  await api('/api/auth/setup', 'POST', {
    token: (await readFile(path.join(data, 'team-setup.token'), 'utf8')).trim(),
    username: 'release-admin',
    name: 'Release Admin',
    password: 'Native bundle smoke password',
  });
  const installation = await api('/api/runner-installation');
  const checksumText = await readFile(
    path.join(root, 'dist', `${archiveName}.sha256`),
    'utf8',
  );
  assert.equal(installation.available, true);
  assert.equal(installation.filename, archiveName);
  assert.equal(installation.bytes, release.installer.bytes);
  assert.equal(installation.runtime.bytes, release.runtime.bytes);
  assert.equal(
    installation.runtime.unpackedBytes,
    release.runtime.unpackedBytes,
  );
  assert.equal(installation.offline.bytes, release.offline.bytes);
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
  const stagedRuntime = path.join(data, 'packages', release.runtime.filename);
  if (offline && !publicDownload)
    await rename(stagedRuntime, `${stagedRuntime}.held`);
  const setup = spawn(
    path.join(bundle, 'camp'),
    [
      'runner',
      'setup',
      '--server',
      origin,
      '--name',
      'Installed Camp Runner',
      '--data-dir',
      runnerData,
      '--code-file',
      '-',
      '--docker-socket',
      process.env.CAMP_TEST_DOCKER_SOCKET || '/var/run/docker.sock',
      ...(!publicDownload ? ['--runtime-source', 'server'] : []),
    ],
    {
      cwd,
      stdio: ['pipe', 'pipe', 'pipe'],
      env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' },
    },
  );
  let setupOutput = '';
  for (const stream of [setup.stdout, setup.stderr])
    stream.on('data', (chunk) => {
      setupOutput += chunk.toString();
    });
  setup.stdin.end(token);
  const setupCode = await new Promise((resolve, reject) => {
    setup.once('error', reject);
    setup.once('exit', resolve);
  });
  assert.equal(setupCode, 0, setupOutput);
  if (publicDownload && !offline)
    assert.ok(
      setupOutput.includes(`Downloading GitHub release ${release.version}`),
      'Setup did not download the fixed public release',
    );
  const savedConfig = JSON.parse(
    await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
  );
  const runtime = path.join(
    runnerData,
    'runtime/cache',
    release.runtime.sha256,
  );
  assert.equal(savedConfig.bundleDir, runtime);
  await command('sha256sum', ['--check', '--quiet', 'SHA256SUMS'], {
    cwd: runtime,
  });
  const prebuilds = path.join(
    runtime,
    'runtime/dsh-runtime/node_modules/.pnpm/node-pty@1.2.0-beta.15/node_modules/node-pty/prebuilds',
  );
  assert.deepEqual(await readdir(prebuilds), ['linux-x64']);
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
      path.join(bundle, release.runtime.filename),
      path.join(bundle, `${release.runtime.filename}.held`),
    );
  await command(
    path.join(bundle, 'camp'),
    [
      'runner',
      'setup',
      '--server',
      origin,
      '--name',
      'Installed Camp Runner',
      '--data-dir',
      runnerData,
    ],
    { cwd, env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' } },
  );
  const repeatedConfig = JSON.parse(
    await readFile(path.join(runnerData, 'daemon.json'), 'utf8'),
  );
  assert.equal(repeatedConfig.runnerId, savedConfig.runnerId);
  assert.equal(repeatedConfig.credential, savedConfig.credential);
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
  const foregroundCommand = setupOutput
    .split('\n')
    .find((line) => line.startsWith('Run in foreground: '))
    ?.slice('Run in foreground: '.length);
  assert.ok(foregroundCommand, 'Setup did not provide a foreground command');
  runner = spawn('/bin/sh', ['-c', `exec ${foregroundCommand}`], {
    cwd,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { PATH: '/usr/bin:/bin', HOME: dir, LANG: 'C.UTF-8' },
  });
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
    if (assigned?.online && assigned.capabilities?.ready) break;
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
  assert.equal(assigned.firstTaskRunId, '');
  assert.equal(assigned.firstTaskSucceededAt, '');
  assert.equal(assigned.modelConfigured, true);
  const { rooms } = await api('/api/rooms');
  let room = rooms[0];
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
  assert.equal(completedRunner.firstTaskRunId, run.id);
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
  await stop(runner);
  runner = undefined;
  await stop(child);
  child = undefined;
  // Distribution caches are rebuildable. Back up all remaining business state,
  // then restart the exact release from that stopped, private copy.
  await rm(path.join(data, 'packages'), { recursive: true, force: true });
  const restored = path.join(dir, 'restored-data');
  await cp(data, restored, { recursive: true });
  child = spawn(
    path.join(root, 'dist/camp'),
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
    run.id,
  );
  await stop(child);
  child = undefined;
  const manifest = JSON.parse(
    await readFile(path.join(runtime, 'manifest.json'), 'utf8'),
  );
  const digest = createHash('sha256')
    .update(JSON.stringify(manifest.layerfs))
    .digest('hex')
    .slice(0, 12);
  console.log(
    `Runner ${mode} install smoke passed: separate Server, ${publicDownload ? 'anonymous GitHub' : 'authenticated mirror'} download, relocated verified runtime, repeat setup, native DSH/LayerFS task, checks, binary bytes, one publication, human approval/delivery, stopped backup/restore and cleanup (${digest}). No paid model endpoint used.`,
  );
  await writeFile(
    path.join(root, `dist/smoke-${mode}.json`),
    `${JSON.stringify(
      {
        schema: 1,
        status: 'passed',
        mode,
        version: release.version || 'dev',
        completedAt: new Date().toISOString(),
        platform: process.platform,
        architecture: process.arch,
        model: {
          kind: 'deterministic-local-provider',
          requests: calls,
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
        ],
        untested: [
          'paid-model-provider',
          'cross-version-upgrade',
          'systemd-service-installation',
        ],
      },
      null,
      2,
    )}\n`,
  );
} finally {
  if (runner) await stop(runner);
  if (child) await stop(child);
  provider.closeAllConnections();
  await new Promise((resolve) => provider.close(resolve));
  await rm(dir, { recursive: true, force: true });
}
