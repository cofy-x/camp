import { Buffer } from 'node:buffer';
import console from 'node:console';
import process from 'node:process';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createRequire } from 'node:module';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { join, posix, relative } from 'node:path';
import { createInterface } from 'node:readline';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath, URL } from 'node:url';
import { toolPatch } from './camp-tools/patch.mjs';

// The real fixed DSH calls deterministic loopback fixtures, with no provider key.
const runtime = fileURLToPath(new URL('.', import.meta.url));
const root = await mkdtemp(join(runtime, '../../.data/camp-tools-probe-'));
const token = 'camp-tools-keyless-fixture-capability';
const fileContent = new Map([
  ['/workspace/brief.txt', 'A bridge-owned document.\n'],
]);
const calls = [];
let profile = 'write';
let step = 0;
let schemas;
const scenarios = {
  coordination: [
    [
      'camp_finish',
      { summary: 'Coordination fixture finished.', artifactPaths: [] },
    ],
  ],
  write: [
    ['list_files', { path: '/workspace' }],
    ['read', { file_path: '/workspace/brief.txt' }],
    [
      'write',
      { file_path: '/workspace/result.txt', content: 'First result.\n' },
    ],
    [
      'edit',
      {
        file_path: '/workspace/result.txt',
        old_string: 'First',
        new_string: 'Edited',
      },
    ],
    [
      'bash',
      {
        description: 'Verify the isolated command bridge',
        command: 'printf bridge-command',
        workdir: '/workspace',
      },
    ],
    ['search_files', { query: 'Edited' }],
  ],
  read: [
    ['list_files', { path: '/workspace' }],
    ['search_files', { query: 'bridge-owned' }],
    ['read', { file_path: '/workspace/brief.txt' }],
    [
      'bash',
      {
        description: 'Attempt an unavailable command',
        command: 'touch /workspace/forbidden',
      },
    ],
    ['write', { file_path: '/workspace/forbidden', content: 'No.' }],
    ['read', { file_path: '/etc/passwd' }],
  ],
};

function streamMessage(content, stopReason) {
  return [
    {
      type: 'message_start',
      message: {
        id: `fixture-${step}`,
        model: 'deepseek-v4-pro',
        usage: { input_tokens: 1, output_tokens: 0 },
      },
    },
    { type: 'content_block_start', index: 0, content_block: content },
    { type: 'content_block_stop', index: 0 },
    {
      type: 'message_delta',
      delta: { stop_reason: stopReason },
      usage: { output_tokens: 1 },
    },
    { type: 'message_stop' },
  ]
    .map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`)
    .join('');
}

async function readJSON(request) {
  let text = '';
  for await (const chunk of request) text += chunk;
  return JSON.parse(text);
}

const model = createServer(async (request, response) => {
  const body = await readJSON(request);
  schemas = body.tools;
  const next = scenarios[profile][step++];
  response.writeHead(200, { 'content-type': 'text/event-stream' });
  response.end(
    streamMessage(
      next
        ? {
            type: 'tool_use',
            id: `${profile}-${step}`,
            name: next[0],
            input: next[1],
          }
        : { type: 'text', text: 'Fixture finished.' },
      next ? 'tool_use' : 'end_turn',
    ),
  );
});

function target(path) {
  return { targetKey: `fixture:${path}`, displayPath: path };
}

function metadata(path) {
  return path === '/workspace' || path === '/references'
    ? { type: 'directory', version: 'directory' }
    : fileContent.has(path)
      ? {
          type: 'file',
          version: fileContent.get(path),
          size: Buffer.byteLength(fileContent.get(path)),
        }
      : null;
}

const bridge = createServer(async (request, response) => {
  assert.equal(request.headers.authorization, `Bearer ${token}`);
  const body = await readJSON(request);
  calls.push({ profile, route: request.url, ...body });
  if (request.url === '/intent') {
    assert.equal(profile, 'coordination');
    assert.equal(body.kind, 'finish');
    response.end(JSON.stringify({ value: { accepted: true } }));
    return;
  }
  if (request.url === '/shell') {
    assert.equal(profile, 'write');
    assert.equal(body.command, 'printf bridge-command');
    assert.equal(body.workdir, '/workspace');
    assert.equal(body.env, undefined);
    response.writeHead(200, { 'content-type': 'application/x-ndjson' });
    response.end(
      [
        { type: 'stdout', text: 'bridge-command' },
        {
          type: 'result',
          cleanupConfirmed: true,
          result: {
            exitCode: 0,
            signal: null,
            timedOut: false,
            aborted: false,
            timeoutMs: body.timeoutMs,
            stdout: { text: 'bridge-command', truncated: false },
            stderr: { text: '', truncated: false },
          },
        },
      ]
        .map((event) => JSON.stringify(event) + '\n')
        .join(''),
    );
    return;
  }
  response.setHeader('content-type', 'application/json');
  if (request.url === '/shell/cancel') {
    response.end(JSON.stringify({ value: { cleanupConfirmed: true } }));
    return;
  }
  assert.equal(request.url, '/fs');
  const path =
    body.target?.displayPath ??
    posix.resolve(body.cwd ?? '/workspace', body.path ?? '');
  if (
    !['/workspace', '/references'].some(
      (base) => path === base || path.startsWith(base + '/'),
    )
  ) {
    response.end(
      JSON.stringify({
        error: {
          code: 'FS_SANDBOX_DENIED',
          message: 'Path is outside Camp files.',
        },
      }),
    );
    return;
  }
  let value;
  switch (body.operation) {
    case 'resolve':
      value = target(path);
      break;
    case 'stat':
    case 'lstat':
      value = metadata(path);
      break;
    case 'readText':
      value = fileContent.get(path);
      break;
    case 'listDir':
      value = [...fileContent]
        .filter(([name]) => posix.dirname(name) === path)
        .map(([name]) => ({
          name: posix.basename(name),
          target: target(name),
          ...metadata(name),
        }));
      break;
    case 'writeText': {
      assert.equal(profile, 'write');
      const before = fileContent.get(path) ?? null;
      fileContent.set(path, body.content);
      value = {
        operation: before === null ? 'create' : 'update',
        version: body.content,
        before,
        after: body.content,
      };
      break;
    }
    case 'editText': {
      assert.equal(profile, 'write');
      const before = fileContent.get(path);
      const after = before.replace(body.edit.oldString, body.edit.newString);
      fileContent.set(path, after);
      value = { before, after, version: after };
      break;
    }
    default:
      throw new Error(`Unexpected file operation: ${body.operation}`);
  }
  response.end(JSON.stringify({ value }));
});

async function listen(server) {
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return `http://127.0.0.1:${server.address().port}`;
}

