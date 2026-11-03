import assert from 'node:assert/strict';
import { createHash, createHmac } from 'node:crypto';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { createPublicWebhook } from './automation-public-webhook.mjs';
import { AcceptanceLedger } from './verify-automation-real.mjs';

const secret = 'private-webhook-secret-that-must-never-be-recorded';
const issue = {
  id: 1001,
  number: 1,
  body: 'Original request.\n',
  user: { id: 7, type: 'User' },
};
const comment = {
  id: 2001,
  body: 'Start the observed work.\n',
  user: { id: 7, type: 'User' },
};

async function fixture(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'camp-public-webhook-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const ledger = await AcceptanceLedger.create(join(directory, 'report'), {});
  const report = {
    repository: 'owner/repo',
    repositoryId: 42,
    githubActorId: 7,
    roomId: 'room',
    sourceId: 'source',
    sourceEvents: [],
  };
  const ownedURL = 'https://hooks.example/api/hooks/github/source';
  const hook = {
    id: 91,
    active: true,
    events: ['issues', 'issue_comment'],
    config: { url: ownedURL, content_type: 'json', insecure_ssl: '0' },
  };
  const requests = [];
  const objects = new Map();
  const events = new Map();
  const rawPayloads = new Map();
  const saves = [];
  let created = false;
  let closed = false;
  let firstRead = true;
  let detailReads = 0;
  let polls = 0;
  let nextDelivery = 1;
  let nextEvent = 1;

  function add(kind, object, original) {
    const deliveryId = nextDelivery++;
    const eventId = original?.eventId ?? `event-${nextEvent++}`;
    const payload =
      kind === 'ping'
        ? {
            repository: { id: 42, full_name: 'owner/repo' },
            hook_id: 91,
            sender: { id: 7, type: 'User' },
          }
        : {
            action: kind === 'issues' ? 'opened' : 'created',
            repository: { id: 42, full_name: 'owner/repo' },
            sender: object.user,
            issue: kind === 'issues' ? object : issue,
            ...(kind === 'issue_comment' ? { comment: object } : {}),
          };
    // Real transport bytes deliberately differ from JSON.stringify(payload).
    const bytes = original?.bytes ?? JSON.stringify(payload, null, 2) + '\n';
    const fingerprint = createHash('sha256').update(bytes).digest('hex');
    const guid =
      original?.guid ??
      `11111111-1111-1111-1111-${String(deliveryId).padStart(12, '0')}`;
    const event = events.get(eventId) ?? {
      id: eventId,
      sourceId: 'source',
      roomId: 'room',
      repositoryId: 42,
      kind,
      objectId: object?.id ?? 0,
      number: object ? 1 : 0,
      status: kind === 'ping' ? 'ignored' : 'pending',
      fingerprint,
    };
    events.set(eventId, event);
    rawPayloads.set(eventId, bytes);
    const delivery = {
      id: deliveryId,
      guid,
      event: kind,
      repository_id: 42,
      redelivery: Boolean(original),
      status_code: 200,
      url: ownedURL,
      request: {
        headers: {
          'X-GitHub-Delivery': guid,
          'X-GitHub-Event': kind,
          'X-GitHub-Hook-ID': '91',
          'X-Hub-Signature-256': `sha256=${createHmac('sha256', secret).update(bytes).digest('hex')}`,
        },
        payload: JSON.parse(bytes),
      },
      response: {
        payload: JSON.stringify({
          eventId,
          status: event.status,
          duplicate: Boolean(original),
        }),
      },
    };
    objects.set(deliveryId, delivery);
    return {
      delivery,
      event,
      bytes,
      eventId,
      guid,
      githubDeliveryId: deliveryId,
    };
  }

  async function github(route, method = 'GET', body, allowEmpty = false) {
    requests.push({ route, method, body, allowEmpty });
    if (method === 'POST' && route === 'repos/owner/repo/hooks') {
      const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
      assert.equal(saved.requests.at(-1).kind, 'create-public-webhook');
      assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
      assert.equal(
        saves.length > 0,
        true,
        'source identity must be saved before creating the hook',
      );
      assert.equal(body.config.secret, secret);
      assert.equal(body.config.url, ownedURL);
      assert.equal(body.config.insecure_ssl, '0');
      assert.deepEqual(body.events, ['issues', 'issue_comment']);
      created = true;
      if (!options.noPing) add('ping');
      if (options.unknownCreate)
        throw new Error('private token upstream detail');
      return structuredClone(hook);
    }
    if (method === 'POST' && route.endsWith('/attempts')) {
      const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
      assert.equal(saved.requests.at(-1).kind, 'redeliver-public-webhook');
      assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
      assert.equal(allowEmpty, true);
      assert.equal(body, undefined);
      if (options.unknownRedelivery)
        throw new Error('private token redelivery error');
      const originalId = Number(route.split('/').at(-2));
      const original = objects.get(originalId);
      const response = JSON.parse(original.response.payload);
      const replay = add(original.event, original.request.payload.comment, {
        eventId: response.eventId,
        guid: original.guid,
        bytes: rawPayloads.get(response.eventId),
      });
      if (options.badReplay) options.badReplay(replay);
      return undefined; // GitHub's accepted redelivery has an empty 202 body.
    }
    if (method === 'PATCH') {
      const saved = JSON.parse(await readFile(ledger.filename, 'utf8'));
      assert.equal(saved.requests.at(-1).kind, 'deactivate-public-webhook');
      assert.equal(saved.requests.at(-1).state, 'reserved-outcome-unknown');
      assert.deepEqual(body, { active: false });
      closed = true;
      if (options.deactivate !== 'lost-unapplied') hook.active = false;
      if (options.deactivate?.startsWith('lost'))
        throw new Error('private token close detail');
      return structuredClone(hook);
    }
    assert.equal(
      method,
      'GET',
      'public helper must never POST directly to Camp',
    );
    if (route === 'repos/owner/repo/hooks?per_page=100') {
      if (firstRead) {
        firstRead = false;
        return options.existingHooks ?? [];
      }
      return created && !options.hideCreatedHook ? [structuredClone(hook)] : [];
    }
    if (route === 'repos/owner/repo/hooks/91') {
      if (closed && options.deactivate === 'readback-unavailable')
        throw new Error('private token readback detail');
      return structuredClone(hook);
    }
    if (route === 'repos/owner/repo/hooks/91/deliveries?per_page=100') {
      options.onDeliveryRead?.({ add, objects, polls });
      if (options.oversizedDeliveries)
        return Array.from({ length: 33 }, (_, index) => ({
          id: index + 1,
          event: 'ping',
        }));
      return [...objects.values()].map(({ id, event, redelivery }) => ({
        id,
        event,
        redelivery,
      }));
    }
    const delivery = route.match(
      /^repos\/owner\/repo\/hooks\/91\/deliveries\/(\d+)$/u,
    );
    if (delivery) {
      detailReads++;
      const value = structuredClone(objects.get(Number(delivery[1])));
      options.editDelivery?.(value);
      return value;
    }
    throw new Error(`Unexpected fixture route: ${route}`);
  }

  const api = async (route, method = 'GET', body, text = false) => {
    requests.push({ route, method, body, text });
    assert.equal(method, 'GET', 'public helper may only read Camp');
    const payload = route.match(
      /^\/api\/github-events\/(event-\d+)\/payload$/u,
    );
    if (payload) {
      assert.equal(text, true);
      return (
        options.editRaw?.(rawPayloads.get(payload[1])) ??
        rawPayloads.get(payload[1])
      );
    }
    const event = route.match(/^\/api\/github-events\/(event-\d+)$/u);
    if (event) return { event: structuredClone(events.get(event[1])) };
    assert.equal(route.startsWith('/api/rooms/room/automation-events?'), true);
    return {
      items: [...events.values()].map((item) => ({
        externalEventId: item.id,
        status: item.status === 'ignored' ? 'ignored' : 'pending',
        ...(options.pingEvaluation && item.kind === 'ping'
          ? { evaluationId: 'unexpected' }
          : {}),
      })),
      through: events.size,
      hasMore: false,
    };
  };
  const helper = createPublicWebhook({
    github,
    api,
    ledger,
    report,
    secret,
    publicOrigin: 'https://hooks.example',
    preflight: async () => {},
    save: async () => {
      saves.push(structuredClone(report));
    },
    wait: async (_, read) => {
      for (let attempt = 0; attempt < 4; attempt++) {
        polls++;
        const result = await read();
        if (result) return result;
      }
      throw new Error(
        'Timed out observing the fixture; no mutation was retried.',
      );
    },
  });
  return {
    helper,
    add,
    requests,
    ledger,
    report,
    events,
    objects,
    rawPayloads,
    hook,
    get detailReads() {
      return detailReads;
    },
  };
}

