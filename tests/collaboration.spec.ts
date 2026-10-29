import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { Task } from '../apps/dashboard/src/features/collaboration/api';
import type { Agent, Run } from '../apps/dashboard/src/features/workspace/api';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

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
    createdAt: now,
    updatedAt: now,
  };
  let exists = false;
  let runs: Run[] = [];
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
  await page.route(`**/api/rooms/${room.id}/tasks`, (route) =>
    route.fulfill({ json: { tasks: exists ? [task] : [] } }),
  );
  await page.route(`**/api/tasks/${task.id}`, (route) =>
    route.fulfill({ json: { task, runs } }),
  );
  await page.reload();
  return {
    room,
    agents,
    get task() {
      return task;
    },
    setTask(value: Partial<Task>) {
      task = { ...task, ...value };
      exists = true;
    },
    setRuns(value: Run[]) {
      runs = value;
    },
    async refresh() {
      const response = await mutate(page, `/rooms/${room.id}/messages`, {
        requestId: randomUUID(),
        content: 'Presentation fixture update.',
        postOnly: true,
      });
      expect(response.ok()).toBeTruthy();
    },
    async open() {
      await workspace(page)
        .getByRole('button', { name: 'Collaborations', exact: true })
        .click();
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
  await page.route(`**/api/rooms/${value.room.id}/tasks`, async (route) => {
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
  await workspace(page)
    .getByRole('button', { name: 'Collaborations', exact: true })
    .click();
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
  await workspace(page)
    .getByRole('button', { name: 'Collaborations', exact: true })
    .click();
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
  expect(created).toHaveLength(2);
  expect(created[1]).toEqual(created[0]);
  expect(created[0]).toMatchObject({
    goal: value.task.goal,
    participantAgentIds: value.agents.map((agent) => agent.id),
    firstAgentId: value.agents[0].id,
  });
  const inputs: Record<string, unknown>[] = [];
  await page.route(`**/api/tasks/${value.task.id}/inputs`, (route) => {
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
  await page.route(`**/api/tasks/${value.task.id}/answers`, async (route) => {
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
  await value.refresh();
  await value.open();

  const continuations: Record<string, unknown>[] = [];
  await page.route(`**/api/tasks/${value.task.id}/continue`, (route) => {
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
    .getByRole('checkbox', {
      name: 'Continue from latest Room files',
      exact: true,
    })
    .check();
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
  expect(continuations).toHaveLength(1);
  expect(continuations[0]).toMatchObject({
    revision: 1,
    addTurns: 2,
    addModelRequests: 4,
    fromLatest: true,
    participantAgentIds: [value.agents[1].id, replacement.id],
    agentId: replacement.id,
  });
  await page.route(`**/api/tasks/${value.task.id}/cancel`, (route) => {
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
  await panel.getByRole('tab', { name: 'Task setup', exact: true }).click();
  await expect(
    panel.getByRole('region', { name: 'Shared goal', exact: true }),
  ).toContainText(value.task.goal);
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
