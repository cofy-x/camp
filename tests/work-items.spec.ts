import { randomUUID } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  openApp,
  openInboxActivity,
  openRoomMore,
  signIn,
  work,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';
import { timelineFixture } from './run-timeline-fixture';
import type {
  WorkItem,
  WorkItemInput,
  WorkActionInput,
} from '../apps/dashboard/src/features/workitems/api';
import type { Agent, Run } from '../apps/dashboard/src/features/workspace/api';
import type { Task } from '../apps/dashboard/src/features/collaboration/api';
import type {
  Evaluation,
  SourceEventPage,
} from '../apps/dashboard/src/features/automation/api';
import type { Memory } from '../apps/dashboard/src/features/memory/api';

const automation = (page: Page) =>
  workspace(page).getByRole('region', { name: 'Room management', exact: true });
const detail = (page: Page) =>
  work(page).getByRole('region', { name: 'Work item detail', exact: true });

async function openItems(page: Page) {
  await openApp(page, 'Workspace');
  await workspace(page)
    .getByRole('button', { name: 'Work', exact: true })
    .click();
  await expect(work(page)).toHaveClass(/is-active/);
}

async function capture(page: Page, scene: string) {
  const captures = [];
  for (const [size, viewport] of [
    ['desktop', { width: 1440, height: 900 }],
    ['narrow', { width: 390, height: 844 }],
  ] as const) {
    await page.setViewportSize(viewport);
    await work(page).evaluate((element) => {
      element.scrollTop = 0;
    });
    const geometry = await work(page).evaluate((element) => {
      const box = element.getBoundingClientRect();
      const left = box.left + element.clientLeft;
      const right = left + element.clientWidth;
      const measure = (node: Element) => {
        const rect = node.getBoundingClientRect();
        const style = getComputedStyle(node);
        return {
          tag: node.tagName.toLowerCase(),
          id: node.id,
          className: node.getAttribute('class'),
          label: node.getAttribute('aria-label'),
          left: rect.left,
          right: rect.right,
          width: rect.width,
          clientWidth: node.clientWidth,
          scrollWidth: node.scrollWidth,
          display: style.display,
          position: style.position,
          cssWidth: style.width,
          minWidth: style.minWidth,
          maxWidth: style.maxWidth,
          overflowX: style.overflowX,
          whiteSpace: style.whiteSpace,
        };
      };
      return {
        viewport: { width: innerWidth, height: innerHeight },
        desktopClass: element.closest('.p9-desktop')?.className,
        root: measure(element),
        page: {
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
        },
        contentOverflow: [
          ...element.querySelectorAll(
            '.p9-window-content, .work-app, .work-app-toolbar, .work-app-body, .work-items, .work-item-list, .work-item-detail, .work-item-overview, .work-create',
          ),
        ]
          .map(measure)
          .filter(
            (node) =>
              node.width > 0 &&
              (node.scrollWidth > node.clientWidth + 1 ||
                node.right > right + 1 ||
                node.left < left - 1),
          ),
        overflowing: [...element.querySelectorAll('*')]
          .map(measure)
          .filter(
            (node) =>
              node.width > 0 &&
              (node.right > right + 1 ||
                node.left < left - 1 ||
                node.scrollWidth > node.clientWidth + 1),
          )
          .sort((a, b) => b.right - a.right)
          .slice(0, 40),
      };
    });
    expect(
      geometry.contentOverflow,
      `Work ${scene} ${size} content overflow:\n${JSON.stringify(geometry, null, 2)}`,
    ).toEqual([]);
    expect(geometry.page.scrollWidth).toBeLessThanOrEqual(
      geometry.page.clientWidth + 1,
    );
    const path = test.info().outputPath(`matter-${scene}-${size}.png`);
    await work(page).screenshot({ path });
    captures.push({
      path,
      viewport,
      bounds: await work(page).boundingBox(),
    });
  }
  await writeFile(
    test.info().outputPath(`matter-${scene}.json`),
    JSON.stringify({ synthetic: true, captures }, null, 2) + '\n',
    { mode: 0o600 },
  );
}

async function presentation(
  page: Page,
  reload: () => Promise<void>,
  initial: Partial<WorkItem> = {},
) {
  await signIn(page);
  const room = await createRoom(
    page,
    `Matter fixture ${randomUUID().slice(0, 8)}`,
  );
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const now = new Date().toISOString();
  const expiry = new Date(Date.now() + 30 * 86400000).toISOString();
  const agent: Agent = {
    id: `matter-agent-${randomUUID()}`,
    name: 'Writer',
    handle: 'writer',
    icon: 'document',
    avatar: 'agent-robot-teal-v1',
    instructions: '',
    access: 'write',
    enabled: true,
    revision: 1,
    createdAt: now,
    updatedAt: now,
  };
  let item: WorkItem = {
    seq: 1,
    id: `matter-${randomUUID()}`,
    roomId: room.id,
    ownerUserId: user.id,
    goal: 'Prepare the customer proposal',
    completionCriteria: '',
    completionMode: 'human',
    status: 'open',
    revision: 1,
    deadline: expiry,
    expiresAt: expiry,
    budget: {
      maxEvaluations: 12,
      maxTasks: 4,
      maxModelRequests: 96,
      maxActions: 16,
      maxFollowUps: 8,
    },
    usage: {
      evaluations: 0,
      tasks: 0,
      modelRequests: 0,
      reservedModelRequests: 0,
      actions: 0,
      followUps: 0,
    },
    evidence: [],
    tasks: [],
    decisions: [],
    timers: [],
    actions: [],
    watches: [],
    createdAt: now,
    updatedAt: now,
    ...initial,
  };
  await page.route('**/api/agents', (route) =>
    route.fulfill({ json: { agents: [agent] } }),
  );
  await page.route(`**/api/rooms/${room.id}/members`, (route) =>
    route.fulfill({ json: { members: [agent] } }),
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
  await page.route(`**/api/rooms/${room.id}/work-item-defaults*`, (route) =>
    route.fulfill({
      json: {
        defaults: {
          budget: item.budget,
          expiresAt: expiry,
          participantAgentIds: [agent.id],
          firstAgentId: agent.id,
          execution: { maxTurns: 6, maxModelRequests: 24 },
        },
      },
    }),
  );
  await page.route(`**/api/rooms/${room.id}/work-items?*`, (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: { items: [item], nextAfter: 1, hasMore: false } })
      : route.fallback(),
  );
  await page.route('**/api/work-items?*', (route) =>
    route.fulfill({
      json: {
        items: [
          { ...item, roomName: room.name, needsYou: Boolean(item.question) },
        ],
        nextCursor: '',
        hasMore: false,
      },
    }),
  );
  await page.route(`**/api/work-items/${item.id}`, (route) =>
    route.fulfill({ json: { item } }),
  );
  await page.route(`**/api/work-items/${item.id}/history/*?*`, (route) => {
    const kind = new URL(route.request().url()).pathname.split('/').at(-1) as
      'evidence' | 'decisions' | 'tasks' | 'timers' | 'actions';
    return route.fulfill({ json: { items: item[kind], nextCursor: '' } });
  });
  await reload();
  return {
    room,
    agent,
    get item() {
      return item;
    },
    setItem(change: Partial<WorkItem>) {
      item = { ...item, ...change };
    },
    async open() {
      await openItems(page);
      await work(page)
        .getByRole('button')
        .filter({ hasText: item.goal })
        .click();
      await expect(detail(page)).toBeVisible();
    },
  };
}