test('public acceptance observes an early signed ping, exact real-object bytes and one GitHub redelivery without Camp writes', async (t) => {
  const f = await fixture(t);
  const ping = await f.helper.connect();
  assert.equal(ping.eventId, 'event-1');
  await f.helper.connect();
  const actualIssue = f.add('issues', issue);
  const observed = await f.helper.receive('issues', issue);
  assert.equal(observed.bytes, actualIssue.bytes);
  assert.notEqual(observed.bytes, JSON.stringify(JSON.parse(observed.bytes)));
  // A same-kind, same-body comment in another conversation must not be adopted.
  const unrelated = f.add('issue_comment', comment);
  unrelated.delivery.request.payload.issue.id = 9999;
  const actual = f.add('issue_comment', comment);
  const admitted = await f.helper.receive('issue_comment', comment);
  assert.equal(admitted.githubDeliveryId, actual.githubDeliveryId);
  const replay = await f.helper.receive('issue_comment', comment, admitted);
  assert.equal(replay.duplicate, true);
  assert.equal(replay.eventId, admitted.eventId);
  assert.equal(replay.guid, admitted.guid);
  assert.notEqual(replay.githubDeliveryId, admitted.githubDeliveryId);
  assert.equal(
    f.report.sourceEvents.filter((item) => item.eventId === admitted.eventId)
      .length,
    1,
  );
  assert.equal(f.report.webhook.deliveries.length, 4);
  await assert.rejects(
    f.helper.receive('issue_comment', comment, admitted),
    /Only one/u,
  );
  const close = await f.helper.close();
  assert.equal(close.disabled, true);
  assert.equal(close.durable, true);
  assert.equal(await f.helper.close(), close);
  assert.equal(
    f.requests.filter(
      (item) => item.method === 'POST' && item.route.endsWith('/hooks'),
    ).length,
    1,
  );
  assert.equal(
    f.requests.filter((item) => item.route.endsWith('/attempts')).length,
    1,
  );
  assert.equal(f.requests.filter((item) => item.method === 'PATCH').length, 1);
  assert.equal(
    f.requests.some((item) => item.route.startsWith('/api/hooks/')),
    false,
  );
  const saved = await readFile(f.ledger.filename, 'utf8');
  assert.equal(saved.includes(secret), false);
  assert.equal(JSON.stringify(f.report).includes(secret), false);
  assert.deepEqual(JSON.parse(saved).consumed, {
    assessments: 0,
    tasks: 0,
    githubInputWrites: 0,
  });
});

