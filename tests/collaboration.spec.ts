import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type {
  Task,
  TaskRecoveryRun,
} from '../apps/dashboard/src/features/collaboration/api';
import type { Agent, Run } from '../apps/dashboard/src/features/workspace/api';
import { fixtureNotifications } from './fixture-notifications';
import {
  openRoomMore,
  createRoom,
  library,
  mutate,
  openApp,
  signIn,
  work,
  workspace,
} from './team-helpers';

const detail = (page: Page) =>
  workspace(page).getByRole('region', {
    name: 'Collaboration details',
    exact: true,
  });

async function fixture(page: Page, name: string) {
  await signIn(page);
  const room = await createRoom(page, name);
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const now = new Date().toISOString();
  const agents: Agent[] = ['Writer', 'Reviewer'].map((name, index) => ({
    id: `collaboration-agent-${randomUUID()}`,
    name,
    handle: name.toLowerCase(),
    ownerUserId: user.id,
    preferredRunnerId: `runner-${index}`,
    icon: 'document',
    avatar: 'agent-robot-teal-v1',
    instructions: '',
    access: 'write',
    enabled: true,
    revision: 1,
    createdAt: now,
    updatedAt: now,
  }));
  let task: Task = {
    id: `collaboration-${randomUUID()}`,
    roomId: room.id,
    resourceId: room.resourceId,
    threadRootId: '',
    originMessageId: '',
    requestedByUserId: user.id,
    goal: 'Prepare a clear onboarding guide together.',
    status: 'queued',
    revision: 1,
    participants: agents.map((agent, index) => ({
      agent,
      runnerId: `runner-${index}`,
      runnerName: `Personal Runner ${index + 1}`,
    })),
    currentAgentId: agents[0].id,
    activeRunId: '',
    baseSharedCommit: 'base',
    checkpointCommit: '',
    publishedCommit: '',
    error: '',
    summary: '',
    turnsUsed: 0,
    maxTurns: 6,
    modelRequestsUsed: 0,
    maxModelRequests: 24,
    inputs: [],
    inputRevision: 0,
    inputBytes: 0,
    coveredInputRevision: 0,
    createdAt: now,
    updatedAt: now,
  };
  let exists = false;
  let runs: Run[] = [];
  let recoveryRun: TaskRecoveryRun | undefined;
  await page.route('**/api/agents', (route) =>
    route.fulfill({ json: { agents } }),
  );
  await page.route(`**/api/rooms/${room.id}/members`, (route) =>
    route.fulfill({ json: { members: agents } }),
  );
  await page.route('**/api/runners', (route) =>
    route.fulfill({
      json: {
        runners: agents.map((agent, index) => ({
          id: agent.preferredRunnerId,
          name: `Personal Runner ${index + 1}`,
          ownerUserId: user.id,
          online: index === 0,
          enabled: true,
          revoked: false,
          capabilities: { ready: true, executor: 'native' },
        })),
      },
    }),
  );
  await page.route('**/api/workspace/status', (route) =>
    route.fulfill({
      json: {
        available: true,
        reason: '',
        budget: { maxTokens: 4096, maxSteps: 8, timeoutSeconds: 180 },
        maxConcurrent: 2,
      },
    }),
  );
  await page.route(`**/api/rooms/${room.id}/tasks?*`, (route) =>
    route.fulfill({ json: { tasks: exists ? [task] : [] } }),
  );
  await page.route(`**/api/tasks/${task.id}?*`, (route) =>
    route.fulfill({
      json: { task: { ...task, inputs: undefined }, recoveryRun },
    }),
  );
  await page.route(`**/api/tasks/${task.id}/inputs?*`, (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const query = new URL(route.request().url()).searchParams;
    const after = Number(query.get('after') || 0);
    const through = Number(query.get('through') ?? task.inputRevision);
    const items = (task.inputs ?? [])
      .filter((input) => input.seq > after && input.seq <= through)
      .slice(0, 20);
    const last = items.at(-1)?.seq ?? after;
    return route.fulfill({
      json: {
        items,
        through,
        hasMore: last < through,
        nextAfter: last < through ? last : null,
      },
    });
  });
  await page.route(`**/api/tasks/${task.id}/runs?*`, (route) =>
    route.fulfill({
      json: {
        items: runs.map((run) => ({
          id: run.id,
          agentId: run.agentId,
          agentName: run.agentSnapshot.name,
          runnerName: run.runnerName,
          checksDeferred: Boolean(run.checksDeferred),
          status: run.status,
          createdAt: run.createdAt,
          updatedAt: run.updatedAt,
        })),
        nextCursor: '',
      },
    }),
  );
  const notify = await fixtureNotifications(page);
  await page.reload();
  await expect(workspace(page)).toBeVisible();
  return {
    room,
    agents,
    notify,
    get task() {
      return task;
    },
    setTask(value: Partial<Task>) {
      task = { ...task, ...value };
      if (value.inputs) {
        task.inputRevision = value.inputs.length;
        task.inputBytes = value.inputs.reduce(
          (bytes, input) =>
            bytes + new TextEncoder().encode(input.content).length,
          0,
        );
      }
      exists = true;
    },
    setRuns(value: Run[]) {
      runs = value;
    },
    setRecoveryRun(value: TaskRecoveryRun | undefined) {
      recoveryRun = value;
    },
    async refresh() {
      await notify('task.updated', room.id, task.id);
    },
    async open() {
      await notify('task.updated', room.id, task.id);
      await openRoomMore(page, 'Execution history…');
      await workspace(page).getByRole('button', { name: task.goal }).click();
      await expect(detail(page)).toBeVisible();
    },
  };
}

