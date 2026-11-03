import assert from 'node:assert/strict';
import { test } from 'node:test';
import { configureRestartResource } from './runner-restart-resource.mjs';

const environment = {
  kind: 'command',
  checks: [{ name: 'native-byte-check', arguments: ['node', 'check.mjs'] }],
};

test('restart checks use the freshly read resource revision in one conditional update', async () => {
  const calls = [];
  const saved = { resource: { revision: 38, environment } };
  const api = async (route, method = 'GET', body) => {
    calls.push({ route, method, body });
    if (method === 'GET') return { resource: { revision: 37 } };
    assert.equal(method, 'PATCH');
    assert.equal(body.expectedRevision, 37);
    return saved;
  };
  assert.equal(
    await configureRestartResource(api, 'controlled-resource', environment),
    saved,
  );
  assert.deepEqual(calls, [
    {
      route: '/api/resources/controlled-resource',
      method: 'GET',
      body: undefined,
    },
    {
      route: '/api/resources/controlled-resource',
      method: 'PATCH',
      body: { expectedRevision: 37, environment },
    },
  ]);
});

test('missing or invalid resource revisions stop before settings or task writes', async () => {
  for (const response of [
    undefined,
    {},
    { resource: null },
    ...[undefined, null, 0, -1, 1.5, '37', Number.MAX_SAFE_INTEGER + 1].map(
      (revision) => ({ resource: { revision } }),
    ),
  ]) {
    const calls = [];
    const api = async (route, method = 'GET') => {
      calls.push({ route, method });
      return response;
    };
    await assert.rejects(
      configureRestartResource(api, 'controlled-resource', environment),
      /Read a current positive resource revision/,
    );
    assert.deepEqual(calls, [
      { route: '/api/resources/controlled-resource', method: 'GET' },
    ]);
  }
});

test('resource read failure or settings conflict propagates without retries or task dispatch', async () => {
  for (const failedMethod of ['GET', 'PATCH']) {
    const failure = Object.assign(new Error('Controlled API refusal.'), {
      status: failedMethod === 'PATCH' ? 409 : 403,
      code: failedMethod === 'PATCH' ? 'revision_conflict' : 'forbidden',
    });
    const calls = [];
    const api = async (route, method = 'GET') => {
      calls.push({ route, method });
      if (method === failedMethod) throw failure;
      return { resource: { revision: 37 } };
    };
    const dispatch = async () => {
      await configureRestartResource(api, 'controlled-resource', environment);
      await api('/api/rooms/controlled-room/messages', 'POST');
    };
    await assert.rejects(dispatch(), (error) => error === failure);
    assert.deepEqual(
      calls.map(({ method }) => method),
      failedMethod === 'GET' ? ['GET'] : ['GET', 'PATCH'],
    );
    assert.ok(
      calls.every(
        ({ route }) => route === '/api/resources/controlled-resource',
      ),
    );
  }
});
