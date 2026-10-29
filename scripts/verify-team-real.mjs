import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, readFile, writeFile, rename } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { root } from './lib.mjs';

// Explicit opt-in only. Reserve each of three allowed submissions durably before
// sending it. Never rerun this script automatically after a failure.
if (
  process.argv[2] !== '--allow-three-real-runs' ||
  !process.argv[3] ||
  !process.argv[4]
)
  throw new Error(
    'Usage: node scripts/verify-team-real.mjs --allow-three-real-runs http://127.0.0.1:PORT /absolute/Camp/data',
  );
const origin = new URL(process.argv[3]).origin;
const data = path.resolve(process.argv[4]);
const reportDir = path.join(data, 'verification');
await mkdir(reportDir, { recursive: true, mode: 0o700 });
const reportFile = path.join(reportDir, 'real-model.json');
const report = {
  startedAt: new Date().toISOString(),
  origin,
  budget: { maxTokens: 4096, maxSteps: 8, timeoutSeconds: 180 },
  attempts: [],
};
await writeFile(reportFile, `${JSON.stringify(report, null, 2)}\n`, {
  flag: 'wx',
  mode: 0o600,
});

async function saveReport() {
  await writeFile(
    `${reportFile}.next`,
    `${JSON.stringify(report, null, 2)}\n`,
    { mode: 0o600 },
  );
  await rename(`${reportFile}.next`, reportFile);
}

function client() {
  let cookie = '';
  return async (route, method = 'GET', body, multipart = false) => {
    const response = await fetch(`${origin}${route}`, {
      method,
      headers: {
        Origin: origin,
        Cookie: cookie,
        ...(!multipart ? { 'Content-Type': 'application/json' } : {}),
      },
      body:
        body === undefined
          ? undefined
          : multipart
            ? body
            : JSON.stringify(body),
      signal: AbortSignal.timeout(30000),
    });
    if (response.headers.has('set-cookie'))
      cookie = response.headers.get('set-cookie').split(';')[0];
    const result = await response.json();
    assert.ok(response.ok, `${method} ${route}: ${JSON.stringify(result)}`);
    return result;
  };
}

