import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { WorkItem } from '../apps/dashboard/src/features/workitems/api';
import type { Task } from '../apps/dashboard/src/features/collaboration/api';
import type { Run } from '../apps/dashboard/src/features/workspace/api';
import {
  createRoom,
  mutate,
  openApp,
  signIn,
  work,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

const detail = (page: Page) =>
  work(page).getByRole('region', { name: 'Work item detail', exact: true });
const inspector = (page: Page) =>
  work(page).getByRole('region', { name: 'Work details', exact: true });
const collaboration = (page: Page) =>
  work(page).getByRole('region', {
    name: 'Collaboration details',
    exact: true,
  });

async function fixture(page: Page) {
  await signIn(page);
  const room = await createRoom(
    page,
    `Notes progress ${randomUUID().slice(0, 8)}`,
  );
  const goal =
    'Build the Notes editor with local persistence, accessible navigation and import/export. '
      .repeat(3)
      .trim();
  const created = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
  });
  expect(created.ok()).toBe(true);
  const original = ((await created.json()) as { item: WorkItem }).item;
  const members = (
    await (await page.request.get(`/api/rooms/${room.id}/members`)).json()
  ).members;
  const agent = {
    ...members[0],
    name: 'Notes developer for the personal workspace and release workflow',
  };
  const now = new Date().toISOString();
  const taskId = `progress-task-${randomUUID()}`;
  const runId = `progress-run-${randomUUID()}`;
  let item: WorkItem = {
    ...original,
    status: 'running',
    revision: original.revision + 1,
    activeTaskId: taskId,
    execution: {
      taskId,
      status: 'running',
      revision: 1,
      summary: '',
      error: '',
      currentAgentName: agent.name,
      activeRunId: runId,
      updatedAt: now,
    },
    tasks: [{ id: taskId, status: 'running', summary: '', createdAt: now }],
  };
  const task: Task = {
    id: taskId,
    workItemId: item.id,
    roomId: room.id,
    resourceId: room.resourceId,
    threadRootId: '',
    originMessageId: '',
    requestedByUserId: item.ownerUserId,
    goal,
    status: 'running',
    revision: 1,
    participants: [
      { agent, runnerId: 'progress-runner', runnerName: 'Notes Runner' },
    ],
    currentAgentId: agent.id,
    activeRunId: runId,
    baseSharedCommit: 'a'.repeat(40),
    checkpointCommit: '',
    publishedCommit: '',
    error: '',
    summary: '',
    turnsUsed: 1,
    maxTurns: 6,
    modelRequestsUsed: 1,
    maxModelRequests: 24,
    inputRevision: 0,
    inputBytes: 0,
    coveredInputRevision: 0,
    createdAt: now,
    updatedAt: now,
  };
  const run: Run = {
    id: runId,
    taskId,
    roomId: room.id,
    resourceId: room.resourceId,
    threadRootId: '',
    originMessageId: '',
    agentId: agent.id,
    agentSnapshot: agent,
    runnerId: 'progress-runner',
    runnerName: 'Notes Runner',
    prompt: goal,
    references: [],
    status: 'running',
    createdAt: now,
    updatedAt: now,
    finishedAt: '',
    error: '',
    output: '',
    sessionId: '',
    sessionMode: 'fresh',
    baselineCommit: 'a'.repeat(40),
    resultCommit: '',
    environment: null,
    checks: [],
    files: [],
    exitCode: null,
    testExitCode: null,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    sessionGeneration: 1,
    published: false,
  };
  const reads = { task: 0, run: 0 };
  const executionWrites: string[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (
      !['GET', 'HEAD'].includes(request.method()) &&
      /^\/api\/(?:tasks\/|runs\/|work-items\/[^/]+\/actions\/|rooms\/[^/]+\/(?:work-items|tasks|messages)$)/.test(
        path,
      )
    )
      executionWrites.push(`${request.method()} ${path}`);
  });
  await page.route(`**/api/rooms/${room.id}/members`, (route) =>
    route.fulfill({ json: { members: [agent] } }),
  );
  await page.route(`**/api/work-items/${item.id}`, (route) =>
    route.fulfill({ json: { item } }),
  );
  await page.route(`**/api/work-items/${item.id}/history/tasks?*`, (route) =>
    route.fulfill({ json: { items: item.tasks, nextCursor: '' } }),
  );
  await page.route(`**/api/tasks/${taskId}?*`, (route) => {
    reads.task++;
    return route.fulfill({ json: { task } });
  });
  await page.route(`**/api/runs/${runId}`, (route) => {
    reads.run++;
    return route.fulfill({ json: { run } });
  });
  await page.route(`**/api/runs/${runId}/events?*`, (route) =>
    route.fulfill({
      json: {
        events: [
          {
            runId,
            seq: 1,
            type: 'tool_call',
            text: JSON.stringify({
              name: 'write',
              arguments: { path: 'src/notes.js' },
            }),
            createdAt: now,
          },
        ],
      },
    }),
  );
  await openApp(page, 'Work');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await work(page)
    .locator('.work-item-list')
    .getByRole('button')
    .filter({ hasText: goal })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'View progress', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Stop…', exact: true }),
  ).toBeEnabled();
  return {
    room,
    get item() {
      return item;
    },
    task,
    run,
    reads,
    executionWrites,
    setItem(change: Partial<WorkItem>) {
      item = { ...item, ...change };
    },
  };
}

