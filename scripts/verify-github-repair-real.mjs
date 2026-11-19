import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { readFile, rename } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { setTimeout as pause } from 'node:timers/promises';
import {
  createPublicWebhook,
  parseGitHubJSON,
} from './automation-public-webhook.mjs';
import {
  AcceptanceLedger,
  cancelTestTasks,
  createPolicyRevocation,
  ghCommand,
} from './verify-automation-real.mjs';
import {
  client,
  normalizeOrigin,
  privateJSON,
  readAccess,
  syncDirectory,
  validatePreflight,
} from './verify-remote-real.mjs';

const workflow = 'camp-ci-repair.yml';
const variable = 'CAMP_CI_REPAIR_CASE';
const budget = {
  maxEvaluations: 1,
  maxTasks: 2,
  maxModelRequests: 24,
  maxActions: 4,
  maxFollowUps: 0,
};
const initialContent = 'Search is ready.\n';
const requestCeilings = { maxTokens: 8192, maxSteps: 32, timeoutSeconds: 600 };
const opaque = /^[a-zA-Z0-9_-]{1,128}$/u;
const sha = /^[a-f0-9]{40}$/u;

async function github(route, method = 'GET', body) {
  const output = await ghCommand(
    [
      'api',
      '--hostname',
      'github.com',
      route,
      '--method',
      method,
      ...(body === undefined ? [] : ['--input', '-']),
    ],
    body === undefined ? undefined : JSON.stringify(body),
  );
  return output.trim() ? parseGitHubJSON(output) : null;
}

function decodeFile(file) {
  assert.equal(file.encoding, 'base64');
  return Buffer.from(file.content, 'base64').toString('utf8');
}

