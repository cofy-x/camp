import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { WorkItem } from '../apps/dashboard/src/features/workitems/api';
import { watchPageErrors } from './page-errors';
import {
  createRoom,
  mutate,
  openApp,
  openMessageAction,
  openInboxActivity,
  settings,
  signIn,
  work,
  openWorkAction,
  workspace,
} from './team-helpers';

const detail = (page: Page) =>
  work(page).getByRole('region', { name: 'Work item detail', exact: true });
const roomFilter = (page: Page) =>
  work(page).getByRole('combobox', { name: 'Room', exact: true });
const viewFilter = (page: Page) =>
  work(page).getByRole('combobox', { name: 'View', exact: true });

async function createMatter(page: Page, roomId: string, goal: string) {
  const response = await mutate(page, `/rooms/${roomId}/work-items`, {
    requestId: randomUUID(),
    goal,
  });
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { item: WorkItem }).item;
}

async function newMatterInRoom(page: Page, roomId: string, global = false) {
  await openApp(page, 'Work');
  await roomFilter(page).selectOption(global ? '' : roomId);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  if (global && roomId) await roomFilter(page).selectOption(roomId);
  await expect(
    work(page).getByRole('form', { name: 'New work', exact: true }),
  ).toBeVisible();
}

test('New Work separates model setup from recording and keeps its draft on return', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Record without a model ${randomUUID().slice(0, 8)}`,
  );
  await page.route('**/api/workspace/status', (route) =>
    route.fulfill({
      json: {
        available: false,
        reason: 'Configure a model connection in Settings.',
        budget: { maxTokens: 4096, maxSteps: 8, timeoutSeconds: 180 },
        maxConcurrent: 2,
      },
    }),
  );
  await newMatterInRoom(page, room.id);
  const form = work(page).getByRole('form', { name: 'New work', exact: true });
  const goal = form.getByLabel('What needs to happen?', { exact: true });
  const title = 'Confirm the editorial handoff';
  await goal.fill(title);
  const start = form.getByRole('button', { name: 'Start work', exact: true });
  const record = form.getByRole('button', { name: 'Keep track', exact: true });
  await expect(start).toBeDisabled();
  await expect(start).toHaveAccessibleDescription(/Start work unavailable/);
  await expect(record).toBeEnabled();
  await expect(record).toHaveAccessibleDescription(
    'Records this without running Agents.',
  );
  await form
    .getByRole('button', { name: 'Open Getting Ready', exact: true })
    .click();
  await expect(settings(page).locator('.p9-window-title')).toHaveText(
    'Getting Ready — Settings',
  );
  await openApp(page, 'Work');
  await expect(goal).toHaveValue(title);
  const submitted = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === `/api/rooms/${room.id}/work-items`,
  );
  await record.click();
  const response = await submitted;
  expect(response.ok()).toBe(true);
  expect(response.request().postDataJSON().start).toBe(false);
  await expect(detail(page)).toContainText(title);
});

test('recording opens the confirmed Work while its detail refreshes', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Confirmed Work ${randomUUID().slice(0, 8)}`,
  );
  const goal = 'Record the editorial handoff';
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let responseStatus = 503;
  await page.route('**/api/work-items/*', async (route) => {
    await held;
    return route.fulfill({
      status: responseStatus,
      json: {
        error: responseStatus === 503 ? 'Detail unavailable' : 'Access revoked',
      },
    });
  });
  try {
    await newMatterInRoom(page, room.id);
    const win = work(page);
    await win.getByLabel('What needs to happen?', { exact: true }).fill(goal);
    const firstRead = page.waitForRequest('**/api/work-items/*');
    await win.getByRole('button', { name: 'Keep track', exact: true }).click();
    await firstRead;
    await expect(detail(page)).toContainText(goal);
    await expect(detail(page)).not.toContainText('Loading work…');
    await win.screenshot({
      path: info.outputPath('work-recorded-receipt.png'),
    });

    release();
    await expect(detail(page)).toContainText('Detail unavailable');
    await expect(detail(page)).toContainText(goal);

    responseStatus = 403;
    await detail(page)
      .getByRole('button', { name: 'Retry work', exact: true })
      .click();
    await expect(detail(page)).toContainText('Access revoked');
    await expect(detail(page)).not.toContainText(goal);
  } finally {
    release();
  }
});

