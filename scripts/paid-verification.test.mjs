import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { createHmac } from 'node:crypto';
import { main } from './verify-collaboration-real.mjs';
import { privateJSON } from './verify-remote-real.mjs';
import { installDemoDispatchGuard } from './demo-remote-ui-driver.mjs';

async function browserDispatchFixture({ failSave = false } = {}) {
  const ledger = {
    origin: 'http://127.0.0.1:3001',
    allowedRuns: 2,
    requests: [],
  };
  const snapshots = [];
  let handler;
  const guard = await installDemoDispatchGuard({
    page: {
      route: async (_, callback) => {
        handler = callback;
      },
    },
    ledger,
    save: async () => {
      if (failSave) throw new Error('disk unavailable');
      snapshots.push(structuredClone(ledger));
    },
  });
  let dispatched = 0;

  async function send(body, pathname = '/api/rooms/room/messages') {
    let aborted = false;
    await handler({
      request: () => ({
        method: () => 'POST',
        url: () => `${ledger.origin}${pathname}`,
        postDataJSON: () => structuredClone(body),
      }),
      fallback: async () => {
        dispatched++;
        assert.equal(
          snapshots.at(-1).requests.at(-1).state,
          'dispatched-outcome-unknown',
        );
      },
      abort: async () => {
        aborted = true;
      },
    });
    return { aborted, dispatched };
  }

  const intent = {
    route: '/api/rooms/room/messages',
    prompt: 'Prepare the source.',
    runnerRevision: 1,
    agentId: 'owned-writer',
  };
  const body = {
    requestId: 'first',
    content: intent.prompt,
    threadRootId: '',
    recipientAgentIds: [intent.agentId],
    runnerRevision: 1,
    references: [],
  };
  return { ledger, snapshots, guard, send, intent, body };
}

test('browser demo persists each of two distinct dispatches before forwarding and never grants a third', async () => {
  const f = await browserDispatchFixture();
  const first = f.guard.reserve(f.intent);
  assert.deepEqual(await f.send(f.body), { aborted: false, dispatched: 1 });
  assert.deepEqual(f.snapshots[0].requests[0].body, f.body);
  first.state = 'terminal';
  first.run = { status: 'succeeded', published: true };
  f.guard.disarm();
  f.guard.reserve({
    ...f.intent,
    prompt: 'Revise from human feedback.',
    feedbackMessageId: 'feedback',
  });
  assert.deepEqual(
    await f.send({
      ...f.body,
      requestId: 'second',
      content: 'Revise from human feedback.',
      references: [{ kind: 'message', messageId: 'feedback' }],
    }),
    { aborted: false, dispatched: 2 },
  );
  f.guard.disarm();
  assert.throws(() => f.guard.reserve(f.intent), /allowance cannot be reused/);
});

test('browser demo consumes an uncertain request and blocks automatic or manual resubmission', async () => {
  const f = await browserDispatchFixture();
  f.guard.reserve(f.intent);
  await f.send(f.body);
  f.guard.disarm();
  assert.throws(() => f.guard.reserve(f.intent), /allowance cannot be reused/);
  assert.deepEqual(await f.send(f.body), { aborted: true, dispatched: 1 });
  assert.equal(f.ledger.requests.length, 1);
});

test('browser demo refuses dispatch if the durable journal cannot be saved', async () => {
  const f = await browserDispatchFixture({ failSave: true });
  f.guard.reserve(f.intent);
  assert.deepEqual(await f.send(f.body), { aborted: true, dispatched: 0 });
  assert.equal(f.guard.blocked(), true);
  f.guard.disarm();
  assert.throws(() => f.guard.reserve(f.intent), /allowance cannot be reused/);
});

test('browser demo rejects changed targets, context, request identities and other work admission', async (t) => {
  for (const [name, change, route] of [
    ['recipient', { recipientAgentIds: ['another-agent'] }],
    ['route revision', { runnerRevision: 2 }],
    ['thread', { threadRootId: 'review-thread' }],
    ['context', { references: [{ kind: 'message', messageId: 'unapproved' }] }],
    ['another Room', {}, '/api/rooms/another/messages'],
    ['collaboration', {}, '/api/rooms/room/tasks'],
    ['continuation', {}, '/api/tasks/task/operations'],
  ])
    await t.test(name, async () => {
      const f = await browserDispatchFixture();
      f.guard.reserve(f.intent);
      assert.deepEqual(await f.send({ ...f.body, ...change }, route), {
        aborted: true,
        dispatched: 0,
      });
    });
  const f = await browserDispatchFixture();
  const first = f.guard.reserve(f.intent);
  await f.send(f.body);
  first.state = 'terminal';
  first.run = { status: 'succeeded', published: true };
  f.guard.disarm();
  f.guard.reserve(f.intent);
  assert.deepEqual(await f.send(f.body), { aborted: true, dispatched: 1 });
});

