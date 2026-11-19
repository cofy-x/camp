import assert from 'node:assert/strict';
import { setTimeout as pause } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';
import {
  deliveryIdentifier,
  parseGitHubJSON,
} from './automation-public-webhook.mjs';
import { ghCommand } from './verify-automation-real.mjs';
import { client, normalizeOrigin, readAccess } from './verify-remote-real.mjs';

const pageSize = 100;
const guidPattern = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/iu;
const identifier = /^[a-zA-Z0-9_-]{1,128}$/u;
const transportFailures = new Set([
  'timed out',
  'failed to connect',
  'failed to connect to host',
  'failed to connect to network',
  'peer certificate cannot be authenticated with given ca certificates',
  'invalid http response',
]);
const usage =
  'Usage: node scripts/reconcile-github-source.mjs ORIGIN ACCESS_JSON ROOM_ID SOURCE_ID HOOK_ID PUBLIC_ORIGIN [--recover DELIVERY_ID]';

const positiveID = (id) => Number.isSafeInteger(id) && id > 0;

function latestAttempts(attempts) {
  assert.ok(Array.isArray(attempts) && attempts.length <= pageSize);
  const latest = new Map();
  for (const row of attempts) {
    assert.ok(guidPattern.test(row.guid));
    const id = deliveryIdentifier(row.id);
    const order = BigInt(id);
    const time = Date.parse(row.delivered_at);
    assert.ok(
      Number.isFinite(time) &&
        (row.status_code === null || Number.isInteger(row.status_code)),
    );
    const guid = row.guid.toLowerCase();
    const previous = latest.get(guid);
    const code = row.status_code;
    const succeeded = row.status === 'OK' || (code >= 200 && code < 300);
    if (
      previous &&
      (time < previous.time ||
        (time === previous.time && order <= previous.order))
    ) {
      previous.reportedSuccess ||= succeeded;
      continue;
    }
    const failed =
      (code >= 400 && code < 600) ||
      ((code === 0 || code === null) &&
        transportFailures.has(row.status?.toLowerCase()));
    latest.set(guid, {
      id,
      order,
      guid,
      time,
      deliveredAt: row.delivered_at,
      statusCode: code,
      reportedSuccess: succeeded || Boolean(previous?.reportedSuccess),
      failed,
    });
  }
  return [...latest.values()].sort((left, right) => {
    if (left.time !== right.time) return right.time - left.time;
    if (left.order === right.order) return 0;
    return left.order > right.order ? -1 : 1;
  });
}

function applyReceipts(deliveries, receipts) {
  const camp = new Map(receipts.map((row) => [row.deliveryId, row]));
  return deliveries.map(
    ({ id, guid, deliveredAt, statusCode, reportedSuccess, failed }) => {
      const receipt = camp.get(guid);
      const { eventId, preparedAt } = receipt ?? {};
      let status = 'delivery_unconfirmed';
      if (eventId) status = 'recorded';
      else if (preparedAt) status = 'prepared';
      else if (reportedSuccess) status = 'receipt_missing_after_success';
      else if (failed) status = 'failed_without_receipt';
      return { id, guid, deliveredAt, statusCode, status, eventId, preparedAt };
    },
  );
}

export function compareDeliveries(attempts, receipts) {
  return applyReceipts(latestAttempts(attempts), receipts);
}

async function github(route, method = 'GET') {
  const body = await ghCommand([
    'api',
    '--hostname',
    'github.com',
    '--method',
    method,
    route,
  ]);
  try {
    return body.trim() === '' ? null : parseGitHubJSON(body);
  } catch {
    throw new Error('The GitHub response could not be read.');
  }
}

function verifyHook(hook, id, callback) {
  assert.ok(
    hook?.id === id && hook.active === true && hook.config?.url === callback,
    'The GitHub hook must be active and point to the exact Camp source.',
  );
}

async function readReceipts(api, route, deliveries) {
  if (deliveries.length === 0) return [];
  const params = new URLSearchParams();
  for (const guid of new Set(deliveries)) params.append('delivery', guid);
  const { receipts } = await api(route + '?' + params);
  return receipts;
}