async function main(args) {
  const [
    flag,
    address,
    accessFile,
    runnerId,
    agentId,
    repository,
    publicOrigin,
    destination,
  ] = args;
  assert.ok(
    args.length === 8 && flag === '--allow-real-ci-repair',
    'Usage: node scripts/verify-github-repair-real.mjs --allow-real-ci-repair ORIGIN ACCESS_JSON NATIVE_RUNNER_ID AGENT_ID PRIVATE_FIXTURE_REPO HTTPS_INGRESS NEW_REPORT_DIR',
  );
  assert.ok(opaque.test(runnerId) && opaque.test(agentId));
  assert.match(repository, /^[a-zA-Z0-9-]+\/[a-zA-Z0-9_.-]+$/u);
  const ingress = new URL(publicOrigin);
  assert.ok(ingress.protocol === 'https:' && ingress.origin === publicOrigin);
  const origin = normalizeOrigin(address);
  const access = await readAccess(accessFile, origin);
  const directory = resolve(destination);
  const ledger = await AcceptanceLedger.create(directory, {
    origin,
    runnerId,
    repository,
    publicOrigin,
  });
  const api = client(origin);
  const repoPath = `repos/${repository}`;
  const tag = randomBytes(6).toString('hex');
  const path = `notes/${tag}.txt`;
  const marker = `Release marker: ${randomBytes(12).toString('hex')}`;
  const title = `Add the search release note ${tag}`;
  const expectedWorkflow = await readFile(
    new URL(`./fixtures/${workflow}`, import.meta.url),
    'utf8',
  );
  const report = {
    schema: 1,
    repository,
    runnerId,
    agentId,
    path,
    title,
    budget,
    status: 'running',
    stage: 'preflight',
    sourceEvents: [],
    attentionReads: 0,
  };
  const reportPath = join(directory, 'report.json');
  let webhook, disablePolicy, failure, modelRevision;

  async function save(stage = report.stage) {
    report.stage = stage;
    await privateJSON(reportPath + '.next', report);
    await rename(reportPath + '.next', reportPath);
    await syncDirectory(directory);
  }

  const deadline = Date.now() + 20 * 60 * 1000;
  const quotaDay = new Date().toISOString().slice(0, 10);

  async function wait(label, read) {
    while (Date.now() < deadline) {
      const result = await read();
      if (result) return result;
      await pause(3000);
    }
    throw new Error(`Timed out waiting for ${label}.`);
  }

  async function preflight() {
    assert.ok(
      Date.now() < deadline &&
        new Date().toISOString().slice(0, 10) === quotaDay,
      'The original time or daily allowance expired.',
    );
    const [model, { runners }] = await Promise.all([
      api('/api/settings/models'),
      api('/api/runners'),
    ]);
    const runner = runners.find((entry) => entry.id === runnerId);
    const limits = validatePreflight(
      model,
      runner,
      modelRevision,
      requestCeilings,
    );
    assert.equal(runner.capabilities.taskInputsVersion, 1);
    modelRevision ??= model.revision;
    report.model = { revision: modelRevision, ...limits };
  }

  const write = (kind, route, method, body, safe = body) =>
    ledger.execute(kind, { route, method, body: safe }, () =>
      api(route, method, body),
    );
  const workItems = (roomId) => api(`/api/rooms/${roomId}/work-items?limit=4`);
  const roomTasks = (roomId) => api(`/api/rooms/${roomId}/tasks?view=summary`);
  const readWorkflow = (ref) =>
    github(
      `${repoPath}/contents/.github/workflows/${workflow}?ref=${ref}`,
    ).then(decodeFile);

  try {
    await save();
    const { user } = await api('/api/auth/login', 'POST', access);
    assert.ok(user.enabled && user.role === 'admin');
    const [repo, actor, hooks, pullRequests, { repositories }, { agents }] =
      await Promise.all([
        github(repoPath),
        github('user'),
        github(`${repoPath}/hooks`),
        github(`${repoPath}/pulls?state=open&per_page=10`),
        api('/api/github-repositories'),
        api('/api/agents'),
      ]);
    assert.ok(
      repo.private &&
        repo.full_name === repository &&
        repo.default_branch === 'main' &&
        repo.permissions?.admin &&
        repo.owner.id === actor.id &&
        repo.allow_squash_merge,
      'Use a private fixture repository owned by the signed-in GitHub account with squash merges enabled.',
    );
    assert.ok(
      pullRequests.length === 0 && !hooks.some((hook) => hook.active),
      'The test repository must have no active webhooks or open PRs.',
    );
    assert.equal(
      await readWorkflow('main'),
      expectedWorkflow,
      'Install the exact CI fixture workflow first.',
    );
    const connection = repositories.find(
      (entry) =>
        entry.enabled &&
        entry.private &&
        entry.repository === repository &&
        entry.repositoryId === repo.id &&
        entry.actorId === actor.id &&
        entry.ciWorkflow === workflow &&
        Number.isSafeInteger(entry.ciWorkflowId),
    );
    assert.ok(
      connection,
      'Connect this fixture repository in Camp with the fixture workflow.',
    );
    const agent = agents.find((entry) => entry.id === agentId);
    assert.ok(
      agent?.enabled &&
        agent.access === 'write' &&
        agent.preferredRunnerId === runnerId,
      'Select an approved writing Agent explicitly routed to this Runner.',
    );
    await preflight();
    Object.assign(report, {
      repositoryId: repo.id,
      githubActorId: actor.id,
      workflowId: connection.ciWorkflowId,
    });
    await save('fixture-configuration');

    const { variables } = await github(
      `${repoPath}/actions/variables?per_page=100`,
    );
    const existing = variables.some((entry) => entry.name === variable);
    const variableRoute = `${repoPath}/actions/variables${existing ? '/' + variable : ''}`;
    const variableMethod = existing ? 'PATCH' : 'POST';
    const fixtureValue = JSON.stringify({ path, marker });
    await ledger.execute(
      'configure-ci-fixture',
      { route: variableRoute, method: variableMethod, path },
      () =>
        github(variableRoute, variableMethod, {
          name: variable,
          value: fixtureValue,
        }),
    );
    assert.equal(
      (await github(`${repoPath}/actions/variables/${variable}`)).value,
      fixtureValue,
    );

    const { room } = await write('create-fixture-room', '/api/rooms', 'POST', {
      name: `CI repair fixture ${tag}`,
    });
    const roomId = (report.roomId = room.id);
    await save('room-configuration');
    await write(
      'join-approved-agent',
      `/api/rooms/${roomId}/members/${agentId}`,
      'PUT',
      {},
    );
    const { resource } = await api(`/api/resources/${room.resourceId}`);
    await write(
      'configure-local-check',
      `/api/resources/${room.resourceId}`,
      'PATCH',
      {
        expectedRevision: resource.revision,
        environment: {
          kind: 'command',
          checks: [
            {
              name: 'release-note-summary',
              arguments: [
                'bash',
                '-c',
                'test "$(head -n 1 -- "$1")" = "Search is ready."',
                '--',
                path,
              ],
            },
          ],
        },
      },
    );
    const route = await api(
      `/api/rooms/${roomId}/work-execution?completion=automatic&agentId=${agentId}`,
    );
    assert.ok(
      !route.issue,
      'The fixture Room cannot use automatic Work with the selected Agent.',
    );
    const secret = randomBytes(32).toString('hex');
    const sourceInput = { requestId: randomUUID(), repository };
    const { source } = await write(
      'create-github-source',
      `/api/rooms/${roomId}/github-sources`,
      'POST',
      { ...sourceInput, secret },
      sourceInput,
    );
    report.sourceId = source.id;
    const policySpec = {
      rules: `Execute the new issue titled "${title}" from the selected GitHub actor as one Work item. Its completion criterion is that ${path} starts with the exact summary line and no other file changes. The authorized CI repair may add the reported marker to that file. TLS callback and CI are delivery steps. The initial execution finishes after locally verifying the summary. Ignore unrelated events.`,
      allowedActorIds: [String(actor.id)],
      participantAgentIds: [agentId],
      firstAgentId: agentId,
      maxTurns: 3,
      maxModelRequests: 12,
      dailyEvaluations: 1,
      dailyTasks: 2,
      itemBudget: budget,
      allowAnswers: false,
      allowComments: false,
    };
    disablePolicy = createPolicyRevocation({
      api,
      ledger,
      roomId,
      sourceId: source.id,
      policySpec,
    });
    const { policy } = await write(
      'authorize-one-issue-and-repair',
      `/api/rooms/${roomId}/automation-policies/${source.id}`,
      'PUT',
      {
        ...policySpec,
        requestId: randomUUID(),
        revision: 0,
        enabled: true,
        mode: 'auto',
      },
    );
    report.policyId = policy.id;
    const delivery = {
      connectionId: connection.id,
      connectionRevision: connection.revision,
      baseBranch: 'main',
      mode: 'merge',
      maxRepairs: 1,
    };
    const { settings } = await write(
      'grant-fixture-delivery',
      `/api/rooms/${roomId}/project-settings`,
      'POST',
      {
        requestId: randomUUID(),
        revision: 0,
        enabled: true,
        participantAgentIds: [agentId],
        firstAgentId: agentId,
        budget,
        completionMode: 'automatic',
        githubDelivery: delivery,
        ruleId: policy.id,
        ruleRevision: policy.revision,
      },
    );
    assert.deepEqual(settings.githubDelivery, delivery);
    assert.deepEqual(settings.budget, budget);
    assert.deepEqual((await workItems(roomId)).items, []);
    report.projectRevision = settings.revision;
    await save('webhook-setup');
    webhook = createPublicWebhook({
      github,
      ledger,
      api,
      report,
      save,
      wait,
      preflight,
      publicOrigin,
      secret,
    });
    await webhook.connect();
    await preflight();

    const issueRoute = `${repoPath}/issues`;
    const body = [
      `Create \`${path}\` with \`Search is ready.\` as its exact first line and a final newline. Write only that line during the initial task, verify it and finish.`,
      'The deployment check validates a second line after a PR is opened. If it reports a specific required release marker, add that exact line below the summary on the repair task.',
      'Change only this note. Preserve the CI workflow and finish when the current instruction has been verified.',
    ].join('\n\n');
    await save('issue-creation');
    const created = await ledger.execute(
      'create-one-fixture-issue',
      { route: issueRoute, title },
      () => github(issueRoute, 'POST', { title, body }),
      { assessments: 1, tasks: 2, githubInputWrites: 1 },
    );
    const issue = await github(`${issueRoute}/${created.number}`);
    assert.ok(
      issue.id === created.id &&
        issue.title === title &&
        issue.user.id === actor.id &&
        issue.body === body,
    );
    report.issue = { number: issue.number, url: issue.html_url };
    await save('issue-webhook');
    const ingressReceipt = await webhook.receive('issues', issue);
    await save('issue-assessment');
    const admission = await wait('the recorded issue decision', async () => {
      const { items } = await api(
        `/api/rooms/${roomId}/automation-events?sourceId=${source.id}&limit=20`,
      );
      const event = items.find(
        (item) => item.externalEventId === ingressReceipt.eventId,
      );
      if (!event || ['pending', 'evaluating'].includes(event.status))
        return null;
      report.admission = {
        status: event.status,
        evaluationId: event.evaluationId,
        taskId: event.taskId,
      };
      assert.ok(
        event.status === 'accepted' && event.taskId,
        'The issue did not admit the initial collaboration. Inspect the retained assessment.',
      );
      return event;
    });
    await save('automatic-delivery');

    const completed = await wait(
      'an automatic repair and exact-head merge',
      async () => {
        const { items } = await workItems(roomId);
        assert.ok(
          items.length <= 1,
          'The single issue admitted duplicate Work.',
        );
        const selected = items[0];
        if (!selected) return null;
        const { item } = await api(`/api/work-items/${selected.id}`);
        assert.equal(item.policyId, policy.id);
        report.workId = item.id;
        const current = item.githubDelivery;
        const transition = {
          state: item.status,
          execution: item.execution?.status,
          delivery: current?.status,
          stage: current?.stage,
          pullRequest: current?.pullRequest,
          head: current?.headSha,
          repairs: current?.repairs,
        };
        const seen = (report.transitions ??= []);
        if (JSON.stringify(seen.at(-1)) !== JSON.stringify(transition)) {
          seen.push(transition);
          await save();
        }
        const attention = await api(`/api/attention?roomId=${roomId}`);
        report.attentionReads++;
        if (attention.total) {
          report.attention = attention.items.map(
            ({ kind, reason, target }) => ({ kind, reason, target }),
          );
          throw new Error('The fixture requested unplanned human attention.');
        }
        assert.ok(item.usage.tasks <= 2 && (current?.repairs ?? 0) <= 1);
        assert.ok(
          item.status !== 'cancelled' && current?.status !== 'blocked',
          'The automatic delivery stopped before its exact-head merge.',
        );
        return item.status === 'completed' && current?.status === 'succeeded'
          ? item
          : null;
      },
    );

    await save('verify-receipts');
    const result = completed.githubDelivery;
    const initialHead = result.previousHeadSha;
    const repairedHead = result.headSha;
    for (const commit of [initialHead, repairedHead, result.mergeSha])
      assert.match(commit, sha);
    assert.ok(
      initialHead !== repairedHead &&
        result.repairs === 1 &&
        result.issueNumber === issue.number &&
        result.repositoryId === repo.id,
    );
    const taskIds = [result.initialTaskId, result.repairTaskId];
    assert.equal(taskIds[0], admission.taskId);
    assert.ok(
      taskIds.every((id) => opaque.test(id)) && taskIds[0] !== taskIds[1],
    );
    const { tasks } = await roomTasks(roomId);
    assert.deepEqual(tasks.map(({ id }) => id).sort(), [...taskIds].sort());
    for (const task of tasks) {
      assert.ok(
        task.status === 'completed' &&
          task.workItemId === completed.id &&
          task.modelRequestsUsed > 0 &&
          task.modelRequestsUsed <= 12 &&
          task.participants.length === 1 &&
          task.participants[0].runnerId === runnerId,
      );
    }
    const requests = tasks.reduce(
      (sum, task) => sum + task.modelRequestsUsed,
      0,
    );
    assert.equal(completed.usage.modelRequests, requests);
    assert.equal(completed.usage.reservedModelRequests, requests);
    assert.equal(completed.usage.tasks, 2);
    assert.equal(completed.usage.actions, 4);
    const { event } = await api(`/api/github-events/${ingressReceipt.eventId}`);
    assert.equal(event.objectId, issue.id);
    const feedback = completed.evidence.filter(
      ({ kind }) => kind === 'delivery.feedback',
    );
    assert.equal(feedback.length, 1);
    const { event: diagnostic } = await api(
      `/api/automation-events/${encodeURIComponent(feedback[0].eventId)}`,
    );
    assert.ok(
      diagnostic.workItemId === completed.id &&
        diagnostic.actorKind === 'system' &&
        diagnostic.content.includes(initialHead) &&
        diagnostic.content.includes(marker),
      'The recorded repair must contain the failed head and actual job diagnostic.',
    );

    async function checkRun(head, conclusion) {
      const { workflow_runs: runs } = await github(
        `${repoPath}/actions/workflows/${connection.ciWorkflowId}/runs?head_sha=${head}&event=pull_request&per_page=10`,
      );
      const run = runs.find(
        (entry) => entry.head_sha === head && entry.event === 'pull_request',
      );
      assert.ok(
        run?.status === 'completed' &&
          run.conclusion === conclusion &&
          run.workflow_id === connection.ciWorkflowId,
      );
      return { id: run.id, head, conclusion, url: run.html_url };
    }

    const [
      pr,
      failed,
      passed,
      diff,
      files,
      initial,
      published,
      trustedWorkflow,
    ] = await Promise.all([
      github(`${repoPath}/pulls/${result.pullRequest}`),
      checkRun(initialHead, 'failure'),
      checkRun(repairedHead, 'success'),
      github(`${repoPath}/compare/${initialHead}...${repairedHead}`),
      github(`${repoPath}/pulls/${result.pullRequest}/files?per_page=10`),
      github(`${repoPath}/contents/${path}?ref=${initialHead}`).then(
        decodeFile,
      ),
      github(`${repoPath}/contents/${path}?ref=${result.mergeSha}`).then(
        decodeFile,
      ),
      readWorkflow(result.mergeSha),
    ]);
    assert.ok(
      pr.merged &&
        pr.number === result.pullRequest &&
        pr.title === title &&
        pr.head.ref === result.branch &&
        pr.head.sha === repairedHead &&
        pr.merge_commit_sha === result.mergeSha &&
        pr.merged_by.id === actor.id &&
        pr.body.includes(`Refs #${issue.number}`),
    );
    assert.equal(
      diff.merge_base_commit.sha,
      initialHead,
      'Repair must advance the existing PR head.',
    );
    assert.deepEqual(
      files.map((file) => file.filename),
      [path],
    );
    assert.equal(initial, initialContent);
    assert.equal(published, initialContent + marker + '\n');
    assert.equal(trustedWorkflow, expectedWorkflow);
    const duplicate = await webhook.receive('issues', issue, ingressReceipt);
    assert.equal(duplicate.eventId, ingressReceipt.eventId);
    assert.equal((await workItems(roomId)).items.length, 1);
    assert.equal((await roomTasks(roomId)).tasks.length, 2);
    Object.assign(report, {
      result: {
        pr: pr.html_url,
        failed,
        passed,
        merge: result.mergeSha,
        taskIds,
        requests,
      },
      redeliveryDeduplicated: true,
    });
  } catch (error) {
    failure = error;
    // Assertion messages may include a complete response. Keep failure output
    // bounded and limited to authored messages and the current stage.
    report.failure = error.generatedMessage ? error.code : error.message;
  }

  const policyReceipt = await disablePolicy?.().catch(() => null);
  const hookReceipt = await webhook?.close().catch(() => null);
  report.cleanup = { policy: policyReceipt, webhook: hookReceipt };
  if (failure && report.policyId) {
    const current = await roomTasks(report.roomId).catch(() => null);
    if (current)
      report.cleanup.tasks = await cancelTestTasks({
        api,
        ledger,
        tasks: current.tasks,
        policyId: report.policyId,
      });
  }
  const cleaned =
    policyReceipt?.policyDisabled &&
    policyReceipt.durable &&
    hookReceipt?.disabled &&
    hookReceipt.durable;
  report.status = !failure && cleaned ? 'verified' : 'stopped';
  await save(report.status === 'verified' ? 'verified' : report.stage);
  assert.equal(
    report.status,
    'verified',
    `CI repair verification stopped at ${report.stage}. Inspect the private report and ledger before further work.`,
  );
  console.log(
    JSON.stringify(
      {
        status: report.status,
        issue: report.issue.url,
        ...report.result,
        attentionReads: report.attentionReads,
        report: reportPath,
      },
      null,
      2,
    ),
  );
}

main(process.argv.slice(2)).catch(({ message }) => {
  console.error(message);
  process.exitCode = 1;
});