test('starting opens queued progress from the confirmed receipt before reading detail', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Queued Work ${randomUUID().slice(0, 8)}`,
  );
  const goal = 'Prepare the draft for the editorial handoff';
  const recorded = await createMatter(page, room.id, goal);
  const taskId = randomUUID();
  const runId = randomUUID();
  const item: WorkItem = {
    ...recorded,
    status: 'running',
    revision: recorded.revision + 1,
    activeTaskId: taskId,
    execution: {
      taskId,
      status: 'queued',
      revision: 1,
      summary: '',
      error: '',
      currentAgentName: 'Assistant',
      activeRunId: runId,
      updatedAt: new Date().toISOString(),
    },
  };
  let reads = 0;
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/workspace/status', (route) =>
    route.fulfill({ json: { available: true } }),
  );
  await page.route(`**/api/rooms/${room.id}/work-execution?*`, (route) =>
    route.fulfill({ json: {} }),
  );
  let posts = 0;
  await page.route(`**/api/rooms/${room.id}/work-items`, (route) => {
    expect(route.request().postDataJSON()).toMatchObject({ goal, start: true });
    posts++;
    return route.fulfill({ status: 201, json: { item } });
  });
  await page.route(
    `**/api/work-items/${item.id}?view=current`,
    async (route) => {
      reads++;
      await held;
      return route.fulfill({
        json: {
          item: {
            ...item,
            execution: { ...item.execution, revision: 2, status: 'running' },
          },
        },
      });
    },
  );
  try {
    await newMatterInRoom(page, room.id);
    const win = work(page);
    await win.getByLabel('What needs to happen?', { exact: true }).fill(goal);
    await win.getByRole('button', { name: 'Start work', exact: true }).click();
    await expect.poll(() => reads).toBeGreaterThan(0);
    await expect(detail(page)).toContainText(goal);
    await expect(detail(page)).toContainText(/Queued · Assistant/);
    await expect(
      detail(page).getByRole('button', { name: 'View progress', exact: true }),
    ).toBeVisible();
    await win.screenshot({
      path: info.outputPath('work-start-queued-receipt.png'),
    });

    release();
    await expect(detail(page)).toContainText('Working · Assistant');
    expect(posts).toBe(1);
  } finally {
    release();
  }
});

for (const recover of [false, true]) {
  test(`a global Work receipt preserves another Room's draft ${recover ? 'during explicit recovery' : 'on a delayed reply'}`, async ({
    page,
  }) => {
    await signIn(page);
    const stamp = randomUUID().slice(0, 8);
    const original = await createRoom(page, `Originating Work ${stamp}`);
    const selected = await createRoom(page, `Another Work Room ${stamp}`);
    const goal = 'Prepare the Room handoff';
    const win = work(page);
    await newMatterInRoom(page, selected.id, true);
    await win.getByText('Details', { exact: true }).click();
    await win.getByLabel('Due date', { exact: true }).fill('2030-03-05');
    await roomFilter(page).selectOption(original.id);
    await win.getByLabel('What needs to happen?', { exact: true }).fill(goal);

    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let committed!: () => void;
    const accepted = new Promise<void>((resolve) => {
      committed = resolve;
    });
    await page.route(
      `**/api/rooms/${original.id}/work-items`,
      async (route) => {
        const response = await route.fetch();
        expect(response.ok()).toBeTruthy();
        committed();
        await held;
        return recover ? route.abort('failed') : route.fulfill({ response });
      },
    );

    const record = win.getByRole('button', { name: 'Keep track', exact: true });
    try {
      await record.click();
      await accepted;
      await roomFilter(page).selectOption(selected.id);
      await expect(roomFilter(page)).toHaveValue(selected.id);
      release();
      if (recover) {
        await win
          .getByRole('button', { name: 'Check status', exact: true })
          .click();
        await expect(win.locator('.p9-window-title')).toHaveText(
          `${original.name} — Work`,
        );
        await expect(detail(page)).toContainText(goal);
      } else {
        await expect(record).toBeEnabled();
        await expect(roomFilter(page)).toHaveValue(selected.id);
        await expect(
          win.getByLabel('What needs to happen?', { exact: true }),
        ).toHaveValue(goal);
      }
      const key = encodeURIComponent('work-item-create::manual');
      const response = await page.request.get(
        `/api/me/drafts/conversation?key=${key}`,
      );
      expect(response.ok()).toBeTruthy();
      const { draft } = await response.json();
      expect(draft.value).toMatchObject({
        goal,
        rooms: { [selected.id]: { dueAt: '2030-03-05' } },
      });
      expect(draft.value.rooms).not.toHaveProperty(original.id);
      const cleanup = await mutate(
        page,
        '/me/drafts/conversation',
        { key: 'work-item-create::manual', revision: draft.revision },
        'DELETE',
      );
      expect(cleanup.ok()).toBeTruthy();
    } finally {
      release();
    }
  });
}

