// Explicit paid acceptance. Unknown outcomes spend their reservation; this
// verifier has no resume, retry, task continuation or comment-resend operation.
import { execFile } from 'node:child_process';
import { createHash, createHmac, randomBytes, randomUUID } from 'node:crypto';
import { mkdir, rename } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import {
  createPublicWebhook,
  parseGitHubJSON,
  PublicWebhookVerificationError,
} from './automation-public-webhook.mjs';
import {
  client,
  normalizeOrigin,
  privateJSON,
  readAccess,
  runSummary,
  syncDirectory,
  validatePreflight,
} from './verify-remote-real.mjs';

export const limits = Object.freeze({
  assessments: 4,
  tasks: 2,
  roundsPerTask: 3,
  modelRequestsPerTask: 12,
  totalRounds: 6,
  totalModelRequests: 24,
  githubInputWrites: 4,
  githubComments: 8,
});
const usage =
  'Usage: node scripts/verify-automation-real.mjs --allow-two-real-tasks ORIGIN ACCESS_JSON NATIVE_RUNNER_ID REPOSITORY NEW_REPORT_DIR [--public-webhook HTTPS_ORIGIN]';
const expectedFile = '# Camp Event Acceptance\nAudience: Acceptance team.\n';
const followupFile = '# Camp Follow-up\nIndependent task: yes.\n';
const stoppedTasks = new Set(['completed', 'cancelled', 'interrupted']);

class VerificationError extends Error {}

function check(condition, message) {
  if (!condition) throw new VerificationError(message);
}

function id(value) {
  check(
    typeof value === 'string' && /^[a-zA-Z0-9_-]{1,128}$/u.test(value),
    'Expected an opaque Camp identifier.',
  );
  return value;
}

function hash(value) {
  return createHash('sha256').update(value).digest('hex');
}

// The permanently claimed directory and ledger are never deleted by the tool.
export class AcceptanceLedger {
  static async create(directory, identity) {
    await mkdir(directory, { mode: 0o700 });
    // Make the exclusive report-directory claim durable before it can reserve
    // any paid work; fsyncing only its contents does not persist its parent entry.
    await syncDirectory(path.dirname(directory));
    const ledger = new AcceptanceLedger(directory, {
      schema: 1,
      ...identity,
      startedAt: new Date().toISOString(),
      limits,
      consumed: { assessments: 0, tasks: 0, githubInputWrites: 0 },
      state: 'preparing',
      requests: [],
    });
    await privateJSON(ledger.filename, ledger.value);
    await syncDirectory(directory);
    return ledger;
  }
  constructor(directory, value) {
    this.directory = directory;
    this.filename = path.join(directory, 'ledger.json');
    this.value = value;
    this.blocked = false;
  }
  async save() {
    const temporary = `${this.filename}.next`;
    await privateJSON(temporary, this.value);
    await rename(temporary, this.filename);
    await syncDirectory(this.directory);
  }
  async execute(kind, details, operation, cost = {}) {
    check(
      !this.blocked,
      'An uncertain operation permanently closed this verification allowance.',
    );
    for (const [key, count] of Object.entries(cost)) {
      check(
        Object.hasOwn(this.value.consumed, key) &&
          Number.isSafeInteger(count) &&
          count >= 0 &&
          this.value.consumed[key] + count <= limits[key],
        'The fixed verification allowance is exhausted.',
      );
    }
    for (const [key, count] of Object.entries(cost))
      this.value.consumed[key] += count;
    const reservation = {
      sequence: this.value.requests.length + 1,
      kind,
      reservedAt: new Date().toISOString(),
      ...details,
      cost,
      state: 'reserved-outcome-unknown',
    };
    this.value.requests.push(reservation);
    try {
      // No network mutation can precede this fsynced reservation.
      await this.save();
      const result = await operation();
      reservation.state = 'confirmed';
      await this.save();
      return result;
    } catch {
      this.blocked = true;
      reservation.state = 'outcome-unknown';
      this.value.state = 'stopped';
      try {
        await this.save();
      } catch {
        /* Preserve the exclusive original claim. */
      }
      throw new VerificationError(
        `${kind} did not produce a durable confirmed outcome. The reservation is consumed and no request was retried.`,
      );
    }
  }
}