test('collaboration is explicit, previews finite grants and keeps inputs separate from ordinary messages', async ({
  page,
}, info) => {
  const value = await fixture(page, `collaboration-start-${info.project.name}`);
  const created: Record<string, unknown>[] = [];
  await page.route(`**/api/rooms/${value.room.id}/tasks?*`, async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({
        json: { tasks: created.length ? [value.task] : [] },
      });
    created.push(route.request().postDataJSON());
    if (created.length === 1) return route.abort();
    return route.fulfill({ json: { task: value.task } });
  });
  await page.route(`**/api/rooms/${value.room.id}/messages`, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    return route.fulfill({
      status: 503,
      json: { error: 'Ordinary request remained a one-shot request.' },
    });
  });
  const composer = workspace(page).getByRole('form', { name: 'Room composer' });
  await composer.getByRole('textbox').fill('@writer Review this once.');
  await composer
    .getByRole('button', { name: 'Send to Agent', exact: true })
    .click();
  await expect(composer).toContainText('Ordinary request remained');
  expect(created).toEqual([]);
  await composer.getByRole('textbox').fill('');
  await openRoomMore(page, 'Execution history…');
  await workspace(page)
    .getByRole('button', { name: 'Start collaboration…', exact: true })
    .click();
  const form = workspace(page).getByRole('region', {
    name: 'Start collaboration',
    exact: true,
  });
  await form
    .getByRole('textbox', { name: 'Shared goal', exact: true })
    .fill(value.task.goal);
  for (const agent of value.agents) {
    const choice = form.getByRole('checkbox', {
      name: agent.name,
      exact: true,
    });
    await choice.check();
    expect((await choice.boundingBox())!.width).toBeLessThan(24);
  }
  await expect(form).toContainText('Up to 6 Agent turns and 24 model requests');
  await expect(form).toContainText('Offline, work will queue');
  if (['chromium', 'touch'].includes(info.project.name))
    await page.screenshot({ path: info.outputPath('collaboration-start.png') });
  await form
    .getByLabel('First Agent', { exact: true })
    .selectOption(value.agents[0].id);
  await form
    .getByRole('button', { name: 'Start collaboration', exact: true })
    .click();
  await expect(
    form.getByRole('textbox', { name: 'Shared goal', exact: true }),
  ).toBeDisabled();
  await expect(form).toContainText('without starting another');
  await page.reload();
  await openRoomMore(page, 'Execution history…');
  await workspace(page)
    .getByRole('button', { name: 'Start collaboration…', exact: true })
    .click();
  await expect(
    form.getByRole('textbox', { name: 'Shared goal', exact: true }),
  ).toHaveValue(value.task.goal);
  await expect(
    form.getByRole('textbox', { name: 'Shared goal', exact: true }),
  ).toBeDisabled();
  await form.getByRole('button', { name: 'Retry Start', exact: true }).click();
  await expect(detail(page)).toContainText('Waiting for Runner');
  await expect(
    detail(page).getByRole('button', { name: 'Show full goal', exact: true }),
  ).toHaveCount(0);
  expect(created).toHaveLength(2);
  expect(created[1]).toEqual(created[0]);
  expect(created[0]).toMatchObject({
    inputProtocol: 1,
    goal: value.task.goal,
    participantAgentIds: value.agents.map((agent) => agent.id),
    firstAgentId: value.agents[0].id,
  });
  const inputs: Record<string, unknown>[] = [];
  await page.route(`**/api/tasks/${value.task.id}/inputs?*`, (route) => {
    if (route.request().method() === 'GET') return route.fallback();
    inputs.push(route.request().postDataJSON());
    return route.fulfill({ json: { task: value.task } });
  });
  await detail(page)
    .getByLabel('Add input for the next turn', { exact: true })
    .fill('Keep the guide concise.');
  await detail(page)
    .getByRole('tab', { name: 'Activity', exact: true })
    .click();
  await detail(page)
    .getByRole('tab', { name: 'Task setup', exact: true })
    .click();
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Add input for the next turn', { exact: true }),
  ).toHaveValue('Keep the guide concise.');
  await detail(page)
    .getByRole('button', { name: 'Add Input', exact: true })
    .click();
  await expect(detail(page)).toContainText('Input saved for the next turn.');
  expect(inputs).toHaveLength(1);
  expect(created).toHaveLength(2);
  await expect(
    detail(page).getByRole('button', {
      name: 'Open Published Files',
      exact: true,
    }),
  ).toHaveCount(0);
  expect(
    await detail(page).evaluate(
      (node) => node.scrollWidth <= node.clientWidth + 1,
    ),
  ).toBe(true);
});