async function stopDraft(page: Page, text: string) {
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await detail(page)
    .getByRole('combobox', { name: 'Manage work', exact: true })
    .selectOption('stop');
  const note = detail(page).getByRole('textbox', {
    name: 'Note (optional)',
    exact: true,
  });
  await note.fill(text);
  return note;
}

test('current progress opens directly and returns to the matter while task inspection keeps its own return', async ({
  page,
  browserName,
}, info) => {
  const errors = watchPageErrors(page, browserName);
  const value = await fixture(page);
  await expect(
    detail(page).getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    detail(page).getByRole('button', { name: 'View progress', exact: true }),
  ).toBeInViewport();
  // Resize hit regions extend beyond desktop window frames; measure content.
  expect(
    await work(page).evaluate((node) =>
      [
        ...node.querySelectorAll(
          '.work-app-body, .work-item-detail, .work-item-state',
        ),
      ]
        .filter((content) => content.clientWidth > 0)
        .filter((content) => content.scrollWidth > content.clientWidth + 1)
        .map((content) => content.className),
    ),
  ).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
  await work(page).screenshot({
    path: info.outputPath('work-current-progress.png'),
  });
  await detail(page)
    .getByRole('button', { name: 'View progress', exact: true })
    .click();
  await expect(
    inspector(page).getByRole('list', {
      name: 'Execution activity',
      exact: true,
    }),
  ).toContainText('src/notes.js');
  await expect(inspector(page)).toContainText('Notes Runner');
  await expect(collaboration(page)).toHaveCount(0);
  await inspector(page)
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(
    detail(page).getByRole('heading', { name: value.item.goal, exact: true }),
  ).toBeVisible();
  await expect(inspector(page)).toHaveCount(0);
  await detail(page)
    .getByRole('button', { name: 'View progress', exact: true })
    .click();
  await inspector(page)
    .getByRole('button', { name: 'Open collaboration', exact: true })
    .click();
  await expect(collaboration(page)).toBeVisible();
  await collaboration(page)
    .getByRole('button', { name: 'View current work', exact: true })
    .click();
  await inspector(page)
    .getByRole('button', { name: 'Back to Collaboration', exact: true })
    .click();
  await expect(collaboration(page)).toBeVisible();
  await collaboration(page)
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  await expect(work(page)).toHaveCount(1);
  await expect(
    workspace(page).getByRole('region', { name: 'Work details', exact: true }),
  ).toHaveCount(0);
  expect(value.executionWrites).toEqual([]);
  expect(errors.errors).toEqual([]);
});

