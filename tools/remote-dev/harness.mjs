import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { readFile, writeFile, mkdir, access } from 'node:fs/promises';
import { createServer } from 'node:http';
import { request as httpsRequest } from 'node:https';
import { setTimeout as delay } from 'node:timers/promises';
import { Buffer } from 'node:buffer';
import { URL } from 'node:url';
import process from 'node:process';
import console from 'node:console';

const origin = process.env.CAMP_TEST_ORIGIN || 'https://server:3443';
const mode = process.argv[2] || 'serve';
let state = {};

async function save() {
  await writeFile('/control/state.json', JSON.stringify(state), {
    mode: 0o600,
  });
}

async function load() {
  state = JSON.parse(await readFile('/control/state.json', 'utf8'));
}

async function api(path, method = 'GET', body) {
  const ca = await readFile('/trust/ca.crt');
  const encoded = body === undefined ? undefined : JSON.stringify(body);
  return new Promise((resolve, reject) => {
    const request = httpsRequest(
      new URL(`/api${path}`, origin),
      {
        method,
        ca,
        timeout: 10000,
        headers: {
          Origin: origin,
          'Content-Type': 'application/json',
          ...(state.cookie ? { Cookie: state.cookie } : {}),
          ...(encoded ? { 'Content-Length': Buffer.byteLength(encoded) } : {}),
        },
      },
      (response) => {
        const chunks = [];
        response.on('data', (chunk) => chunks.push(chunk));
        response.on('error', reject);
        response.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          let value;
          try {
            value = text ? JSON.parse(text) : {};
          } catch {
            value = text;
          }
          if (response.statusCode < 200 || response.statusCode >= 300) {
            reject(
              new Error(
                `${method} ${path}: HTTP ${response.statusCode}: ${typeof value === 'object' ? value.error : 'invalid response'}`,
              ),
            );
            return;
          }
          const cookies = response.headers['set-cookie'];
          if (cookies?.length) state.cookie = cookies[0].split(';')[0];
          resolve(value);
        });
      },
    );
    request.on('error', reject);
    request.on('timeout', () =>
      request.destroy(new Error('Request timed out.')),
    );
    request.end(encoded);
  });
}

async function until(label, read, accept, timeout = 45000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await read();
      if (accept(last)) return last;
    } catch (error) {
      last = error.message;
    }
    await delay(150);
  }
  throw new Error(`Timed out at ${label}: ${JSON.stringify(last)}`);
}

async function init() {
  for (const directory of [
    '/certs',
    '/trust',
    '/control',
    '/join-a',
    '/join-b',
  ])
    await mkdir(directory, { recursive: true, mode: 0o700 });
  try {
    await access('/certs/server.crt');
    return;
  } catch {
    /* A new project needs a new local CA. */
  }
  const openssl = (args) => execFileSync('openssl', args, { stdio: 'ignore' });
  openssl([
    'req',
    '-x509',
    '-newkey',
    'rsa:2048',
    '-nodes',
    '-keyout',
    '/certs/ca.key',
    '-out',
    '/trust/ca.crt',
    '-days',
    '30',
    '-subj',
    '/CN=Camp remote development CA',
  ]);
  openssl([
    'req',
    '-newkey',
    'rsa:2048',
    '-nodes',
    '-keyout',
    '/certs/server.key',
    '-out',
    '/certs/server.csr',
    '-subj',
    '/CN=server',
  ]);
  await writeFile(
    '/certs/extensions',
    'subjectAltName=DNS:server,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n',
    { mode: 0o600 },
  );
  openssl([
    'x509',
    '-req',
    '-in',
    '/certs/server.csr',
    '-CA',
    '/trust/ca.crt',
    '-CAkey',
    '/certs/ca.key',
    '-CAcreateserial',
    '-out',
    '/certs/server.crt',
    '-days',
    '30',
    '-extfile',
    '/certs/extensions',
  ]);
  await writeFile(
    '/control/account.json',
    JSON.stringify({
      username: 'remote-owner',
      name: 'Remote Test Owner',
      password: randomBytes(32).toString('base64url'),
    }),
    { mode: 0o600 },
  );
  console.log(
    'Created private TLS and account fixtures for this Compose project.',
  );
}

