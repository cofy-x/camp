import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  AcceptanceLedger,
  cancelTestTasks,
  createPolicyRevocation,
  limits,
  main,
} from './verify-automation-real.mjs';
import { privateJSON } from './verify-remote-real.mjs';

test('automation ledger fsyncs before dispatch and enforces every fixed reservation ceiling', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-automation-budget-'));
  try {
    const ledger = await AcceptanceLedger.create(join(directory, 'report'), {
      origin: 'http://127.0.0.1:3001',
    });
    let calls = 0;
    await ledger.execute(
      'authorized-input',
      { requestId: 'first' },
      async () => {
        calls++;
        const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
        assert.equal(saved.requests[0].state, 'reserved-outcome-unknown');
        assert.deepEqual(saved.consumed, {
          assessments: 4,
          tasks: 2,
          githubInputWrites: 4,
        });
      },
      { assessments: 4, tasks: 2, githubInputWrites: 4 },
    );
    for (const cost of [
      { assessments: 1 },
      { tasks: 1 },
      { githubInputWrites: 1 },
      { tasks: -1 },
      { unbounded: 1 },
    ])
      await assert.rejects(
        ledger.execute(
          'excess',
          {},
          async () => {
            calls++;
          },
          cost,
        ),
        /allowance/u,
      );
    assert.equal(calls, 1);
    assert.equal((await stat(ledger.filename)).mode & 0o077, 0);
    await assert.rejects(
      AcceptanceLedger.create(join(directory, 'report'), {}),
      { code: 'EEXIST' },
    );
    assert.equal(limits.totalRounds, limits.tasks * limits.roundsPerTask);
    assert.equal(
      limits.totalModelRequests,
      limits.tasks * limits.modelRequestsPerTask,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('unknown writes permanently consume the reservation and a disk failure prevents dispatch', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-automation-unknown-'));
  try {
    const ledger = await AcceptanceLedger.create(
      join(directory, 'unknown'),
      {},
    );
    let calls = 0;
    await assert.rejects(
      ledger.execute(
        'one-input',
        {},
        async () => {
          calls++;
          throw new Error('private-token in upstream error');
        },
        { assessments: 1, tasks: 1 },
      ),
      /reservation is consumed/u,
    );
    await assert.rejects(
      ledger.execute('same-or-next-input', {}, async () => {
        calls++;
      }),
      /permanently closed/u,
    );
    const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
    assert.equal(calls, 1);
    assert.equal(saved.consumed.tasks, 1);
    assert.equal(saved.requests[0].state, 'outcome-unknown');
    assert.equal(JSON.stringify(saved).includes('private-token'), false);
    const disk = await AcceptanceLedger.create(join(directory, 'disk'), {});
    disk.save = async () => {
      throw new Error('disk unavailable');
    };
    await assert.rejects(
      disk.execute(
        'input',
        {},
        async () => {
          calls++;
        },
        { assessments: 1 },
      ),
      /durable confirmed/u,
    );
    assert.equal(calls, 1);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

for (const scenario of [
  'confirmed',
  'lost-applied',
  'lost-unapplied',
  'readback-unavailable',
])
  test(`temporary policy revocation is single-attempt with durable ${scenario} evidence`, async () => {
    const directory = await mkdtemp(join(tmpdir(), 'camp-automation-revoke-'));
    try {
      const ledger = await AcceptanceLedger.create(
        join(directory, 'report'),
        {},
      );
      let enabled = true;
      let reads = 0;
      let writes = 0;
      const api = async (route, method = 'GET', body) => {
        if (method === 'GET') {
          reads++;
          if (reads > 1 && scenario === 'readback-unavailable')
            throw new Error('private-token must not be exposed');
          return {
            policies: [
              {
                id: 'policy',
                sourceId: 'source',
                revision: enabled ? 3 : 4,
                mode: 'auto',
                enabled,
              },
            ],
          };
        }
        writes++;
        assert.equal(route, '/api/rooms/room/automation-policies/source');
        assert.equal(body.enabled, false);
        assert.equal(body.revision, 3);
        const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
        assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
        assert.equal(saved.requests.at(-1).requestId, body.requestId);
        if (scenario !== 'lost-unapplied') enabled = false;
        if (scenario.startsWith('lost'))
          throw new Error('private-token must not be exposed');
        return { policy: { id: 'policy', enabled: false, revision: 4 } };
      };
      const revoke = createPolicyRevocation({
        api,
        ledger,
        roomId: 'room',
        sourceId: 'source',
        policySpec: {
          rules: 'Explicit acceptance work.',
          maxTurns: 3,
          maxModelRequests: 12,
        },
      });
      const [receipt, concurrent] = await Promise.all([revoke(), revoke()]);
      assert.equal(receipt, concurrent);
      assert.equal(await revoke(), receipt);
      assert.equal(writes, 1);
      assert.equal(reads, 2);
      assert.equal(receipt.durable, true);
      assert.equal(
        receipt.policyDisabled,
        ['confirmed', 'lost-applied'].includes(scenario),
      );
      assert.equal(
        receipt.state,
        receipt.policyDisabled ? 'disabled' : 'unknown',
      );
      assert.equal(
        receipt.confirmedBy,
        scenario === 'lost-applied'
          ? 'readback-after-unknown'
          : scenario === 'confirmed'
            ? 'readback'
            : undefined,
      );
      const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
      assert.deepEqual(saved.cleanup, receipt);
      assert.equal(saved.requests.length, 1);
      assert.deepEqual(saved.consumed, {
        assessments: 0,
        tasks: 0,
        githubInputWrites: 0,
      });
      assert.equal(JSON.stringify(saved).includes('private-token'), false);
      await assert.rejects(
        ledger.execute('another-input', {}, async () => {}),
        /permanently closed/u,
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

test('already-disabled policies need no mutation and an unjournaled revocation is never dispatched', async () => {
  const directory = await mkdtemp(
    join(tmpdir(), 'camp-automation-revoke-guard-'),
  );
  try {
    let writes = 0;
    const ledger = await AcceptanceLedger.create(
      join(directory, 'disabled'),
      {},
    );
    const revoke = createPolicyRevocation({
      ledger,
      roomId: 'room',
      sourceId: 'source',
      policySpec: {},
      api: async (_, method = 'GET') => {
        if (method !== 'GET') writes++;
        return {
          policies: [
            { id: 'policy', sourceId: 'source', enabled: false, revision: 4 },
          ],
        };
      },
    });
    const receipt = await revoke();
    assert.equal(receipt.policyDisabled, true);
    assert.equal(receipt.state, 'already-disabled');
    assert.equal(receipt.durable, true);
    assert.equal(writes, 0);
    const disk = await AcceptanceLedger.create(join(directory, 'disk'), {});
    disk.save = async () => {
      throw new Error('disk unavailable');
    };
    const unsaved = createPolicyRevocation({
      ledger: disk,
      roomId: 'room',
      sourceId: 'source',
      policySpec: {},
      api: async (_, method = 'GET') => {
        if (method !== 'GET') writes++;
        return {
          policies: [
            { id: 'policy', sourceId: 'source', enabled: true, revision: 4 },
          ],
        };
      },
    });
    const unknown = await unsaved();
    assert.equal(unknown.policyDisabled, false);
    assert.equal(unknown.writeAttempted, false);
    assert.equal(unknown.durable, false);
    assert.equal(writes, 0);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('failure cleanup journals each cancellation, retains unknown replies and leaves unrelated tasks alone', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'camp-automation-cancel-'));
  try {
    const ledger = await AcceptanceLedger.create(join(directory, 'report'), {});
    ledger.blocked = true;
    const requests = [];
    const tasks = [
      {
        id: 'test-first',
        automationPolicyId: 'test-policy',
        revision: 3,
        status: 'running',
      },
      { id: 'test-second', revision: 1, status: 'queued' },
      {
        id: 'unrelated',
        automationPolicyId: 'another-policy',
        revision: 2,
        status: 'running',
      },
      {
        id: 'finished',
        automationPolicyId: 'test-policy',
        revision: 4,
        status: 'completed',
      },
    ];
    const receipts = await cancelTestTasks({
      ledger,
      tasks,
      policyId: 'test-policy',
      taskIds: ['test-second'],
      api: async (route, method, body) => {
        const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
        assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
        assert.equal(saved.requests.at(-1).requestId, body.requestId);
        assert.equal(method, 'POST');
        requests.push(route);
        if (requests.length === 1) throw new Error('private provider detail');
        return {};
      },
    });
    assert.deepEqual(requests, [
      '/api/tasks/test-first/cancel?view=summary',
      '/api/tasks/test-second/cancel?view=summary',
    ]);
    assert.deepEqual(
      receipts.map((receipt) => receipt.state),
      ['outcome-unknown', 'confirmed'],
    );
    const saved = await readFile(ledger.filename, 'utf8');
    assert.equal(saved.includes('private provider detail'), false);
    assert.equal(JSON.parse(saved).requests.length, 2);
    let unjournaled = 0;
    ledger.save = async () => {
      throw new Error('disk unavailable');
    };
    const failed = await cancelTestTasks({
      ledger,
      tasks: [tasks[0]],
      policyId: 'test-policy',
      api: async () => {
        unjournaled++;
      },
    });
    assert.equal(unjournaled, 0);
    assert.equal(failed[0].attempted, false);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

for (const unknown of [
  'github',
  'ingress',
  'public-github',
  'public-github-cleanup-unknown',
])
  test(`real automation driver stops after uncertain ${unknown}, retains budget and never journals credentials`, async () => {
    const directory = await mkdtemp(join(tmpdir(), 'camp-automation-driver-'));
    const reportDir = join(directory, 'report');
    const token = 'private-github-token-that-must-not-be-journaled';
    const password = 'private-camp-password-that-must-not-be-journaled';
    let secret;
    let policy;
    let issue;
    let githubWrites = 0;
    let ingresses = 0;
    let disabled = 0;
    let publicConnections = 0;
    let publicReceives = 0;
    let publicCloses = 0;
    const publicMode = unknown.startsWith('public-');
    const server = createServer(async (request, response) => {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const bytes = Buffer.concat(chunks);
      const body = bytes.length ? JSON.parse(bytes) : null;
      const route = new URL(request.url, 'http://localhost').pathname;
      let value;
      if (route === '/api/auth/login')
        value = { user: { id: 'owner', role: 'admin', enabled: true } };
      else if (route === '/api/settings/models')
        value = {
          revision: 'frozen-model',
          apiKeyConfigured: true,
          maxTokens: 4096,
          maxSteps: 8,
          timeoutSeconds: 180,
        };
      else if (route === '/api/runners')
        value = {
          runners: [
            {
              id: 'native-runner',
              enabled: true,
              revoked: false,
              online: true,
              slots: 1,
              capabilities: {
                executor: 'native',
                ready: true,
                taskInputsVersion: 1,
              },
            },
          ],
        };
      else if (route === '/api/rooms')
        value = {
          room: { id: 'room', resourceId: 'resource', runnerRevision: 1 },
        };
      else if (route === '/api/rooms/room/runner')
        value = {
          room: {
            id: 'room',
            resourceId: 'resource',
            runnerId: body.runnerId,
            runnerRevision: 2,
          },
        };
      else if (route === '/api/resources/resource')
        value = { resource: { id: 'resource', revision: 1 } };
      else if (route === '/api/rooms/room/agents')
        value = { agent: { id: 'writer' } };
      else if (route === '/api/rooms/room/github-sources') {
        secret = body.secret;
        value = { source: { id: 'source' } };
      } else if (route === '/api/rooms/room/automation-policies/source') {
        policy = {
          ...body,
          id: 'policy',
          roomId: 'room',
          sourceId: 'source',
          revision: (policy?.revision ?? 0) + 1,
        };
        if (!body.enabled) disabled++;
        value = { policy };
      } else if (route === '/api/github-sources/source/comment-credential') {
        assert.equal(body.token, token);
        assert.equal(body.actorId, 123);
        policy.revision++;
        value = { kind: 'policy', policy };
      } else if (
        route === '/api/rooms/room/automation-policies' &&
        unknown === 'public-github-cleanup-unknown' &&
        disabled > 0
      ) {
        response
          .writeHead(503, { 'Content-Type': 'application/json' })
          .end(JSON.stringify({ error: token }));
        return;
      } else if (route === '/api/rooms/room/automation-policies')
        value = { policies: policy ? [policy] : [] };
      else if (route === '/api/rooms/room/tasks') value = { tasks: [] };
      else if (route === '/api/hooks/github/source') {
        ingresses++;
        assert.equal(request.headers.cookie, undefined);
        assert.equal(request.headers.origin, undefined);
        assert.equal(request.headers.authorization, undefined);
        assert.equal(
          request.headers['x-hub-signature-256'],
          `sha256=${createHmac('sha256', secret).update(bytes).digest('hex')}`,
        );
        const saved = JSON.parse(
          await readFile(join(reportDir, 'ledger.json'), 'utf8'),
        );
        assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
        assert.equal(saved.consumed.assessments, 1);
        assert.equal(saved.requests.at(-1).kind, 'signed-ingress');
        assert.equal(saved.requests.at(-1).eventKind, 'issues');
        response
          .writeHead(503, { 'Content-Type': 'application/json' })
          .end(JSON.stringify({ error: token }));
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
    await privateJSON(access, {
      origin,
      admin: { username: 'owner', password },
    });
    const github = async (args, stdin) => {
      if (args[0] === 'auth') return token;
      assert.deepEqual(args.slice(0, 3), ['api', '--hostname', 'github.com']);
      const route = args[3];
      const method = args[5];
      if (route === 'user')
        return JSON.stringify({
          id: 123,
          login: 'fixture-owner',
          type: 'User',
        });
      if (route === 'repos/owner/repo')
        return JSON.stringify({
          id: 456,
          full_name: 'owner/repo',
          private: true,
        });
      if (route === 'repos/owner/repo/issues?state=all&per_page=1') return '[]';
      if (route === 'repos/owner/repo/issues' && method === 'POST') {
        githubWrites++;
        assert.equal(args.includes('--input'), true);
        const saved = JSON.parse(
          await readFile(join(reportDir, 'ledger.json'), 'utf8'),
        );
        assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
        assert.equal(saved.consumed.githubInputWrites, 1);
        assert.equal(saved.requests.at(-1).kind, 'github-create-issue');
        assert.equal(saved.consumed.assessments, publicMode ? 1 : 0);
        assert.equal(saved.consumed.tasks, 0);
        if (publicMode) {
          // The real callback can escape before GitHub returns its write
          // response. Its assessment allowance must already be on disk.
          assert.equal(publicConnections, 1);
          assert.deepEqual(saved.requests.at(-1).cost, {
            githubInputWrites: 1,
            assessments: 1,
          });
        }
        if (unknown === 'github' || publicMode) throw new Error(token);
        issue = {
          ...JSON.parse(stdin),
          id: 999,
          number: 1,
          html_url: 'https://github.com/owner/repo/issues/1',
          user: { id: 123, login: 'fixture-owner', type: 'User' },
        };
        return JSON.stringify(issue);
      }
      if (route === 'repos/owner/repo/issues/1') return JSON.stringify(issue);
      throw new Error(
        'Unexpected GitHub request in uncertain-outcome fixture.',
      );
    };
    const args = [
      '--allow-two-real-tasks',
      origin,
      access,
      'native-runner',
      'owner/repo',
      reportDir,
      ...(publicMode ? ['--public-webhook', 'https://callbacks.example'] : []),
    ];
    const dependencies = {
      gh: github,
      ...(publicMode
        ? {
            publicWebhook: (options) => {
              assert.equal(options.publicOrigin, 'https://callbacks.example');
              assert.equal(options.secret, secret);
              assert.equal(options.report.ingressMode, 'github-public');
              return {
                connect: async () => {
                  publicConnections++;
                },
                receive: async () => {
                  publicReceives++;
                  throw new Error(
                    'An unknown GitHub write must not be replayed.',
                  );
                },
                close: async () => {
                  publicCloses++;
                  assert.equal(disabled, 1);
                  const saved = JSON.parse(
                    await readFile(join(reportDir, 'ledger.json'), 'utf8'),
                  );
                  // Hook shutdown is still attempted after an unavailable
                  // policy readback, using its own independent cleanup owner.
                  assert.equal(
                    saved.cleanup.policyDisabled,
                    unknown !== 'public-github-cleanup-unknown',
                  );
                  return { disabled: true, durable: true };
                },
              };
            },
          }
        : {}),
    };
    try {
      await assert.rejects(
        main(args, dependencies),
        /reservation is consumed/u,
      );
      assert.equal(githubWrites, 1);
      assert.equal(ingresses, unknown === 'ingress' ? 1 : 0);
      assert.equal(disabled, 1);
      assert.equal(publicConnections, publicMode ? 1 : 0);
      assert.equal(publicReceives, 0);
      assert.equal(publicCloses, publicMode ? 1 : 0);
      const ledger = await readFile(join(reportDir, 'ledger.json'), 'utf8');
      const report = await readFile(join(reportDir, 'report.json'), 'utf8');
      for (const credential of [token, password, secret]) {
        assert.equal(ledger.includes(credential), false);
        assert.equal(report.includes(credential), false);
      }
      const saved = JSON.parse(ledger);
      assert.equal(saved.consumed.githubInputWrites, 1);
      assert.equal(
        saved.consumed.assessments,
        unknown === 'ingress' || publicMode ? 1 : 0,
      );
      assert.equal(saved.consumed.tasks, 0);
      const stopped = JSON.parse(report);
      assert.equal(
        stopped.cleanup.policyDisabled,
        unknown !== 'public-github-cleanup-unknown',
      );
      if (publicMode) {
        assert.equal(stopped.ingressMode, 'github-public');
        assert.deepEqual(stopped.cleanup.githubWebhook, {
          disabled: true,
          durable: true,
        });
        assert.equal(
          saved.requests.filter((request) => request.kind === 'signed-ingress')
            .length,
          0,
        );
        const request = saved.requests.find(
          (request) => request.kind === 'github-create-issue',
        );
        assert.equal(request.state, 'outcome-unknown');
        assert.equal(stopped.sourceEvents.length, 0);
      }
      await assert.rejects(main(args, dependencies), { code: 'EEXIST' });
      assert.equal(githubWrites, 1);
      assert.equal(publicCloses, publicMode ? 1 : 0);
    } finally {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