test('a question retry retains its exact request after live updates advance the collaboration', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-answer-${info.project.name}`,
  );
  value.setTask({
    status: 'waiting_for_person',
    checkpointCommit: 'checkpoint',
    turnsUsed: 1,
    modelRequestsUsed: 2,
    question: {
      id: 'question-one',
      text: 'Who should use the guide?',
      options: ['New teammates', 'Team leads'],
      answer: '',
      answeredByUserId: '',
    },
  });
  await value.refresh();
  await value.open();
  await expect(detail(page)).toContainText('Room files have not been changed');
  await expect(
    detail(page).getByRole('button', { name: 'Open Task Draft', exact: true }),
  ).toBeVisible();
  if (['chromium', 'touch'].includes(info.project.name))
    await page.screenshot({
      path: info.outputPath('collaboration-question.png'),
    });
  await detail(page)
    .getByRole('tab', { name: 'Activity', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toBeVisible();
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  const answers: Record<string, unknown>[] = [];
  let receipt: Task | undefined;
  await page.route(`**/api/tasks/${value.task.id}/answers?*`, async (route) => {
    answers.push(route.request().postDataJSON());
    if (answers.length === 1) {
      value.setTask({
        status: 'queued',
        revision: 2,
        question: { ...value.task.question!, answer: 'New teammates' },
      });
      receipt = value.task;
      return route.abort();
    }
    return route.fulfill({ json: { task: receipt } });
  });
  await detail(page)
    .getByRole('button', { name: 'New teammates', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Answer and Continue', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Retry Save', exact: true }),
  ).toBeVisible();
  await value.refresh();
  await expect(detail(page)).toContainText('Waiting for Runner');
  await expect(
    detail(page).getByRole('textbox', {
      name: 'Add input for the next turn',
      exact: true,
    }),
  ).toBeDisabled();
  await expect(
    detail(page).getByRole('button', { name: 'Add Input', exact: true }),
  ).toBeDisabled();
  await expect(
    detail(page).getByRole('button', { name: 'Stop…', exact: true }),
  ).toBeDisabled();
  expect(answers).toHaveLength(1);
  value.setTask({
    status: 'completed',
    revision: 3,
    publishedCommit: 'published',
    summary: 'The guide is ready for review.',
  });
  await value.refresh();
  await expect(detail(page)).toContainText('Work completed');
  await page.reload();
  await value.open();
  await expect(detail(page)).toContainText(
    'A previous action is awaiting confirmation.',
  );
  await detail(page)
    .getByRole('button', { name: 'Retry Save', exact: true })
    .click();
  await expect(detail(page)).toContainText('Answer saved.');
  expect(answers).toHaveLength(2);
  expect(answers[1]).toEqual(answers[0]);
  expect(answers[1]).toMatchObject({
    inputProtocol: 1,
    revision: 1,
    questionId: 'question-one',
    content: 'New teammates',
  });
  await expect(detail(page)).toContainText('Work completed');
  await expect(
    detail(page).getByRole('button', {
      name: 'Open Published Files',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Stop…', exact: true }),
  ).toHaveCount(0);
});

test('continuation authorizes additional work and changed Runners explicitly while preserving task drafts', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-continue-${info.project.name}`,
  );
  value.setTask({
    status: 'needs_attention',
    checkpointCommit: 'retained-draft',
    turnsUsed: 6,
    modelRequestsUsed: 24,
    error: 'Room files changed while the task was working.',
  });
  const replacement: Agent = {
    ...value.agents[0],
    id: 'replacement-agent',
    name: 'Replacement Writer',
    handle: 'replacement',
  };
  await page.route(`**/api/rooms/${value.room.id}/members`, (route) =>
    route.fulfill({ json: { members: [value.agents[1], replacement] } }),
  );
  await value.notify('membership.updated', value.room.id, replacement.id);
  await value.refresh();
  await value.open();

  const continuations: Record<string, unknown>[] = [];
  await expect(detail(page).getByRole('status')).toContainText(
    'Model request allowance exhausted (24/24). Add model requests in Continue to resume.',
  );
  await expect(detail(page).getByRole('alert')).toHaveText(
    'Room files changed while the task was working.',
  );
  await page.route(`**/api/tasks/${value.task.id}/continue?*`, (route) => {
    continuations.push(route.request().postDataJSON());
    value.setTask({
      status: 'queued',
      revision: 2,
      maxTurns: 8,
      maxModelRequests: 28,
      error: '',
    });
    return route.fulfill({ json: { task: value.task } });
  });
  await detail(page)
    .getByRole('button', { name: 'Continue…', exact: true })
    .click();
  await detail(page)
    .getByRole('tab', { name: 'Activity', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Next Agent', { exact: true }),
  ).toBeVisible();
  await detail(page).getByLabel('Additional turns', { exact: true }).fill('2');
  await detail(page)
    .getByLabel('Additional model requests', { exact: true })
    .fill('4');
  await detail(page)
    .getByLabel('Starting files', { exact: true })
    .selectOption('latest');
  await detail(page)
    .getByRole('checkbox', {
      name: 'Change participants or Runners',
      exact: true,
    })
    .check();
  await expect(detail(page)).toContainText('No longer available in this Room');
  await detail(page)
    .getByRole('checkbox', { name: value.agents[0].name, exact: true })
    .uncheck();
  await detail(page)
    .getByRole('checkbox', { name: replacement.name, exact: true })
    .check();
  await detail(page)
    .getByLabel('Next Agent', { exact: true })
    .selectOption(replacement.id);
  await expect(detail(page)).toContainText('will not overwrite or merge');

  await detail(page)
    .getByLabel('What should happen next?', { exact: true })
    .fill('Apply the requested update using the latest files.');
  await detail(page)
    .getByRole('button', { name: 'Continue collaboration', exact: true })
    .click();
  await expect(detail(page)).toContainText('Continuation requested.');
  await expect(detail(page)).not.toContainText('allowance exhausted');
  expect(continuations).toHaveLength(1);
  expect(continuations[0]).toMatchObject({
    revision: 1,
    addTurns: 2,
    addModelRequests: 4,
    fromLatest: true,
    participantAgentIds: [value.agents[1].id, replacement.id],
    agentId: replacement.id,
  });
  await page.route(`**/api/tasks/${value.task.id}/cancel?*`, (route) => {
    value.setTask({ status: 'cancelled', revision: 3 });
    return route.fulfill({ json: { task: value.task } });
  });
  await detail(page)
    .getByRole('button', { name: 'Stop…', exact: true })
    .click();
  const confirmation = page.getByRole('dialog', {
    name: 'Stop collaboration',
    exact: true,
  });
  await expect(confirmation).toContainText(
    'Saved task drafts remain available',
  );
  await confirmation.getByRole('button', { name: 'Stop', exact: true }).click();
  await expect(detail(page)).toContainText('Cancelled');
  await expect(
    detail(page).getByRole('button', { name: 'Open Task Draft', exact: true }),
  ).toBeVisible();
});