async function bootstrap() {
  await until(
    'TLS Server readiness',
    () => api('/health'),
    () => true,
  );
  const account = JSON.parse(await readFile('/control/account.json', 'utf8'));
  const status = await api('/auth/status');
  let session;
  if (status.setupRequired)
    session = await api('/auth/setup', 'POST', {
      ...account,
      teamName: 'Camp remote validation',
      token: (await readFile('/server-data/team-setup.token', 'utf8')).trim(),
    });
  else
    session = await api('/auth/login', 'POST', {
      username: account.username,
      password: account.password,
    });
  state.user = session.user;
  const { runners } = await api('/runners');
  for (const name of ['daemon-a', 'daemon-b']) {
    if (runners.some((runner) => runner.name === name)) continue;
    const joined = await api('/runner-enrollments', 'POST', { name });
    await writeFile(`/join-${name.at(-1)}/token`, joined.token, {
      mode: 0o600,
    });
  }
  await save();
  console.log(
    'Team setup, login and one-use Runner enrollment completed through authenticated APIs.',
  );
}

async function ready() {
  const result = await until(
    'both fixture Runners online',
    () => api('/runners'),
    ({ runners }) =>
      ['daemon-a', 'daemon-b'].every((name) =>
        runners.some(
          (runner) =>
            runner.name === name && runner.online && runner.capabilities.ready,
        ),
      ),
  );
  state.runners = Object.fromEntries(
    result.runners.map((runner) => [runner.name, runner]),
  );
  await save();
  return result;
}

async function newRoom(runnerName) {
  const { room } = await api('/rooms', 'POST', {
    name: `Remote ${runnerName} ${randomUUID().slice(0, 8)}`,
    description: 'Deterministic protocol acceptance',
  });
  const { agent } = await api(`/rooms/${room.id}/agents`, 'POST', {
    name: 'Fixture Writer',
    handle: `fixture-${randomUUID().slice(0, 8)}`,
    access: 'write',
    enabled: true,
    instructions: 'Produce the requested file.',
    preferredRunnerId: state.runners[runnerName].id,
  });
  const routed = await api(`/rooms/${room.id}/runner`, 'PUT', {
    runnerId: state.runners[runnerName].id,
    revision: room.runnerRevision,
  });
  return { room: routed.room, agent };
}

async function send(work, content) {
  const payload = {
    requestId: randomUUID(),
    content,
    recipientAgentIds: [work.agent.id],
    runnerRevision: work.room.runnerRevision,
  };
  const result = await api(`/rooms/${work.room.id}/messages`, 'POST', payload);
  const replay = await api(`/rooms/${work.room.id}/messages`, 'POST', payload);
  assert.equal(
    result.runs[0].id,
    replay.runs[0].id,
    'Message retry must preserve one Run.',
  );
  assert.equal(
    result.runs[0].runnerId,
    work.room.runnerId,
    'Run must freeze selected Runner.',
  );
  return result.runs[0];
}

async function terminal(id, expected) {
  const { run } = await until(
    `Run ${id} ${expected}`,
    () => api(`/runs/${id}`),
    ({ run }) =>
      ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(run.status),
    90000,
  );
  assert.equal(run.status, expected, run.error);
  return run;
}

async function waiting(id) {
  await until(
    'fixture execution barrier',
    () => api(`/runs/${id}/events`),
    ({ events }) =>
      events.some((event) =>
        event.text?.includes('Waiting at the cancellation barrier'),
      ),
  );
}