test('Work prioritizes attention across Rooms and Room and Inbox shortcuts reuse its single window', async ({
  page,
  browserName,
}, info) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const first = await createRoom(
    page,
    `Work intake ${randomUUID().slice(0, 8)}`,
  );
  const second = await createRoom(
    page,
    `Work delivery ${randomUUID().slice(0, 8)}`,
  );
  const needsMe = await createMatter(page, first.id, 'Confirm the launch date');
  const question = 'Which launch date should we use?';
  const asked = await mutate(page, `/work-items/${needsMe.id}/decisions`, {
    requestId: randomUUID(),
    revision: needsMe.revision,
    kind: 'request_judgment',
    reason: 'The launch date has two viable options.',
    question,
  });
  expect(asked.ok()).toBe(true);
  const active = await createMatter(
    page,
    second.id,
    'Prepare the launch checklist',
  );
  const completed = await createMatter(
    page,
    first.id,
    'Archive the approved launch plan',
  );
  const closed = await mutate(
    page,
    `/work-items/${completed.id}/actions/complete`,
    {
      requestId: randomUUID(),
      revision: completed.revision,
      comment: 'The approved plan has been archived.',
    },
  );
  expect(closed.ok()).toBe(true);

  const queries: URLSearchParams[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname === '/api/work-items') queries.push(url.searchParams);
  });
  await openApp(page, 'Work');
  await expect(work(page).locator('.p9-window-title')).toHaveText('Work');
  await expect(viewFilter(page)).toHaveValue('mine');
  await expect(roomFilter(page)).toHaveValue('');
  const rows = work(page).locator('.work-item-list > li');
  await expect(rows.first()).toContainText(needsMe.goal);
  await expect(rows.first()).toContainText(first.name);
  await expect(rows.first()).toContainText(question);
  expect(queries.at(-1)?.get('scope')).toBe('mine');
  expect(queries.at(-1)?.get('view')).toBe('active');

  await viewFilter(page).selectOption({ label: 'Needs me' });
  const attentionRows = work(page)
    .getByRole('list', { name: 'Needs me', exact: true })
    .locator(':scope > li');
  await expect(
    attentionRows
      .filter({ hasText: needsMe.goal })
      .filter({ hasText: first.name }),
  ).toContainText(question);
  await expect(
    attentionRows
      .filter({ hasText: active.goal })
      .filter({ hasText: second.name }),
  ).toHaveCount(0);
  await viewFilter(page).selectOption({ label: 'Completed' });
  await expect(rows.first()).toContainText(completed.goal);
  expect(queries.at(-1)?.get('view')).toBe('done');
  await viewFilter(page).selectOption({ label: 'All work' });
  await roomFilter(page).selectOption(second.id);
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText(active.goal);
  expect(queries.at(-1)?.get('scope')).toBe('team');
  expect(queries.at(-1)?.get('view')).toBe('all');
  expect(queries.at(-1)?.get('roomId')).toBe(second.id);
  await work(page).screenshot({
    path: info.outputPath('work-room-filter.png'),
  });

  await openApp(page, 'Workspace');
  const shortcut = workspace(page).getByRole('button', {
    name: 'Work',
    exact: true,
  });
  await expect(shortcut).not.toHaveAttribute('aria-pressed');
  await shortcut.click();
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(work(page)).toHaveCount(1);
  await expect(roomFilter(page)).toHaveValue(second.id);
  await expect(viewFilter(page)).toHaveValue('all');
  await rows.getByRole('button').filter({ hasText: active.goal }).click();
  await expect(detail(page)).toContainText(active.goal);
  await openWorkAction(page, 'Open Room');
  await expect(workspace(page)).toHaveClass(/is-active/);
  await expect(
    workspace(page).getByLabel(`Message ${second.name}`, { exact: true }),
  ).toBeVisible();

  await expect
    .poll(async () => {
      const { items } = await (await page.request.get('/api/me/inbox')).json();
      return items.some(
        (item: { workItemId?: string }) => item.workItemId === needsMe.id,
      );
    })
    .toBe(true);
  const inbox = await openInboxActivity(page);
  const notice = inbox
    .getByRole('button')
    .filter({ hasText: needsMe.goal })
    .filter({ hasText: first.name });
  await expect(notice).toContainText(question);
  await notice.click();
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(work(page)).toHaveCount(1);
  await expect(detail(page)).toContainText(needsMe.goal);
  await expect(detail(page)).toContainText(question);
  expect(errors.errors).toEqual([]);
});