for (const scenario of [
  'lost-applied',
  'lost-unapplied',
  'readback-unavailable',
])
  test(`public webhook cleanup is one durable authority-removing attempt with ${scenario} evidence`, async (t) => {
    const f = await fixture(t, { deactivate: scenario });
    await f.helper.connect();
    f.ledger.blocked = true;
    const result = await f.helper.close();
    assert.equal(result.disabled, scenario === 'lost-applied');
    assert.equal(result.durable, true);
    assert.equal(result.writeAttempted, true);
    assert.equal(await f.helper.close(), result);
    assert.equal(
      f.requests.filter((item) => item.method === 'PATCH').length,
      1,
    );
    const saved = await readFile(f.ledger.filename, 'utf8');
    assert.equal(saved.includes('private token'), false);
    assert.equal(saved.includes(secret), false);
    assert.equal(
      JSON.parse(saved).requests.at(-1).state,
      scenario === 'lost-applied' ? 'confirmed-disabled' : 'outcome-unknown',
    );
  });

test('an unknown hook create is discovered only by its unique callback, never recreated, and deactivated once', async (t) => {
  const f = await fixture(t, { unknownCreate: true });
  await assert.rejects(f.helper.connect(), /reservation is consumed/u);
  await assert.rejects(f.helper.connect(), /reservation is consumed/u);
  assert.equal(f.ledger.blocked, true);
  const closed = await f.helper.close();
  assert.equal(closed.disabled, true);
  assert.equal(closed.hookId, 91);
  assert.equal(f.requests.filter((item) => item.method === 'POST').length, 1);
  assert.equal(f.requests.filter((item) => item.method === 'PATCH').length, 1);
});