test('opening progress preserves an unsaved decision and waits for draft recovery', async ({
  page,
}) => {
  const value = await fixture(page);
  let fail = true;
  await page.route('**/api/me/drafts/conversation', (route) => {
    const request = route.request();
    if (
      fail &&
      request.method() === 'PUT' &&
      request.postDataJSON().key === `work-item-decision:${value.item.id}`
    )
      return route.fulfill({
        status: 503,
        json: { error: 'This private decision could not be saved.' },
      });
    return route.fallback();
  });
  const text = 'Keep this note private while I inspect the current work.';
  const note = await stopDraft(page, text);
  await detail(page)
    .getByRole('button', { name: 'View progress', exact: true })
    .click();
  await expect(
    work(page)
      .getByRole('alert')
      .filter({ hasText: 'A private draft could not be saved.' }),
  ).toBeVisible();
  await expect(note).toHaveValue(text);
  await expect(inspector(page)).toHaveCount(0);
  expect(value.reads.run).toBe(0);
  fail = false;
  await detail(page)
    .getByRole('button', { name: 'Compare Private Drafts', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Save My Draft', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'View progress', exact: true })
    .click();
  await expect(inspector(page)).toBeVisible();
  await inspector(page)
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(
    detail(page).getByRole('tab', { name: 'Manage', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(note).toHaveValue(text);
  expect(value.executionWrites).toEqual([]);
});

test('a newer navigation supersedes progress while its private draft is saving', async ({
  page,
}) => {
  const value = await fixture(page);
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let writes = 0;
  await page.route('**/api/me/drafts/conversation', async (route) => {
    const request = route.request();
    if (
      request.method() !== 'PUT' ||
      request.postDataJSON().key !== `work-item-decision:${value.item.id}`
    )
      return route.fallback();
    writes++;
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  try {
    await stopDraft(page, 'Keep this decision after leaving the matter.');
    await detail(page)
      .getByRole('button', { name: 'View progress', exact: true })
      .click();
    await expect.poll(() => writes).toBeGreaterThan(0);
    await detail(page)
      .getByRole('button', { name: 'Back to Work', exact: true })
      .click();
    release();
    await expect(
      work(page).getByRole('combobox', { name: 'View', exact: true }),
    ).toBeVisible();
    await expect(inspector(page)).toHaveCount(0);
    expect(value.reads.task).toBe(0);
    expect(value.reads.run).toBe(0);
    expect(value.executionWrites).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('progress refuses a task belonging to another matter and can retry the matching route', async ({
  page,
}) => {
  const value = await fixture(page);
  value.task.workItemId = 'another-matter';
  await detail(page)
    .getByRole('button', { name: 'View progress', exact: true })
    .click();
  await expect(work(page).getByRole('alert')).toContainText(
    'This collaboration belongs to another work item.',
  );
  await expect(inspector(page)).toHaveCount(0);
  expect(value.reads.run).toBe(0);
  value.task.workItemId = value.item.id;
  await work(page)
    .getByRole('button', { name: 'Retry progress', exact: true })
    .click();
  await expect(inspector(page)).toBeVisible();
  await inspector(page)
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  expect(value.executionWrites).toEqual([]);
});

test('progress is offered only for the matter current active execution', async ({
  page,
}) => {
  const value = await fixture(page);
  value.setItem({
    revision: value.item.revision + 1,
    execution: { ...value.item.execution!, activeRunId: '' },
  });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'View progress', exact: true }),
  ).toHaveCount(0);
  value.setItem({
    revision: value.item.revision + 1,
    execution: {
      ...value.item.execution!,
      taskId: 'unrelated-task',
      activeRunId: value.run.id,
    },
  });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'View progress', exact: true }),
  ).toHaveCount(0);
  expect(value.reads.run).toBe(0);
  expect(value.executionWrites).toEqual([]);
});