test('team members can answer while only the requester or admin can continue or stop', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-permissions-${info.project.name}`,
  );
  value.setTask({
    status: 'waiting_for_person',
    requestedByUserId: 'another-member',
    question: {
      id: 'question-owner',
      text: 'Which audience?',
      options: [],
      answer: '',
      answeredByUserId: '',
    },
  });
  await page.route('**/api/auth/status', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    return route.fulfill({
      response,
      json: { ...body, user: { ...body.user, role: 'member' } },
    });
  });
  await page.reload();
  await value.open();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', {
      name: 'Answer and Continue',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Continue…', exact: true }),
  ).toHaveCount(0);
  await expect(
    detail(page).getByRole('button', { name: 'Stop…', exact: true }),
  ).toHaveCount(0);
});

test('completed collaboration puts its fixed result before a long summary and keeps setup and activity separate', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-result-${info.project.name}`,
  );
  const listing = await (
    await page.request.get(`/api/resources/${value.room.resourceId}/files`)
  ).json();
  const first = await mutate(
    page,
    `/resources/${value.room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: listing.commit,
      path: 'guide.md',
      content: '# Collaboration version\n\nOriginal agreed instructions.',
    },
    'PUT',
  );
  expect(first.ok()).toBeTruthy();
  const { publication: published } = await first.json();
  const later = await mutate(
    page,
    `/resources/${value.room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: published.commit,
      path: 'guide.md',
      content: '# Later Room version\n\nSubsequent unrelated changes.',
    },
    'PUT',
  );
  expect(later.ok()).toBeTruthy();
  const now = new Date().toISOString();
  value.setTask({
    status: 'completed',
    checkpointCommit: published.commit,
    publishedCommit: published.commit,
    goal:
      'Prepare the onboarding guide with clear responsibilities.\n\n' +
      'Preserve the original sources and include the agreed review requirements. '.repeat(
        15,
      ),
    summary:
      '## What changed\n\nThe **onboarding guide** includes the agreed instructions.\n\n' +
      Array.from(
        { length: 12 },
        (_, index) =>
          `### Section ${index + 1}\n\nThe team can read this section and verify its documented instructions.`,
      ).join('\n\n'),
    turnsUsed: 3,
    modelRequestsUsed: 9,
    inputs: [
      {
        id: 'review-input',
        seq: 1,
        authorUserId: 'teammate',
        authorName: 'Teammate',
        content: 'Keep the published guide independent of later Room changes.',
        createdAt: now,
      },
    ],
  });
  value.setRuns([
    {
      id: 'completed-collaboration-turn',
      taskId: value.task.id,
      roomId: value.room.id,
      threadRootId: '',
      originMessageId: '',
      agentId: value.agents[0].id,
      resourceId: value.room.resourceId,
      runnerId: 'runner-0',
      runnerName: 'Personal Runner 1',
      prompt: value.task.goal,
      references: [],
      status: 'succeeded',
      createdAt: now,
      updatedAt: now,
      finishedAt: now,
      error: '',
      output: '',
      sessionId: '',
      sessionMode: 'new',
      baselineCommit: listing.commit,
      resultCommit: published.commit,
      agentSnapshot: value.agents[0],
      environment: null,
      checks: [],
      files: [],
      exitCode: 0,
      testExitCode: null,
      maxTokens: 4096,
      maxSteps: 8,
      timeoutSeconds: 180,
      sessionGeneration: 1,
      published: true,
    },
  ]);
  await value.refresh();
  await value.open();
  const panel = detail(page);
  const result = panel.getByRole('button', {
    name: 'Open Published Files',
    exact: true,
  });
  const summary = panel.getByRole('region', {
    name: 'Collaboration summary',
    exact: true,
  });
  await expect(result).toBeInViewport();
  await expect(
    summary.getByRole('heading', { name: 'What changed', exact: true }),
  ).toBeVisible();
  await expect(summary.locator('strong')).toContainText('onboarding guide');
  await expect(
    panel.getByRole('region', {
      name: 'Collaboration participants',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    panel.getByRole('region', { name: 'Collaboration turns', exact: true }),
  ).toHaveCount(0);
  const resultBounds = await result.boundingBox();
  const summaryBounds = await summary.boundingBox();
  expect(resultBounds!.y + resultBounds!.height).toBeLessThan(summaryBounds!.y);
  expect(
    await panel.evaluate((node) => node.scrollWidth > node.clientWidth),
  ).toBe(false);
  if (['chromium', 'touch'].includes(info.project.name))
    await workspace(page).screenshot({
      path: info.outputPath('collaboration-result.png'),
    });
  const goal = panel.getByRole('heading', {
    name: value.task.goal,
    exact: true,
  });
  const collapsedHeight = (await goal.boundingBox())!.height;
  await panel
    .getByRole('button', { name: 'Show full goal', exact: true })
    .click();
  await expect(
    panel.getByRole('button', { name: 'Show less', exact: true }),
  ).toHaveAttribute('aria-expanded', 'true');
  await expect
    .poll(async () => (await goal.boundingBox())!.height)
    .toBeGreaterThan(collapsedHeight);
  await panel.getByRole('tab', { name: 'Task setup', exact: true }).click();
  await expect(
    panel.getByRole('heading', { name: value.task.goal, exact: true }),
  ).toBeVisible();
  await panel.getByRole('button', { name: 'Show less', exact: true }).click();
  await expect(
    panel.getByRole('button', { name: 'Show full goal', exact: true }),
  ).toHaveAttribute('aria-expanded', 'false');
  await expect
    .poll(async () => (await goal.boundingBox())!.height)
    .toBeCloseTo(collapsedHeight, 0);
  await expect(
    panel.getByRole('region', {
      name: 'Collaboration participants',
      exact: true,
    }),
  ).toContainText('Personal Runner 2');
  await expect(
    panel.getByRole('region', { name: 'Collaboration allowance', exact: true }),
  ).toContainText('9 / 24');
  await panel.getByRole('tab', { name: 'Activity', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(
    panel
      .getByRole('region', { name: 'Collaboration turns', exact: true })
      .getByRole('button', { name: 'Writer · Completed', exact: true }),
  ).toBeVisible();
  await expect(
    panel
      .getByRole('region', { name: 'Collaboration turns', exact: true })
      .getByRole('button', { name: 'Writer · Completed', exact: true }),
  ).toHaveAccessibleDescription('Personal Runner 1');
  await expect(
    panel.getByRole('region', { name: 'Team input', exact: true }),
  ).toContainText('independent of later Room changes');
  if (['chromium', 'touch'].includes(info.project.name))
    await workspace(page).screenshot({
      path: info.outputPath('collaboration-activity.png'),
    });
  await result.click();
  const selected = library(page).getByRole('region', {
    name: 'Selected file',
    exact: true,
  });
  await expect(selected).toContainText('Original agreed instructions.');
  await expect(selected).not.toContainText('Subsequent unrelated changes.');
  await expect(library(page)).toContainText('Historical version · read only');
  await expect(
    selected.getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeDisabled();
});

test('history pages keep their frozen position and drafts through refresh and failed reads', async ({
  page,
}, info) => {
  const value = await fixture(page, `collaboration-pages-${info.project.name}`);
  const inputs = Array.from({ length: 45 }, (_, index) => ({
    id: `input-${index + 1}`,
    seq: index + 1,
    authorUserId: 'teammate',
    authorName: 'Teammate',
    content: `Original constraint ${index + 1}.`,
    createdAt: new Date().toISOString(),
  }));
  value.setTask({ inputs });
  await value.refresh();
  await value.open();
  const panel = detail(page);
  await panel
    .getByLabel('Add input for the next turn', { exact: true })
    .fill('An unfinished new constraint.');
  await panel.getByRole('tab', { name: 'Activity', exact: true }).click();
  const history = panel.getByRole('region', {
    name: 'Team input',
    exact: true,
  });
  await expect(history).toContainText('Original constraint 1.');
  await expect(history).not.toContainText('Original constraint 21.');
  let fail = true;
  const reads: string[] = [];
  await page.route(`**/api/tasks/${value.task.id}/inputs?*`, (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const query = new URL(route.request().url()).searchParams;
    if (query.get('after') !== '20') return route.fallback();
    reads.push(query.toString());
    if (!fail) return route.fallback();
    fail = false;
    return route.fulfill({
      status: 503,
      json: { error: 'History is temporarily unavailable.' },
    });
  });
  await history
    .getByRole('button', { name: 'Next Inputs', exact: true })
    .click();
  await expect(history).toContainText('History is temporarily unavailable.');
  await history
    .getByRole('button', { name: 'Retry Inputs', exact: true })
    .click();
  await expect(history).toContainText('Original constraint 21.');
  expect(reads[1]).toEqual(reads[0]);
  expect(reads[1]).toContain('through=45');
  value.setTask({
    revision: 2,
    inputs: [
      ...inputs,
      { ...inputs[0], id: 'input-46', seq: 46, content: 'New constraint 46.' },
    ],
  });
  await value.refresh();
  await expect(
    history.getByRole('button', { name: 'View New Inputs', exact: true }),
  ).toBeVisible();
  await expect(history).toContainText('Original constraint 21.');
  await expect(history).not.toContainText('New constraint 46.');
  await panel.getByRole('tab', { name: 'Overview', exact: true }).click();
  await expect(
    panel.getByLabel('Add input for the next turn', { exact: true }),
  ).toHaveValue('An unfinished new constraint.');
  await panel.getByRole('tab', { name: 'Activity', exact: true }).click();
  await expect(history).toContainText('Original constraint 21.');
  await history
    .getByRole('button', { name: 'Next Inputs', exact: true })
    .click();
  await expect(history).toContainText('Original constraint 45.');
  await expect(history).not.toContainText('New constraint 46.');
  await history
    .getByRole('button', { name: 'View New Inputs', exact: true })
    .click();
  await expect(history).toContainText('New constraint 46.');
  await history
    .getByRole('button', { name: 'Previous Inputs', exact: true })
    .click();
  await expect(history).toContainText('Original constraint 45.');
  await expect(history).not.toContainText('New constraint 46.');
  if (['chromium', 'touch'].includes(info.project.name))
    await workspace(page).screenshot({
      path: info.outputPath('collaboration-pages.png'),
    });
  expect(
    await panel.evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
  ).toBe(true);

  await panel
    .getByRole('button', { name: 'Back to Collaborations', exact: true })
    .click();
  value.setTask({
    revision: 3,
    inputs: [
      ...value.task.inputs!,
      { ...inputs[0], id: 'input-47', seq: 47, content: 'New constraint 47.' },
    ],
  });
  let releaseHead = () => {};
  const headGate = new Promise<void>((resolve) => {
    releaseHead = resolve;
  });
  let headReads = 0;
  const pinnedRanges: string[] = [];
  await page.route(`**/api/tasks/${value.task.id}/inputs?*`, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    const query = new URL(route.request().url()).searchParams;
    const through = query.get('through');
    if (through) pinnedRanges.push(through);
    else {
      headReads++;
      await headGate;
    }
    return route.fallback();
  });
  try {
    await value.open();
    expect(headReads).toBe(0);
    await panel.getByRole('tab', { name: 'Activity', exact: true }).click();
    await expect.poll(() => headReads).toBeGreaterThan(0);
    const note = panel.getByLabel('Add input for the next turn', {
      exact: true,
    });
    const reopenedDraft = 'Keep my note while the latest input range loads.';
    await note.fill(reopenedDraft);
    expect(pinnedRanges).toEqual([]);
    releaseHead();
    await expect.poll(() => pinnedRanges).toContain('47');
    await expect(note).toHaveValue(reopenedDraft);
    await history
      .getByRole('button', { name: 'Next Inputs', exact: true })
      .click();
    await expect(history).toContainText('Original constraint 21.');
    await history
      .getByRole('button', { name: 'Next Inputs', exact: true })
      .click();
    await expect(history).toContainText('New constraint 47.');
  } finally {
    releaseHead();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('legacy pending task actions retry their original body after the input protocol upgrade', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-legacy-action-${info.project.name}`,
  );
  value.setTask({
    status: 'waiting_for_person',
    question: {
      id: 'legacy-question',
      text: 'Who is this for?',
      options: [],
      answer: '',
      answeredByUserId: '',
    },
  });
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const saved = {
    key: 'legacy-key',
    kind: 'answer',
    requestId: 'legacy-answer',
    revision: 1,
    content: 'Original saved answer',
    agentId: value.agents[0].id,
    questionId: 'legacy-question',
    addTurns: 0,
    addModelRequests: 0,
    fromLatest: false,
  };
  await page.evaluate(
    ({ key, value }) =>
      sessionStorage.setItem(key, JSON.stringify({ version: 1, value })),
    {
      key: `camp:receipt:${user.id}:task-action:${value.task.id}`,
      value: saved,
    },
  );
  const requests: unknown[] = [];
  await page.route(`**/api/tasks/${value.task.id}/answers?*`, async (route) => {
    requests.push(route.request().postDataJSON());
    return route.fulfill({ json: { task: value.task } });
  });
  await value.open();
  await expect(detail(page)).toContainText(
    'A previous action is awaiting confirmation.',
  );
  await detail(page)
    .getByRole('button', { name: 'Retry Save', exact: true })
    .click();
  await expect(detail(page)).toContainText('Answer saved.');
  expect(requests).toEqual([
    {
      requestId: saved.requestId,
      revision: saved.revision,
      questionId: saved.questionId,
      content: saved.content,
    },
  ]);
});

test('legacy pending collaboration starts retain the original protocol and identity', async ({
  page,
}, info) => {
  const value = await fixture(
    page,
    `collaboration-legacy-start-${info.project.name}`,
  );
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const saved = {
    requestId: 'legacy-start',
    goal: 'Original pending goal',
    participantAgentIds: value.agents.map((agent) => agent.id),
    firstAgentId: value.agents[0].id,
    threadRootId: '',
  };
  await page.evaluate(
    ({ key, value }) =>
      sessionStorage.setItem(key, JSON.stringify({ version: 1, value })),
    {
      key: `camp:receipt:${user.id}:collaboration-start:${value.room.id}`,
      value: saved,
    },
  );
  const requests: unknown[] = [];
  await page.route(`**/api/rooms/${value.room.id}/tasks?*`, async (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: { tasks: [] } })
      : (requests.push(route.request().postDataJSON()),
        route.fulfill({ json: { task: value.task } })),
  );
  await openRoomMore(page, 'Execution history…');
  await workspace(page)
    .getByRole('button', { name: 'Start collaboration…', exact: true })
    .click();
  const form = workspace(page).getByRole('region', {
    name: 'Start collaboration',
    exact: true,
  });
  await expect(
    form.getByRole('textbox', { name: 'Shared goal', exact: true }),
  ).toHaveValue(saved.goal);
  await form.getByRole('button', { name: 'Retry Start', exact: true }).click();
  await expect(detail(page)).toBeVisible();
  expect(requests).toEqual([saved]);
});

test('model refresh is explicit and an uncertain continuation preserves its exact authorization', async ({
  page,
}, info) => {
  const value = await fixture(page, `model-refresh-${info.project.name}`);
  value.setTask({
    status: 'needs_attention',
    turnsUsed: 1,
    modelRequestsUsed: 2,
    error: 'The model response reached its output limit.',
  });
  await value.refresh();
  await value.open();
  await detail(page)
    .getByRole('button', { name: 'Continue…', exact: true })
    .click();
  const refresh = detail(page).getByRole('checkbox', {
    name: 'Use current model settings',
    exact: true,
  });
  await expect(refresh).not.toBeChecked();
  await refresh.check();
  await expect(detail(page)).toContainText(
    'Earlier executions keep their settings.',
  );
  await detail(page)
    .getByLabel('What should happen next?', { exact: true })
    .fill(
      'Continue with the current model settings and the existing allowance.',
    );
  const requests: Record<string, unknown>[] = [];
  let receipt: Task | undefined;
  await page.route(
    `**/api/tasks/${value.task.id}/continue?*`,
    async (route) => {
      requests.push(route.request().postDataJSON());
      if (requests.length === 1) {
        value.setTask({ status: 'queued', revision: 2, error: '' });
        receipt = value.task;
        return route.abort();
      }
      return route.fulfill({ json: { task: receipt } });
    },
  );
  await detail(page)
    .getByRole('button', { name: 'Continue collaboration', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Retry Save', exact: true }),
  ).toBeVisible();
  await expect(refresh).toBeChecked();
  await expect(refresh).toBeDisabled();
  await page.reload();
  await value.open();
  await expect(refresh).toBeChecked();
  await expect(refresh).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Retry Save', exact: true })
    .click();
  await expect(detail(page)).toContainText('Continuation requested.');
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(requests[0]).toMatchObject({
    revision: 1,
    refreshModelSettings: true,
    addTurns: 0,
    addModelRequests: 0,
  });
});

test('captured partial files are an explicit starting choice preserved across uncertain recovery', async ({
  page,
}, info) => {
  const value = await fixture(page, `captured-recovery-${info.project.name}`);
  const runId = `captured-${randomUUID()}`;
  value.setTask({
    status: 'needs_attention',
    turnsUsed: 2,
    modelRequestsUsed: 18,
    checkpointCommit: 'original-task-draft',
    error: 'The last execution reached its step limit.',
  });
  value.setRecoveryRun({
    runId,
    commit: 'b'.repeat(40),
    agentName: 'Writer',
    fileCount: 6,
  });
  await value.refresh();
  await value.open();
  await detail(page)
    .getByRole('button', { name: 'Continue…', exact: true })
    .click();
  const starting = detail(page).getByLabel('Starting files', { exact: true });
  await expect(starting).toHaveValue('draft');
  await starting.selectOption(`run:${runId}`);
  await expect(detail(page)).toContainText(
    'Checks have not passed; Room files stay unchanged.',
  );
  await expect(
    detail(page).getByRole('button', {
      name: 'Review Saved Files',
      exact: true,
    }),
  ).toBeVisible();
  await starting.selectOption('latest');
  await expect(detail(page)).toContainText(
    'Start a new draft from current Room files.',
  );
  await expect(
    detail(page).getByRole('button', {
      name: 'Review Saved Files',
      exact: true,
    }),
  ).toHaveCount(0);
  await starting.selectOption(`run:${runId}`);
  await detail(page)
    .getByLabel('What should happen next?', { exact: true })
    .fill('Continue the captured implementation and run its checks.');
  const requests: Record<string, unknown>[] = [];
  let receipt: Task | undefined;
  await page.route(
    `**/api/tasks/${value.task.id}/continue?*`,
    async (route) => {
      requests.push(route.request().postDataJSON());
      if (requests.length === 1) {
        value.setTask({
          status: 'queued',
          revision: 2,
          error: '',
          checkpointCommit: 'captured-files',
        });
        value.setRecoveryRun(undefined);
        receipt = value.task;
        return route.abort();
      }
      return route.fulfill({ json: { task: receipt } });
    },
  );
  await detail(page)
    .getByRole('button', { name: 'Continue collaboration', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Retry Save', exact: true }),
  ).toBeVisible();
  await expect(starting).toBeDisabled();
  await page.reload();
  await value.open();
  await expect(starting).toHaveValue(`run:${runId}`);
  await expect(starting).toBeDisabled();
  await expect(starting.locator('option:checked')).toHaveText(
    'Saved work from selected attempt',
  );
  await detail(page)
    .getByRole('button', { name: 'Retry Save', exact: true })
    .click();
  await expect(detail(page)).toContainText('Continuation requested.');
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(requests[0]).toMatchObject({
    fromRunId: runId,
    fromLatest: false,
    addTurns: 0,
    addModelRequests: 0,
  });
});

test('a stale saved-file choice remains visible and requires a new starting choice', async ({
  page,
}, info) => {
  const value = await fixture(page, `stale-capture-${info.project.name}`);
  const runId = `old-capture-${randomUUID()}`;
  value.setTask({ status: 'needs_attention', turnsUsed: 1 });
  value.setRecoveryRun({
    runId,
    commit: 'b'.repeat(40),
    agentName: 'Writer',
    fileCount: 3,
  });
  await value.refresh();
  await value.open();
  await detail(page)
    .getByRole('button', { name: 'Continue…', exact: true })
    .click();
  const starting = detail(page).getByLabel('Starting files', { exact: true });
  await starting.selectOption(`run:${runId}`);
  await detail(page)
    .getByLabel('What should happen next?', { exact: true })
    .fill('Keep my continuation instruction.');
  value.setTask({ revision: 2 });
  value.setRecoveryRun(undefined);
  await value.refresh();
  await expect(starting).toHaveValue(`run:${runId}`);
  await expect(detail(page)).toContainText('Choose starting files again.');
  await expect(
    detail(page).getByRole('button', {
      name: 'Continue collaboration',
      exact: true,
    }),
  ).toBeDisabled();
  await starting.selectOption('draft');
  await expect(
    detail(page).getByRole('button', {
      name: 'Continue collaboration',
      exact: true,
    }),
  ).toBeEnabled();
  await expect(
    detail(page).getByLabel('What should happen next?', { exact: true }),
  ).toHaveValue('Keep my continuation instruction.');
});

for (const host of ['Workspace', 'Work'] as const) {
  test(`reviewing exact saved files preserves unfinished continuation in ${host}`, async ({
    page,
  }, info) => {
    const value = await fixture(
      page,
      `review-partial-${host}-${info.project.name}`,
    );
    const initial = await (
      await page.request.get(`/api/resources/${value.room.resourceId}/files`)
    ).json();
    const saved = await mutate(
      page,
      `/resources/${value.room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: initial.commit,
        path: 'captured.md',
        content:
          '# Captured implementation\n\nPartial files from the failed attempt.',
      },
      'PUT',
    );
    expect(saved.ok()).toBe(true);
    const captured = (await saved.json()).publication.commit as string;
    const newer = await mutate(
      page,
      `/resources/${value.room.resourceId}/files`,
      {
        requestId: randomUUID(),
        expectedCommit: captured,
        path: 'captured.md',
        content:
          '# Newer shared files\n\nThis is not the selected captured version.',
      },
      'PUT',
    );
    expect(newer.ok()).toBe(true);
    if (host === 'Work') {
      const created = await mutate(page, `/rooms/${value.room.id}/work-items`, {
        requestId: randomUUID(),
        goal: value.task.goal,
      });
      expect(created.ok()).toBe(true);
      value.setTask({ workItemId: (await created.json()).item.id });
    }
    const runId = `review-captured-${randomUUID()}`;
    value.setTask({
      status: 'needs_attention',
      turnsUsed: 2,
      modelRequestsUsed: 18,
      checkpointCommit: initial.commit,
    });
    value.setRecoveryRun({
      runId,
      commit: captured,
      agentName: 'Writer',
      fileCount: 1,
    });
    await value.refresh();
    await openRoomMore(page, 'Execution history…');
    await workspace(page)
      .getByRole('button', { name: value.task.goal })
      .click();
    const win = host === 'Work' ? work(page) : workspace(page);
    const panel = win.getByRole('region', {
      name: 'Collaboration details',
      exact: true,
    });
    await expect(panel).toBeVisible();
    await panel.getByRole('button', { name: 'Continue…', exact: true }).click();
    await panel
      .getByLabel('Starting files', { exact: true })
      .selectOption(`run:${runId}`);
    const instruction = `Keep this unfinished ${host} continuation while reviewing the captured files.`;
    await panel
      .getByLabel('What should happen next?', { exact: true })
      .fill(instruction);
    await panel
      .getByLabel('Additional model requests', { exact: true })
      .fill('16');
    await panel
      .getByRole('checkbox', {
        name: 'Use current model settings',
        exact: true,
      })
      .check();
    const writes: string[] = [];
    const fileReads: string[] = [];
    page.on('request', (request) => {
      const url = new URL(request.url());
      if (
        request.method() === 'GET' &&
        url.pathname === `/api/resources/${value.room.resourceId}/files`
      )
        fileReads.push(url.searchParams.get('commit') || '');
      if (
        !['GET', 'HEAD'].includes(request.method()) &&
        (/^\/api\/(?:tasks\/|runs\/|daemon\/model\/|work-items\/[^/]+\/actions\/)/.test(
          url.pathname,
        ) ||
          /^\/api\/rooms\/[^/]+\/(?:messages|tasks|work-items)$/.test(
            url.pathname,
          ) ||
          /^\/api\/resources\/[^/]+\/(?:files|restore)$/.test(url.pathname))
      )
        writes.push(`${request.method()} ${url.pathname}`);
    });
    await panel
      .getByRole('button', { name: 'Review Saved Files', exact: true })
      .click();
    await expect(library(page)).toBeVisible();
    await expect(library(page)).toContainText('Historical version · read only');
    await expect(
      library(page).getByRole('heading', {
        name: 'Captured implementation',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      library(page).getByRole('heading', {
        name: 'Newer shared files',
        exact: true,
      }),
    ).toHaveCount(0);
    expect(fileReads).toContain(captured);
    await library(page)
      .getByRole('button', { name: /^Close .+ — Library$/ })
      .click();
    await openApp(page, host);
    await expect(panel).toBeVisible();
    await expect(
      panel.getByLabel('What should happen next?', { exact: true }),
    ).toHaveValue(instruction);
    await expect(
      panel.getByLabel('Starting files', { exact: true }),
    ).toHaveValue(`run:${runId}`);
    await expect(
      panel.getByLabel('Additional model requests', { exact: true }),
    ).toHaveValue('16');
    await expect(
      panel.getByRole('checkbox', {
        name: 'Use current model settings',
        exact: true,
      }),
    ).toBeChecked();
    expect(writes).toEqual([]);
  });
}