test('a recorded matter recovers uncertain writes, answers judgment and closes with human confirmation', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Human confirmation ${randomUUID().slice(0, 8)}`,
  );
  await openItems(page);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Confirm the customer’s acceptance');
  let creates = 0;
  await page.route(`**/api/rooms/${room.id}/work-items`, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    creates++;
    expect(route.request().postDataJSON().start).toBe(false);
    expect((await route.fetch()).ok()).toBe(true);
    return route.abort('failed');
  });
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  await expect(
    work(page).getByRole('button', { name: 'Check status', exact: true }),
  ).toBeVisible();
  await expect(
    work(page).getByLabel('What needs to happen?', { exact: true }),
  ).toBeDisabled();
  await capture(page, 'uncertain-record');
  await openApp(page, 'Workspace');
  await workspace(page)
    .getByRole('button', { name: 'Discussion', exact: true })
    .click();
  await openItems(page);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  expect(creates).toBe(1);
  const { items } = (await (
    await page.request.get(`/api/rooms/${room.id}/work-items`)
  ).json()) as { items: WorkItem[] };
  expect(items).toHaveLength(1);
  const id = items[0].id;
  expect(items[0].completionMode).toBe('human');
  expect(
    (await (await page.request.get(`/api/work-items/${id}`)).json()).item.tasks,
  ).toHaveLength(0);
  const asked = await mutate(page, `/work-items/${id}/decisions`, {
    requestId: randomUUID(),
    revision: items[0].revision,
    kind: 'request_judgment',
    reason: 'The customer response needs confirmation.',
    question: 'Has the customer accepted the proposal?',
  });
  expect(asked.ok()).toBe(true);
  await expect(
    detail(page).getByRole('heading', {
      name: 'Has the customer accepted the proposal?',
      exact: true,
    }),
  ).toBeVisible();
  await detail(page)
    .getByLabel('Your answer', { exact: true })
    .fill('Yes. 客户确认。');
  let answers = 0;
  await page.route(`**/api/work-items/${id}/actions/answer`, async (route) => {
    answers++;
    expect((await route.fetch()).ok()).toBe(true);
    return route.abort('failed');
  });
  await detail(page)
    .getByRole('button', { name: 'Send answer', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Check status', exact: true }),
  ).toBeVisible();
  await detail(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  expect(answers).toBe(1);
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveCount(0);
  await detail(page)
    .getByRole('button', { name: 'Mark done…', exact: true })
    .click();
  await detail(page)
    .getByRole('form', { name: 'Next step', exact: true })
    .getByLabel('Note (optional)', { exact: true })
    .fill('The owner checked the customer’s confirmation.');
  await detail(page)
    .getByRole('button', { name: 'Mark done', exact: true })
    .click();
  await expect(
    detail(page).getByText('Completed', { exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('form', { name: 'Next step', exact: true }),
  ).toHaveCount(0);
  const completed = (await (
    await page.request.get(`/api/work-items/${id}`)
  ).json()) as { item: WorkItem };
  expect(completed.item.tasks).toHaveLength(0);
  expect(completed.item.evidence.length).toBeGreaterThan(0);
  const overview = detail(page).locator('.work-item-overview');
  await expect(overview.getByText(/^Marked complete · /)).toBeVisible();
  await expect(
    overview.getByText('The owner checked the customer’s confirmation.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(overview.locator('time')).toHaveAttribute(
    'datetime',
    completed.item.decisions.find((decision) => decision.kind === 'complete')!
      .createdAt,
  );
  await expect(
    overview.getByRole('region', { name: 'Work result', exact: true }),
  ).toHaveCount(0);
  await capture(page, 'completed-overview');
  let historyReads = 0;
  let allowRecovery = false;
  let recoverHistory!: () => void;
  const recovery = new Promise<void>((resolve) => {
    recoverHistory = resolve;
  });
  await page.route(
    `**/api/work-items/${id}/history/decisions?*`,
    async (route) => {
      historyReads++;
      // A background refresh may supersede the first failed read. Keep the
      // failure until manual retry, rather than counting requests.
      if (!allowRecovery)
        return route.fulfill({
          status: 503,
          json: { error: 'Decision history is temporarily unavailable.' },
        });
      await recovery;
      return route.fallback();
    },
  );
  try {
    await detail(page)
      .getByRole('tab', { name: 'Activity', exact: true })
      .click();
    await expect(
      detail(page).getByLabel('Show activity', { exact: true }),
    ).toHaveValue('decisions');
    await expect(
      detail(page)
        .getByRole('region', { name: 'Decisions', exact: true })
        .locator('summary'),
    ).toHaveCount(0);
    await expect(
      detail(page).getByRole('button', {
        name: 'Retry Decisions',
        exact: true,
      }),
    ).toBeVisible();
    const beforeUpdate = historyReads;
    expect(
      (
        await mutate(page, `/rooms/${room.id}/messages`, {
          requestId: randomUUID(),
          content: 'An unrelated Room update must retain history recovery.',
        })
      ).ok(),
    ).toBe(true);
    // Unrelated Room activity preserves history. Foreground recovery rechecks
    // the retained read and must keep its error until an accepted retry.
    await page.evaluate(() =>
      document.dispatchEvent(new Event('visibilitychange')),
    );
    await expect.poll(() => historyReads).toBeGreaterThan(beforeUpdate);
    await expect(
      detail(page).getByRole('button', {
        name: 'Retry Decisions',
        exact: true,
      }),
    ).toBeVisible();
    const beforeRetry = historyReads;
    allowRecovery = true;
    await detail(page)
      .getByRole('button', { name: 'Retry Decisions', exact: true })
      .click();
    await expect.poll(() => historyReads).toBeGreaterThan(beforeRetry);
    await expect(
      detail(page).getByRole('button', {
        name: 'Retry Decisions',
        exact: true,
      }),
    ).toBeVisible();
    recoverHistory();
    await expect(
      detail(page).getByRole('button', {
        name: 'Retry Decisions',
        exact: true,
      }),
    ).toHaveCount(0);
    await detail(page)
      .getByRole('button', { name: 'Refresh', exact: true })
      .click();
    await expect(
      detail(page)
        .getByRole('region', { name: 'Decisions', exact: true })
        .getByText('The owner checked the customer’s confirmation.', {
          exact: true,
        }),
    ).toBeVisible();
    await capture(page, 'completed-human');
  } finally {
    recoverHistory();
  }
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await work(page)
    .getByLabel('View', { exact: true })
    .selectOption({ label: 'Completed' });
  await expect(
    work(page)
      .getByRole('button')
      .filter({ hasText: 'Confirm the customer’s acceptance' }),
  ).toBeVisible();
  expect(errors.errors).toEqual([]);
});

test('message follow-up links the original and future replies in one recorded matter', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Follow replies ${randomUUID().slice(0, 8)}`,
  );
  const text = 'Please confirm the customer’s release acceptance.';
  const posted = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: text,
    postOnly: true,
  });
  expect(posted.ok()).toBe(true);
  const { message } = await posted.json();
  await openRoomMore(page, 'Activity…');
  const activityEvent = automation(page)
    .locator('.automation-event-list > li')
    .filter({ hasText: text });
  await activityEvent.getByText('Details', { exact: true }).click();
  await activityEvent
    .getByRole('button', { name: 'Track Work Item…', exact: true })
    .click();
  await expect(
    work(page).getByRole('checkbox', {
      name: 'Follow Room discussion',
      exact: true,
    }),
  ).toBeChecked();
  await openApp(page, 'Workspace');
  await workspace(page)
    .getByRole('button', { name: 'Discussion', exact: true })
    .click();
  const source = workspace(page).locator(`[data-message-id="${message.id}"]`);
  await source.getByRole('button', { name: 'Follow up…', exact: true }).click();
  await expect(
    work(page).getByLabel('What needs to happen?', { exact: true }),
  ).toHaveValue(text);
  await expect(
    work(page).getByRole('checkbox', { name: 'Follow replies', exact: true }),
  ).toBeChecked();
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Follow the release acceptance');
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  const list = (await (
    await page.request.get(`/api/rooms/${room.id}/work-items`)
  ).json()) as { items: WorkItem[] };
  const { item } = (await (
    await page.request.get(`/api/work-items/${list.items[0].id}`)
  ).json()) as { item: WorkItem };
  expect(item.tasks).toHaveLength(0);
  expect(item.evidence).toHaveLength(1);
  expect(item.watches).toEqual([
    expect.objectContaining({
      sourceId: 'room',
      threadKey: expect.stringContaining(message.id),
      enabled: true,
    }),
  ]);
  expect(
    (
      await mutate(page, `/rooms/${room.id}/messages`, {
        requestId: randomUUID(),
        content: 'An unrelated Room note.',
        postOnly: true,
      })
    ).ok(),
  ).toBe(true);
  expect(
    (
      await mutate(page, `/rooms/${room.id}/messages`, {
        requestId: randomUUID(),
        content: 'The customer accepted this release.',
        threadRootId: message.id,
      })
    ).ok(),
  ).toBe(true);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/work-items/${item.id}`)).json())
          .item.evidence.length,
    )
    .toBe(2);
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/work-items/${item.id}`) &&
        response.request().method() === 'GET',
    ),
    detail(page).getByRole('button', { name: 'Refresh', exact: true }).click(),
  ]);
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await detail(page)
    .getByLabel('Manage work', { exact: true })
    .selectOption('following');
  await expect(
    detail(page).getByText('Following replies', { exact: true }),
  ).toBeVisible();
  await detail(page)
    .getByLabel('Manage work', { exact: true })
    .selectOption('allowance');
  const allowance = detail(page).getByRole('form', {
    name: 'Work allowance',
    exact: true,
  });
  let failAllowanceDraft = true;
  await page.route('**/api/me/drafts/conversation', (route) => {
    if (
      route.request().method() !== 'PUT' ||
      route.request().postDataJSON().key !== `work-item-allowance:${item.id}` ||
      !failAllowanceDraft
    )
      return route.fallback();
    return route.fulfill({
      status: 503,
      json: { error: 'The allowance draft could not be saved.' },
    });
  });
  await allowance.getByLabel('Collaborations', { exact: true }).fill('5');
  await expect(
    detail(page).getByText('The allowance draft could not be saved.', {
      exact: true,
    }),
  ).toBeVisible();
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await expect(allowance).toBeHidden();
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Review allowance', exact: true }),
  ).toBeVisible();
  failAllowanceDraft = false;
  await detail(page)
    .getByRole('button', { name: 'Review allowance', exact: true })
    .click();
  await expect(
    detail(page).getByRole('tab', { name: 'Manage', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    detail(page).getByLabel('Manage work', { exact: true }),
  ).toHaveValue('allowance');
  await expect(
    allowance.getByLabel('Collaborations', { exact: true }),
  ).toHaveValue('5');
  await detail(page)
    .getByRole('button', { name: 'Compare Private Drafts', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Save My Draft', exact: true })
    .click();
  await expect(
    detail(page).getByText('The allowance draft could not be saved.', {
      exact: true,
    }),
  ).toHaveCount(0);
  const useCurrent = allowance.getByRole('button', {
    name: 'Use current limits version',
    exact: true,
  });
  if (await useCurrent.isVisible()) await useCurrent.click();
  await expect(
    allowance.getByRole('button', { name: 'Save allowance', exact: true }),
  ).toBeEnabled();
  const changed = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/work-items/${item.id}/actions/authorize`) &&
      response.request().method() === 'POST',
  );
  await allowance
    .getByRole('button', { name: 'Save allowance', exact: true })
    .click();
  const changedResponse = await changed;
  if (changedResponse.status() === 409) {
    await allowance
      .getByRole('button', { name: 'Use current limits version', exact: true })
      .click();
    await expect(
      allowance.getByLabel('Collaborations', { exact: true }),
    ).toHaveValue('5');
    await allowance
      .getByRole('button', { name: 'Save allowance', exact: true })
      .click();
  } else expect(changedResponse.ok()).toBe(true);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/work-items/${item.id}`)).json())
          .item.budget.maxTasks,
    )
    .toBe(5);
  await detail(page)
    .getByLabel('Manage work', { exact: true })
    .selectOption('reminder');
  const reminders: WorkActionInput[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      request.url().endsWith(`/api/work-items/${item.id}/actions/wait`)
    )
      reminders.push(request.postDataJSON() as WorkActionInput);
  });
  const condition = 'A customer acceptance arrives.';
  await detail(page)
    .getByLabel('Waiting for (optional)', { exact: true })
    .fill(condition);
  const reminderTime = detail(page).getByLabel('Remind me at', { exact: true });
  const saveReminder = detail(page).getByRole('button', {
    name: 'Save reminder',
    exact: true,
  });
  await expect(reminderTime).toHaveValue('');
  await expect(saveReminder).toBeDisabled();
  await reminderTime.press('Enter');
  expect(reminders).toHaveLength(0);
  const beforeReminder = (await (
    await page.request.get(`/api/work-items/${item.id}`)
  ).json()) as { item: WorkItem };
  expect(beforeReminder.item.timers).toHaveLength(0);
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await expect(
    detail(page).getByLabel('Waiting for (optional)', { exact: true }),
  ).toHaveValue(condition);
  await expect(saveReminder).toBeDisabled();
  const due = await page.evaluate(() => {
    const date = new Date(Date.now() + 3000);
    return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
      .toISOString()
      .slice(0, 19)
      .replace(/:00$/, '');
  });
  await reminderTime.fill(due);
  await expect(saveReminder).toBeEnabled();
  await saveReminder.click();
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/work-items/${item.id}`)).json())
          .item.status,
    )
    .toBe('needs_attention');
  await expect
    .poll(async () => {
      const saved = (await (
        await page.request.get(`/api/work-items/${item.id}`)
      ).json()) as { item: WorkItem };
      return (
        saved.item.timers.length === 1 &&
        saved.item.timers[0].status === 'fired'
      );
    })
    .toBe(true);
  expect(reminders).toHaveLength(1);
  expect(reminders[0]).toMatchObject({ waitCondition: condition });
  expect(reminders[0].resumeAt).toBeTruthy();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/work-items/${item.id}`) &&
        response.request().method() === 'GET',
    ),
    detail(page).getByRole('button', { name: 'Refresh', exact: true }).click(),
  ]);
  await expect(
    detail(page).getByRole('tab', { name: 'Manage', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  const unsentCondition = 'Keep this unfinished follow-up reminder.';
  await detail(page)
    .getByLabel('Waiting for (optional)', { exact: true })
    .fill(unsentCondition);
  await openInboxActivity(page);
  await page
    .getByRole('region', { name: 'Your inbox', exact: true })
    .getByRole('button')
    .filter({ hasText: 'Follow the release acceptance' })
    .filter({ hasText: room.name })
    .click();
  await expect(
    detail(page).getByRole('heading', {
      name: 'Follow the release acceptance',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    detail(page).getByLabel('Manage work', { exact: true }),
  ).toBeHidden();
  await expect(reminderTime).toBeHidden();
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await expect(
    detail(page).getByLabel('Waiting for (optional)', { exact: true }),
  ).toHaveValue(unsentCondition);
  expect(reminders).toHaveLength(1);
  await detail(page)
    .getByLabel('Manage work', { exact: true })
    .selectOption('allowance');
  failAllowanceDraft = true;
  const privateNote = 'Keep this allowance note after the work is completed.';
  await allowance
    .getByLabel('Note (optional)', { exact: true })
    .fill(privateNote);
  await expect(
    detail(page)
      .getByRole('alert')
      .filter({ hasText: 'The allowance draft could not be saved.' }),
  ).toBeVisible();
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await expect(allowance).toBeHidden();
  const current = (await (
    await page.request.get(`/api/work-items/${item.id}`)
  ).json()) as { item: WorkItem };
  const externallyCompleted = await mutate(
    page,
    `/work-items/${item.id}/actions/complete`,
    {
      requestId: randomUUID(),
      revision: current.item.revision,
      comment: 'The owner confirmed completion in another session.',
    },
  );
  expect(externallyCompleted.ok()).toBe(true);
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByText('Completed', { exact: true }),
  ).toBeVisible();
  await expect(
    detail(page)
      .getByRole('alert')
      .filter({ hasText: 'The allowance draft could not be saved.' }),
  ).toBeVisible();
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await expect(detail(page)).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Review allowance', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page)
      .locator('button:enabled:visible')
      .filter({
        hasText: /^(Save allowance|Start work…|Mark done…|Stop work)$/,
      }),
  ).toHaveCount(0);
  failAllowanceDraft = false;
  await detail(page)
    .getByRole('button', { name: 'Review allowance', exact: true })
    .click();
  const recoveredAllowance = detail(page).getByRole('region', {
    name: 'Allowance and access',
    exact: true,
  });
  const recoveredNote = recoveredAllowance.getByRole('textbox', {
    name: 'Note (optional)',
    exact: true,
  });
  await expect(recoveredAllowance).toBeVisible();
  await expect(recoveredNote).toBeVisible();
  await expect(recoveredNote).toHaveValue(privateNote);
  await expect(recoveredNote).toBeDisabled();
  await expect(
    recoveredAllowance.getByRole('button', {
      name: 'Save allowance',
      exact: true,
    }),
  ).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Compare Private Drafts', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Save My Draft', exact: true })
    .click();
  await expect(
    detail(page)
      .getByRole('alert')
      .filter({ hasText: 'The allowance draft could not be saved.' }),
  ).toHaveCount(0);
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await expect(detail(page)).toHaveCount(0);
  expect(errors.errors).toEqual([]);
});

