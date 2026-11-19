import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  compareDeliveries,
  reconcileSource,
} from './reconcile-github-source.mjs';

const guid = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee';
const other = '11111111-bbbb-cccc-dddd-eeeeeeeeeeee';
const settings = {
  roomId: 'room',
  sourceId: 'source',
  hookId: 31,
  publicOrigin: 'https://camp.example.test',
};
const attempt = (id, guid, status_code) => ({
  id,
  guid,
  status_code,
  delivered_at: `2026-11-19T00:00:0${id}Z`,
});

test('checks Camp receipts and latest attempts for each GUID', () => {
  const report = compareDeliveries(
    [attempt(2, guid, 503), attempt(3, other, 200), attempt(1, guid, 503)],
    [{ deliveryId: guid, eventId: 'committed' }],
  );
  assert.deepEqual(
    report.map((row) => [row.id, row.status, row.eventId]),
    [
      [3, 'receipt_missing_after_success', undefined],
      [2, 'recorded', 'committed'],
    ],
  );
});

test('an earlier provider success makes the missing receipt a discrepancy', () => {
  const report = compareDeliveries(
    [
      attempt(2, guid, 503),
      { ...attempt(3, other, null), status: 'Timed out' },
      { ...attempt(1, guid, 200), status: 'OK' },
    ],
    [],
  );
  assert.deepEqual(
    report.map((row) => [row.id, row.status]),
    [
      [3, 'failed_without_receipt'],
      [2, 'receipt_missing_after_success'],
    ],
  );
});

function fixture() {
  const writes = [];
  let prepared = false;
  let received = false;
  const provider = async (route, method = 'GET') => {
    if (route === 'repos/acme/project') return { id: 17 };
    if (route === 'repos/acme/project/hooks/31')
      return {
        id: 31,
        active: true,
        config: { url: settings.publicOrigin + '/api/hooks/github/source' },
      };
    if (route === 'repos/acme/project/hooks/31/deliveries?per_page=100')
      return [attempt(2, guid, 503), attempt(1, guid, 500)];
    assert.equal(route, 'repos/acme/project/hooks/31/deliveries/2/attempts');
    assert.equal(method, 'POST');
    assert.ok(prepared);
    writes.push('redeliver');
    throw new Error('lost reply');
  };
  const api = async (route, method = 'GET', body) => {
    if (route === '/api/rooms/room/github-sources')
      return {
        sources: [
          {
            id: 'source',
            enabled: true,
            repositoryId: 17,
            repository: 'acme/project',
            revision: 6,
          },
        ],
      };
    if (method === 'POST') {
      assert.equal(route, '/api/github-sources/source/recovery');
      assert.deepEqual(body, { revision: 6, deliveryId: guid });
      writes.push('prepare');
      const fresh = !prepared;
      prepared = true;
      return { prepared: fresh };
    }
    assert.equal(route, '/api/github-sources/source/recovery?delivery=' + guid);
    return {
      receipts: [
        {
          deliveryId: guid,
          preparedAt: prepared ? '2026-11-19T00:00:04Z' : undefined,
          eventId: received ? 'committed' : undefined,
        },
      ],
    };
  };
  return {
    deps: { provider, api, wait: async () => {} },
    writes,
    receive() {
      received = true;
    },
  };
}

test('scan is read-only and a lost recovery reply never sends again', async () => {
  const f = fixture();
  const scan = await reconcileSource(settings, f.deps);
  assert.equal(scan.deliveries[0].status, 'failed_without_receipt');
  assert.deepEqual(f.writes, []);
  await assert.rejects(
    reconcileSource({ ...settings, recoveryId: 1 }, f.deps),
    /latest attempt/u,
  );
  const target = { ...settings, recoveryId: 2 };
  assert.equal(
    (await reconcileSource(target, f.deps)).recovery.status,
    'awaiting_receipt',
  );
  assert.equal(
    (await reconcileSource(target, f.deps)).recovery.status,
    'prepared',
  );
  f.receive();
  assert.equal(
    (await reconcileSource(target, f.deps)).recovery.eventId,
    'committed',
  );
  assert.deepEqual(f.writes, ['prepare', 'redeliver']);
});

test('uncertain dispatch can be resolved by the exact Camp receipt', async () => {
  const f = fixture();
  const result = await reconcileSource(
    { ...settings, recoveryId: 2 },
    {
      ...f.deps,
      wait: async () => f.receive(),
    },
  );
  assert.equal(result.recovery.status, 'recorded');
  assert.deepEqual(f.writes, ['prepare', 'redeliver']);
});