test('Work list aligns state beside short titles across desktop and narrow views', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(page, `Work list ${randomUUID().slice(0, 8)}`);
  const first = await createMatter(
    page,
    room.id,
    '# Prepare a supplier packet\n\nInclude all requirements and keep the supporting notes alongside the original outline.',
  );
  const second = await createMatter(
    page,
    room.id,
    'Check the delivery contact',
  );
  const question = 'Which contact should receive the confirmed delivery?';
  await page.route('**/api/work-items?*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            ...first,
            roomName: room.name,
            dueAt: '2026-12-03',
            needsYou: false,
          },
          {
            ...second,
            roomName: room.name,
            status: 'waiting_for_person',
            currentDecision: { kind: 'judgment', text: question },
            needsYou: true,
          },
        ],
        nextCursor: '',
        hasMore: false,
      },
    }),
  );
  await openApp(page, 'Work');
  const window = work(page);
  const list = window.locator('.work-list');
  const rows = list.getByRole('button');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toHaveAccessibleName(
    'Prepare a supplier packet Open',
  );
  await expect(rows.first()).toHaveAccessibleDescription(
    `${room.name} · Due 2026-12-03`,
  );
  await expect(rows.first()).not.toContainText('Include all requirements');
  await expect(rows.nth(1)).toHaveAccessibleName(
    `Check the delivery contact Needs your judgment ${question}`,
  );

  for (const [scene, width, height, frameWidth] of [
    ['757', 765, 900, 757],
    ['390', 390, 844, 382],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect
      .poll(async () => (await window.boundingBox())?.width)
      .toBe(frameWidth);
    const titles = await list.locator('.work-item-row-title').all();
    const statuses = await list.locator('.work-list-status').all();
    const rightEdges: number[] = [];
    for (const [index, status] of statuses.entries()) {
      const title = (await titles[index].boundingBox())!;
      const bounds = (await status.boundingBox())!;
      expect(bounds.x).toBeGreaterThanOrEqual(title.x + title.width);
      expect(Math.abs(bounds.y - title.y)).toBeLessThanOrEqual(1);
      rightEdges.push(bounds.x + bounds.width);
    }
    expect(
      Math.max(...rightEdges) - Math.min(...rightEdges),
    ).toBeLessThanOrEqual(1);
    expect(
      await list.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
    ).toBe(true);
    await window.screenshot({
      path: info.outputPath(`work-list-${scene}.png`),
    });
  }
  await rows.first().press('Enter');
  await expect(detail(page)).toBeVisible();
});