const modelURL = await listen(model);
const bridgeURL = await listen(bridge);
const fromTools = createRequire(
  createRequire(import.meta.url).resolve('@deepseek-ai/dsh-tools'),
);
const llm =
  '/runtime/' + relative(runtime, fromTools.resolve('@deepseek-ai/dsh-llm'));
const queuedPlugin = join(root, 'queued-context.mjs');
await writeFile(
  queuedPlugin,
  `import { createUserMessage } from ${JSON.stringify(llm)};
export const inject = ['tools'];
export function apply(ctx) {
  ctx.on('tools/execute', async (exec, next) => {
    const result = await next();
    if (exec.name !== 'camp_finish' || result.isError) return result;
    return { ...result, additionalContexts: [...(result.additionalContexts ?? []), createUserMessage({
      content: [{type:'text',text:'queued-after-coordination-probe'}], source: {kind:'camp-coordination-probe',form:'context'},
    })] };
  });
}
`,
);

async function run(access) {
  profile = access;
  step = 0;
  const workspace = join(root, access);
  await mkdir(workspace);
  const overlay = join(root, `${access}.json`);
  await writeFile(
    overlay,
    JSON.stringify([
      ...toolPatch(access === 'coordination' ? 'read' : access),
      ...(access === 'coordination'
        ? [
            {
              insert: [
                {
                  id: 'camp-coordination',
                  name: '/runtime/camp-tools/coordination.mjs',
                },
                { id: 'probe-queued-context', name: '/queued-context.mjs' },
              ],
            },
          ]
        : []),
      { id: 'session-log-deepseek', disabled: true },
      { id: 'llm-retry', disabled: true },
      {
        id: 'sandbox-policy',
        config: { mode: 'workspace-write', workspaceRoot: '/workspace' },
      },
    ]),
  );
  const child = spawn(
    'bwrap',
    [
      '--ro-bind',
      '/usr',
      '/usr',
      '--ro-bind',
      '/bin',
      '/bin',
      '--ro-bind',
      '/lib',
      '/lib',
      '--ro-bind',
      '/lib64',
      '/lib64',
      '--ro-bind',
      process.execPath,
      '/node',
      '--ro-bind',
      runtime,
      '/runtime',
      '--ro-bind',
      queuedPlugin,
      '/queued-context.mjs',
      '--ro-bind',
      overlay,
      '/patch.json',
      '--ro-bind',
      workspace,
      '/workspace',
      '--tmpfs',
      '/home',
      '--dir',
      '/home/user',
      '--tmpfs',
      '/tmp',
      '--dev',
      '/dev',
      '--unshare-user',
      '--unshare-pid',
      '--unshare-ipc',
      '--unshare-uts',
      '--proc',
      '/proc',
      '--die-with-parent',
      '--new-session',
      '--cap-drop',
      'ALL',
      '--chdir',
      '/workspace',
      '--',
      '/node',
      '/runtime/node_modules/@deepseek-ai/dsh/lib/bin.js',
      '--profile',
      'sdk-minimal',
      '--patch',
      '/patch.json',
    ],
    {
      env: {
        PATH: '/usr/bin:/bin',
        HOME: '/home/user',
        DSH_HOME: '/home/user/.dsh',
        DSH_TELEMETRY_DISABLED: '1',
        DEEPSEEK_API_KEY: 'keyless-fixture',
        DEEPSEEK_BASE_URL: modelURL,
        CAMP_TOOL_ENDPOINT: bridgeURL,
        CAMP_TOOL_TOKEN: token,
        CAMP_COORDINATION_ENDPOINT: bridgeURL,
        CAMP_COORDINATION_TOKEN: token,
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    },
  );
  const exit = once(child, 'exit');
  const pending = new Map();
  const events = [];
  let stderr = '';
  let sequence = 0;
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => {
    stderr += chunk;
  });
  const lines = createInterface({ input: child.stdout });
  lines.on('line', (line) => {
    const frame = JSON.parse(line);
    events.push(frame);
    if (pending.has(frame.id)) {
      const { resolve, reject } = pending.get(frame.id);
      pending.delete(frame.id);
      if (frame.error) reject(new Error(frame.error.message));
      else resolve(frame.result);
    }
  });
  const rpc = async (method, params) => {
    const id = ++sequence;
    const reply = new Promise((resolve, reject) =>
      pending.set(id, { resolve, reject }),
    );
    child.stdin.write(
      JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n',
    );
    return Promise.race([
      reply,
      delay(30_000, null, { ref: false }).then(() => {
        throw new Error(`${method} timed out: ${stderr}`);
      }),
    ]);
  };
  try {
    await rpc('initialize', {
      cwd: '/workspace',
      provider: 'deepseek-official',
      model: 'deepseek-v4-pro',
      maxTokens: 1024,
    });
    await rpc('session/prompt', {
      sessionId: `camp-${access}`,
      contentBlocks: [
        { type: 'text', text: 'Exercise the Camp public tools fixture.' },
      ],
    });
    await rpc('session/wait', { sessionId: `camp-${access}` });
    assert.equal(
      events.find((frame) => frame.params?.event?.type === 'turn/end')?.params
        .event.data.reason.kind,
      'completed',
      stderr,
    );
    const names = schemas.map((tool) => tool.name).sort();
    assert.deepEqual(
      names,
      (access === 'write'
        ? ['bash', 'edit', 'list_files', 'read', 'search_files', 'write']
        : access === 'coordination'
          ? [
              'list_files',
              'read',
              'search_files',
              'camp_handoff',
              'camp_ask_person',
              'camp_finish',
            ]
          : ['list_files', 'read', 'search_files']
      ).sort(),
      stderr,
    );
    const output = JSON.stringify(
      events.filter((frame) => frame.params?.event?.type === 'tool/result'),
    );
    if (access === 'coordination') {
      assert.equal(
        step,
        1,
        'Deferred context started another model request after finish.',
      );
      assert.match(JSON.stringify(events), /queued-after-coordination-probe/);
      assert.equal(calls.filter((call) => call.route === '/intent').length, 1);
    } else assert.match(output, /bridge-owned/);
    if (access === 'write') {
      assert.equal(
        fileContent.get('/workspace/result.txt'),
        'Edited result.\n',
      );
      assert.match(output, /bridge-command/);
      assert.deepEqual(
        calls.find((call) => call.operation === 'writeText').expected,
        { kind: 'createIfAbsent' },
      );
      assert.deepEqual(
        calls.find((call) => call.operation === 'editText').expected,
        { version: 'First result.\n' },
      );
      assert.equal(
        schemas.find((tool) => tool.name === 'bash').input_schema.properties
          .run_in_background,
        undefined,
      );
    } else if (access === 'read') {
      assert.equal(fileContent.has('/workspace/forbidden'), false);
      assert.equal(
        calls.some(
          (call) =>
            call.profile === 'read' &&
            (call.route === '/shell' ||
              ['writeText', 'editText'].includes(call.operation)),
        ),
        false,
      );
      assert.match(output, /FS_SANDBOX_DENIED/);
    }
    await rpc('shutdown');
    assert.equal((await exit)[0], 0, stderr);
    console.log(
      `${access}: exact tool catalog, bridged file operations, clean shutdown passed`,
    );
  } finally {
    child.kill('SIGKILL');
    lines.close();
  }
}

try {
  await run('write');
  await run('read');
  await run('coordination');
} finally {
  model.closeAllConnections();
  bridge.closeAllConnections();
  await Promise.all([
    new Promise((resolve) => model.close(resolve)),
    new Promise((resolve) => bridge.close(resolve)),
  ]);
  await rm(root, { recursive: true, force: true });
}
