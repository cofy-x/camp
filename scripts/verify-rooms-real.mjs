// Explicit opt-in smoke check. This makes at most three user-triggered model runs
// against an already running local Camp server; it never reads model credentials.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const [address, consent] = process.argv.slice(2);
if (!address || consent !== '--allow-model-calls') {
  throw new Error(
    'Usage: node scripts/verify-rooms-real.mjs http://127.0.0.1:13312 --allow-model-calls',
  );
}
const base = new URL(address);
assert.equal(base.protocol, 'http:');
assert.ok(['localhost', '127.0.0.1', '[::1]'].includes(base.hostname));
assert.equal(base.username + base.password + base.search + base.hash, '');
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const reportPath = resolve(root, '.data/rooms-real-verification.json');
const tag = Date.now().toString(36);
const report = {
  startedAt: new Date().toISOString(),
  base: base.origin,
  runs: [],
  rooms: [],
  checks: [],
};
let submitted = 0;

async function api(path, method = 'GET', body) {
  const response = await fetch(new URL(path, base), {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  });
  if (!response.ok)
    throw new Error(
      `${method} ${path}: ${response.status} ${await response.text()}`,
    );
  return response.json();
}

async function run(room, agent, prompt) {
  assert.ok(submitted < 3, 'The three-run demonstration limit was reached.');
  submitted += 1;
  const sent = await api(`/api/rooms/${room.id}/messages`, 'POST', {
    requestId: randomUUID(),
    content: prompt,
    recipientAgentIds: [agent.id],
  });
  assert.equal(sent.runs.length, 1);
  const id = sent.runs[0].id;
  const deadline = Date.now() + 210000;
  let result;
  do {
    result = (await api(`/api/runs/${id}`)).run;
    if (
      ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
        result.status,
      )
    )
      break;
    await delay(1000);
  } while (Date.now() < deadline);
  const messages = (await api(`/api/rooms/${room.id}/messages`)).messages;
  const final = messages.filter(
    (message) => message.runId === id && message.authorKind === 'agent',
  );
  report.runs.push({ prompt, ...result, reply: final[0]?.content ?? '' });
  await saveReport();
  console.log(
    `${agent.name}: ${result.status}; ${result.files.length} changed files; ${result.checks.length} checks`,
  );
  assert.equal(result.status, 'succeeded', result.error);
  assert.equal(
    final.length,
    1,
    'Exactly one final chat reply must be durable.',
  );
  return result;
}

async function saveReport() {
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, JSON.stringify(report, null, 2) + '\n', {
    mode: 0o600,
  });
}

async function sourceHash() {
  const hash = createHash('sha256');
  for (const name of ['greet.js', 'greet.test.js', 'package.json'])
    hash.update(await readFile(resolve(root, 'examples/greet', name)));
  return hash.digest('hex');
}

try {
  const status = await api('/api/workspace/status');
  assert.equal(status.available, true, status.reason);
  assert.deepEqual(status.budget, {
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
  });
  report.budget = status.budget;
  const documentRoom = (
    await api('/api/rooms', 'POST', {
      name: `writing-${tag}`,
      description: 'A real Writer and Reviewer collaboration.',
    })
  ).room;
  report.rooms.push(documentRoom);
  const writer = (
    await api('/api/agents', 'POST', {
      name: 'Writer',
      handle: `writer-${tag}`,
      icon: 'document',
      instructions:
        'Write concise, useful documents. Perform the requested file work and report exactly what changed.',
      access: 'write',
      enabled: true,
    })
  ).agent;
  const reviewer = (
    await api('/api/agents', 'POST', {
      name: 'Reviewer',
      handle: `reviewer-${tag}`,
      icon: 'checklist',
      instructions:
        'Read the actual artifacts before reviewing. Give concrete feedback. You have read-only access.',
      access: 'read',
      enabled: true,
    })
  ).agent;
  for (const agent of [writer, reviewer])
    await api(`/api/rooms/${documentRoom.id}/members/${agent.id}`, 'PUT', {});
  await api(`/api/rooms/${documentRoom.id}`, 'PATCH', {
    defaultAgentId: writer.id,
  });
  await run(
    documentRoom,
    writer,
    'Create brief.md in the current empty workspace: a concise launch checklist for a small community reading club. Include a clear purpose, three preparation steps, and one success measure. Keep it under 180 words. Use one batched shell command to write and read back the file, then give a brief final summary. No dependencies or tests are needed.',
  );
  const files = (await api(`/api/resources/${documentRoom.resourceId}/files`))
    .files;
  assert.ok(files.includes('brief.md'));
  const review = await run(
    documentRoom,
    reviewer,
    'Read brief.md from the shared workspace. Review its clarity and practicality, quote one short concrete detail from the file, and suggest two improvements. Do not modify any files. Give a concise final review.',
  );
  assert.equal(review.files.length, 0);
  assert.equal(review.published, false);
  report.checks.push(
    'Empty Room generated a published document; Reviewer read the shared result without changes.',
  );

  const before = await sourceHash();
  const resource = (
    await api('/api/resources', 'POST', {
      name: 'Greet example',
      path: resolve(root, 'examples/greet'),
      environment: {
        kind: 'node',
        nodeVersion: '24.16.0',
        packageManager: 'node',
        checks: [{ name: 'test' }],
        previewDirectory: '',
      },
    })
  ).resource;
  const codeRoom = (
    await api('/api/rooms', 'POST', {
      name: `greet-${tag}`,
      description: 'A real file edit and independent test run.',
    })
  ).room;
  await api(`/api/rooms/${codeRoom.id}`, 'PATCH', { resourceId: resource.id });
  report.rooms.push({ ...codeRoom, resourceId: resource.id });
  const assistant = (await api('/api/agents')).agents.find(
    (agent) => agent.id === codeRoom.defaultAgentId,
  );
  assert.ok(assistant);
  const built = await run(
    codeRoom,
    assistant,
    'Inspect greet.js and greet.test.js together. Update greet to trim whitespace around the name. For an omitted or blank name, return exactly Hello, friend! Preserve existing normal-name behavior. Add meaningful tests for these cases, run node --test, and briefly report actual results. This is a tiny dependency-free project; batch reads and edits, do not install anything.',
  );
  assert.equal(built.published, true);
  assert.equal(built.testExitCode, 0);
  assert.ok(
    built.checks.length > 0 &&
      built.checks.every((check) => check.status === 'passed'),
  );
  assert.equal(
    await sourceHash(),
    before,
    'The imported external source was modified.',
  );
  report.checks.push(
    'Greet changes published; independent Node checks passed; external source remained unchanged.',
  );
  report.finishedAt = new Date().toISOString();
  console.log(`Real verification passed. Report: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  process.exitCode = 1;
  console.error(report.error);
} finally {
  report.submitted = submitted;
  await saveReport();
}