test('starting work freezes the actual finite grant and recovers the same request after navigation', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const value = await presentation(page, errors.reload);
  let receipt: WorkItem | null = null;
  const bodies: WorkItemInput[] = [];
  await page.route(
    `**/api/rooms/${value.room.id}/work-items`,
    async (route) => {
      const body = route.request().postDataJSON() as WorkItemInput;
      bodies.push(body);
      value.setItem({
        goal: body.goal,
        status: 'running',
        activeTaskId: 'fixture-started-task',
        revision: 2,
        usage: { ...value.item.usage, tasks: 1, reservedModelRequests: 24 },
      });
      receipt = value.item;
      return route.abort('failed');
    },
  );
  await page.route('**/api/work-item-requests/*', (route) =>
    receipt
      ? route.fulfill({ json: { item: receipt } })
      : route.fulfill({ status: 404, json: { error: 'Not confirmed' } }),
  );
  const policy = {
    id: 'fixture-rule',
    roomId: value.room.id,
    sourceId: 'room',
    revision: 3,
    ownerUserId: value.item.ownerUserId,
    enabled: true,
    mode: 'auto',
    rules: 'Follow customer replies.',
    allowedActorIds: [value.item.ownerUserId],
    participantAgentIds: [value.agent.id],
    firstAgentId: value.agent.id,
    maxTurns: 4,
    maxModelRequests: 12,
    dailyEvaluations: 12,
    dailyTasks: 4,
    allowAnswers: false,
    allowComments: false,
    credentialConfigured: false,
    startAfter: 0,
  };
  await page.route(
    `**/api/rooms/${value.room.id}/automation-policies`,
    (route) => route.fulfill({ json: { policies: [policy] } }),
  );
  let releaseDefaults!: () => void;
  const defaultsReady = new Promise<void>((resolve) => {
    releaseDefaults = resolve;
  });
  await page.route(
    `**/api/rooms/${value.room.id}/work-item-defaults?policyId=*`,
    async (route) => {
      await defaultsReady;
      return route.fulfill({
        json: {
          defaults: {
            budget: value.item.budget,
            expiresAt: value.item.expiresAt,
            participantAgentIds: [value.agent.id],
            firstAgentId: value.agent.id,
            execution: { maxTurns: 6, maxModelRequests: 24 },
          },
        },
      });
    },
  );
  expect(
    (
      await mutate(
        page,
        '/me/drafts/conversation',
        {
          key: `work-item-create:${value.room.id}:manual`,
          value: {
            goal: '',
            participantAgentIds: [value.agent.id, 'former-agent'],
            firstAgentId: 'former-agent',
          },
          revision: 0,
        },
        'PUT',
      )
    ).ok(),
  ).toBe(true);
  await openItems(page);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Draft a one-page customer proposal');
  await expect(
    work(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeDisabled();
  await work(page).getByLabel('Change Agents', { exact: true }).click();
  await work(page)
    .getByRole('checkbox', { name: 'former-agent · Unavailable', exact: true })
    .click();
  await expect(
    work(page).getByRole('checkbox', {
      name: 'former-agent · Unavailable',
      exact: true,
    }),
  ).toHaveCount(0);
  await work(page).getByLabel('Change Agents', { exact: true }).click();
  await expect(
    work(page).getByText('Writer · May edit Room files', { exact: true }),
  ).toBeVisible();
  await expect(
    work(page).getByText('Up to 6 turns · 24 model requests', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    work(page).getByText('Team-visible draft', {
      exact: true,
    }),
  ).toBeVisible();
  await work(page).getByText('Options', { exact: true }).click();
  await work(page)
    .getByLabel('Future follow-up', { exact: true })
    .selectOption(policy.id);
  await expect(
    work(page).getByText('Up to 6 turns · 24 model requests', {
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    work(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeDisabled();
  await expect(
    work(page).getByRole('button', { name: 'Keep track', exact: true }),
  ).toBeDisabled();
  releaseDefaults();
  await expect(
    work(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeEnabled();
  await work(page)
    .getByLabel('Future follow-up', { exact: true })
    .selectOption('');
  await expect(
    work(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeEnabled();
  await work(page)
    .getByRole('button', { name: 'Start work', exact: true })
    .click();
  await expect(
    work(page).getByRole('button', { name: 'Check status', exact: true }),
  ).toBeVisible();
  await openApp(page, 'Workspace');
  await workspace(page)
    .getByRole('button', { name: 'Discussion', exact: true })
    .click();
  await openItems(page);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  await expect(
    detail(page).getByText('Working · Writer', { exact: true }),
  ).toHaveCount(0);
  await expect(
    detail(page).getByRole('heading', {
      name: 'Draft a one-page customer proposal',
      exact: true,
    }),
  ).toBeVisible();
  expect(bodies).toHaveLength(1);
  expect(bodies[0]).toMatchObject({
    start: true,
    goal: 'Draft a one-page customer proposal',
    participantAgentIds: [value.agent.id],
    firstAgentId: value.agent.id,
    budget: value.item.budget,
  });
  expect(bodies[0].expiresAt).toBeTruthy();
  expect(errors.errors).toEqual([]);
});

test('the current task question answers in place with task revision and an exact uncertain receipt', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const value = await presentation(page, errors.reload, {
    status: 'waiting_for_person',
    completionCriteria: 'The proposal addresses the confirmed audience.',
    question: 'Who should the proposal address?',
  });
  await value.open();
  await detail(page)
    .getByLabel('Your answer', { exact: true })
    .fill('New customers');
  // A task can ask a new question without changing the matter revision.
  // The earlier matter answer has no saved Task target to continue yet.
  value.setItem({
    status: 'running',
    question: undefined,
    activeTaskId: 'question-task',
    execution: {
      taskId: 'question-task',
      status: 'waiting_for_person',
      revision: 3,
      summary: '',
      error: '',
      question: {
        id: 'question-one',
        text: 'Which audience should the proposal address?',
        options: ['New customers', 'Existing customers'],
        answer: '',
      },
      currentAgentName: 'Writer',
      activeRunId: '',
      updatedAt: new Date().toISOString(),
    },
  });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByRole('heading', {
      name: 'Which audience should the proposal address?',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveValue('New customers');
  await expect(
    detail(page).getByRole('button', {
      name: 'Answer and continue',
      exact: true,
    }),
  ).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Use current state', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', {
      name: 'Answer and continue',
      exact: true,
    }),
  ).toBeEnabled();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveValue('New customers');
  await expect(
    detail(page).getByRole('tablist', { name: 'Work details', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    detail(page).getByRole('heading', {
      name: 'Which audience should the proposal address?',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByText('Needs your answer · Writer', { exact: true }),
  ).toHaveCount(0);
  await expect(detail(page).locator('summary:visible')).toHaveCount(0);
  await expect(
    detail(page).getByLabel('Manage work', { exact: true }),
  ).toBeHidden();
  await expect(
    detail(page).getByLabel('Show activity', { exact: true }),
  ).toBeHidden();
  await expect(
    detail(page).getByRole('button', { name: 'Cancel', exact: true }),
  ).toHaveCount(0);
  await expect(
    detail(page).getByLabel('Decision', { exact: true }),
  ).toHaveCount(0);
  await detail(page)
    .getByRole('button', { name: 'New customers', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveValue('New customers');
  await detail(page)
    .getByRole('tab', { name: 'Activity', exact: true })
    .click();
  await detail(page)
    .getByLabel('Show activity', { exact: true })
    .selectOption('tasks');
  await expect(
    detail(page).getByRole('region', { name: 'Work sessions', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page)
      .getByRole('region', { name: 'Work sessions', exact: true })
      .locator('summary'),
  ).toHaveCount(0);
  await detail(page).getByText('Complete when', { exact: true }).click();
  await expect(
    detail(page).getByText('The proposal addresses the confirmed audience.', {
      exact: true,
    }),
  ).toBeVisible();
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await detail(page)
    .getByLabel('Manage work', { exact: true })
    .selectOption('following');
  await expect(
    detail(page).getByText('No conversations followed.', { exact: true }),
  ).toBeVisible();
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveValue('New customers');
  await expect(
    detail(page).getByText('The proposal addresses the confirmed audience.', {
      exact: true,
    }),
  ).toBeHidden();
  await expect(detail(page).locator('summary:visible')).toHaveCount(0);
  await capture(page, 'question');
  value.setItem({
    revision: 2,
    execution: { ...value.item.execution!, revision: 4 },
  });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', {
      name: 'Use current state',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', {
      name: 'Answer and continue',
      exact: true,
    }),
  ).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Use current state', exact: true })
    .click();
  const bodies: WorkActionInput[] = [];
  let submitted: WorkItem | null = null;
  await page.route(
    `**/api/work-items/${value.item.id}/actions/answer`,
    async (route) => {
      bodies.push(route.request().postDataJSON());
      value.setItem({
        revision: 3,
        execution: {
          ...value.item.execution!,
          status: 'queued',
          revision: 5,
          question: {
            ...value.item.execution!.question!,
            answer: 'New customers',
          },
        },
      });
      submitted = value.item;
      return route.abort('failed');
    },
  );
  await page.route('**/api/work-item-requests/*', (route) =>
    route.fulfill({ json: { item: submitted } }),
  );
  await detail(page)
    .getByRole('button', { name: 'Answer and continue', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Check status', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toBeDisabled();
  await detail(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveCount(0);
  expect(bodies).toHaveLength(1);
  expect(bodies[0]).toMatchObject({
    revision: 2,
    taskId: 'question-task',
    taskRevision: 4,
    questionId: 'question-one',
    content: 'New customers',
  });
  expect(bodies[0].participantAgentIds).toBeUndefined();
  expect(errors.errors).toEqual([]);
});

test('results expose exact files and the final finite grant; stop remains visible until execution stops', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const value = await presentation(page, errors.reload, {
    status: 'open',
    execution: {
      taskId: 'result-task',
      status: 'completed',
      revision: 4,
      summary: 'The customer proposal is ready for your review.',
      error: '',
      currentAgentName: 'Writer',
      activeRunId: '',
      updatedAt: new Date().toISOString(),
    },
    usage: {
      evaluations: 0,
      tasks: 3,
      modelRequests: 70,
      reservedModelRequests: 72,
      actions: 0,
      followUps: 0,
    },
    result: {
      taskId: 'result-task',
      summary: 'The customer proposal is ready for your review.',
      resourceId: 'fixture-resource',
      commit: 'b'.repeat(40),
      published: false,
      files: [
        { path: 'proposal.md', status: 'added' },
        { path: 'old-proposal.md', status: 'deleted' },
      ],
    },
  });
  const resultFiles = value.item.result!.files;
  value.setItem({
    result: {
      ...value.item.result!,
      resourceId: value.room.resourceId,
      files: resultFiles.slice(0, 1),
    },
  });
  const opened: string[] = [];
  await page.route(`**/api/resources/${value.room.resourceId}/**`, (route) => {
    opened.push(route.request().url());
    return route.fulfill({
      status: 404,
      json: { error: 'Exact-version fixture destination' },
    });
  });
  await openItems(page);
  const row = work(page)
    .getByRole('button')
    .filter({ hasText: value.item.goal });
  await expect(row.getByText('Result ready', { exact: true })).toBeVisible();
  await row.click();
  await expect(
    detail(page).getByText('Result ready', { exact: true }),
  ).toBeVisible();
  const workResult = detail(page).getByRole('region', {
    name: 'Work result',
    exact: true,
  });
  await expect(
    workResult.getByRole('button', { name: 'Open files', exact: true }),
  ).toHaveCount(0);
  expect(
    await workResult.evaluate((element) => {
      const files = element.querySelector('.work-item-files');
      const summary = element.querySelector('.workspace-markdown');
      return Boolean(
        files &&
        summary &&
        files.compareDocumentPosition(summary) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      );
    }),
  ).toBe(true);
  value.setItem({ result: { ...value.item.result!, files: resultFiles } });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    workResult.getByRole('button', { name: 'Open files', exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByText('The customer proposal is ready for your review.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByText('Team draft', { exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'old-proposal.md', exact: true }),
  ).toHaveCount(0);
  await capture(page, 'result');
  await detail(page)
    .getByRole('button', { name: 'proposal.md', exact: true })
    .click();
  await expect(library(page)).toBeVisible();
  await expect.poll(() => opened.length).toBeGreaterThan(0);
  expect(opened.some((url) => url.includes(`commit=${'b'.repeat(40)}`))).toBe(
    true,
  );
  await library(page)
    .getByRole('button', { name: /^Close .+ — Library$/ })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Request changes…', exact: true })
    .click();
  await expect(
    detail(page).getByText('Up to 6 turns · 24 model requests', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeEnabled();
  await detail(page)
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(
    detail(page).getByRole('form', { name: 'Next step', exact: true }),
  ).toHaveCount(0);
  await detail(page)
    .getByRole('button', { name: 'Request changes…', exact: true })
    .click();
  const actions: Record<string, unknown>[] = [];
  await page.route(
    `**/api/work-items/${value.item.id}/actions/start`,
    (route) => {
      actions.push(route.request().postDataJSON());
      value.setItem({
        revision: 2,
        status: 'running',
        activeTaskId: 'last-task',
        execution: {
          taskId: 'last-task',
          status: 'running',
          revision: 1,
          summary: 'Updating the proposal.',
          error: '',
          currentAgentName: 'Writer',
          activeRunId: 'running-fixture',
          updatedAt: new Date().toISOString(),
        },
      });
      return route.fulfill({ json: { item: value.item } });
    },
  );
  await detail(page)
    .getByLabel('What should happen next? (optional)', { exact: true })
    .fill('Use a one-page format.');
  await detail(page)
    .getByRole('button', { name: 'Start work', exact: true })
    .click();
  await expect(
    detail(page).getByRole('button', { name: 'Stop…', exact: true }),
  ).toBeVisible();
  await detail(page)
    .getByRole('button', { name: 'Stop…', exact: true })
    .click();
  await page.route(
    `**/api/work-items/${value.item.id}/actions/stop`,
    (route) => {
      actions.push(route.request().postDataJSON());
      value.setItem({ revision: 3, status: 'stopping' });
      return route.fulfill({ json: { item: value.item } });
    },
  );
  await detail(page)
    .getByRole('button', { name: 'Stop work', exact: true })
    .click();
  await expect(
    detail(page).getByText(
      'Stopping the current work. Future follow-ups are disabled.',
      { exact: true },
    ),
  ).toBeVisible();
  value.setItem({ revision: 4, status: 'cancelled', activeTaskId: undefined });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByText('Cancelled', { exact: true }),
  ).toBeVisible();
  expect(actions).toHaveLength(2);
  expect(actions[0].comment).toBe('Use a one-page format.');
  expect(errors.errors).toEqual([]);
});
test('completion confirmation is explicit and waits for unresolved external action receipts', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const value = await presentation(page, errors.reload, {
    status: 'waiting_for_person',
    questionKind: 'completion',
    question: 'The proposal is ready. Confirm that this work is done.',
  });
  const confirms: WorkActionInput[] = [];
  await page.route(
    `**/api/work-items/${value.item.id}/actions/complete`,
    (route) => {
      confirms.push(route.request().postDataJSON());
      value.setItem({
        revision: 2,
        status: 'waiting',
        question: undefined,
        questionKind: undefined,
        waitCondition:
          'Waiting for confirmed external action delivery receipts.',
        actions: [
          {
            id: 'pending-comment',
            kind: 'comment',
            status: 'unknown',
            body: 'Proposal ready.',
            createdAt: value.item.createdAt,
          },
        ],
      });
      return route.fulfill({ json: { item: value.item } });
    },
  );
  await value.open();
  await expect(
    detail(page).getByLabel('Your answer', { exact: true }),
  ).toHaveCount(0);
  await expect(
    detail(page).getByRole('button', { name: 'Confirm done', exact: true }),
  ).toBeVisible();
  await capture(page, 'completion-confirmation');
  await detail(page)
    .getByRole('button', { name: 'Confirm done', exact: true })
    .click();
  await expect(
    detail(page).getByText(
      'Waiting for confirmed external action delivery receipts.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    detail(page).getByText('Completed', { exact: true }),
  ).toHaveCount(0);
  expect(confirms).toHaveLength(1);
  expect(confirms[0]).toMatchObject({ revision: 1 });
  expect(confirms[0].content).toBeUndefined();
  value.setItem({
    revision: 3,
    status: 'completed',
    waitCondition: undefined,
    actions: [],
  });
  await detail(page)
    .getByRole('button', { name: 'Refresh', exact: true })
    .click();
  await expect(
    detail(page).getByText('Completed', { exact: true }),
  ).toBeVisible();
  expect(errors.errors).toEqual([]);
});

test('blocked work opens its saved execution for an explicit continuation grant', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const value = await presentation(page, errors.reload, {
    status: 'running',
    activeTaskId: 'blocked-task',
    execution: {
      taskId: 'blocked-task',
      status: 'needs_attention',
      revision: 4,
      summary: 'The first draft is saved.',
      error: 'The work session reached its model request limit.',
      currentAgentName: 'Writer',
      activeRunId: '',
      updatedAt: new Date().toISOString(),
    },
  });
  const { user } = await (await page.request.get('/api/auth/status')).json();
  await page.route('**/api/tasks/blocked-task?*', (route) =>
    route.fulfill({
      json: {
        task: {
          id: 'blocked-task',
          workItemId: value.item.id,
          roomId: value.room.id,
          resourceId: value.room.resourceId,
          threadRootId: '',
          originMessageId: '',
          requestedByUserId: user.id,
          goal: value.item.goal,
          status: 'needs_attention',
          revision: 4,
          participants: [
            {
              agent: value.agent,
              runnerId: 'fixture-runner',
              runnerName: 'Fixture Runner',
            },
          ],
          currentAgentId: value.agent.id,
          activeRunId: '',
          baseSharedCommit: 'a'.repeat(40),
          checkpointCommit: 'b'.repeat(40),
          publishedCommit: '',
          error: 'The work session reached its model request limit.',
          summary: 'The first draft is saved.',
          turnsUsed: 6,
          maxTurns: 6,
          modelRequestsUsed: 24,
          maxModelRequests: 24,
          inputRevision: 0,
          inputBytes: 0,
          coveredInputRevision: 0,
          createdAt: value.item.createdAt,
          updatedAt: value.item.updatedAt,
        },
      },
    }),
  );
  await openItems(page);
  const pausedRow = work(page)
    .getByRole('button')
    .filter({ hasText: value.item.goal });
  await expect(
    pausedRow.getByText('Needs attention', { exact: true }),
  ).toBeVisible();
  await expect(pausedRow.getByText('Working', { exact: true })).toHaveCount(0);
  await capture(page, 'paused-work-list');
  await value.open();
  await expect(
    detail(page).getByText('Needs attention · Writer', { exact: true }),
  ).toBeVisible();
  await expect(
    detail(page).getByRole('button', { name: 'Start work…', exact: true }),
  ).toHaveCount(0);
  await detail(page).getByRole('tab', { name: 'Manage', exact: true }).click();
  await expect(
    detail(page).getByLabel('Manage work', { exact: true }),
  ).toHaveValue('allowance');
  let releaseSave!: () => void;
  let observedSave!: () => void;
  const saveGate = new Promise<void>((resolve) => {
    releaseSave = resolve;
  });
  const saveObserved = new Promise<void>((resolve) => {
    observedSave = resolve;
  });
  await page.route('**/api/me/drafts/conversation', async (route) => {
    if (
      route.request().method() !== 'PUT' ||
      route.request().postDataJSON().key !==
        `work-item-allowance:${value.item.id}`
    )
      return route.fallback();
    observedSave();
    await saveGate;
    return route.fallback();
  });
  await detail(page)
    .getByRole('form', { name: 'Work allowance', exact: true })
    .getByLabel('Note (optional)', { exact: true })
    .fill('Keep this allowance note.');
  await detail(page)
    .getByRole('tab', { name: 'Overview', exact: true })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Review and continue…', exact: true })
    .click();
  await saveObserved;
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  releaseSave();
  await expect(detail(page)).toHaveCount(0);
  await expect(
    work(page).getByRole('region', {
      name: 'Collaboration details',
      exact: true,
    }),
  ).toHaveCount(0);
  await work(page)
    .getByRole('button')
    .filter({ hasText: value.item.goal })
    .click();
  await detail(page)
    .getByRole('button', { name: 'Review and continue…', exact: true })
    .click();
  const execution = work(page).getByRole('region', {
    name: 'Collaboration details',
    exact: true,
  });
  await expect(execution).toBeVisible();
  await expect(work(page)).toHaveClass(/is-active/);
  await execution
    .getByRole('button', { name: 'Continue…', exact: true })
    .click();
  await expect(
    execution.getByLabel('Additional model requests', { exact: true }),
  ).toBeVisible();
  await execution
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(
    detail(page).getByRole('heading', { name: value.item.goal, exact: true }),
  ).toBeVisible();
  expect(errors.errors).toEqual([]);
});

test('linked task and run inspection stay inside Work and return to the same matter', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  const taskId = `work-task-${randomUUID()}`;
  const runId = `work-run-${randomUUID()}`;
  const createdAt = new Date().toISOString();
  const value = await presentation(page, errors.reload, {
    status: 'running',
    activeTaskId: taskId,
    tasks: [{ id: taskId, status: 'running', summary: '', createdAt }],
  });
  const task: Task = {
    id: taskId,
    workItemId: value.item.id,
    roomId: value.room.id,
    resourceId: value.room.resourceId,
    threadRootId: '',
    originMessageId: '',
    requestedByUserId: value.item.ownerUserId,
    goal: value.item.goal,
    status: 'running',
    revision: 2,
    participants: [
      {
        agent: value.agent,
        runnerId: 'fixture-runner',
        runnerName: 'Fixture Runner',
      },
    ],
    currentAgentId: value.agent.id,
    activeRunId: runId,
    baseSharedCommit: 'a'.repeat(40),
    checkpointCommit: 'a'.repeat(40),
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
    createdAt,
    updatedAt: createdAt,
  };
  const run: Run = {
    id: runId,
    taskId,
    roomId: value.room.id,
    threadRootId: '',
    originMessageId: '',
    agentId: value.agent.id,
    resourceId: value.room.resourceId,
    runnerId: 'fixture-runner',
    runnerName: 'Fixture Runner',
    prompt: value.item.goal,
    references: [],
    status: 'running',
    createdAt,
    updatedAt: createdAt,
    finishedAt: '',
    error: '',
    output: '',
    sessionId: '',
    sessionMode: 'fresh',
    baselineCommit: 'a'.repeat(40),
    resultCommit: '',
    agentSnapshot: value.agent,
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
  await page.route(`**/api/tasks/${taskId}?*`, (route) =>
    route.fulfill({ json: { task } }),
  );
  await page.route(`**/api/runs/${runId}?view=summary`, (route) =>
    route.fulfill({ json: { run: timelineFixture(run) } }),
  );
  await page.route(`**/api/runs/${runId}/reply`, (route) =>
    route.fulfill({ json: { message: null } }),
  );
  await page.route(`**/api/runs/${runId}`, (route) =>
    route.fulfill({ json: { run } }),
  );
  await page.route(`**/api/runs/${runId}/events?*`, (route) =>
    route.fulfill({ json: { events: [] } }),
  );
  await value.open();
  await detail(page)
    .getByRole('tab', { name: 'Activity', exact: true })
    .click();
  await detail(page)
    .getByLabel('Show activity', { exact: true })
    .selectOption('tasks');
  await detail(page)
    .getByRole('button', { name: 'Open execution', exact: true })
    .click();
  const execution = work(page).getByRole('region', {
    name: 'Collaboration details',
    exact: true,
  });
  await expect(execution).toContainText(value.item.goal);
  await execution
    .getByRole('button', { name: 'View current work', exact: true })
    .click();
  const inspector = work(page).getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  await expect(inspector).toContainText('Fixture Runner');
  await inspector
    .getByRole('button', { name: 'Open collaboration', exact: true })
    .click();
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(inspector).toHaveCount(0);
  await expect(execution).toContainText(value.item.goal);
  await execution
    .getByRole('button', { name: 'View current work', exact: true })
    .click();
  await inspector
    .getByRole('button', { name: 'Back to Collaboration', exact: true })
    .click();
  await execution
    .getByRole('button', { name: 'Back to Work Item', exact: true })
    .click();
  await expect(detail(page)).toContainText(value.item.goal);
  await expect(
    workspace(page).getByRole('region', {
      name: 'Collaboration details',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(work(page)).toHaveCount(1);
  expect(errors.errors).toEqual([]);
});

test('memory is room-scoped, versioned and recoverable without granting work', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Room memory ${randomUUID().slice(0, 8)}`,
  );
  await openRoomMore(page, 'Team notes…');
  const memory = workspace(page).getByRole('region', {
    name: 'Room memory',
    exact: true,
  });
  await memory.getByRole('button', { name: 'New note…', exact: true }).click();
  const original = 'The customer prefers concise proposals. 客户偏好简洁。';
  await memory.getByLabel('Note', { exact: true }).fill(original);
  await expect
    .poll(async () => {
      const saved = await (
        await page.request.get(
          `/api/me/drafts/conversation?key=${encodeURIComponent(`memory:${room.id}:new`)}`,
        )
      ).json();
      return saved.draft.value?.content;
    })
    .toBe(original);
  await memory.getByRole('button', { name: 'Save note', exact: true }).click();
  await expect(memory.getByText(original, { exact: true })).toBeVisible();
  await memory.getByRole('button', { name: 'Revise…', exact: true }).click();
  const updated = original + ' Include a one-page budget.';
  await memory.getByLabel('Note', { exact: true }).fill(updated);
  await page.route('**/api/memories/*', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback();
    expect((await route.fetch()).ok()).toBe(true);
    return route.abort('failed');
  });
  await memory.getByRole('button', { name: 'Save note', exact: true }).click();
  await expect(
    memory.getByRole('button', { name: 'Check Saved Operation', exact: true }),
  ).toBeVisible();
  await memory
    .getByRole('button', { name: 'Check Saved Operation', exact: true })
    .click();
  await expect(memory.locator('pre')).toHaveText(updated);
  await expect(memory.locator('pre')).toBeVisible();
  await memory
    .getByRole('button', { name: 'Previous Revision', exact: true })
    .click();
  await expect(memory.locator('pre')).toHaveText(original);
  await memory
    .getByRole('button', { name: 'Current Revision', exact: true })
    .click();
  await memory
    .getByRole('button', { name: 'Remove note', exact: true })
    .click();
  await expect(memory.locator('pre')).toHaveCount(0);
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/work-items`)).json())
      .items,
  ).toHaveLength(0);
  await errors.reload();
  await openRoomMore(page, 'Team notes…');
  await memory.getByRole('button', { name: 'New note…', exact: true }).click();
  await expect(memory.getByLabel('Note', { exact: true })).toBeEnabled();
  await expect(memory.getByLabel('Note', { exact: true })).toHaveValue('');
  expect(errors.errors).toEqual([]);
});

test('assessment context pages retain frozen work and memory revisions after current values change', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Frozen context ${randomUUID().slice(0, 8)}`,
  );
  const created = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal: 'Prepare a proposal',
    completionCriteria: 'The owner verifies it.',
  });
  expect(created.ok()).toBe(true);
  const { item } = (await created.json()) as { item: WorkItem };
  const notes: Memory[] = [];
  for (const content of [
    'The customer prefers a one-page proposal.',
    'The budget requires an explicit owner decision.',
  ]) {
    const saved = await mutate(page, `/rooms/${room.id}/memories`, {
      requestId: randomUUID(),
      revision: 0,
      content,
      provenance: { kind: 'human_note' },
    });
    expect(saved.ok()).toBe(true);
    notes.push((await saved.json()).memory);
  }
  expect(
    (
      await mutate(
        page,
        `/memories/${notes[0].id}`,
        {
          requestId: randomUUID(),
          revision: 1,
          content: 'The customer now prefers a detailed proposal.',
          provenance: { kind: 'human_note' },
        },
        'PUT',
      )
    ).ok(),
  ).toBe(true);
  expect(
    (
      await mutate(page, `/work-items/${item.id}/decisions`, {
        requestId: randomUUID(),
        revision: item.revision,
        kind: 'cancel',
        reason:
          'The current matter was cancelled after this historical assessment.',
      })
    ).ok(),
  ).toBe(true);
  const frozenWork = { ...item, recentDecisions: item.decisions };
  const evaluation: Evaluation = {
    id: 'fixture-frozen-context',
    status: 'observed',
    policy: {
      id: 'historical-policy',
      roomId: room.id,
      sourceId: 'room',
      revision: 1,
      ownerUserId: item.ownerUserId,
      enabled: true,
      mode: 'observe',
      rules: 'Assess the proposal request.',
      allowedActorIds: [item.ownerUserId],
      participantAgentIds: ['historical-agent'],
      firstAgentId: 'historical-agent',
      maxTurns: 6,
      maxModelRequests: 24,
      dailyEvaluations: 12,
      dailyTasks: 4,
      allowAnswers: false,
      allowComments: false,
      credentialConfigured: false,
      startAfter: 0,
    },
    events: [],
    tasks: [],
    totalTasks: 0,
    tasksOmitted: false,
    workItems: [frozenWork],
    totalWorkItems: 1,
    workItemsOmitted: false,
    memories: [notes[0]],
    totalMemories: 2,
    memoriesOmitted: true,
    modelRevision: 'historical-model',
    assessment: {
      outcome: 'wait',
      reason: 'Wait for the owner.',
      workItemId: item.id,
      waitCondition: 'The owner provides acceptance.',
    },
    createdAt: item.createdAt,
  };
  await page.route(
    '**/api/automation-evaluations/fixture-frozen-context',
    (route) => route.fulfill({ json: evaluation }),
  );
  await page.route(
    '**/api/automation-evaluations/fixture-frozen-context/context/*?*',
    (route) => {
      const url = new URL(route.request().url());
      const after = Number(url.searchParams.get('after'));
      if (url.pathname.endsWith('/work-items'))
        return route.fulfill({
          json: { items: [frozenWork], nextAfter: 1, hasMore: false, total: 1 },
        });
      return route.fulfill({
        json: {
          items: [notes[after]],
          nextAfter: after + 1,
          hasMore: after === 0,
          total: 2,
        },
      });
    },
  );
  await page.route(`**/api/rooms/${room.id}/automation-events?*`, (route) => {
    const page: SourceEventPage = {
      items: [
        {
          seq: 1,
          id: 'fixture-context-event',
          roomId: room.id,
          sourceId: 'room',
          threadKey: `${room.id}:room`,
          kind: 'room.message',
          actorId: item.ownerUserId,
          actorLogin: 'Owner',
          actorKind: 'user',
          content: 'Please prepare the customer proposal.',
          preview: 'Please prepare the customer proposal.',
          occurredAt: item.createdAt,
          status: 'observed',
          evaluationId: evaluation.id,
        },
      ],
      through: 1,
      nextAfter: null,
      nextBefore: null,
      hasMore: false,
    };
    return route.fulfill({ json: page });
  });
  await openRoomMore(page, 'Activity…');
  const record = automation(page)
    .locator('.automation-event-list > li')
    .filter({ hasText: 'Please prepare the customer proposal.' });
  await record.getByText('Details', { exact: true }).click();
  await record.getByText('Assessment decision', { exact: true }).click();
  await record.getByText('Work items assessed', { exact: true }).click();
  await expect(
    record.getByText(`Open · Revision ${item.revision}`, { exact: true }),
  ).toBeVisible();
  await record.getByText('Memory assessed', { exact: true }).click();
  await expect(
    record.getByText(notes[0].content, { exact: true }),
  ).toBeVisible();
  await expect(
    record.getByText('The customer now prefers a detailed proposal.', {
      exact: true,
    }),
  ).toHaveCount(0);
  await record
    .getByRole('button', { name: 'More Context', exact: true })
    .click();
  await expect(
    record.getByText(notes[1].content, { exact: true }),
  ).toBeVisible();
  await record
    .getByRole('button', { name: 'Open Current Work Item', exact: true })
    .click();
  await expect(
    detail(page).getByText('Cancelled', { exact: true }),
  ).toBeVisible();
  expect(errors.errors).toEqual([]);
});
