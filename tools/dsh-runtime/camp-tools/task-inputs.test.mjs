import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readTaskInputs } from './task-inputs.mjs';

test('task input tool keeps scope fixed, bounds requests and preserves original fields', async () => {
  const page = {
    items: [
      {
        seq: 1,
        id: 'original',
        authorName: 'Person',
        content: '<original>',
        createdAt: 'unchanged',
      },
    ],
    through: 3,
    nextAfter: 1,
    hasMore: true,
  };
  let requests = 0;
  const bridge = {
    request: async (path, body) => {
      requests++;
      assert.equal(path, '/task-inputs');
      assert.deepEqual(body, { after: 0, limit: 20 });
      return new globalThis.Response(JSON.stringify({ value: page }));
    },
  };
  assert.deepEqual(await readTaskInputs(bridge, { after: 0 }), page);
  for (const args of [
    { after: -1 },
    { after: 1.5 },
    { after: 0, limit: 21 },
    { after: 0, taskId: 'other' },
    { after: 0, url: 'https://outside' },
  ])
    await assert.rejects(readTaskInputs(bridge, args));
  assert.equal(requests, 1);
  await assert.rejects(
    readTaskInputs(
      {
        request: async () =>
          new globalThis.Response(JSON.stringify({ error: 'revoked' }), {
            status: 410,
          }),
      },
      { after: 0 },
    ),
    /unavailable/u,
  );
  await assert.rejects(
    readTaskInputs(
      { request: async () => new globalThis.Response(' '.repeat(65580)) },
      { after: 0 },
    ),
    /size limit/u,
  );
});
