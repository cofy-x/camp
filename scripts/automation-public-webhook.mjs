// Public acceptance observes GitHub's own deliveries. It never sends a local
// signed substitute or retries a mutation whose outcome is unknown.
import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';

const maxDeliveries = 32;
const maxPayloadBytes = 1 << 20;
const identifier = /^[a-zA-Z0-9_-]{1,128}$/u;
const guidPattern = /^[a-f\d]{8}(?:-[a-f\d]{4}){3}-[a-f\d]{12}$/iu;

function check(value, message) {
  if (!value) throw new Error(message);
}

function positive(value) {
  return Number.isSafeInteger(value) && value > 0;
}

function header(headers, name) {
  const entries = Object.entries(headers ?? {}).filter(
    ([key]) => key.toLowerCase() === name.toLowerCase(),
  );
  check(
    entries.length === 1 && typeof entries[0][1] === 'string',
    'GitHub delivery headers are incomplete or ambiguous.',
  );
  return entries[0][1];
}

export function createPublicWebhook({
  github,
  ledger,
  api,
  report,
  save,
  wait,
  preflight,
  publicOrigin,
  secret,
}) {
  const origin = new URL(publicOrigin);
  check(
    origin.protocol === 'https:' &&
      origin.origin === publicOrigin &&
      !origin.username &&
      !origin.password &&
      !origin.search &&
      !origin.hash &&
      identifier.test(report.sourceId) &&
      identifier.test(report.roomId) &&
      positive(report.repositoryId) &&
      positive(report.githubActorId) &&
      typeof secret === 'string' &&
      Buffer.byteLength(secret) >= 32 &&
      Buffer.byteLength(secret) <= 256,
    'Public webhook acceptance needs an HTTPS origin and a saved source.',
  );
  const url = `${origin.origin}/api/hooks/github/${report.sourceId}`;
  const route = `repos/${report.repository}/hooks`;
  const state = (report.webhook = {
    url,
    limits: { creates: 1, redeliveries: 1, deactivations: 1 },
    deliveries: [],
  });
  const intent = (ledger.value.publicWebhook = {
    url,
    limits: state.limits,
    createAttempted: false,
    redeliveryAttempted: false,
    deactivateAttempted: false,
  });
  const details = new Map();
  let baselineIds = new Set();
  let issueId;
  let issueNumber;
  let connecting;
  let closing;

  async function hooks() {
    const values = await github(`${route}?per_page=100`);
    check(
      Array.isArray(values) && values.length <= maxDeliveries,
      'The repository webhook list exceeded its bounded acceptance scope.',
    );
    return values;
  }

  async function deliveries() {
    check(positive(state.id), 'The owned webhook identity is unavailable.');
    const values = await github(`${route}/${state.id}/deliveries?per_page=100`);
    check(
      Array.isArray(values) &&
        values.length <= maxDeliveries &&
        values.every((item) => positive(item.id)),
      'The webhook delivery history exceeded its bounded acceptance scope.',
    );
    return values;
  }

  async function detail(item) {
    if (details.has(item.id)) return details.get(item.id);
    const value = await github(`${route}/${state.id}/deliveries/${item.id}`);
    check(
      value.id === item.id &&
        guidPattern.test(value.guid) &&
        value.repository_id === report.repositoryId &&
        Buffer.byteLength(JSON.stringify(value)) <= 2 * maxPayloadBytes,
      'GitHub returned inconsistent or oversized delivery evidence.',
    );
    if (positive(value.status_code) && value.response)
      details.set(item.id, value);
    return value;
  }

  function matches(payload, kind, object) {
    if (
      payload?.repository?.id !== report.repositoryId ||
      payload.repository.full_name?.toLowerCase() !==
        report.repository.toLowerCase()
    )
      return false;
    if (kind === 'ping') return payload.hook_id === state.id;
    const subject = kind === 'issues' ? payload.issue : payload.comment;
    return (
      subject?.id === object.id &&
      payload.action === (kind === 'issues' ? 'opened' : 'created') &&
      payload.sender?.id === object.user?.id &&
      subject.user?.id === object.user?.id &&
      subject.body === object.body &&
      payload.issue?.number ===
        (kind === 'issues' ? object.number : issueNumber) &&
      (kind === 'issues' || payload.issue?.id === issueId)
    );
  }

  async function canonicalEvent(eventId) {
    let after = 0;
    let through;
    for (let page = 0; page < 3; page++) {
      const query = new URLSearchParams({
        sourceId: report.sourceId,
        after: String(after),
        limit: '20',
      });
      if (through !== undefined) query.set('through', String(through));
      const result = await api(
        `/api/rooms/${report.roomId}/automation-events?${query}`,
      );
      const found = result.items.find(
        (item) => item.externalEventId === eventId,
      );
      if (found) return found;
      if (!result.hasMore) return null;
      through ??= result.through;
      check(
        Number.isSafeInteger(result.nextAfter) && result.nextAfter > after,
        'The canonical event cursor did not advance.',
      );
      after = result.nextAfter;
    }
    throw new Error(
      'The canonical event history exceeded its acceptance bound.',
    );
  }

  async function receiveDelivery(kind, object, excluded = new Set(), original) {
    return wait(`public GitHub ${kind} delivery`, async () => {
      const candidates = (await deliveries()).filter(
        (item) => item.event === kind && !excluded.has(item.id),
      );
      for (const item of candidates) {
        const delivery = await detail(item);
        if (!matches(delivery.request?.payload, kind, object)) continue;
        if (!positive(delivery.status_code)) continue;
        check(
          delivery.status_code === 200 && delivery.url === url,
          'GitHub did not confirm the public callback with HTTP 200.',
        );
        if (original)
          check(
            delivery.redelivery === true &&
              delivery.id !== original.githubDeliveryId &&
              delivery.guid === original.guid,
            'GitHub did not record a distinct redelivery attempt.',
          );
        const headers = delivery.request.headers;
        check(
          header(headers, 'X-GitHub-Delivery') === delivery.guid &&
            header(headers, 'X-GitHub-Event') === kind &&
            header(headers, 'X-GitHub-Hook-ID') === String(state.id),
          'GitHub delivery identity headers differ from the owned hook.',
        );
        let response;
        try {
          response = JSON.parse(delivery.response.payload);
        } catch {
          throw new Error('The public callback receipt is unreadable.');
        }
        check(
          identifier.test(response?.eventId) &&
            response.duplicate === Boolean(original) &&
            (!original || response.eventId === original.eventId),
          'The public callback did not return the expected immutable receipt.',
        );
        const { event } = await api(`/api/github-events/${response.eventId}`);
        check(
          event.id === response.eventId &&
            event.sourceId === report.sourceId &&
            event.roomId === report.roomId &&
            event.repositoryId === report.repositoryId &&
            event.kind === kind &&
            (kind === 'ping' ||
              (event.objectId === object.id &&
                event.number ===
                  (kind === 'issues' ? object.number : issueNumber))),
          'The retained Camp event differs from the actual GitHub object.',
        );
        const bytes = await api(
          `/api/github-events/${response.eventId}/payload`,
          'GET',
          undefined,
          true,
        );
        check(
          typeof bytes === 'string' &&
            Buffer.byteLength(bytes) <= maxPayloadBytes,
          'The retained original webhook payload exceeds its bound.',
        );
        const fingerprint = createHash('sha256').update(bytes).digest('hex');
        const signature = header(headers, 'X-Hub-Signature-256');
        const expected = `sha256=${createHmac('sha256', secret).update(bytes).digest('hex')}`;
        check(
          /^sha256=[a-f\d]{64}$/iu.test(signature) &&
            timingSafeEqual(Buffer.from(signature), Buffer.from(expected)) &&
            event.fingerprint === fingerprint &&
            (!original || original.bytes === bytes),
          "The retained payload does not match GitHub's exact signed bytes.",
        );
        let payload;
        try {
          payload = JSON.parse(bytes);
        } catch {
          throw new Error(
            'The retained original is not a valid GitHub payload.',
          );
        }
        check(
          isDeepStrictEqual(payload, delivery.request.payload) &&
            matches(payload, kind, object),
          'The retained original differs from GitHub delivery evidence.',
        );
        if (kind === 'ping') {
          const canonical = await canonicalEvent(response.eventId);
          check(
            event.status === 'ignored' &&
              canonical?.status === 'ignored' &&
              !canonical.evaluationId,
            'The automatic hook ping entered assessment or execution.',
          );
        }
        const evidence = {
          githubDeliveryId: delivery.id,
          guid: delivery.guid,
          eventId: response.eventId,
          kind,
          externalObjectId: object?.id ?? 0,
          statusCode: delivery.status_code,
          redelivery: delivery.redelivery === true,
          duplicate: response.duplicate,
          payloadSHA256: fingerprint,
          payloadBytes: Buffer.byteLength(bytes),
        };
        if (
          !state.deliveries.some(
            (entry) => entry.githubDeliveryId === delivery.id,
          )
        ) {
          check(
            state.deliveries.length < maxDeliveries,
            'Too many public delivery receipts.',
          );
          state.deliveries.push(evidence);
        }
        if (
          !report.sourceEvents.some(
            (entry) => entry.eventId === response.eventId,
          )
        )
          report.sourceEvents.push({
            eventId: response.eventId,
            deliveryId: delivery.guid,
            githubDeliveryId: delivery.id,
            payloadSHA256: fingerprint,
            kind,
            externalObjectId: object?.id ?? 0,
          });
        await save();
        return {
          eventId: response.eventId,
          guid: delivery.guid,
          githubDeliveryId: delivery.id,
          duplicate: response.duplicate,
          bytes,
        };
      }
      return null;
    });
  }

  function connect() {
    return (connecting ??= (async () => {
      await preflight();
      // Persist source/URL ownership before a hook can cause any callback.
      await save();
      const existing = await hooks();
      check(
        !existing.some((hook) => hook.config?.url === url),
        'This callback already has a webhook; no hook was created or adopted.',
      );
      baselineIds = new Set(existing.map((hook) => hook.id));
      intent.createAttempted = true;
      const hook = await ledger.execute(
        'create-public-webhook',
        { method: 'POST', route: `github:${route}`, callbackURL: url },
        () =>
          github(route, 'POST', {
            name: 'web',
            active: true,
            events: ['issues', 'issue_comment'],
            config: { url, content_type: 'json', insecure_ssl: '0', secret },
          }),
      );
      check(
        positive(hook.id) &&
          !baselineIds.has(hook.id) &&
          hook.config?.url === url &&
          hook.active === true &&
          hook.config.content_type === 'json' &&
          String(hook.config.insecure_ssl) === '0' &&
          Array.isArray(hook.events) &&
          hook.events.length === 2 &&
          hook.events.includes('issues') &&
          hook.events.includes('issue_comment'),
        'GitHub did not confirm the unique active acceptance webhook.',
      );
      state.id = intent.hookId = hook.id;
      await ledger.save();
      await save();
      return receiveDelivery('ping');
    })());
  }

  async function receive(kind, object, original) {
    check(
      ['issues', 'issue_comment'].includes(kind) &&
        positive(object?.id) &&
        positive(object.user?.id) &&
        object.user.id === report.githubActorId &&
        (kind !== 'issues' || positive(object.number)) &&
        (kind !== 'issue_comment' ||
          (positive(issueId) && positive(issueNumber))),
      'Use an actual acceptance issue or comment in its verified conversation.',
    );
    await connecting;
    check(positive(state.id), 'Connect the owned public webhook first.');
    if (kind === 'issues') {
      check(
        !issueId || issueId === object.id,
        'The acceptance issue identity changed.',
      );
      issueId = object.id;
      issueNumber = object.number;
    }
    let excluded;
    if (original) {
      check(
        !intent.redeliveryAttempted &&
          positive(original.githubDeliveryId) &&
          state.deliveries.some(
            (entry) =>
              entry.githubDeliveryId === original.githubDeliveryId &&
              entry.eventId === original.eventId,
          ),
        'Only one confirmed original delivery may be explicitly redelivered.',
      );
      await preflight();
      excluded = new Set((await deliveries()).map((item) => item.id));
      intent.redeliveryAttempted = true;
      const redeliveryRoute = `${route}/${state.id}/deliveries/${original.githubDeliveryId}/attempts`;
      await ledger.execute(
        'redeliver-public-webhook',
        {
          method: 'POST',
          route: `github:${redeliveryRoute}`,
          eventId: original.eventId,
          githubDeliveryId: original.githubDeliveryId,
        },
        () => github(redeliveryRoute, 'POST', undefined, true),
      );
    }
    return receiveDelivery(kind, object, excluded, original);
  }

  function close() {
    return (closing ??= (async () => {
      ledger.blocked = true;
      const receipt = {
        disabled: false,
        durable: false,
        writeAttempted: false,
        state: 'unknown',
        startedAt: new Date().toISOString(),
      };
      state.cleanup = intent.cleanup = receipt;

      async function persist() {
        receipt.finishedAt = new Date().toISOString();
        receipt.durable = true;
        try {
          await ledger.save();
          await save();
        } catch {
          receipt.durable = false;
        }
        return receipt;
      }

      if (!intent.createAttempted) {
        receipt.disabled = true;
        receipt.state = 'not-created';
        return persist();
      }
      let hook;
      try {
        if (state.id) hook = await github(`${route}/${state.id}`);
        else {
          const found = (await hooks()).filter(
            (item) => item.config?.url === url && !baselineIds.has(item.id),
          );
          check(
            found.length <= 1,
            'The unconfirmed hook identity is ambiguous.',
          );
          hook = found[0];
        }
        if (!hook) {
          // An unanswered creation can still be in flight. One empty read is
          // not proof that GitHub will never commit that earlier request.
          receipt.reason =
            'The unconfirmed webhook was not found; a late creation cannot be excluded.';
          return persist();
        }
        check(
          positive(hook.id) &&
            hook.config?.url === url &&
            !baselineIds.has(hook.id),
          'The webhook ownership changed; no deactivation was sent.',
        );
      } catch {
        receipt.reason =
          'The owned webhook could not be identified; no deactivation was sent.';
        return persist();
      }
      state.id = intent.hookId = receipt.hookId = hook.id;
      if (hook.active === false) {
        receipt.disabled = true;
        receipt.state = 'already-disabled';
        receipt.confirmedBy = 'readback';
        return persist();
      }
      intent.deactivateAttempted = true;
      const reservation = {
        sequence: ledger.value.requests.length + 1,
        kind: 'deactivate-public-webhook',
        method: 'PATCH',
        route: `github:${route}/${hook.id}`,
        body: { active: false },
        reservedAt: new Date().toISOString(),
        cost: {},
        state: 'reserved-outcome-unknown',
      };
      ledger.value.requests.push(reservation);
      try {
        await ledger.save();
      } catch {
        receipt.reason =
          'The deactivation intent was not durable; no request was sent.';
        return persist();
      }
      receipt.writeAttempted = true;
      try {
        await github(`${route}/${hook.id}`, 'PATCH', { active: false });
      } catch {
        receipt.responseUnknown = true;
      }
      try {
        const current = await github(`${route}/${hook.id}`);
        if (
          current.id === hook.id &&
          current.active === false &&
          current.config?.url === url
        ) {
          receipt.disabled = true;
          receipt.state = 'disabled';
          receipt.confirmedBy = receipt.responseUnknown
            ? 'readback-after-unknown'
            : 'readback';
          reservation.state = 'confirmed-disabled';
        } else {
          receipt.reason =
            'The webhook remains active or changed; no second deactivation was sent.';
          reservation.state = 'outcome-unknown';
        }
      } catch {
        receipt.reason =
          'Webhook deactivation could not be verified; no request was retried.';
        reservation.state = 'outcome-unknown';
      }
      return persist();
    })());
  }

  return { connect, receive, close };
}