export async function reconcileSource(
  { roomId, sourceId, hookId, publicOrigin, recoveryId },
  { api, provider = github, wait = pause },
) {
  const { sources } = await api('/api/rooms/' + roomId + '/github-sources');
  const source = sources.find((entry) => entry.id === sourceId);
  assert.ok(
    source?.enabled && positiveID(source.repositoryId),
    'Verify the enabled Camp source with a signed delivery first.',
  );
  assert.match(source.repository, /^[a-zA-Z0-9-]+\/[a-zA-Z0-9_.-]+$/u);
  const repositoryPath = 'repos/' + source.repository;
  const hookPath = repositoryPath + '/hooks/' + hookId;
  const callback = publicOrigin + '/api/hooks/github/' + sourceId;
  const [repo, hook] = await Promise.all([
    provider(repositoryPath),
    provider(hookPath),
  ]);
  assert.equal(
    repo.id,
    source.repositoryId,
    'GitHub repository identity changed.',
  );
  verifyHook(hook, hookId, callback);

  const attempts = await provider(
    hookPath + '/deliveries?per_page=' + pageSize,
  );
  // Validate the bounded provider snapshot before expanding query parameters.
  const latest = latestAttempts(attempts);
  const route = '/api/github-sources/' + sourceId + '/recovery';
  const receipts = await readReceipts(
    api,
    route,
    latest.map((row) => row.guid),
  );
  const deliveries = applyReceipts(latest, receipts);
  const coverage = {
    checkedAt: new Date().toISOString(),
    recentAttempts: attempts.length,
    pageLimit: pageSize,
    pageLimitReached: attempts.length === pageSize,
    oldestObserved:
      attempts.length === 0
        ? undefined
        : attempts.reduce(
            (oldest, row) =>
              Date.parse(row.delivered_at) < Date.parse(oldest)
                ? row.delivered_at
                : oldest,
            attempts[0].delivered_at,
          ),
  };
  const report = {
    repository: source.repository,
    sourceId,
    hookId,
    coverage,
    deliveries,
  };
  if (recoveryId === undefined) return report;

  const exactID = deliveryIdentifier(recoveryId);
  const target = deliveries.find((row) => row.id === exactID);
  assert.ok(
    target,
    'Choose the latest attempt for a GUID from this bounded report.',
  );
  if (target.status === 'recorded' || target.status === 'prepared')
    return { ...report, recovery: target };
  assert.equal(
    target.status,
    'failed_without_receipt',
    'Verify the provider outcome before attempting another delivery.',
  );
  const deliveryPath = hookPath + '/deliveries/' + target.id;
  const [currentHook, original] = await Promise.all([
    provider(hookPath),
    provider(deliveryPath),
  ]);
  verifyHook(currentHook, hookId, callback);
  assert.ok(
    currentHook.config.content_type === 'json' &&
      ['0', 0].includes(currentHook.config.insecure_ssl),
    'Configure the GitHub hook to send JSON and verify HTTPS before recovery.',
  );
  assert.ok(
    original &&
      deliveryIdentifier(original.id) === target.id &&
      original.guid?.toLowerCase() === target.guid &&
      original.repository_id === source.repositoryId &&
      original.url === callback,
    'The historical delivery must belong to this repository and exact Camp callback.',
  );
  const { prepared } = await api(route, 'POST', {
    revision: source.revision,
    deliveryId: target.guid,
  });
  if (!prepared) {
    const [current] = await readReceipts(api, route, [target.guid]);
    return {
      ...report,
      recovery: {
        ...target,
        ...current,
        status: current.eventId ? 'recorded' : 'prepared',
      },
    };
  }

  // Camp committed the observation-only reservation. An uncertain POST is
  // followed by receipt reads. Another invocation cannot repeat the POST.
  try {
    await provider(deliveryPath + '/attempts', 'POST');
  } catch {
    // The remote request may already have been accepted.
  }
  for (let check = 0; check < 5; check++) {
    const [current] = await readReceipts(api, route, [target.guid]);
    if (current.eventId)
      return {
        ...report,
        recovery: { ...target, ...current, status: 'recorded' },
      };
    if (check < 4) await wait(2000);
  }
  return { ...report, recovery: { ...target, status: 'awaiting_receipt' } };
}

async function main(args) {
  const [address, accessFile, roomId, sourceId, hook, ingress, flag, delivery] =
    args;
  const hookId = Number(hook);
  assert.notEqual(
    process.env.NODE_TLS_REJECT_UNAUTHORIZED,
    '0',
    'TLS certificate verification must remain enabled.',
  );
  assert.ok(
    (args.length === 6 || (args.length === 8 && flag === '--recover')) &&
      identifier.test(roomId) &&
      identifier.test(sourceId) &&
      positiveID(hookId),
    usage,
  );
  const recoveryId =
    delivery === undefined ? undefined : deliveryIdentifier(delivery);
  const origin = normalizeOrigin(address);
  const publicOrigin = normalizeOrigin(ingress);
  assert.equal(
    new URL(publicOrigin).protocol,
    'https:',
    'Provide the public HTTPS webhook origin.',
  );
  const access = await readAccess(accessFile, origin);
  const api = client(origin);
  const { user } = await api('/api/auth/login', 'POST', access);
  assert.equal(
    user.role,
    'admin',
    'Use a Camp administrator account to check webhook receipts.',
  );
  const report = await reconcileSource(
    { roomId, sourceId, hookId, publicOrigin, recoveryId },
    { api },
  );
  process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  if (report.recovery && report.recovery.status !== 'recorded')
    process.exitCode = 2;
}

if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write('Source check stopped. ' + error.message + '\n');
    process.exitCode = 1;
  });
}