async function check() {
  await ready();
  const a = await newRoom('daemon-a');
  const b = await newRoom('daemon-b');
  const [first, second] = await Promise.all([
    send(a, 'Draft one for approval'),
    send(b, 'Independent second draft'),
  ]);
  const [one, two] = await Promise.all([
    terminal(first.id, 'succeeded'),
    terminal(second.id, 'succeeded'),
  ]);
  assert.equal(one.published, true);
  assert.equal(two.published, true);
  let result = await api(`/rooms/${a.room.id}/reviews`, 'POST', {
    requestId: randomUUID(),
    title: 'Review remote result',
    resourceId: a.room.resourceId,
    path: 'result.txt',
    commit: one.resultCommit,
    reviewerUserId: state.user.id,
  });
  result = await api(`/reviews/${result.review.id}/actions`, 'POST', {
    requestId: randomUUID(),
    revision: result.review.revision,
    action: 'request_changes',
    comment: 'Please revise the opening.',
  });
  const revised = await terminal(
    (await send(a, 'Revise the opening for human review.')).id,
    'succeeded',
  );
  result = await api(`/reviews/${result.review.id}/actions`, 'POST', {
    requestId: randomUUID(),
    revision: result.review.revision,
    action: 'resubmit',
    commit: revised.resultCommit,
  });
  result = await api(`/reviews/${result.review.id}/actions`, 'POST', {
    requestId: randomUUID(),
    revision: result.review.revision,
    action: 'approve',
  });
  result = await api(`/reviews/${result.review.id}/actions`, 'POST', {
    requestId: randomUUID(),
    revision: result.review.revision,
    action: 'deliver',
  });
  const delivered = result.review.deliveredCommit;
  await terminal(
    (await send(a, 'A later independent revision.')).id,
    'succeeded',
  );
  assert.equal(
    (await api(`/reviews/${result.review.id}`)).review.deliveredCommit,
    delivered,
  );
  const waitingRun = await send(a, '[fixture:wait]');
  await waiting(waitingRun.id);
  await terminal((await send(b, 'B continues while A waits.')).id, 'succeeded');
  await api(`/runs/${waitingRun.id}/cancel`, 'POST', {});
  await terminal(waitingRun.id, 'cancelled');
  const { sources } = await api(
    `/runners/${state.runners['daemon-a'].id}/sources`,
  );
  const source = sources.find(
    (item) => item.alias === 'notes' && item.available,
  );
  assert.ok(source);
  const imported = await api('/source-imports', 'POST', {
    requestId: randomUUID(),
    runnerId: state.runners['daemon-a'].id,
    sourceId: source.id,
    expectedSourceRevision: source.revision,
    roomName: `Imported Runner notes ${randomUUID().slice(0, 8)}`,
  });
  const receipt = await until(
    'source snapshot receipt',
    () => api(`/source-imports/${imported.sourceImport.id}`),
    ({ sourceImport }) =>
      ['succeeded', 'failed', 'interrupted'].includes(sourceImport.status),
  );
  assert.equal(
    receipt.sourceImport.status,
    'succeeded',
    receipt.sourceImport.error,
  );
  const { rooms } = await api('/rooms');
  const room = rooms.find((item) => item.id === receipt.sourceImport.roomId);
  const route = await api(`/rooms/${room.id}/runner`, 'PUT', {
    runnerId: state.runners['daemon-b'].id,
    revision: room.runnerRevision,
  });
  const { agent } = await api(`/rooms/${room.id}/agents`, 'POST', {
    name: 'Imported Writer',
    handle: `imported-${randomUUID().slice(0, 8)}`,
    access: 'write',
    enabled: true,
    preferredRunnerId: state.runners['daemon-b'].id,
  });
  await terminal(
    (
      await send(
        { room: route.room, agent },
        'Process the imported snapshot on the other Runner.',
      )
    ).id,
    'succeeded',
  );
  console.log(
    'PASS: two Runners, fixed routing, message replay, cancellation, review/revision/delivery, immutable delivery and cross-Runner source capture.',
  );
}

