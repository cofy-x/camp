import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { join, resolve } from 'node:path';
import process from 'node:process';
import { createInterface } from 'node:readline';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath, URL } from 'node:url';

const runtime = fileURLToPath(new URL('.', import.meta.url));
const cli = '/runtime/node_modules/@deepseek-ai/dsh/lib/bin.js';
const probeRoot = resolve(runtime, '../../.data/dsh-probe');

await mkdir(probeRoot, { recursive: true });
const root = await mkdtemp(join(probeRoot, 'run-'));
const workspace = join(root, 'project');
await mkdir(workspace);
await writeFile(join(workspace, 'package.json'), '{"type":"module"}\n');
await writeFile(
  join(workspace, 'greet.js'),
  'export const greet = () => "Hi";\n',
);
const command = [
  'test -z "$DEEPSEEK_API_KEY" || exit 90',
  'if touch /home/must-not-write 2>/dev/null; then exit 91; fi',
  'cat > greet.js <<\'JS\'\nexport const greet = (name = "World") => `Hello, ${name}!`;\nJS',
  'cat > greet.test.js <<\'JS\'\nimport { test } from "node:test";\nimport assert from "node:assert/strict";\nimport { greet } from "./greet.js";\ntest("named greeting", () => assert.equal(greet("Camp"), "Hello, Camp!"));\ntest("default greeting", () => assert.equal(greet(), "Hello, World!"));\nJS',
  '/node --test greet.test.js',
].join('\n');
const requests = [];
let scenario = 'success';
const descendantTag = `camp-dsh-probe-${process.pid}-${Date.now()}`;