test('Work keeps a paged frontier during background updates and explicitly refreshes to newest work', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(page, `Work pages ${randomUUID().slice(0, 8)}`);
  const goals: string[] = [];
  for (let index = 1; index <= 21; index++) {
    const goal = `Recorded matter ${String(index).padStart(2, '0')}`;
    await createMatter(page, room.id, goal);
    goals.push(goal);
  }
  const cursors: string[] = [];
  page.on('response', (response) => {
    const url = new URL(response.url());
    if (
      url.pathname === '/api/work-items' &&
      url.searchParams.get('roomId') === room.id &&
      url.searchParams.get('scope') === 'mine' &&
      url.searchParams.get('view') === 'active' &&
      response.ok()
    )
      cursors.push(url.searchParams.get('cursor') || '');
  });
  await openApp(page, 'Work');
  await expect(viewFilter(page)).toHaveValue('mine');
  await roomFilter(page).selectOption(room.id);
  const titles = work(page).locator('.work-item-row-title');
  await expect(titles).toHaveText(goals.slice(1).reverse());
  const previous = work(page).getByRole('button', {
    name: 'Previous work',
    exact: true,
  });
  await expect(previous).toHaveCount(0);
  await work(page)
    .getByRole('button', { name: 'More work', exact: true })
    .click();
  await expect(titles).toHaveText([goals[0]]);
  await expect(previous).toBeVisible();
  const cursor = cursors.at(-1)!;
  expect(cursor).not.toBe('');
  const backgroundReads = cursors.filter((value) => value === cursor).length;
  const newest = 'Recorded after the page frontier';
  await createMatter(page, room.id, newest);
  await expect
    .poll(() => cursors.filter((value) => value === cursor).length)
    .toBeGreaterThan(backgroundReads);
  await expect(titles).toHaveText([goals[0]]);
  await expect(previous).toBeVisible();
  expect(cursors.at(-1)).toBe(cursor);

  await openWorkAction(page, 'Refresh');
  await expect(titles).toHaveText([newest, ...goals.slice(2).reverse()]);
  await expect(previous).toHaveCount(0);
  expect(cursors.at(-1)).toBe('');
  expect(errors.errors).toEqual([]);
});