async function taskState(id, expected) {
  const result = await until(
    `Collaboration ${id} ${expected}`,
    () => api(`/tasks/${id}`),
    ({ task }) =>
      task.status === expected ||
      ['needs_attention', 'cancelled', 'completed'].includes(task.status),
    90000,
  );
  assert.equal(result.task.status, expected, result.task.error);
  return result;
}

async function beginCollaboration() {
  await ready();
  const work = await newRoom('daemon-a');
  const { agent: reviewer } = await api(
    `/rooms/${work.room.id}/agents`,
    'POST',
    {
      name: 'Collaboration Reviewer',
      handle: `reviewer-${randomUUID().slice(0, 8)}`,
      access: 'write',
      enabled: true,
      preferredRunnerId: state.runners['daemon-b'].id,
    },
  );
  const input = {
    requestId: randomUUID(),
    goal: '[fixture:handoff-ask] Draft a guide, ask the team about its audience, and finish.',
    participantAgentIds: [work.agent.id, reviewer.id],
    firstAgentId: work.agent.id,
  };
  const created = await api(`/rooms/${work.room.id}/tasks`, 'POST', input);
  const replay = await api(`/rooms/${work.room.id}/tasks`, 'POST', input);
  assert.equal(replay.task.id, created.task.id);
  const { task, runs } = await taskState(created.task.id, 'waiting_for_person');
  assert.equal(task.activeRunId, '');
  assert.equal(task.turnsUsed, 2);
  assert.equal(task.currentAgentId, reviewer.id);
  assert.ok(task.question?.id);
  assert.equal(runs.length, 2);
  assert.equal(runs[0].runnerId, state.runners['daemon-a'].id);
  assert.equal(runs[1].runnerId, state.runners['daemon-b'].id);
  assert.equal(runs[1].inputCommit, runs[0].resultCommit);
  assert.ok(runs.every((run) => !run.published));
  const { resources } = await api('/resources');
  const resource = resources.find((item) => item.id === work.room.resourceId);
  assert.ok(resource);
  assert.equal(resource.currentCommit, task.baseSharedCommit);
  assert.equal(
    (await api(`/resources/${work.room.resourceId}/history`)).publications
      .length,
    0,
  );
  state.collaboration = { work, reviewer, task, runs };
  await save();
  console.log(
    'Reached a durable collaboration question after one handoff across both Runners; shared files remain unchanged.',
  );
}

async function finishCollaboration() {
  await ready();
  const { work, reviewer, task: saved, runs: before } = state.collaboration;
  const { task: restored, runs: restoredRuns } = await taskState(
    saved.id,
    'waiting_for_person',
  );
  assert.equal(restored.question.id, saved.question.id);
  assert.equal(restored.checkpointCommit, saved.checkpointCommit);
  assert.equal(restored.turnsUsed, 2);
  assert.deepEqual(
    restoredRuns.map((run) => run.id),
    before.map((run) => run.id),
  );
  const answer = {
    requestId: randomUUID(),
    revision: restored.revision,
    questionId: restored.question.id,
    content: 'Internal team',
  };
  const accepted = await api(`/tasks/${saved.id}/answers`, 'POST', answer);
  const replay = await api(`/tasks/${saved.id}/answers`, 'POST', answer);
  assert.equal(accepted.task.activeRunId, replay.task.activeRunId);
  const { task, runs } = await taskState(saved.id, 'completed');
  assert.equal(task.turnsUsed, 3);
  assert.equal(task.question.answer, answer.content);
  assert.equal(task.processedInputRevision, 1);
  assert.ok(task.publishedCommit);
  assert.equal(runs.length, 3);
  assert.equal(runs[2].agentId, reviewer.id);
  assert.equal(runs[2].runnerId, state.runners['daemon-b'].id);
  assert.equal(runs[2].inputCommit, before[1].resultCommit);
  assert.equal(runs[2].published, true);
  for (const run of runs) {
    const { events } = await api(`/runs/${run.id}/events`);
    assert.equal(
      events.filter((event) => event.text?.includes('Execution accepted'))
        .length,
      1,
      'Each authorized round must execute once.',
    );
  }
  const history = await api(`/resources/${work.room.resourceId}/history`);
  assert.equal(history.publications.length, 1);
  const content = await api(
    `/resources/${work.room.resourceId}/file?commit=${task.publishedCommit}&path=result.txt&download=1`,
  );
  assert.ok(content.includes('Runner daemon-b'));
  assert.ok(content.includes(answer.content));
  let result = await api(`/rooms/${work.room.id}/reviews`, 'POST', {
    requestId: randomUUID(),
    title: 'Review the completed collaboration',
    resourceId: work.room.resourceId,
    path: 'result.txt',
    commit: task.publishedCommit,
    reviewerUserId: state.user.id,
  });
  for (const action of ['approve', 'deliver'])
    result = await api(`/reviews/${result.review.id}/actions`, 'POST', {
      requestId: randomUUID(),
      revision: result.review.revision,
      action,
    });
  assert.equal(result.review.deliveredCommit, task.publishedCommit);
  console.log(
    'PASS: collaboration handoff, durable question across Server/Runner restarts, answer replay, exact draft transfer, one final publication and human delivery.',
  );
}

