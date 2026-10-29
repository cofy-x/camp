import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { BridgeClient, boundedText } from './transport.mjs';
import { CampCommand, resolveCommand } from './shell.mjs';
import { listFiles, searchFiles } from './discovery.mjs';
import { toolPatch } from './patch.mjs';

const capability = 'fixture-capability-kept-out-of-model-context';

test('bridge accepts only loopback origins and never prints capabilities', () => {
  for (const endpoint of [
    'https://example.com',
    'http://localhost:1234',
    'http://127.0.0.1:1234/path',
    'http://user:pass@127.0.0.1:1234',
    'invalid',
  ]) {
    assert.throws(
      () =>
        new BridgeClient({
          CAMP_TOOL_ENDPOINT: endpoint,
          CAMP_TOOL_TOKEN: capability,
        }),
      (error) =>
        !error.message.includes(capability) &&
        !error.message.includes(endpoint),
    );
  }
  assert.doesNotThrow(
    () =>
      new BridgeClient({
        CAMP_TOOL_ENDPOINT: 'http://127.0.0.1:1234',
        CAMP_TOOL_TOKEN: capability,
      }),
  );
  assert.throws(
    () =>
      new BridgeClient({
        CAMP_TOOL_ENDPOINT: 'http://127.0.0.1:1234',
        CAMP_TOOL_TOKEN: 'short',
      }),
  );
});

