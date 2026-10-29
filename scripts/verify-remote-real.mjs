// Explicit paid acceptance. Each ledger permits at most two task submissions;
// even an uncertain response consumes its reservation. There is no resume/retry.
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { mkdir, open, rename } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const usage =
  'Usage: node scripts/verify-remote-real.mjs --allow-two-real-runs ORIGIN ACCESS_JSON RUNNER_ID [REPORT_DIR]';
const ceilings = { maxTokens: 4096, maxSteps: 8, timeoutSeconds: 180 };
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'interrupted']);
const filePath = 'delivery.md';
const profile = {
  kind: 'command',
  checks: [
    {
      name: 'delivery',
      arguments: [
        'bash',
        '-c',
        "test -f delivery.md && test \"$(wc -l < delivery.md)\" -eq 3 && grep -qxF '# Camp Remote Acceptance' delivery.md && grep -Eq '^Revision: [12]$' delivery.md && grep -qxF 'Acceptance: remote DSH and LayerFS' delivery.md",
      ],
    },
  ],
};

class VerificationError extends Error {}

function requireValue(condition, message) {
  if (!condition) throw new VerificationError(message);
}

function identifier(value) {
  requireValue(
    typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value),
    'Expected an opaque API identifier.',
  );
  return value;
}

export function normalizeOrigin(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new VerificationError('Provide a valid Camp origin.');
  }
  requireValue(
    url.username === '' &&
      url.password === '' &&
      url.search === '' &&
      url.hash === '' &&
      url.pathname === '/' &&
      (url.protocol === 'https:' ||
        (url.protocol === 'http:' &&
          ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))),
    'Use an HTTPS origin, or loopback HTTP, without credentials or a path.',
  );
  return url.origin;
}

export function validatePreflight(settings, runner, expectedRevision) {
  requireValue(
    settings?.apiKeyConfigured === true,
    'Configure the provider on Server first.',
  );
  const budget = {};
  for (const [key, limit] of Object.entries(ceilings)) {
    requireValue(
      Number.isSafeInteger(settings[key]) &&
        settings[key] >= 1 &&
        settings[key] <= limit,
      `The actual ${key} setting must be between 1 and ${limit}.`,
    );
    budget[key] = settings[key];
  }
  identifier(settings.revision);
  requireValue(
    expectedRevision === undefined || settings.revision === expectedRevision,
    'Model configuration changed during verification; no further task will be sent.',
  );
  requireValue(
    runner?.enabled === true &&
      runner.revoked === false &&
      runner.online === true &&
      runner.capabilities?.executor === 'native' &&
      runner.capabilities.ready === true &&
      Number.isSafeInteger(runner.slots) &&
      runner.slots > 0,
    'The selected Runner must be enabled, online and ready with the native executor.',
  );
  return budget;
}

function contents(revision) {
  return `# Camp Remote Acceptance\nRevision: ${revision}\nAcceptance: remote DSH and LayerFS\n`;
}

function digest(value) {
  return createHash('sha256').update(value).digest('hex');
}