async function begin() {
  await ready();
  const work = await newRoom('daemon-a');
  const run = await send(work, '[fixture:wait]');
  await waiting(run.id);
  state.pending = { work, runId: run.id };
  await save();
  console.log('Reached durable fixture execution barrier.');
}

async function finish() {
  const expected = process.argv[3] || 'cancelled';
  if (expected === 'cancelled') {
    await ready();
    const current = (await api(`/runs/${state.pending.runId}`)).run;
    assert.ok(
      ['running', 'preparing', 'checking'].includes(current.status),
      `Live operation was lost: ${current.status}`,
    );
    await api(`/runs/${state.pending.runId}/cancel`, 'POST', {});
  }
  await terminal(state.pending.runId, expected);
  console.log(`PASS: recovery produced ${expected} without another execution.`);
}

function serve() {
  createServer(async (request, response) => {
    if (request.url === '/health') {
      response.end('ok');
      return;
    }
    if (!request.url?.endsWith('/v1/messages')) {
      response.writeHead(404).end();
      return;
    }
    let bytes = 0;
    for await (const chunk of request) {
      bytes += chunk.length;
      if (bytes > 1 << 20) {
        response.writeHead(413).end();
        return;
      }
    }
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const events = [
      {
        type: 'message_start',
        message: {
          id: 'camp-fixture',
          model: 'deepseek-v4-pro',
          usage: { input_tokens: 1, output_tokens: 0 },
        },
      },
      {
        type: 'content_block_start',
        index: 0,
        content_block: {
          type: 'text',
          text: 'Deterministic local model response.',
        },
      },
      { type: 'content_block_stop', index: 0 },
      {
        type: 'message_delta',
        delta: { stop_reason: 'end_turn' },
        usage: { output_tokens: 1 },
      },
      { type: 'message_stop' },
    ];
    for (const event of events)
      response.write(
        `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`,
      );
    response.end();
  }).listen(3456, '0.0.0.0');
}

if (mode === 'init') await init();
else if (mode === 'serve') serve();
else if (mode === 'bootstrap') await bootstrap();
else {
  await load();
  if (mode === 'ready') await ready();
  else if (mode === 'check') await check();
  else if (mode === 'task-begin') await beginCollaboration();
  else if (mode === 'task-finish') await finishCollaboration();
  else if (mode === 'begin') await begin();
  else if (mode === 'finish') await finish();
  else if (mode === 'offline')
    await until(
      'Runner offline barrier',
      () => api('/runners'),
      ({ runners }) =>
        runners.some((runner) => runner.name === 'daemon-a' && !runner.online),
    );
  else throw new Error(`Unknown harness mode ${mode}.`);
}