test('message follow-up opens Work with exact source context and returns to the Room draft', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Work source ${randomUUID().slice(0, 8)}`,
  );
  const content = 'Follow the customer acceptance of release 4.2.';
  const posted = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content,
    postOnly: true,
  });
  expect(posted.ok()).toBe(true);
  const { message } = await posted.json();
  const input = workspace(page).getByLabel(`Message ${room.name}`, {
    exact: true,
  });
  const unsent = 'Keep this Room note while I track the acceptance.';
  await input.fill(unsent);
  const source = workspace(page).locator(`[data-message-id="${message.id}"]`);
  await openMessageAction(page, source, 'Follow up…');
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(work(page).locator('.p9-window-title')).toHaveText('New Work');
  await expect(roomFilter(page)).toHaveValue(room.id);
  await expect(roomFilter(page)).toBeDisabled();
  await expect(
    work(page).getByLabel('What needs to happen?', { exact: true }),
  ).toHaveValue(content);
  await expect(
    work(page).getByRole('checkbox', { name: 'Follow replies', exact: true }),
  ).toBeChecked();
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  await expect(detail(page)).toContainText(content);
  const { items } = await (
    await page.request.get(`/api/rooms/${room.id}/work-items`)
  ).json();
  const { item } = (await (
    await page.request.get(`/api/work-items/${items[0].id}`)
  ).json()) as { item: WorkItem };
  expect(item.tasks).toHaveLength(0);
  expect(item.evidence).toEqual([
    expect.objectContaining({ eventId: `room:${message.id}` }),
  ]);
  expect(item.watches).toEqual([
    expect.objectContaining({
      threadKey: `${room.id}:${message.id}`,
      enabled: true,
    }),
  ]);
  await openWorkAction(page, 'Open Room');
  await expect(workspace(page)).toHaveClass(/is-active/);
  await expect(input).toHaveValue(unsent);
  await expect(source).toContainText(content);
  expect(errors.errors).toEqual([]);
});

test('a failed private creation draft blocks changing Rooms until explicitly recovered', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const first = await createRoom(
    page,
    `Draft origin ${randomUUID().slice(0, 8)}`,
  );
  const second = await createRoom(
    page,
    `Draft other ${randomUUID().slice(0, 8)}`,
  );
  await openApp(page, 'Work');
  await roomFilter(page).selectOption(first.id);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await expect(roomFilter(page)).toHaveValue(first.id);
  const draft = 'Keep this first Room goal until private storage recovers.';
  let fail = true;
  let creates = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/api\/rooms\/[^/]+\/work-items$/.test(new URL(request.url()).pathname)
    )
      creates++;
  });
  await page.route('**/api/me/drafts/conversation', (route) => {
    const request = route.request();
    if (
      fail &&
      request.method() === 'PUT' &&
      request.postDataJSON().key === `work-item-create:${first.id}:manual`
    ) {
      return route.fulfill({
        status: 503,
        json: { error: 'The Work draft could not be saved.' },
      });
    }
    return route.fallback();
  });
  const goal = work(page).getByLabel('What needs to happen?', { exact: true });
  await goal.fill(draft);
  await expect(
    work(page)
      .getByRole('alert')
      .filter({ hasText: 'The Work draft could not be saved.' }),
  ).toBeVisible();
  await roomFilter(page).selectOption(second.id);
  await expect(roomFilter(page)).toHaveValue(first.id);
  await expect(goal).toHaveValue(draft);
  await expect(
    work(page)
      .getByRole('alert')
      .filter({ hasText: 'Resolve its error before leaving.' }),
  ).toBeVisible();
  expect(creates).toBe(0);

  fail = false;
  await work(page)
    .getByRole('button', { name: 'Compare Private Drafts', exact: true })
    .click();
  await work(page)
    .getByRole('button', { name: 'Save My Draft', exact: true })
    .click();
  await expect(
    work(page)
      .getByRole('alert')
      .filter({ hasText: 'The Work draft could not be saved.' }),
  ).toHaveCount(0);
  await roomFilter(page).selectOption(second.id);
  await expect(roomFilter(page)).toHaveValue(second.id);
  await expect(goal).toHaveValue('');
  await roomFilter(page).selectOption(first.id);
  await expect(goal).toHaveValue(draft);
  expect(creates).toBe(0);
  expect(errors.errors).toEqual([]);
});

test('recording work waits for private draft deletion before leaving and reopens a blank form after reload', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Clear work draft ${randomUUID().slice(0, 8)}`,
  );
  await newMatterInRoom(page, room.id);
  const draftKey = `work-item-create:${room.id}:manual`;
  const draftUrl = `/api/me/drafts/conversation?key=${encodeURIComponent(draftKey)}`;
  const goal = 'Record this goal once and clear its private creation draft.';
  const input = work(page).getByLabel('What needs to happen?', { exact: true });
  await input.fill(goal);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(draftUrl)).json()).draft.value?.goal,
    )
    .toBe(goal);
  let creates = 0;
  let requestId = '';
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === `/api/rooms/${room.id}/work-items`
    ) {
      creates++;
      requestId = request.postDataJSON().requestId;
    }
  });
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let deleting = false;
  await page.route('**/api/me/drafts/conversation', async (route) => {
    if (
      route.request().method() !== 'DELETE' ||
      route.request().postDataJSON().key !== draftKey
    )
      return route.fallback();
    deleting = true;
    await held;
    return route.continue();
  });
  try {
    await work(page)
      .getByRole('button', { name: 'Keep track', exact: true })
      .click();
    await expect.poll(() => deleting).toBe(true);
    await expect(input).toBeVisible();
    await expect(input).toBeDisabled();
    await expect(
      work(page).getByRole('button', { name: 'Keep track', exact: true }),
    ).toBeDisabled();
    await expect(detail(page)).toHaveCount(0);
    expect(creates).toBe(1);
    const receipt = await page.request.get(
      `/api/work-item-requests/${requestId}`,
    );
    expect(receipt.ok()).toBe(true);
    const { item } = (await receipt.json()) as { item: WorkItem };
    expect(item.goal).toBe(goal);
    expect(item.tasks).toHaveLength(0);
    release();
    await expect(detail(page)).toContainText(goal);
    await expect
      .poll(
        async () =>
          (await (await page.request.get(draftUrl)).json()).draft.value,
      )
      .toBeNull();
    await errors.reload();
    await newMatterInRoom(page, room.id);
    await expect(input).toHaveValue('');
    await expect(input).toBeEditable();
    const { items } = await (
      await page.request.get(`/api/rooms/${room.id}/work-items`)
    ).json();
    expect(items.map((value: WorkItem) => value.id)).toEqual([item.id]);
    expect(creates).toBe(1);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
  }
});