test('an empty read after unknown hook creation is not proof that a late creation cannot happen', async (t) => {
  const f = await fixture(t, { unknownCreate: true, hideCreatedHook: true });
  await assert.rejects(f.helper.connect(), /reservation is consumed/u);
  const closed = await f.helper.close();
  assert.equal(closed.disabled, false);
  assert.match(closed.reason, /late creation/u);
  assert.equal(f.requests.filter((item) => item.method === 'PATCH').length, 0);
});

test('a pre-existing callback hook is neither adopted nor deactivated', async (t) => {
  const f = await fixture(t, {
    existingHooks: [
      {
        id: 81,
        config: { url: 'https://hooks.example/api/hooks/github/source' },
      },
    ],
  });
  await assert.rejects(f.helper.connect(), /already has a webhook/u);
  const closed = await f.helper.close();
  assert.equal(closed.state, 'not-created');
  assert.equal(
    f.requests.some((item) => item.method !== 'GET'),
    false,
  );
});

test('unknown GitHub redelivery consumes its single mutation and can only be followed by cleanup', async (t) => {
  const f = await fixture(t, { unknownRedelivery: true });
  await f.helper.connect();
  f.add('issues', issue);
  await f.helper.receive('issues', issue);
  f.add('issue_comment', comment);
  const admitted = await f.helper.receive('issue_comment', comment);
  await assert.rejects(
    f.helper.receive('issue_comment', comment, admitted),
    /reservation is consumed/u,
  );
  await assert.rejects(
    f.helper.receive('issue_comment', comment, admitted),
    /Only one/u,
  );
  assert.equal(
    f.requests.filter((item) => item.route.endsWith('/attempts')).length,
    1,
  );
  assert.equal((await f.helper.close()).disabled, true);
});

for (const scenario of [
  'signature',
  'raw-bytes',
  'header-identity',
  'repository',
  'ping-evaluation',
])
  test(`public evidence rejects ${scenario} without fallback or a repeated hook mutation`, async (t) => {
    const f = await fixture(t, {
      editDelivery: (delivery) => {
        if (scenario === 'signature')
          delivery.request.headers['X-Hub-Signature-256'] =
            `sha256=${'0'.repeat(64)}`;
        if (scenario === 'header-identity')
          delivery.request.headers['X-GitHub-Hook-ID'] = '92';
        if (scenario === 'repository') delivery.repository_id = 43;
      },
      editRaw:
        scenario === 'raw-bytes'
          ? (bytes) => JSON.stringify(JSON.parse(bytes))
          : undefined,
      pingEvaluation: scenario === 'ping-evaluation',
    });
    await assert.rejects(f.helper.connect());
    await f.helper.close();
    assert.equal(f.requests.filter((item) => item.method === 'POST').length, 1);
    assert.equal(
      f.requests.some((item) => item.route.startsWith('/api/hooks/')),
      false,
    );
  });