// One authority-removing write is allowed even after work admission closed.
// A lost reply is verified only by reads; success/catch share the same attempt.
export function createPolicyRevocation({
  api,
  ledger,
  roomId,
  sourceId,
  policySpec,
}) {
  let attempt;
  return () => (attempt ??= revoke());

  async function revoke() {
    ledger.blocked = true;
    const receipt = {
      policyDisabled: false,
      state: 'unknown',
      startedAt: new Date().toISOString(),
      writeAttempted: false,
      durable: false,
    };
    ledger.value.cleanup = receipt;
    const route = `/api/rooms/${roomId}/automation-policies`;
    let latest;

    async function persist() {
      receipt.finishedAt = new Date().toISOString();
      receipt.durable = true;
      try {
        await ledger.save();
      } catch {
        receipt.durable = false;
      }
      return receipt;
    }

    try {
      latest = (await api(route)).policies.find(
        (item) => item.sourceId === sourceId,
      );
    } catch {
      receipt.reason =
        'The temporary policy could not be read. No disable request was sent.';
      return persist();
    }
    receipt.policyId = latest?.id;
    if (!latest?.enabled) {
      receipt.policyDisabled = true;
      receipt.state = latest ? 'already-disabled' : 'no-policy';
      receipt.confirmedRevision = latest?.revision;
      receipt.confirmedBy = 'readback';
      return persist();
    }
    receipt.requestId = randomUUID();
    receipt.requestedRevision = latest.revision;
    const body = {
      ...policySpec,
      requestId: receipt.requestId,
      revision: latest.revision,
      enabled: false,
      mode: latest.mode,
    };
    const reservation = {
      sequence: ledger.value.requests.length + 1,
      kind: 'disable-test-policy',
      method: 'PUT',
      route: `${route}/${sourceId}`,
      requestId: receipt.requestId,
      body,
      reservedAt: new Date().toISOString(),
      cost: {},
      state: 'reserved-outcome-unknown',
    };
    ledger.value.requests.push(reservation);
    try {
      await ledger.save();
    } catch {
      receipt.reason =
        'The disable intent could not be reserved durably. No disable request was sent.';
      return persist();
    }
    receipt.writeAttempted = true;
    try {
      await api(reservation.route, 'PUT', body);
    } catch {
      receipt.responseUnknown = true;
    }
    try {
      const current = (await api(route)).policies.find(
        (item) => item.sourceId === sourceId,
      );
      if (!current || (current.id === latest.id && current.enabled === false)) {
        receipt.policyDisabled = true;
        receipt.state = 'disabled';
        receipt.confirmedRevision = current?.revision;
        receipt.confirmedBy = receipt.responseUnknown
          ? 'readback-after-unknown'
          : 'readback';
        reservation.state = 'confirmed-disabled';
      } else {
        receipt.reason =
          'The temporary policy is still enabled or changed. No second disable request was sent.';
        reservation.state = 'outcome-unknown';
      }
    } catch {
      receipt.reason =
        'The temporary policy disable could not be verified. No second disable request was sent.';
      reservation.state = 'outcome-unknown';
    }
    return persist();
  }
}

// Failure cleanup removes only this verifier's authority. Each cancellation has
// its own durable identity, including an unknown reply, and is never retried.
export async function cancelTestTasks({
  api,
  ledger,
  tasks,
  policyId,
  taskIds = [],
}) {
  const receipts = [];
  for (const task of tasks) {
    if (
      stoppedTasks.has(task.status) ||
      !(
        (policyId && task.automationPolicyId === policyId) ||
        taskIds.includes(task.id)
      )
    )
      continue;
    const route = `/api/tasks/${id(task.id)}/cancel?view=summary`;
    const body = {
      inputProtocol: 1,
      requestId: randomUUID(),
      revision: task.revision,
    };
    const reservation = {
      sequence: ledger.value.requests.length + 1,
      kind: 'cancel-test-task',
      method: 'POST',
      route,
      requestId: body.requestId,
      body,
      reservedAt: new Date().toISOString(),
      cost: {},
      state: 'reserved-outcome-unknown',
    };
    ledger.value.requests.push(reservation);
    const receipt = {
      taskId: task.id,
      requestId: body.requestId,
      attempted: false,
      state: 'not-dispatched',
    };
    receipts.push(receipt);
    try {
      await ledger.save();
    } catch {
      break;
    }
    receipt.attempted = true;
    try {
      await api(route, 'POST', body);
      receipt.state = reservation.state = 'confirmed';
    } catch {
      receipt.state = reservation.state = 'outcome-unknown';
    }
    try {
      await ledger.save();
    } catch {
      break;
    }
  }
  return receipts;
}

export function ghCommand(args, input) {
  return new Promise((resolve, reject) => {
    const command = execFile(
      'gh',
      args,
      {
        encoding: 'utf8',
        timeout: 30000,
        maxBuffer: 2 << 20,
        windowsHide: true,
      },
      (error, stdout) => {
        if (error)
          reject(
            new VerificationError(
              'GitHub CLI request failed; no request was retried.',
            ),
          );
        else resolve(stdout);
      },
    );
    command.stdin.on('error', () => {});
    command.stdin.end(input);
  });
}