for (const global of [false, true]) {
  test(`failed ${global ? 'global' : 'Room'} creation draft cleanup retains the exact request across reload and recovers the committed work without another POST`, async ({
    page,
    browserName,
  }) => {
    const errors = watchPageErrors(page, browserName);
    await signIn(page);
    const room = await createRoom(
      page,
      `Recover work draft ${randomUUID().slice(0, 8)}`,
    );
    await newMatterInRoom(page, room.id, global);
    const { user } = await (await page.request.get('/api/auth/status')).json();
    const draftRoom = global ? '' : room.id;
    const draftKey = `work-item-create:${draftRoom}:manual`;
    const receiptKey = `camp:receipt:${user.id}:work-item-operation:create:${draftRoom}:manual`;
    const draftUrl = `/api/me/drafts/conversation?key=${encodeURIComponent(draftKey)}`;
    const goal = 'Recover this recorded work rather than creating a duplicate.';
    const input = work(page).getByLabel('What needs to happen?', {
      exact: true,
    });
    await input.fill(goal);
    await expect
      .poll(
        async () =>
          (await (await page.request.get(draftUrl)).json()).draft.value?.goal,
      )
      .toBe(goal);
    let creates = 0;
    let body: { requestId: string; goal: string } | undefined;
    page.on('request', (request) => {
      if (
        request.method() === 'POST' &&
        new URL(request.url()).pathname === `/api/rooms/${room.id}/work-items`
      ) {
        creates++;
        body = request.postDataJSON();
      }
    });
    let fail = true;
    await page.route('**/api/me/drafts/conversation', (route) => {
      if (
        fail &&
        route.request().method() === 'DELETE' &&
        route.request().postDataJSON().key === draftKey
      ) {
        return route.fulfill({
          status: 503,
          json: { error: 'The recorded Work draft could not be cleared.' },
        });
      }
      return route.fallback();
    });
    await work(page)
      .getByRole('button', { name: 'Keep track', exact: true })
      .click();
    await expect(
      work(page)
        .getByRole('alert')
        .filter({ hasText: 'The recorded Work draft could not be cleared.' }),
    ).toBeVisible();
    await expect(input).toBeDisabled();
    await expect(detail(page)).toHaveCount(0);
    expect(creates).toBe(1);
    const retained = await page.evaluate(
      (key) => sessionStorage.getItem(key),
      receiptKey,
    );
    expect(retained).not.toBeNull();
    expect(JSON.parse(retained!).value.body).toEqual(body);
    const committed = await page.request.get(
      `/api/work-item-requests/${body!.requestId}`,
    );
    expect(committed.ok()).toBe(true);
    const { item } = (await committed.json()) as { item: WorkItem };
    expect(item.goal).toBe(goal);
    page.once('dialog', (dialog) => dialog.accept());
    await errors.reload();
    await newMatterInRoom(page, global ? '' : room.id, global);
    if (global) await expect(roomFilter(page)).toHaveValue('');
    expect(
      await page.evaluate((key) => sessionStorage.getItem(key), receiptKey),
    ).toBe(retained);
    await expect(
      work(page).getByRole('button', { name: 'Check status', exact: true }),
    ).toBeEnabled();
    await expect(input).toBeDisabled();
    await expect(
      work(page)
        .getByRole('alert')
        .filter({ hasText: 'The recorded Work draft could not be cleared.' }),
    ).toBeVisible();
    fail = false;
    await work(page)
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await work(page)
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await (await page.request.get(draftUrl)).json()).draft.value,
      )
      .toBeNull();
    await work(page)
      .getByRole('button', { name: 'Check status', exact: true })
      .click();
    await expect(detail(page)).toContainText(goal);
    expect(
      await page.evaluate((key) => sessionStorage.getItem(key), receiptKey),
    ).toBeNull();
    expect(creates).toBe(1);
    await errors.reload();
    await newMatterInRoom(page, room.id, global);
    await expect(input).toHaveValue('');
    await expect(input).toBeEditable();
    const { items } = await (
      await page.request.get(`/api/rooms/${room.id}/work-items`)
    ).json();
    expect(items.map((value: WorkItem) => value.id)).toEqual([item.id]);
    expect(creates).toBe(1);
    expect(errors.errors).toEqual([]);
  });
}