test('an old successful delivery cannot stand in for a newly requested redelivery', async (t) => {
  const f = await fixture(t, {
    badReplay: ({ delivery }) => {
      delivery.redelivery = false;
    },
  });
  await f.helper.connect();
  f.add('issues', issue);
  await f.helper.receive('issues', issue);
  f.add('issue_comment', comment);
  const admitted = await f.helper.receive('issue_comment', comment);
  await assert.rejects(
    f.helper.receive('issue_comment', comment, admitted),
    /distinct redelivery/u,
  );
  assert.equal(
    f.requests.filter((item) => item.route.endsWith('/attempts')).length,
    1,
  );
});

test('redelivery must retain the GitHub GUID as well as the exact original bytes', async (t) => {
  const f = await fixture(t, {
    badReplay: ({ delivery }) => {
      delivery.guid = '22222222-2222-2222-2222-222222222222';
      delivery.request.headers['X-GitHub-Delivery'] = delivery.guid;
    },
  });
  await f.helper.connect();
  f.add('issues', issue);
  await f.helper.receive('issues', issue);
  f.add('issue_comment', comment);
  const admitted = await f.helper.receive('issue_comment', comment);
  await assert.rejects(
    f.helper.receive('issue_comment', comment, admitted),
    /distinct redelivery/u,
  );
});

test('delivery polling is bounded and a missing ping never causes a local signed substitute', async (t) => {
  for (const options of [{ oversizedDeliveries: true }, { noPing: true }]) {
    const f = await fixture(t, options);
    await assert.rejects(f.helper.connect(), /bounded|Timed out/u);
    assert.equal(f.requests.filter((item) => item.method === 'POST').length, 1);
    assert.equal(f.detailReads, 0);
    assert.equal((await f.helper.close()).disabled, true);
  }
});

test('a failed ledger save prevents hook creation and deactivation dispatch', async (t) => {
  const f = await fixture(t);
  f.ledger.save = async () => {
    throw new Error('disk failure');
  };
  await assert.rejects(f.helper.connect(), /durable confirmed/u);
  assert.equal(
    f.requests.some((item) => item.method !== 'GET'),
    false,
  );
  const closed = await f.helper.close();
  assert.equal(closed.durable, false);
  assert.equal(
    f.requests.some((item) => item.method !== 'GET'),
    false,
  );
});

test('an unjournaled deactivation is never dispatched even for a confirmed active hook', async (t) => {
  const f = await fixture(t);
  await f.helper.connect();
  f.ledger.save = async () => {
    throw new Error('disk unavailable');
  };
  const closed = await f.helper.close();
  assert.equal(closed.disabled, false);
  assert.equal(closed.durable, false);
  assert.equal(closed.writeAttempted, false);
  assert.match(closed.reason, /intent was not durable/u);
  assert.equal(
    f.requests.some((item) => item.method === 'PATCH'),
    false,
  );
});

test('in-progress delivery evidence is refreshed by reads without resubmitting its event', async (t) => {
  let pending = null;
  let reads = 0;
  const f = await fixture(t, {
    onDeliveryRead: () => {
      if (pending && ++reads === 2) pending.delivery.status_code = 200;
    },
  });
  await f.helper.connect();
  pending = f.add('issues', issue);
  pending.delivery.status_code = 0;
  const received = await f.helper.receive('issues', issue);
  assert.equal(received.eventId, pending.eventId);
  assert.equal(reads, 2);
  assert.equal(f.requests.filter((item) => item.method === 'POST').length, 1);
});