test('bridge authenticates, preserves file error identity and refuses redirects', async () => {
  let requests = 0;
  const server = createServer((request, response) => {
    requests++;
    assert.equal(request.headers.authorization, `Bearer ${capability}`);
    if (request.url === '/redirect') {
      response.writeHead(302, { location: '/unwanted' });
      response.end();
      return;
    }
    response.end(
      JSON.stringify({
        error: {
          code: 'FS_STALE_VERSION',
          message: 'File changed since it was read.',
        },
      }),
    );
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const client = new BridgeClient({
    CAMP_TOOL_ENDPOINT: `http://127.0.0.1:${server.address().port}`,
    CAMP_TOOL_TOKEN: capability,
  });
  try {
    await assert.rejects(client.json('/fs', { operation: 'writeText' }), {
      code: 'FS_STALE_VERSION',
    });
    await assert.rejects(client.json('/redirect', {}), /unavailable/);
    assert.equal(requests, 2);
  } finally {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
});

test('bridge rejects oversized responses without retaining unbounded text', async () => {
  await assert.rejects(
    boundedText(new globalThis.Response('123456789'), 8),
    /size limit/,
  );
});

test('foreground command specs reject background and environment overrides', () => {
  const spec = resolveCommand({
    command: 'echo ready',
    timeoutMs: 900_000,
    stdoutMaxBytes: 100_000,
  });
  assert.equal(spec.timeoutMs, 120_000);
  assert.equal(spec.stdoutMaxBytes, 65_536);
  assert.equal(spec.onExpiry, 'kill');
  for (const extra of [
    { onExpiry: 'none' },
    { env: { PRIVATE_KEY: 'secret' } },
    { stdin: '' },
    { timeoutMs: -1 },
    { sandboxPolicy: { mode: 'danger-full-access' } },
  ]) {
    assert.throws(() => resolveCommand({ command: 'echo ready', ...extra }));
  }
});

function terminal(spec, overrides = {}) {
  return {
    type: 'result',
    cleanupConfirmed: true,
    result: {
      exitCode: 0,
      signal: null,
      timedOut: false,
      aborted: false,
      timeoutMs: spec.timeoutMs,
      stdout: { text: 'hello', truncated: false },
      stderr: { text: '', truncated: false },
      ...overrides,
    },
  };
}

function eventResponse(events) {
  return new globalThis.Response(
    events.map((event) => JSON.stringify(event) + '\n').join(''),
  );
}

test('command consumes real output without duplicate incremental reads and awaits terminal cleanup', async () => {
  const spec = resolveCommand({ command: 'echo hello' });
  const command = new CampCommand(
    {
      request: async () =>
        eventResponse([
          { type: 'stdout', text: 'hel' },
          { type: 'stdout', text: 'lo' },
          terminal(spec),
        ]),
    },
    spec,
  );
  await command.done;
  assert.equal((await command.result()).exitCode, 0);
  assert.equal(command.status, 'completed');
  assert.equal(command.readOutput().delta, 'hello');
  assert.equal(command.readOutput().delta, '');
  assert.equal(command.observed.stdout.readFrom(0).text, 'hello');
  assert.equal(command.kill(), false);
});

test('command cancellation waits for a separate cleanup acknowledgement', async () => {
  let releaseCancel;
  let completeStream;
  let cancelCalls = 0;
  const spec = resolveCommand({ command: 'sleep 100' });
  const stream = new globalThis.ReadableStream({
    start(controller) {
      completeStream = () => {
        controller.enqueue(
          new globalThis.TextEncoder().encode(
            JSON.stringify(
              terminal(spec, {
                exitCode: null,
                signal: 'SIGTERM',
                aborted: true,
              }),
            ) + '\n',
          ),
        );
        controller.close();
      };
    },
  });
  const bridge = {
    request: async () => new globalThis.Response(stream),
    json: async (path, payload) => {
      assert.equal(path, '/shell/cancel');
      assert.match(payload.commandId, /^[\da-f-]{36}$/u);
      cancelCalls++;
      await new Promise((resolve) => {
        releaseCancel = resolve;
      });
      return { cleanupConfirmed: true };
    },
  };
  const command = new CampCommand(bridge, spec);
  assert.equal(command.kill(), true);
  assert.equal(command.kill(), false);
  completeStream();
  let finished = false;
  void command.done.then(() => {
    finished = true;
  });
  await delay(5);
  assert.equal(finished, false);
  releaseCancel();
  assert.equal((await command.result()).aborted, true);
  assert.equal(cancelCalls, 1);
  assert.equal(command.status, 'killed');
});

test('missing terminal result and unconfirmed cleanup cannot become successful commands', async () => {
  const spec = resolveCommand({ command: 'echo hello' });
  let cancelled = 0;
  const command = new CampCommand(
    {
      request: async () => eventResponse([{ type: 'stdout', text: 'partial' }]),
      json: async () => {
        cancelled++;
        return { cleanupConfirmed: true };
      },
    },
    spec,
  );
  await assert.rejects(command.result(), /without a terminal result/);
  assert.equal(cancelled, 1);
  assert.equal(command.readOutput().delta, 'partial');
  const unsafe = new CampCommand(
    {
      request: async () =>
        eventResponse([{ ...terminal(spec), cleanupConfirmed: false }]),
      json: async () => ({ cleanupConfirmed: false }),
    },
    spec,
  );
  await assert.rejects(unsafe.result(), /cleanup is unconfirmed/);
});

test('file discovery is bounded, literal and has no command dependency', async () => {
  const target = (path) => ({ targetKey: path, displayPath: path });
  const fs = {
    resolve: async (path) => target(path),
    listDir: async () => [
      {
        name: 'notes.txt',
        type: 'file',
        size: 20,
        target: target('/workspace/notes.txt'),
      },
      {
        name: 'huge.txt',
        type: 'file',
        size: 1024 * 1024,
        target: target('/workspace/huge.txt'),
      },
    ],
    readText: async (file) => {
      assert.equal(file.displayPath, '/workspace/notes.txt');
      return 'A literal [query].\nOther text.';
    },
  };
  assert.equal(
    (await listFiles(fs, {})).text,
    '/workspace/notes.txt\n/workspace/huge.txt',
  );
  const matches = await searchFiles(fs, { query: '[query]' });
  assert.match(matches.text, /notes.txt:1: A literal \[query\]/);
  assert.match(matches.text, /1 binary, unknown-size or large files skipped/);
  assert.equal(matches.truncated, false);
  await assert.rejects(searchFiles(fs, { query: '' }));
});

test('profiles remove all local process tools and restrict read-only access', () => {
  const read = toolPatch('read');
  for (const id of [
    'filesystem',
    'subprocess',
    'pty',
    'terminal-bash',
    'persistent-bash',
    'jobs',
  ]) {
    assert.equal(read.find((row) => row.id === id).disabled, true);
  }
  assert.deepEqual(read.find((row) => row.id === 'tool-fs').config, {
    read: true,
    readImage: false,
    write: false,
    edit: false,
  });
  assert.equal(
    read
      .find((row) => row.insert)
      .insert.some((row) => row.id === 'camp-shell'),
    false,
  );
});