test('confirming recorded work preserves a different private draft explicitly selected during cleanup recovery', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Keep newer work draft ${randomUUID().slice(0, 8)}`,
  );
  await newMatterInRoom(page, room.id);
  const draftKey = `work-item-create:${room.id}:manual`;
  const draftUrl = `/api/me/drafts/conversation?key=${encodeURIComponent(draftKey)}`;
  const goal = 'This original goal has already been recorded.';
  const input = work(page).getByLabel('What needs to happen?', { exact: true });
  await input.fill(goal);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(draftUrl)).json()).draft.value?.goal,
    )
    .toBe(goal);
  let creates = 0;
  let requestId = '';
  let deletes = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === `/api/rooms/${room.id}/work-items`
    ) {
      creates++;
      requestId = request.postDataJSON().requestId;
    }
  });
  await page.route('**/api/me/drafts/conversation', (route) => {
    if (
      route.request().method() === 'DELETE' &&
      route.request().postDataJSON().key === draftKey
    ) {
      deletes++;
      return route.fulfill({
        status: 503,
        json: { error: 'Compare the newer draft before clearing anything.' },
      });
    }
    return route.fallback();
  });
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  await expect(
    work(page)
      .getByRole('alert')
      .filter({ hasText: 'Compare the newer draft before clearing anything.' }),
  ).toBeVisible();
  expect(creates).toBe(1);
  const { item } = (await (
    await page.request.get(`/api/work-item-requests/${requestId}`)
  ).json()) as { item: WorkItem };
  expect(item.goal).toBe(goal);
  const { draft } = await (await page.request.get(draftUrl)).json();
  const newer = {
    goal: 'Keep this different goal for a future matter.',
    completionCriteria: 'A separate deliverable is reviewed.',
  };
  const changed = await mutate(
    page,
    '/me/drafts/conversation',
    {
      key: draftKey,
      revision: draft.revision,
      value: newer,
    },
    'PUT',
  );
  expect(changed.ok()).toBe(true);
  const { draft: chosen } = await changed.json();
  await work(page)
    .getByRole('button', { name: 'Compare Private Drafts', exact: true })
    .click();
  await expect(work(page).locator('.camp-draft-comparison')).toContainText(
    newer.goal,
  );
  await work(page)
    .getByRole('button', { name: 'Use Other Draft', exact: true })
    .click();
  await expect(input).toHaveValue(newer.goal);
  await expect(input).toBeDisabled();
  const failedDeletes = deletes;
  await work(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  await expect(detail(page)).toContainText(goal);
  expect(creates).toBe(1);
  expect(deletes).toBe(failedDeletes);
  const { draft: preserved } = await (await page.request.get(draftUrl)).json();
  expect(preserved.revision).toBe(chosen.revision);
  expect(preserved.value).toEqual(newer);
  await detail(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await newMatterInRoom(page, room.id);
  await expect(input).toHaveValue(newer.goal);
  await expect(input).toBeEditable();
  const { items } = await (
    await page.request.get(`/api/rooms/${room.id}/work-items`)
  ).json();
  expect(items.map((value: WorkItem) => value.id)).toEqual([item.id]);
  expect(creates).toBe(1);
  expect(errors.errors).toEqual([]);
});

test('a saved desktop gains an accessible Work icon without moving or covering existing icons', async ({
  page,
  browserName,
}, info) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const size = page.viewportSize()!;
  const oldIds = ['workspace', 'library', 'agents', 'runners', 'settings'];
  const icons = Object.fromEntries(
    oldIds.map((id, index) => [
      `system:${id}`,
      { x: size.width - 100, y: 54 + index * 74 },
    ]),
  );
  icons['system:agents'] = { x: 20, y: 130 };
  await page.route('**/api/bootstrap?clientId=*', async (route) => {
    const response = await route.fetch();
    const bootstrap = await response.json();
    return route.fulfill({
      json: {
        ...bootstrap,
        layout: {
          version: 1,
          windows: [],
          activeWindowId: null,
          icons,
        },
      },
    });
  });
  await errors.reload();
  const desktop = page.getByRole('group', {
    name: 'Desktop icons',
    exact: true,
  });
  const workIcon = desktop.getByRole('button', { name: 'Work', exact: true });
  await expect(workIcon).toBeVisible();
  const workBounds = (await workIcon.boundingBox())!;
  for (const id of oldIds) {
    const icon = desktop.locator(`[data-icon-id="system:${id}"]`);
    await expect(icon).toHaveCSS('left', `${icons[`system:${id}`].x}px`);
    await expect(icon).toHaveCSS('top', `${icons[`system:${id}`].y}px`);
    const saved = (await icon.boundingBox())!;
    expect(
      workBounds.x < saved.x + saved.width &&
        workBounds.x + workBounds.width > saved.x &&
        workBounds.y < saved.y + saved.height &&
        workBounds.y + workBounds.height > saved.y,
    ).toBe(false);
  }
  await page.screenshot({ path: info.outputPath('work-saved-desktop.png') });
  await workIcon.dblclick();
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(
    work(page).getByRole('region', { name: 'Work items', exact: true }),
  ).toBeVisible();
  expect(errors.errors).toEqual([]);
});