const admin = client();
const member = client();
const password = randomBytes(18).toString('base64url');
const memberPassword = randomBytes(18).toString('base64url');
const accessFile = path.join(reportDir, 'preview-access.json');
try {
  const status = await admin('/api/auth/status');
  let access;
  if (status.setupRequired) {
    const token = (
      await readFile(path.join(data, 'team-setup.token'), 'utf8')
    ).trim();
    await admin('/api/auth/setup', 'POST', {
      token,
      username: 'camp-admin',
      name: 'Alex',
      teamName: 'Camp Studio',
      password,
    });
    access = {
      origin,
      admin: { username: 'camp-admin', password },
      member: { username: 'camp-member', password: memberPassword },
    };
    await writeFile(accessFile, `${JSON.stringify(access, null, 2)}\n`, {
      flag: 'wx',
      mode: 0o600,
    });
  } else {
    access = JSON.parse(await readFile(accessFile, 'utf8'));
    await admin('/api/auth/login', 'POST', access.admin);
  }
  const settings = await admin('/api/settings/models');
  assert.equal(
    settings.apiKeyConfigured,
    true,
    'Configure the real model before running this explicit check.',
  );
  for (const [field, value] of Object.entries(report.budget))
    assert.equal(settings[field], value, `Use the accepted ${field} budget.`);
  const { token } = await admin('/api/team/invites', 'POST', {
    expiresInHours: 2,
  });
  await member('/api/auth/accept-invite', 'POST', {
    token,
    username: access.member.username,
    name: 'Morgan',
    password: access.member.password,
  });
  const { room } = await admin('/api/rooms', 'POST', {
    name: 'Launch proposal',
    description: 'Collect a brief, draft a proposal, review and deliver.',
  });
  const { agent: writer } = await admin('/api/agents', 'POST', {
    name: 'Writer',
    handle: 'writer',
    icon: 'document',
    instructions:
      'Write clear, concise documents from the provided sources. Save requested deliverables to Room files. Do not invent confirmed facts.',
    access: 'write',
    enabled: true,
  });
  const { agent: reviewer } = await admin('/api/agents', 'POST', {
    name: 'Reviewer',
    handle: 'reviewer',
    icon: 'agent',
    instructions:
      'Review documents for clarity, unsupported claims and actionable next steps. Read the actual supplied file. You cannot modify files. Keep reviews concise.',
    access: 'read',
    enabled: true,
  });
  for (const agent of [writer, reviewer])
    await admin(`/api/rooms/${room.id}/members/${agent.id}`, 'PUT', {});
  const brief = new FormData();
  brief.append('requestId', randomUUID());
  brief.append(
    'file',
    new File(
      [
        '# Launch brief\nA three-person design studio needs a shared weekly client update. The pilot lasts two weeks. Success means less time assembling updates and clearer next steps. Budget and customer consent are not yet decided.\n',
      ],
      'brief.md',
      { type: 'text/markdown' },
    ),
  );
  const upload = await member(
    `/api/rooms/${room.id}/uploads`,
    'POST',
    brief,
    true,
  );
  const reference = {
    kind: 'file',
    resourceId: upload.reference.resourceId,
    commit: upload.reference.commit,
    path: upload.reference.path,
  };
  const discussion = await member(`/api/rooms/${room.id}/messages`, 'POST', {
    requestId: randomUUID(),
    content:
      '@camp-admin I uploaded the brief. Please ask Writer for a short proposal.',
    references: [reference],
  });
  assert.equal(discussion.runs.length, 0);

  async function run(
    role,
    api,
    target,
    content,
    recipient,
    references = [],
    threadRootId = '',
  ) {
    assert.ok(report.attempts.length < 3);
    const attempt = {
      role,
      roomId: target.id,
      requestId: randomUUID(),
      reservedAt: new Date().toISOString(),
    };
    report.attempts.push(attempt);
    await saveReport();
    const receipt = await api(`/api/rooms/${target.id}/messages`, 'POST', {
      requestId: attempt.requestId,
      content,
      recipientAgentIds: [recipient],
      references,
      threadRootId,
    });
    assert.equal(receipt.runs.length, 1);
    attempt.runId = receipt.runs[0].id;
    attempt.messageId = receipt.message.id;
    await saveReport();
    const deadline = Date.now() + 220000;
    let result;
    do {
      ({ run: result } = await api(`/api/runs/${attempt.runId}`));
      if (
        ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
          result.status,
        )
      )
        break;
      await pause(1000);
    } while (Date.now() < deadline);
    if (
      !['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
        result.status,
      )
    ) {
      await api(`/api/runs/${attempt.runId}/cancel`, 'POST', {});
      throw new Error(
        'Run did not reach a terminal state within the verification deadline; cancellation requested. No retry.',
      );
    }
    Object.assign(attempt, {
      status: result.status,
      error: result.error,
      published: result.published,
      resultCommit: result.resultCommit,
      files: result.files.map((file) => file.path),
      testExitCode: result.testExitCode,
      finishedAt: result.finishedAt,
    });
    await saveReport();
    console.log(
      `${role}: ${result.status}; Run ${result.id}; published=${result.published}`,
    );
    return { result, receipt };
  }

  const written = await run(
    'Writer',
    admin,
    room,
    'Read the attached brief and write proposal.md, a concise English proposal under 450 words with Goal, Two-week pilot, Success criteria, and Open decisions. Clearly label unknowns. Save the file and give a short summary.',
    writer.id,
    [reference],
  );
  const proposalCommit = written.result.published
    ? written.result.resultCommit
    : '';
  const reviewReferences = proposalCommit
    ? [
        {
          kind: 'file',
          resourceId: room.resourceId,
          commit: proposalCommit,
          path: 'proposal.md',
        },
      ]
    : [reference];
  const reviewed = await run(
    'Reviewer',
    member,
    room,
    proposalCommit
      ? 'Read proposal.md. Review it against the brief in this conversation. Give at most three actionable suggestions and distinguish verified facts from open decisions. Do not edit files.'
      : 'Read the attached brief. The prior drafting attempt did not publish a document. Identify three concrete questions the team must resolve before drafting. Do not claim that a proposal exists.',
    reviewer.id,
    reviewReferences,
    written.receipt.message.id,
  );
  if (proposalCommit) {
    const response = await admin(
      `/api/resources/${room.resourceId}/files?commit=${proposalCommit}`,
    );
    assert.ok(response.files.includes('proposal.md'));
  }
  const { room: coding } = await admin('/api/rooms', 'POST', {
    name: 'Greet example',
    description: 'Small Node example with independent checks.',
  });
  const { resource } = await admin('/api/resources', 'POST', {
    name: 'Greet',
    path: path.join(root, 'examples/greet'),
    environment: {
      kind: 'node',
      nodeVersion: '24.16.0',
      packageManager: 'node',
      checks: [{ name: 'test', script: '' }],
    },
  });
  await admin(`/api/rooms/${coding.id}`, 'PATCH', { resourceId: resource.id });
  const built = await run(
    'Assistant',
    admin,
    coding,
    'Modify greet to trim surrounding whitespace from names. If the name is omitted or blank, return Hello, friend! Preserve named greetings. Add tests for omitted, empty, whitespace-only and padded names. Run the tests and report the actual result.',
    coding.defaultAgentId,
  );
  report.completedAt = new Date().toISOString();
  report.passed =
    [written, reviewed, built].every(
      ({ result }) => result.status === 'succeeded',
    ) &&
    written.result.published &&
    built.result.published &&
    built.result.testExitCode === 0;
  await saveReport();
  console.log(
    `Real-model acceptance recorded at ${reportFile}. Exactly ${report.attempts.length} submissions; no automatic retries.`,
  );
  assert.equal(
    report.passed,
    true,
    'At least one real-model acceptance criterion failed. Inspect saved Runs; do not automatically retry.',
  );
} catch (error) {
  report.failure =
    error instanceof Error ? error.message : 'Verification failed';
  await saveReport();
  throw error;
}