for (const githubObservation of [false, true])
  test(`paid collaboration freezes the current protocol and never resubmits an uncertain task (GitHub: ${githubObservation})`, async () => {
    const directory = await mkdtemp(join(tmpdir(), 'camp-paid-driver-'));
    const submissions = [];
    let webhookSecret = '';
    const server = createServer(async (request, response) => {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : null;
      const route = new URL(request.url, 'http://localhost');
      let value;
      if (route.pathname === '/api/auth/login')
        value = { user: { id: 'owner', role: 'admin', enabled: true } };
      else if (route.pathname === '/api/settings/models')
        value = {
          apiKeyConfigured: true,
          revision: 'config-1',
          maxTokens: 4096,
          maxSteps: 8,
          timeoutSeconds: 180,
        };
      else if (route.pathname === '/api/runners')
        value = {
          runners: ['runner-a', 'runner-b'].map((id) => ({
            id,
            enabled: true,
            revoked: false,
            online: true,
            slots: 1,
            capabilities: { executor: 'native', ready: true },
          })),
        };
      else if (route.pathname === '/api/rooms')
        value = { room: { id: 'room', resourceId: 'resource' } };
      else if (route.pathname === '/api/resources/resource')
        value = { resource: { id: 'resource', revision: 1 } };
      else if (route.pathname === '/api/rooms/room/agents')
        value = {
          agent: { id: body.access === 'read' ? 'reviewer' : 'writer' },
        };
      else if (route.pathname === '/api/rooms/room/github-sources') {
        webhookSecret = body.secret;
        value = { source: { id: 'source' } };
      } else if (route.pathname === '/api/hooks/github/source') {
        assert.equal(request.headers.cookie, undefined);
        assert.equal(request.headers.origin, undefined);
        assert.equal(request.headers.authorization, undefined);
        const bytes = Buffer.concat(chunks);
        assert.equal(
          request.headers['x-hub-signature-256'],
          `sha256=${createHmac('sha256', webhookSecret).update(bytes).digest('hex')}`,
        );
        value = { eventId: 'source-event', status: 'pending' };
      } else if (route.pathname === '/api/rooms/room/orchestration')
        value = { items: [] };
      else if (
        route.pathname === '/api/rooms/room/tasks' &&
        request.method === 'GET'
      )
        value = { tasks: [] };
      else if (route.pathname === '/api/rooms/room/tasks') {
        submissions.push({ view: route.searchParams.get('view'), body });
        response
          .writeHead(503, { 'Content-Type': 'application/json' })
          .end(JSON.stringify({ error: 'uncertain response fixture' }));
        return;
      } else {
        response.writeHead(404).end();
        return;
      }
      response
        .writeHead(200, { 'Content-Type': 'application/json' })
        .end(JSON.stringify(value));
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const access = join(directory, 'access.json');
    const reportDirectory = join(directory, 'report');
    const args = [
      '--allow-one-real-task',
      origin,
      access,
      'runner-a',
      'runner-b',
      reportDirectory,
      ...(githubObservation ? ['--github-observation'] : []),
    ];
    try {
      await privateJSON(access, {
        origin,
        admin: { username: 'fixture', password: 'fixture-only' },
      });
      await assert.rejects(main(args), /HTTP 503/);
      assert.equal(submissions.length, 1);
      assert.equal(submissions[0].view, 'summary');
      assert.equal(submissions[0].body.inputProtocol, 1);
      assert.equal(
        submissions[0].body.sourceEventId,
        githubObservation ? 'source-event' : undefined,
      );
      const report = JSON.parse(
        await readFile(join(reportDirectory, 'report.json'), 'utf8'),
      );
      assert.deepEqual(report.request, submissions[0].body);
      assert.equal(report.maxTasks, 1);
      assert.equal(report.maxTurns, 6);
      assert.equal(report.maxModelRequests, 24);
      assert.equal(report.githubObservation, githubObservation);
      await assert.rejects(main(args), { code: 'EEXIST' });
      assert.equal(submissions.length, 1);
    } finally {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