export async function main(args = process.argv.slice(2), dependencies = {}) {
  check(
    (args.length === 6 ||
      (args.length === 8 && args[6] === '--public-webhook')) &&
      args[0] === '--allow-two-real-tasks',
    usage,
  );
  check(
    process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0',
    'TLS certificate verification must remain enabled.',
  );
  const origin = normalizeOrigin(args[1]);
  const publicOrigin = args.length === 8 ? normalizeOrigin(args[7]) : undefined;
  check(
    publicOrigin === undefined || new URL(publicOrigin).protocol === 'https:',
    'The public webhook origin must use verified HTTPS.',
  );
  const runnerId = id(args[3]);
  const repository = args[4];
  check(
    /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9_.-]{1,100}$/u.test(
      repository,
    ) &&
      !repository.endsWith('/.') &&
      !repository.endsWith('/..'),
    'Use one GitHub owner/repository.',
  );
  const access = await readAccess(path.resolve(args[2]), origin);
  const reportDir = path.resolve(args[5]);
  const ledger = await AcceptanceLedger.create(reportDir, {
    origin,
    runnerId,
    repository,
    ...(publicOrigin ? { publicOrigin } : {}),
  });
  const reportFile = path.join(reportDir, 'report.json');
  const report = {
    schema: 1,
    origin,
    runnerId,
    repository,
    startedAt: ledger.value.startedAt,
    status: 'preparing',
    stage: 'preflight',
    limits,
    ingressMode: publicOrigin ? 'github-public' : 'locally-signed',
    scope: publicOrigin
      ? 'GitHub-initiated HTTPS webhook deliveries, real Server assessment, native DSH execution and actual GitHub comments. The supplied temporary ingress is not production availability or whole-application public deployment qualification.'
      : 'Real GitHub objects and comments, real Server assessment and native DSH execution; ingress uses locally signed payloads reconstructed from actual GitHub API objects. Public webhook delivery, DNS and TLS deployment are not tested.',
    checks: [],
    sourceEvents: [],
    tasks: [],
    comments: [],
  };
  await privateJSON(reportFile, report);
  const api = client(origin);
  const gh = dependencies.gh ?? ghCommand;
  const poll = dependencies.pause ?? pause;
  const period = dependencies.pollInterval ?? 1000;
  const deadline = Date.now() + 15 * 60 * 1000;
  const quotaDay = new Date().toISOString().slice(0, 10);
  let configuration;
  let policy;
  let policySpec;
  let issue;
  let githubUser;
  let githubRepository;
  let secret;
  let revokePolicy;
  let publicWebhook;

  async function save() {
    const temporary = `${reportFile}.next`;
    await privateJSON(temporary, report);
    await rename(temporary, reportFile);
    await syncDirectory(reportDir);
  }

  async function closeTestPolicy() {
    revokePolicy ??= createPolicyRevocation({
      api,
      ledger,
      roomId: report.roomId,
      sourceId: report.sourceId,
      policySpec,
    });
    const receipt = await revokePolicy();
    report.cleanup ??= { cancellationRequested: [] };
    report.cleanup.policyDisabled = receipt.policyDisabled;
    report.cleanup.policyRevocation = receipt;
    await save();
    return receipt;
  }

  async function ghAPI(route, method = 'GET', body, allowEmpty = false) {
    const output = await gh(
      [
        'api',
        '--hostname',
        'github.com',
        route,
        '--method',
        method,
        '--header',
        'Accept: application/vnd.github+json',
        '--header',
        'X-GitHub-Api-Version: 2026-03-10',
        ...(body === undefined ? [] : ['--input', '-']),
      ],
      body === undefined ? undefined : JSON.stringify(body),
    );
    if (allowEmpty && output.trim() === '') return null;
    try {
      return parseGitHubJSON(output);
    } catch {
      throw new VerificationError(
        'GitHub returned an unreadable response; no request was retried.',
      );
    }
  }

  async function preflight() {
    check(
      Date.now() < deadline &&
        new Date().toISOString().slice(0, 10) === quotaDay,
      'The verification time or original daily allowance ended; no further work will be sent.',
    );
    const settings = await api('/api/settings/models');
    const { runners } = await api('/api/runners');
    const runner = runners.find((item) => item.id === runnerId);
    const budget = validatePreflight(settings, runner, configuration?.revision);
    check(
      runner.capabilities.taskInputsVersion === 1,
      'The native Runner must support bounded task inputs.',
    );
    configuration ??= { revision: settings.revision, budget };
  }

  async function write(kind, route, method, body, safe = body) {
    return ledger.execute(
      kind,
      { method, route, requestId: body?.requestId, body: safe },
      () => api(route, method, body),
    );
  }

  async function ghWrite(kind, route, body, cost = {}) {
    await preflight();
    return ledger.execute(
      kind,
      { method: 'POST', route: `github:${route}`, body },
      () => ghAPI(route, 'POST', body),
      { githubInputWrites: 1, ...(publicOrigin ? cost : {}) },
    );
  }

  async function wait(description, read) {
    report.stage = description;
    await save();
    while (Date.now() < deadline) {
      const result = await read();
      if (result) return result;
      await poll(period);
    }
    throw new VerificationError(
      `Timed out while observing ${description}. No request was resubmitted.`,
    );
  }

  async function allEvents() {
    let after = 0;
    let through;
    const events = [];
    for (let page = 0; page < 10; page++) {
      const query = new URLSearchParams({ after: String(after), limit: '20' });
      if (through !== undefined) query.set('through', String(through));
      const result = await api(
        `/api/rooms/${report.roomId}/automation-events?${query}`,
      );
      events.push(...result.items);
      through ??= result.through;
      if (!result.hasMore) return events;
      check(
        Number.isSafeInteger(result.nextAfter) && result.nextAfter > after,
        'The event history cursor did not advance.',
      );
      after = result.nextAfter;
    }
    throw new VerificationError(
      'The isolated verification history exceeded its expected bounded size.',
    );
  }

  async function eventState(eventId, expected) {
    return wait(`event ${expected}`, async () => {
      const event = (await allEvents()).find(
        (item) => item.externalEventId === eventId,
      );
      if (!event || ['pending', 'evaluating'].includes(event.status))
        return null;
      check(
        event.status === expected,
        'The source event did not reach its expected decision. Inspect its retained Camp assessment.',
      );
      return event;
    });
  }

  async function taskState(taskId, expected) {
    return wait(`task ${expected}`, async () => {
      const { task } = await api(`/api/tasks/${id(taskId)}?view=summary`);
      check(
        task.maxTurns === limits.roundsPerTask &&
          task.maxModelRequests === limits.modelRequestsPerTask &&
          task.turnsUsed <= limits.roundsPerTask &&
          task.modelRequestsUsed <= limits.modelRequestsPerTask,
        'A task exceeded its frozen verification allowance.',
      );
      check(
        !['needs_attention', 'cancelled', 'interrupted'].includes(task.status),
        'The native task requires attention or stopped. No continuation or retry was authorized.',
      );
      if (task.status === expected) return task;
      check(
        !(
          stoppedTasks.has(task.status) ||
          (expected === 'completed' && task.status === 'waiting_for_person')
        ),
        'The task reached an unexpected terminal state or requested another answer.',
      );
      return null;
    });
  }

  async function sentComment(taskId, questionId = '') {
    return wait('native GitHub comment receipt', async () => {
      const { deliveries } = await api(
        `/api/tasks/${id(taskId)}/deliveries?limit=20`,
      );
      const delivery = deliveries.find(
        (item) => (item.questionId ?? '') === questionId,
      );
      if (!delivery) return null;
      check(
        !['unknown', 'failed', 'blocked'].includes(delivery.status),
        'An external comment requires recovery. No resend was authorized.',
      );
      if (delivery.status !== 'sent') return null;
      const actual = await ghAPI(
        `repos/${repository}/issues/comments/${delivery.commentId}`,
      );
      check(
        actual.id === delivery.commentId &&
          actual.user?.id === githubUser.id &&
          actual.body === `${delivery.body}\n\n${delivery.marker}`,
        'The actual GitHub comment differs from its durable Camp receipt.',
      );
      report.comments.push({
        id: actual.id,
        url: actual.html_url,
        taskId,
        questionId,
        actorId: actual.user.id,
        bodySHA256: hash(actual.body),
        effectId: delivery.id,
      });
      await save();
      if (publicWebhook) {
        const echo = await ingress('issue_comment', actual);
        const ignored = await eventState(echo.eventId, 'ignored');
        check(
          ignored.reason === 'own_output' && !ignored.evaluationId,
          'An actual output webhook entered assessment.',
        );
      }
      return actual;
    });
  }

  function payload(kind, object) {
    return {
      action: kind === 'issues' ? 'opened' : 'created',
      repository: {
        id: githubRepository.id,
        full_name: githubRepository.full_name,
      },
      sender: object.user,
      issue,
      ...(kind === 'issue_comment' ? { comment: object } : {}),
    };
  }

  async function ingress(kind, object, cost = {}, original) {
    await preflight();
    if (publicWebhook) return publicWebhook.receive(kind, object, original);
    const bytes = original?.bytes ?? JSON.stringify(payload(kind, object));
    const guid = original?.guid ?? randomUUID();
    const result = await ledger.execute(
      original ? 'signed-ingress-replay' : 'signed-ingress',
      {
        method: 'POST',
        route: `/api/hooks/github/${report.sourceId}`,
        deliveryId: guid,
        eventKind: kind,
        payloadSHA256: hash(bytes),
        externalObjectId: object.id,
      },
      async () => {
        let response;
        try {
          response = await fetch(
            new URL(`/api/hooks/github/${report.sourceId}`, origin),
            {
              method: 'POST',
              redirect: 'error',
              headers: {
                'Content-Type': 'application/json',
                'X-GitHub-Event': kind,
                'X-GitHub-Delivery': guid,
                'X-Hub-Signature-256': `sha256=${createHmac('sha256', secret).update(bytes).digest('hex')}`,
              },
              body: bytes,
              signal: AbortSignal.timeout(30000),
            },
          );
          check(response.ok, 'Signed GitHub ingress was not accepted.');
          return await response.json();
        } catch {
          throw new VerificationError(
            'Signed ingress did not return a confirmed receipt.',
          );
        }
      },
      cost,
    );
    id(result.eventId);
    if (!original)
      report.sourceEvents.push({
        eventId: result.eventId,
        deliveryId: guid,
        payloadSHA256: hash(bytes),
        kind,
        externalObjectId: object.id,
      });
    await save();
    return { ...result, bytes, guid };
  }

  async function exactFile(task, filename, expected) {
    check(
      /^[0-9a-f]{40}$/u.test(task.publishedCommit),
      'Expected a published immutable Git commit.',
    );
    const text = await api(
      `/api/resources/${report.resourceId}/file?${new URLSearchParams({ path: filename, commit: task.publishedCommit })}`,
      'GET',
      undefined,
      true,
    );
    check(
      text === expected,
      'The published file does not match its exact acceptance bytes.',
    );
    return { path: filename, commit: task.publishedCommit, sha256: hash(text) };
  }

  async function snapshotTasks(expectedCount) {
    const { tasks } = await api(
      `/api/rooms/${report.roomId}/tasks?view=summary`,
    );
    check(
      tasks.length === expectedCount && tasks.length <= limits.tasks,
      'The source admitted an unexpected number of tasks.',
    );
    let rounds = 0;
    let requests = 0;
    const summaries = [];
    for (const task of tasks) {
      check(
        task.maxTurns === limits.roundsPerTask &&
          task.maxModelRequests === limits.modelRequestsPerTask,
        'The admitted task allowance differs from the fixed authorization.',
      );
      rounds += task.turnsUsed;
      requests += task.modelRequestsUsed;
      const { items, nextCursor } = await api(`/api/tasks/${id(task.id)}/runs`);
      check(
        !nextCursor && items.length <= limits.roundsPerTask,
        'The task produced too many execution rounds.',
      );
      const runs = [];
      for (const item of items) {
        const { run } = await api(`/api/runs/${id(item.id)}`);
        check(
          run.runnerId === runnerId &&
            run.executor === 'native' &&
            run.taskInputProtocol === 1,
          'A task round did not use the selected native Runner and bounded input protocol.',
        );
        if (run.published)
          check(
            run.status === 'succeeded' &&
              run.checks?.length === 1 &&
              run.checks[0].status === 'passed',
            'Final publication lacks the actual configured check result.',
          );
        runs.push({
          ...runSummary(run),
          checks: (run.checks ?? []).map((entry) => ({
            name: entry.name,
            status: entry.status,
            exitCode: entry.exitCode,
          })),
        });
      }
      summaries.push({
        id: task.id,
        status: task.status,
        maxTurns: task.maxTurns,
        turnsUsed: task.turnsUsed,
        maxModelRequests: task.maxModelRequests,
        modelRequestsUsed: task.modelRequestsUsed,
        inputRevision: task.inputRevision,
        publishedCommit: task.publishedCommit,
        sourceEventId: task.sourceEventId,
        automationSourceEventId: task.automationSourceEventId,
        runs,
      });
    }
    check(
      rounds <= limits.totalRounds && requests <= limits.totalModelRequests,
      'The total native execution allowance was exceeded.',
    );
    const evaluations = new Set(
      (await allEvents()).map((event) => event.evaluationId).filter(Boolean),
    );
    check(
      evaluations.size <= limits.assessments,
      'The assessment allowance was exceeded.',
    );
    report.tasks = summaries;
    report.counts = {
      tasks: tasks.length,
      rounds,
      modelRequests: requests,
      assessments: evaluations.size,
    };
    await save();
    return tasks;
  }

  async function savePolicy(mode, enabled = true) {
    await preflight();
    const body = {
      ...policySpec,
      requestId: randomUUID(),
      revision: policy?.revision ?? 0,
      mode,
      enabled,
    };
    const result = await write(
      'save-policy',
      `/api/rooms/${report.roomId}/automation-policies/${report.sourceId}`,
      'PUT',
      body,
    );
    policy = result.policy;
    check(
      policy.maxTurns === limits.roundsPerTask &&
        policy.maxModelRequests === limits.modelRequestsPerTask &&
        policy.dailyEvaluations === limits.assessments &&
        policy.dailyTasks === limits.tasks,
      'The saved policy exceeded the fixed allowance.',
    );
    report.policyId = policy.id;
    report.policyRevision = policy.revision;
    await save();
  }

  try {
    const { user } = await api('/api/auth/login', 'POST', access);
    check(
      user?.role === 'admin' && user.enabled === true,
      'Use an enabled Camp administrator.',
    );
    await preflight();
    report.modelRevision = configuration.revision;
    report.executionBudget = configuration.budget;
    ledger.value.modelRevision = configuration.revision;
    ledger.value.executionBudget = configuration.budget;
    await ledger.save();
    await save();
    githubUser = await ghAPI('user');
    githubRepository = await ghAPI(`repos/${repository}`);
    check(
      Number.isSafeInteger(githubUser.id) &&
        githubUser.id > 0 &&
        githubUser.type === 'User',
      'Use an authenticated GitHub user account.',
    );
    check(
      githubRepository.private === true &&
        Number.isSafeInteger(githubRepository.id) &&
        githubRepository.id > 0 &&
        githubRepository.full_name.toLowerCase() === repository.toLowerCase(),
      'Use the explicitly chosen private GitHub repository.',
    );
    check(
      (await ghAPI(`repos/${repository}/issues?state=all&per_page=1`))
        .length === 0,
      'Use a fresh example repository with no existing issues or pull requests.',
    );
    const token = (
      await gh(['auth', 'token', '--hostname', 'github.com'])
    ).trim();
    check(
      token.length >= 20 && token.length <= 1024 && !/\s/u.test(token),
      'The GitHub CLI did not provide a usable private credential.',
    );
    report.githubActorId = githubUser.id;
    report.repositoryId = githubRepository.id;
    report.repositoryURL = `https://github.com/${repository}`;
    const tag = randomUUID().slice(0, 8);
    const { room: createdRoom } = await write(
      'create-room',
      '/api/rooms',
      'POST',
      {
        name: `Event automation acceptance ${tag}`,
        description: publicOrigin
          ? 'Two finite native tasks with GitHub-initiated HTTPS webhooks and actual GitHub comments.'
          : 'Two finite native tasks with real GitHub comments and locally signed real-object ingress.',
      },
    );
    const roomId = (report.roomId = id(createdRoom.id));
    const resourceId = (report.resourceId = id(createdRoom.resourceId));
    await save();
    await write('assign-runner', `/api/rooms/${roomId}/runner`, 'PUT', {
      runnerId,
      revision: createdRoom.runnerRevision,
    });
    const { resource } = await api(`/api/resources/${resourceId}`);
    await write('configure-check', `/api/resources/${resourceId}`, 'PATCH', {
      expectedRevision: resource.revision,
      environment: {
        kind: 'command',
        checks: [
          {
            name: 'event-acceptance',
            arguments: [
              'bash',
              '-c',
              "test -f event-acceptance.md && printf '# Camp Event Acceptance\\nAudience: Acceptance team.\\n' | cmp -s - event-acceptance.md",
            ],
          },
        ],
      },
    });
    const { agent } = await write(
      'create-writer',
      `/api/rooms/${roomId}/agents`,
      'POST',
      {
        name: 'Event Acceptance Writer',
        handle: `event-writer-${tag}`,
        preferredRunnerId: runnerId,
        access: 'write',
        enabled: true,
        instructions:
          'Follow the current collaboration goal and handoff. When the goal explicitly requires a missing human answer, ask once with camp_ask_person before editing. Once that answer is present, do not ask the same question again. Use actual file or bash tools, read back exact requested bytes, then call camp_finish with artifact paths. Never hand off to yourself or invent another execution allowance.',
      },
    );
    report.agentId = id(agent.id);
    secret = randomBytes(32).toString('hex');
    const sourceRequest = { requestId: randomUUID(), repository, secret };
    const { source } = await write(
      'create-source',
      `/api/rooms/${roomId}/github-sources`,
      'POST',
      sourceRequest,
      { requestId: sourceRequest.requestId, repository },
    );
    report.sourceId = id(source.id);
    policySpec = {
      rules:
        'Accept explicit new document requests for this acceptance workspace. Preserve exact filenames, file bytes and ordered ask-before-write steps in an accepted goal. An observation decision is not executed work: accept a later explicit start when there is no admitted task with that goal. Related active tasks receive context; ignore a request already being executed. After a task has completed, accept an explicitly separate follow-up request as a new task, never a continuation.',
      allowedActorIds: [String(githubUser.id)],
      participantAgentIds: [agent.id],
      firstAgentId: agent.id,
      maxTurns: limits.roundsPerTask,
      maxModelRequests: limits.modelRequestsPerTask,
      dailyEvaluations: limits.assessments,
      dailyTasks: limits.tasks,
      allowAnswers: true,
      allowComments: true,
    };
    await savePolicy('observe');
    const credentialRequest = {
      requestId: randomUUID(),
      revision: policy.revision,
      token,
      actorId: githubUser.id,
    };
    const credential = await write(
      'save-comment-credential',
      `/api/github-sources/${source.id}/comment-credential`,
      'PUT',
      credentialRequest,
      {
        requestId: credentialRequest.requestId,
        revision: policy.revision,
        actorId: githubUser.id,
      },
    );
    policy = credential.policy;
    report.policyRevision = policy.revision;
    await save();
    if (publicOrigin) {
      publicWebhook = (dependencies.publicWebhook ?? createPublicWebhook)({
        github: ghAPI,
        ledger,
        api,
        report,
        save,
        wait,
        preflight,
        publicOrigin,
        secret,
      });
      await publicWebhook.connect();
      report.checks.push('github-initiated-https-ping');
    }
    const firstRequest = `Please create event-acceptance.md. FIRST use camp_ask_person to ask who the audience should be, then wait for an answer. Do not write the file or finish before that answer. After the answer, use actual tools to write exactly these two lines with a final newline:\n${expectedFile}\nRead the file back and call camp_finish listing event-acceptance.md. The supplied audience must be confirmed by the answer, not assumed from the example bytes. Do not change other files.`;
    const createdIssue = await ghWrite(
      'github-create-issue',
      `repos/${repository}/issues`,
      { title: `Camp event acceptance ${tag}`, body: firstRequest },
      { assessments: 1 },
    );
    check(
      Number.isSafeInteger(createdIssue.number) && createdIssue.number > 0,
      'GitHub did not return an issue identity.',
    );
    report.issueNumber = createdIssue.number;
    report.issueURL = createdIssue.html_url;
    await save();
    issue = await ghAPI(`repos/${repository}/issues/${createdIssue.number}`);
    check(
      issue.user?.id === githubUser.id,
      'The actual issue belongs to another GitHub actor.',
    );
    const observed = await ingress('issues', issue, { assessments: 1 });
    const observation = await eventState(observed.eventId, 'observed');
    const decision = await api(
      `/api/automation-evaluations/${id(observation.evaluationId)}`,
    );
    check(
      decision.assessment?.outcome === 'accept',
      'The real assessment did not accept the observation as a candidate.',
    );
    await snapshotTasks(0);
    report.checks.push('real-assessment-observe-without-task');
    await savePolicy('auto');
    await snapshotTasks(0);
    check(
      (await allEvents()).filter((event) => event.evaluationId).length === 1,
      'Enabling Auto replayed an earlier observation.',
    );
    report.checks.push('auto-new-frontier-no-history-replay');
    const createComment = async (kind, text, cost = {}) => {
      const created = await ghWrite(
        kind,
        `repos/${repository}/issues/${issue.number}/comments`,
        { body: text },
        cost,
      );
      check(
        Number.isSafeInteger(created.id) && created.id > 0,
        'GitHub did not return the comment identity.',
      );
      report.comments.push({
        id: created.id,
        url: created.html_url,
        actorId: githubUser.id,
        role: kind,
        bodySHA256: hash(text),
      });
      await save();
      const actual = await ghAPI(
        `repos/${repository}/issues/comments/${created.id}`,
      );
      check(
        actual.user?.id === githubUser.id && actual.body === text,
        'The actual GitHub input comment differs from the requested bytes.',
      );
      return actual;
    };
    const start = await createComment(
      'github-request-first-task',
      `Start this previously observed work now as a new task. The earlier observation admitted no execution. Complete this request:\n\n${firstRequest}`,
      { assessments: 1, tasks: 1 },
    );
    const admitted = await ingress('issue_comment', start, {
      assessments: 1,
      tasks: 1,
    });
    const accepted = await eventState(admitted.eventId, 'accepted');
    const firstId = (report.firstTaskId = id(accepted.taskId));
    await save();
    const question = await taskState(firstId, 'waiting_for_person');
    check(
      question.question?.id && !question.question.answer,
      'The native task did not leave one unanswered question.',
    );
    report.questionId = id(question.question.id);
    await sentComment(firstId, question.question.id);
    report.checks.push('native-ask-person-and-actual-github-question');
    const answer = await createComment(
      'github-answer-first-task',
      `/camp answer ${question.question.id} Acceptance team.`,
    );
    const answered = await ingress('issue_comment', answer);
    await eventState(answered.eventId, 'answered');
    const first = await taskState(firstId, 'completed');
    check(
      first.question?.answer === 'Acceptance team.' &&
        first.question.answeredByUserId === '' &&
        first.question.answeredByExternalActorId === String(githubUser.id),
      'The external answer lost its exact content or independent actor attribution.',
    );
    report.firstFile = await exactFile(
      first,
      'event-acceptance.md',
      expectedFile,
    );
    const actualFinal = await sentComment(firstId);
    await snapshotTasks(1);
    report.checks.push(
      'external-answer-native-finish-checks-and-exact-publication',
    );
    const beforeReplay = { ...report.counts };
    const replay = await ingress('issue_comment', start, {}, admitted);
    check(
      replay.duplicate === true && replay.eventId === admitted.eventId,
      'The original signed delivery did not replay its immutable receipt.',
    );
    await snapshotTasks(1);
    check(
      ['tasks', 'rounds', 'modelRequests', 'assessments'].every(
        (key) => report.counts[key] === beforeReplay[key],
      ),
      'Redelivery created additional work or assessment.',
    );
    if (!publicWebhook) {
      const echo = await ingress('issue_comment', actualFinal);
      const ignored = await eventState(echo.eventId, 'ignored');
      check(
        ignored.reason === 'own_output' && !ignored.evaluationId,
        'The actual native comment echo entered assessment.',
      );
    }
    await snapshotTasks(1);
    report.checks.push(
      'delivery-guid-replay-and-actual-own-comment-no-new-work',
    );
    const firstSnapshot = {
      revision: first.revision,
      inputRevision: first.inputRevision,
      turnsUsed: first.turnsUsed,
      publishedCommit: first.publishedCommit,
    };
    const followup = await createComment(
      'github-request-second-task',
      `New independent request after the earlier task completed: create only follow-up.md with exactly these two lines and a final newline:\n${followupFile}\nPreserve event-acceptance.md exactly. Do not repeat the earlier audience question. Use real tools, read the file back and finish with camp_finish listing follow-up.md.`,
      { assessments: 1, tasks: 1 },
    );
    const followupAdmission = await ingress('issue_comment', followup, {
      assessments: 1,
      tasks: 1,
    });
    const secondAccepted = await eventState(
      followupAdmission.eventId,
      'accepted',
    );
    const secondId = (report.secondTaskId = id(secondAccepted.taskId));
    check(
      secondId !== firstId,
      'A completed task was revived instead of creating a new finite task.',
    );
    const second = await taskState(secondId, 'completed');
    report.followupFile = await exactFile(second, 'follow-up.md', followupFile);
    await exactFile(second, 'event-acceptance.md', expectedFile);
    await sentComment(secondId);
    const { task: original } = await api(`/api/tasks/${firstId}?view=summary`);
    check(
      original.status === 'completed' &&
        Object.entries(firstSnapshot).every(
          ([key, value]) => original[key] === value,
        ),
      'The follow-up request mutated the earlier completed task.',
    );
    await snapshotTasks(2);
    const actualComments = await ghAPI(
      `repos/${repository}/issues/${issue.number}/comments?per_page=100`,
    );
    check(
      Array.isArray(actualComments) &&
        actualComments.length <= limits.githubComments &&
        actualComments.every((comment) => comment.user?.id === githubUser.id),
      'The real GitHub conversation exceeded its comment allowance or contains an unexpected actor.',
    );
    report.counts.githubComments = actualComments.length;
    report.counts.githubInputWrites = ledger.value.consumed.githubInputWrites;
    report.checks.push(
      'terminal-followup-new-task-new-allowance-old-task-unchanged',
      'actual-native-comment-receipts-and-fixed-counts',
    );
    if (publicWebhook)
      report.checks.push(
        'all-three-actual-output-webhooks-ignored-without-assessment',
      );
    const cleanup = await closeTestPolicy();
    if (publicWebhook) {
      report.cleanup.githubWebhook = await publicWebhook.close();
      await save();
      check(
        report.cleanup.githubWebhook.disabled &&
          report.cleanup.githubWebhook.durable,
        'The temporary GitHub webhook could not be confirmed inactive.',
      );
      report.checks.push('temporary-github-webhook-disabled-and-read-back');
    }
    check(
      cleanup.policyDisabled && cleanup.durable,
      'The temporary policy could not be confirmed disabled with a durable receipt. No cleanup mutation was retried.',
    );
    report.checks.push('temporary-policy-disabled-and-read-back');
    report.status = ledger.value.state = 'passed';
    report.finishedAt = ledger.value.finishedAt = new Date().toISOString();
    await ledger.save();
    await save();
    console.log(
      `Passed: real assessment, two native tasks, external answer and actual GitHub comments. Private report: ${reportDir}`,
    );
    return report;
  } catch (error) {
    report.status = ledger.value.state = 'stopped';
    report.finishedAt = ledger.value.finishedAt = new Date().toISOString();
    report.reason =
      error instanceof VerificationError ||
      error instanceof PublicWebhookVerificationError
        ? error.message
        : 'Verification stopped after an unexpected local error. No operation was retried.';
    report.cleanup ??= { policyDisabled: false, cancellationRequested: [] };
    // Cleanup removes authority only. It does not retry an uncertain write or
    // create another task, prompt, comment or execution allowance.
    if (report.roomId && report.sourceId && policySpec) {
      try {
        await closeTestPolicy();
      } catch {
        /* The private report retains all known source/Room identities. */
      }
      if (publicWebhook) {
        try {
          report.cleanup.githubWebhook = await publicWebhook.close();
          await save();
        } catch {
          /* Policy and hook revocation are independent single attempts. */
        }
      }
      try {
        const { tasks } = await api(
          `/api/rooms/${report.roomId}/tasks?view=summary`,
        );
        report.cleanup.cancellations = await cancelTestTasks({
          api,
          ledger,
          tasks,
          policyId: report.policyId,
          taskIds: [report.firstTaskId, report.secondTaskId].filter(Boolean),
        });
        report.cleanup.cancellationRequested = report.cleanup.cancellations
          .filter((receipt) => receipt.attempted)
          .map((receipt) => receipt.taskId);
      } catch {
        /* Never hide the original failure behind recovery diagnostics. */
      }
    }
    try {
      await ledger.save();
      await save();
    } catch {
      /* Keep the exclusive original ledger. */
    }
    throw new VerificationError(report.reason);
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main().catch((error) => {
    console.error(
      error instanceof VerificationError
        ? error.message
        : 'Verification could not start. Inspect private inputs and the permanent ledger; no automatic retry was performed.',
    );
    process.exitCode = 1;
  });
}