export async function syncDirectory(directory) {
  // Windows does not support opening directories for fsync through this API.
  if (process.platform === 'win32') return;
  const handle = await open(directory, 'r');
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

export async function privateJSON(filename, value, flags = 'wx') {
  const handle = await open(filename, flags, 0o600);
  try {
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
  } finally {
    await handle.close();
  }
}

export async function readAccess(filename, origin) {
  const handle = await open(
    filename,
    constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
  );
  try {
    const stat = await handle.stat();
    requireValue(
      stat.isFile() && stat.size <= 65536,
      'Access JSON must be a small regular private file.',
    );
    requireValue(
      process.platform === 'win32' ||
        ((stat.mode & 0o077) === 0 && stat.uid === process.getuid()),
      'Access JSON must be owned by this user and inaccessible to group/other users (chmod 600).',
    );
    let value;
    try {
      value = JSON.parse(await handle.readFile('utf8'));
    } catch {
      throw new VerificationError('Access JSON is not valid JSON.');
    }
    requireValue(
      normalizeOrigin(value.origin) === origin,
      'Access JSON belongs to a different origin.',
    );
    requireValue(
      typeof value.admin?.username === 'string' &&
        value.admin.username.length > 0 &&
        typeof value.admin?.password === 'string' &&
        value.admin.password.length > 0,
      'Access JSON must contain admin.username and admin.password.',
    );
    return { username: value.admin.username, password: value.admin.password };
  } finally {
    await handle.close();
  }
}

export function client(origin) {
  let cookie = '';
  return async (route, method = 'GET', body, text = false) => {
    let response;
    try {
      response = await fetch(new URL(route, origin), {
        method,
        redirect: 'error',
        headers: {
          Origin: origin,
          ...(cookie ? { Cookie: cookie } : {}),
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(30000),
      });
    } catch {
      throw new VerificationError(
        `${method} request failed; no request was retried. Inspect the ledger and Camp before any further paid work.`,
      );
    }
    for (const value of response.headers.getSetCookie()) {
      if (value.startsWith('camp_session=')) cookie = value.split(';')[0];
    }
    // Do not print provider messages, response bodies, credentials or cookies.
    requireValue(
      response.ok,
      `${method} request returned HTTP ${response.status}; no request was retried.`,
    );
    try {
      return text ? await response.text() : await response.json();
    } catch {
      throw new VerificationError(
        `${method} response could not be read; inspect the ledger and Camp.`,
      );
    }
  };
}

export function runSummary(run) {
  return {
    id: identifier(run.id),
    runnerId: identifier(run.runnerId),
    attemptId: run.attemptId ? identifier(run.attemptId) : '',
    status: terminal.has(run.status) ? run.status : 'pending',
    published: run.published === true,
    sessionId: run.sessionId ? identifier(run.sessionId) : '',
    sessionMode: ['new', 'resume', 'rebuild'].includes(run.sessionMode)
      ? run.sessionMode
      : '',
    sessionGeneration: run.sessionGeneration,
    baselineCommit: run.baselineCommit,
    resultCommit: run.resultCommit,
    maxTokens: run.maxTokens,
    maxSteps: run.maxSteps,
    timeoutSeconds: run.timeoutSeconds,
  };
}

export async function main(args = process.argv.slice(2)) {
  requireValue(
    args.length >= 4 && args.length <= 5 && args[0] === '--allow-two-real-runs',
    usage,
  );
  requireValue(
    process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0',
    'TLS certificate verification must remain enabled.',
  );
  const origin = normalizeOrigin(args[1]);
  const runnerId = identifier(args[3]);
  const access = await readAccess(path.resolve(args[2]), origin);
  const projectRoot = fileURLToPath(new URL('../', import.meta.url));
  const reportDir = path.resolve(
    args[4] ??
      path.join(
        projectRoot,
        '.data',
        'verification',
        `remote-real-${digest(`${origin}\n${runnerId}`).slice(0, 16)}`,
      ),
  );
  await mkdir(reportDir, { recursive: true, mode: 0o700 });
  const ledgerFile = path.join(reportDir, 'ledger.json');
  const reportFile = path.join(reportDir, 'report.json');
  const ledger = {
    schema: 1,
    startedAt: new Date().toISOString(),
    origin,
    runnerId,
    allowedRuns: 2,
    ceilings,
    state: 'preflight',
    requests: [],
  };
  try {
    // This permanent exclusive claim is also the lock. Never delete/reuse it,
    // even when this process failed before learning whether a POST was accepted.
    await privateJSON(ledgerFile, ledger);
    await syncDirectory(reportDir);
  } catch (error) {
    if (error.code === 'EEXIST')
      throw new VerificationError(
        'This verification ledger already exists. No requests were sent. Inspect it and Camp; a new report directory requires a new explicit two-run budget.',
      );
    throw error;
  }
  const report = {
    schema: 1,
    origin,
    runnerId,
    startedAt: ledger.startedAt,
    status: 'pending',
    runs: [],
    checks: [],
  };

  async function save() {
    for (const [filename, value] of [
      [ledgerFile, ledger],
      [reportFile, report],
    ]) {
      const temporary = `${filename}.next`;
      await privateJSON(temporary, value);
      await rename(temporary, filename);
      await syncDirectory(reportDir);
    }
  }

  const api = client(origin);

  async function preflight(expectedRevision) {
    const settings = await api('/api/settings/models');
    const { runners } = await api('/api/runners');
    const runner = runners.find((value) => value.id === runnerId);
    const budget = validatePreflight(settings, runner, expectedRevision);
    return { revision: settings.revision, budget };
  }

  async function exactFile(resourceId, commit, revision) {
    requireValue(
      typeof commit === 'string' && /^[0-9a-f]{40}$/.test(commit),
      'Expected an immutable Git commit.',
    );
    const value = await api(
      `/api/resources/${identifier(resourceId)}/file?${new URLSearchParams({ path: filePath, commit })}`,
      'GET',
      undefined,
      true,
    );
    requireValue(
      value === contents(revision),
      `Published revision ${revision} does not match the requested exact file.`,
    );
    return { commit, sha256: digest(value), revision };
  }

  try {
    const { user } = await api('/api/auth/login', 'POST', access);
    requireValue(
      user?.role === 'admin' && user.enabled === true,
      'An enabled administrator account is required.',
    );
    const actorId = identifier(user.id);
    const configuration = await preflight();
    ledger.configurationRevision = configuration.revision;
    report.budget = configuration.budget;
    await save();
    const tag = randomUUID().slice(0, 8);
    let { room } = await api('/api/rooms', 'POST', {
      name: `Remote provider acceptance ${tag}`,
      description:
        'Two explicitly budgeted native Runs, human review and immutable delivery.',
    });
    const roomId = identifier(room.id);
    const resourceId = identifier(room.resourceId);
    ledger.roomId = report.roomId = roomId;
    report.resourceId = resourceId;
    await save();
    ({ room } = await api(`/api/rooms/${roomId}/runner`, 'PUT', {
      runnerId,
      revision: room.runnerRevision,
    }));
    requireValue(
      Number.isSafeInteger(room.runnerRevision) && room.runnerId === runnerId,
      'Room routing was not saved.',
    );
    await api(`/api/resources/${resourceId}`, 'PATCH', {
      environment: profile,
    });
    const { agent } = await api(`/api/rooms/${roomId}/agents`, 'POST', {
      name: 'Acceptance Writer',
      preferredRunnerId: runnerId,
      handle: `acceptance-${tag}`,
      icon: 'document',
      access: 'write',
      enabled: true,
      instructions:
        'Use actual tools to make only the requested file change in the isolated workspace. Read the resulting file to verify it. Keep final replies concise.',
    });
    const agentId = identifier(agent.id);
    ledger.agentId = report.agentId = agentId;
    ledger.state = 'ready';
    await save();

    async function submit(content) {
      await preflight(configuration.revision);
      requireValue(
        ledger.requests.length < 2,
        'The two-run budget is exhausted.',
      );
      const request = {
        reservedAt: new Date().toISOString(),
        method: 'POST',
        route: `/api/rooms/${roomId}/messages`,
        body: {
          requestId: randomUUID(),
          runnerRevision: room.runnerRevision,
          threadRootId: '',
          recipientAgentIds: [agentId],
          content,
        },
        state: 'reserved-outcome-unknown',
      };
      ledger.requests.push(request);
      ledger.state = 'executing';
      // Both the full body and UUID reach stable storage before the network.
      await save();
      const result = await api(request.route, request.method, request.body);
      requireValue(
        result.runs?.length === 1 && result.runs[0].agentId === agentId,
        'Expected exactly one Run for the selected Agent. Inspect Camp; no prompt will be retried.',
      );
      let run = result.runs[0];
      const id = identifier(run.id);
      request.runId = id;
      request.state = 'accepted';
      await save();
      for (const [key, value] of Object.entries(configuration.budget)) {
        if (run[key] !== value) {
          request.state = 'cancellation-requested-budget-mismatch';
          await save();
          await api(`/api/runs/${id}/cancel`, 'POST', {});
          throw new VerificationError(
            'The admitted Run budget changed; cancellation requested and no further task will be sent.',
          );
        }
      }
      requireValue(
        run.runnerId === runnerId,
        'The admitted Run targeted a different Runner.',
      );
      // Includes bounded upload time. Polling is read-only; a timeout does not
      // cancel or resubmit an uncertain Run. Its ID remains in the ledger.
      const deadline =
        Date.now() + (configuration.budget.timeoutSeconds + 360) * 1000;
      while (!terminal.has(run.status) && Date.now() < deadline) {
        await pause(1000);
        ({ run } = await api(`/api/runs/${id}`));
      }
      request.state = terminal.has(run.status)
        ? 'terminal'
        : 'observation-timeout';
      request.run = runSummary(run);
      report.runs.push(request.run);
      await save();
      requireValue(
        run.status === 'succeeded' && run.published === true,
        'Run did not finish with a successful final publication. Inspect its Camp diagnostics; no prompt will be retried.',
      );
      requireValue(
        Array.isArray(run.checks) &&
          run.checks.length === 1 &&
          run.checks[0].name === 'delivery' &&
          run.checks[0].status === 'passed',
        'The configured delivery check did not pass.',
      );
      const { events } = await api(`/api/runs/${id}/events`);
      request.run.observedModelEvents = events.filter(
        (event) => event.type === 'model',
      ).length;
      report.checks.push(`run-${report.runs.length}-published-and-checked`);
      await save();
      console.log(
        `Run ${report.runs.length}/2 published and passed its configured check.`,
      );
      return run;
    }

    const first = await submit(
      `Create delivery.md with exactly these three lines and a final newline:\n${contents(1)}Use the actual file-writing or bash tool; do not merely describe the change. Read the file back. Do not modify other files.`,
    );
    report.firstFile = await exactFile(resourceId, first.resultCommit, 1);
    requireValue(
      first.sessionId,
      'The first native Run did not retain a session identity.',
    );
    let { review } = await api(`/api/rooms/${roomId}/reviews`, 'POST', {
      requestId: randomUUID(),
      title: 'Remote provider acceptance',
      resourceId,
      path: filePath,
      commit: first.resultCommit,
      reviewerUserId: actorId,
    });
    const reviewId = identifier(review.id);
    ledger.reviewId = report.reviewId = reviewId;
    await save();

    async function reviewAction(action, extra = {}) {
      ({ review } = await api(`/api/reviews/${reviewId}/actions`, 'POST', {
        requestId: randomUUID(),
        revision: review.revision,
        action,
        ...extra,
      }));
      await save();
    }

    await reviewAction('request_changes', {
      comment: 'Change only Revision: 1 to Revision: 2.',
    });
    // Use the original Room conversation, not the new review thread: native
    // conversation identity includes threadRootId.
    const second = await submit(
      'Apply the requested revision using an actual tool: change only the Revision: 1 line in delivery.md to Revision: 2. Preserve the other two lines and final newline. Read the resulting file to verify it. Do not modify other files.',
    );
    requireValue(
      second.sessionMode === 'resume' &&
        second.sessionId === first.sessionId &&
        second.sessionGeneration === first.sessionGeneration,
      'The second Run did not resume the same native session generation.',
    );
    requireValue(
      second.baselineCommit === first.resultCommit &&
        second.resultCommit !== first.resultCommit,
      'The second Run did not revise the first published version.',
    );
    report.secondFile = await exactFile(resourceId, second.resultCommit, 2);
    report.checks.push('native-session-resume');
    await reviewAction('resubmit', { commit: second.resultCommit });
    await reviewAction('approve');
    await reviewAction('deliver');
    requireValue(
      review.status === 'delivered' &&
        review.deliveredCommit === second.resultCommit,
      'Review did not deliver the exact revised commit.',
    );
    await exactFile(resourceId, first.resultCommit, 1);
    await exactFile(resourceId, review.deliveredCommit, 2);
    report.deliveredCommit = review.deliveredCommit;
    report.checks.push(
      'request-changes-resubmit-approve-deliver',
      'immutable-original-and-delivered-file',
    );
    report.status = ledger.state = 'passed';
    report.finishedAt = ledger.finishedAt = new Date().toISOString();
    await save();
    console.log(
      'Passed: two remote native Runs, session resume, human review and immutable delivery.',
    );
    console.log(`Private report and permanent ledger: ${reportDir}`);
  } catch (error) {
    report.status = ledger.state = 'stopped';
    report.finishedAt = ledger.finishedAt = new Date().toISOString();
    // Only our bounded, authored diagnostics enter the report. Upstream errors,
    // model output and file-system error messages may contain private values.
    report.reason =
      error instanceof VerificationError
        ? error.message
        : 'Verification stopped after an unexpected local error. Inspect the ledger and Camp; do not repeat the paid prompts.';
    try {
      await save();
    } catch {
      console.error(
        'Could not update the report. The exclusive ledger remains reserved; inspect Camp before further paid work.',
      );
    }
    throw new VerificationError(report.reason);
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    await main();
  } catch (error) {
    console.error(
      error instanceof VerificationError
        ? error.message
        : 'Verification could not start. Check the private input files and ledger; no automatic retry was performed.',
    );
    process.exitCode = 1;
  }
}