function messages(content, stopReason) {
  return [
    {
      type: 'message_start',
      message: {
        id: `probe-${requests.length}`,
        model: 'deepseek-v4-pro',
        usage: { input_tokens: 3, output_tokens: 0 },
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

const modelServer = createServer((request, response) => {
  let body = '';
  request.setEncoding('utf8');
  request.on('data', (chunk) => {
    body += chunk;
  });
  request.on('end', () => {
    assert.equal(request.headers['x-api-key'], 'camp-keyless-probe');
    requests.push(JSON.parse(body));
    response.writeHead(200, { 'content-type': 'text/event-stream' });
    if (scenario === 'cancel') {
      response.end(
        messages(
          {
            type: 'tool_use',
            id: 'slow-command',
            name: 'bash',
            input: {
              command: `setsid /node -e 'setInterval(() => {}, 1000)' ${descendantTag} &
echo running > cancellation-started
sleep 120
echo leaked > cancellation-leaked`,
            },
          },
          'tool_use',
        ),
      );
    } else if (requests.length === 1) {
      response.end(
        messages(
          {
            type: 'tool_use',
            id: 'change-and-test',
            name: 'bash',
            input: { command },
          },
          'tool_use',
        ),
      );
    } else {
      response.end(
        messages(
          { type: 'text', text: 'Updated greet and passed two Node tests.' },
          'end_turn',
        ),
      );
    }
  });
});
await new Promise((resolveListen) =>
  modelServer.listen(0, '127.0.0.1', resolveListen),
);
const baseURL = `http://127.0.0.1:${modelServer.address().port}`;
const overlay = join(root, 'camp.patch.yml');
await writeFile(
  overlay,
  [
    '- id: sandbox-policy',
    '  config:',
    '    mode: workspace-write',
    '    workspaceRoot: /workspace',
    '- id: session-log-deepseek',
    '  disabled: true',
    '- id: llm-retry',
    '  disabled: true',
    '- id: terminal-bash',
    '  config:',
    '    timeoutMs: 10000',
    '    disposeGraceMs: 1000',
    '- id: persistent-bash',
    '  config:',
    '    timeoutMs: 10000',
    '    maxOutputChars: 16000',
    '',
  ].join('\n'),
);

async function descendants() {
  const entries = await readdir('/proc');
  const matches = await Promise.all(
    entries
      .filter((entry) => /^\d+$/.test(entry))
      .map(async (entry) => {
        try {
          const cmdline = await readFile(`/proc/${entry}/cmdline`, 'utf8');
          return cmdline.includes(descendantTag) ? Number(entry) : undefined;
        } catch (error) {
          if (['ENOENT', 'ESRCH', 'EACCES'].includes(error.code))
            return undefined;
          throw error;
        }
      }),
  );
  return matches.filter((pid) => pid !== undefined);
}

async function run(cancel, force = false) {
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
      '/etc/ssl',
      '/etc/ssl',
      '--ro-bind',
      '/etc/resolv.conf',
      '/etc/resolv.conf',
      '--ro-bind',
      '/etc/hosts',
      '/etc/hosts',
      '--ro-bind',
      '/etc/nsswitch.conf',
      '/etc/nsswitch.conf',
      '--ro-bind',
      process.execPath,
      '/node',
      '--ro-bind',
      runtime,
      '/runtime',
      '--ro-bind',
      overlay,
      '/config/camp.patch.yml',
      '--bind',
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
      '--unshare-pid',
      '--proc',
      '/proc',
      '--die-with-parent',
      '--chdir',
      '/workspace',
      '--',
      '/node',
      cli,
      '--profile',
      'sdk-minimal',
      '--patch',
      '/config/camp.patch.yml',
    ],
    {
      cwd: workspace,
      env: {
        PATH: '/usr/local/bin:/usr/bin:/bin',
        HOME: '/home/user',
        DSH_HOME: '/home/user/.dsh',
        DSH_TELEMETRY_DISABLED: '1',
        DEEPSEEK_API_KEY: 'camp-keyless-probe',
        DEEPSEEK_BASE_URL: baseURL,
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    },
  );
  const exited = once(child, 'exit');
  const events = [];
  const pending = new Map();
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => {
    stderr += chunk;
  });
  const lines = createInterface({ input: child.stdout });
  lines.on('line', (line) => {
    const frame = JSON.parse(line);
    events.push(frame);
    if (pending.has(frame.id)) {
      const { resolveReply, rejectReply } = pending.get(frame.id);
      pending.delete(frame.id);
      if (frame.error) rejectReply(new Error(frame.error.message));
      else resolveReply(frame.result);
    }
  });
  let id = 0;

  async function rpc(method, params) {
    const requestId = ++id;
    const reply = new Promise((resolveReply, rejectReply) =>
      pending.set(requestId, { resolveReply, rejectReply }),
    );
    child.stdin.write(
      `${JSON.stringify({ jsonrpc: '2.0', id: requestId, method, params })}\n`,
    );
    return Promise.race([
      reply,
      delay(25000, null, { ref: false }).then(() => {
        throw new Error(`RPC timeout: ${method}; ${stderr}`);
      }),
    ]);
  }

  try {
    const initialized = await rpc('initialize', {
      cwd: '/workspace',
      provider: 'deepseek-official',
      model: 'deepseek-v4-pro',
      maxTokens: 1024,
    });
    assert.equal(initialized.serverInfo.name, 'deepseek-harness-sdk-runtime');
    await rpc('session/prompt', {
      sessionId: cancel ? 'probe-cancel' : 'probe-success',
      contentBlocks: [
        {
          type: 'text',
          text: cancel ? 'Run a long command.' : 'Update greet and its tests.',
        },
      ],
    });
    if (cancel) {
      for (let attempt = 0; attempt < 100; attempt++) {
        try {
          await readFile(join(workspace, 'cancellation-started'));
          if ((await descendants()).length > 0) break;
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
        if (attempt === 99)
          throw new Error(`The long-running command did not start: ${stderr}`);
        await delay(100);
      }
      if (force) child.kill('SIGKILL');
      else child.stdin.end();
    } else {
      await rpc('session/wait', { sessionId: 'probe-success' });
      const end = events.find(
        (frame) => frame.params?.event?.type === 'turn/end',
      );
      assert.equal(end?.params.event.data.reason.kind, 'completed');
      const output = events
        .filter((frame) => frame.params?.event?.type === 'tool/result')
        .map((frame) => JSON.stringify(frame.params.event.data))
        .join('\n');
      assert.match(output, /pass 2/);
      assert.match(output, /Command finished with exit code 0/);
      assert.equal(requests.length, 2);
      assert.equal(requests[0].max_tokens, 1024);
      await rpc('shutdown');
    }
    const exit = await Promise.race([
      exited,
      delay(10000, null, { ref: false }).then(() => {
        throw new Error(`Timed out waiting for process exit: ${stderr}`);
      }),
    ]);
    assert.equal(exit[0], force ? null : 0, stderr);
    if (force) assert.equal(exit[1], 'SIGKILL');
    if (cancel) {
      for (
        let attempt = 0;
        attempt < 100 && (await descendants()).length > 0;
        attempt++
      )
        await delay(50);
      assert.deepEqual(
        await descendants(),
        [],
        'Cancelled runtime left a setsid descendant alive',
      );
    }
    await writeFile(
      join(
        root,
        force
          ? 'kill-events.json'
          : cancel
            ? 'cancel-events.json'
            : 'success-events.json',
      ),
      JSON.stringify(events, null, 2),
    );
    await writeFile(
      join(
        root,
        force
          ? 'kill-stderr.log'
          : cancel
            ? 'cancel-stderr.log'
            : 'success-stderr.log',
      ),
      stderr,
    );
  } finally {
    child.kill('SIGKILL');
    lines.close();
  }
}

try {
  await run(false);
  scenario = 'cancel';
  await run(true);
  await run(true, true);
  await assert.rejects(readFile(join(workspace, 'cancellation-leaked')), {
    code: 'ENOENT',
  });
  process.stdout.write(
    `PASS: real DSH JSON-RPC, file edits, Node tests, write boundary, credential scrub, EOF cancellation, and SIGKILL cleanup of setsid descendants.\nArtifacts: ${root}\n`,
  );
} finally {
  modelServer.closeAllConnections();
  await new Promise((resolveClose) => modelServer.close(resolveClose));
}
