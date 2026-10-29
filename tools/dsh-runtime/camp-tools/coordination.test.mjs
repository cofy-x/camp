import assert from 'node:assert/strict';
import { test } from 'node:test';
import process from 'node:process';
import { apply } from './coordination.mjs';

test('coordination acknowledges durable intent before concluding and blocks extra model steps', async (t) => {
  const previous = {
    endpoint: process.env.CAMP_COORDINATION_ENDPOINT,
    token: process.env.CAMP_COORDINATION_TOKEN,
  };
  process.env.CAMP_COORDINATION_ENDPOINT = 'http://127.0.0.1:1234';
  process.env.CAMP_COORDINATION_TOKEN =
    'private-coordination-capability-kept-out-of-schema';
  t.after(() => {
    for (const [key, value] of [
      ['CAMP_COORDINATION_ENDPOINT', previous.endpoint],
      ['CAMP_COORDINATION_TOKEN', previous.token],
    ]) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
  let gate;
  const definitions = [];
  const calls = [];
  let fail = true;
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    calls.push(JSON.parse(options.body));
    if (fail)
      return new globalThis.Response(
        JSON.stringify({ error: { message: 'Could not save.' } }),
        { status: 503 },
      );
    return new globalThis.Response(
      JSON.stringify({ value: { accepted: true } }),
    );
  });
  apply({
    tools: {
      register: (definition) => {
        definitions.push(definition);
        return () => {};
      },
    },
    effect: (create) => create(),
    on: (_name, callback) => {
      gate = callback;
    },
  });
  assert.deepEqual(
    definitions.map((item) => item.name),
    ['camp_handoff', 'camp_ask_person', 'camp_finish'],
  );
  assert.equal(
    JSON.stringify(definitions).includes(process.env.CAMP_COORDINATION_TOKEN),
    false,
  );
  let concluded = 0;
  let nextSteps = 0;
  const exec = {
    callId: 'call-1',
    signal: new globalThis.AbortController().signal,
    concludeTurn: () => {
      concluded++;
    },
  };
  await gate({}, async () => {
    nextSteps++;
    return { kind: 'enter', messages: ['initial'] };
  });
  await assert.rejects(
    definitions[2].execute({ summary: 'Done' }, exec),
    /Could not save/u,
  );
  assert.equal(concluded, 0);
  fail = false;
  assert.deepEqual(await definitions[2].execute({ summary: 'Done' }, exec), {
    accepted: true,
  });
  assert.equal(concluded, 1);
  assert.deepEqual(calls[1], {
    summary: 'Done',
    artifactPaths: [],
    callId: 'call-1',
    kind: 'finish',
  });
  assert.deepEqual(
    await gate({}, async () => {
      nextSteps++;
    }),
    { kind: 'enter', messages: [] },
  );
  assert.equal(nextSteps, 1);
});
