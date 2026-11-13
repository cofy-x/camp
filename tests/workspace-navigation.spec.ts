import { expect, test, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import {
  createRoom,
  mutate,
  openApp,
  openMessageAction,
  openInboxActivity,
  openRoomMore,
  post,
  settings,
  signIn,
  work,
  workspace,
} from './team-helpers';
import type { Message } from '../apps/dashboard/src/features/workspace/api';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

test('Room daily navigation opens work directly and remembers a message with exact provenance', async ({
  page,
}, info) => {
  await page.route('**/api/workspace/status', async (route) => {
    const response = await route.fetch();
    return route.fulfill({
      response,
      json: {
        ...(await response.json()),
        available: false,
        reason: 'Server-owned execution setup details.',
      },
    });
  });
  await errors.get(page)!.reload();
  const room = await createRoom(page, `Daily work ${info.project.name}`);
  const win = workspace(page);
  const unavailable = win.locator('.workspace-unavailable');
  await expect(
    unavailable.getByText('Agent work unavailable', { exact: true }),
  ).toBeVisible();
  await expect(unavailable.locator('summary')).toHaveCount(0);
  await expect(unavailable).not.toContainText(
    'Server-owned execution setup details.',
  );
  const actions = win.locator('.workspace-toolbar-actions');
  await expect(actions.getByRole('button')).toHaveText([
    'Discussion',
    'Work',
    'Files',
    'More',
  ]);
  await expect(
    actions.getByRole('button', { name: 'Discussion', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true');
  const original =
    'Use the customer-facing title when publishing the weekly summary.';
  await post(page, room.name, original);
  const message = win
    .locator('.workspace-message.author-user')
    .filter({ hasText: original });
  const messageId = (await message.getAttribute('data-message-id'))!;
  const draft = page.getByLabel(`Message ${room.name}`, { exact: true });
  await draft.fill('Keep this unfinished discussion draft.');
  await unavailable
    .getByRole('button', { name: 'Set up…', exact: true })
    .click();
  await expect(
    settings(page).getByLabel('Control panel', { exact: true }),
  ).toHaveValue('readiness');
  await settings(page)
    .getByRole('button', {
      name: 'Close Getting Ready — Settings',
      exact: true,
    })
    .click();
  await expect(draft).toHaveValue('Keep this unfinished discussion draft.');
  let writes = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      request.url().endsWith(`/api/rooms/${room.id}/memories`)
    )
      writes++;
  });
  await openMessageAction(page, message, 'Remember this…');
  const notes = win.getByRole('region', { name: 'Room notes', exact: true });
  await expect(notes.getByLabel('Note', { exact: true })).toHaveValue(original);
  expect(writes).toBe(0);
  const note = 'Publish the weekly summary with the customer-facing title.';
  await notes.getByLabel('Note', { exact: true }).fill(note);
  await notes.getByRole('button', { name: 'Save note', exact: true }).click();
  await expect(notes.getByText(note, { exact: true })).toBeVisible();
  await page.screenshot({ path: info.outputPath('room-team-note.png') });
  const saved = await (
    await page.request.get(`/api/rooms/${room.id}/memories`)
  ).json();
  expect(saved.memories[0].provenance).toEqual({ kind: 'message', messageId });
  expect(writes).toBe(1);
  await notes
    .getByRole('button', { name: 'Original message', exact: true })
    .click();
  await expect(draft).toHaveValue('Keep this unfinished discussion draft.');
  await expect(win.locator(`[data-message-id="${messageId}"]`)).toHaveClass(
    /is-source/,
  );
  await expect(
    actions.getByRole('button', { name: 'Work', exact: true }),
  ).not.toHaveAttribute('aria-pressed');
  await actions.getByRole('button', { name: 'Work', exact: true }).click();
  await expect(work(page)).toBeVisible();
  await expect(work(page)).toHaveClass(/is-active/);
  await page.screenshot({ path: info.outputPath('room-daily-work.png') });
  await expect(
    win.getByRole('region', { name: 'Room management', exact: true }),
  ).toHaveCount(0);
  await openApp(page, 'Workspace');
  await openRoomMore(page, 'Execution history…');
  await expect(win.locator('.p9-window-title')).toHaveText(
    `Execution history — ${room.name} — Workspace`,
  );
  await expect(
    win.getByRole('region', { name: 'Collaborations', exact: true }),
  ).toBeVisible();
  await actions
    .getByRole('button', { name: 'Discussion', exact: true })
    .click();
  await expect(draft).toHaveValue('Keep this unfinished discussion draft.');
});

test('Inbox identifies the actual matter and opens it from a linked execution notification', async ({
  page,
}, info) => {
  const room = await createRoom(page, `Inbox matter ${info.project.name}`);
  const goal = 'Confirm the customer’s launch date';
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
    completionCriteria: 'The customer confirms a date.',
  });
  expect(response.ok()).toBe(true);
  const { item } = await response.json();
  const notificationId = randomUUID();
  const question = 'Should we offer Tuesday or Thursday?';
  let readAt = '';
  let reads = 0;
  await page.route('**/api/me/inbox', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: notificationId,
            kind: 'task_question',
            title: goal,
            question,
            summary: 'An older execution summary.',
            taskId: 'retained-execution',
            workItemId: item.id,
            roomId: room.id,
            roomName: room.name,
            messageId: '',
            runId: '',
            reviewTitle: '',
            createdAt: new Date().toISOString(),
            readAt,
          },
        ],
      },
    }),
  );
  await page.route(`**/api/me/inbox/${notificationId}/read`, (route) => {
    reads++;
    readAt = new Date().toISOString();
    return route.fulfill({ json: {} });
  });
  await errors.get(page)!.reload();
  await expect(workspace(page)).toBeVisible();
  const inbox = await openInboxActivity(page);
  await expect(inbox.getByText(goal, { exact: true })).toBeVisible();
  await expect(inbox.getByText(question, { exact: true })).toBeVisible();
  await expect(inbox).not.toContainText('An older execution summary.');
  await page.screenshot({ path: info.outputPath('inbox-actual-matter.png') });
  await inbox.getByRole('button').filter({ hasText: goal }).click();
  await expect(work(page)).toContainText(goal);
  await expect(
    workspace(page).getByRole('region', {
      name: 'Collaboration details',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect.poll(() => reads).toBe(1);
});

test('linked execution attention and system records open the matter without reading a separate task', async ({
  page,
}, info) => {
  const room = await createRoom(page, `Linked work ${info.project.name}`);
  const goal = 'Confirm the review audience';
  const created = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
    completionCriteria: 'The owner confirms the audience.',
  });
  expect(created.ok()).toBe(true);
  const { item } = await created.json();
  const taskId = `linked-execution-${randomUUID()}`;
  const system: Message = {
    id: randomUUID(),
    roomId: room.id,
    taskId,
    threadRootId: '',
    authorKind: 'system',
    authorUserId: '',
    authorName: 'Camp',
    agentId: '',
    runId: '',
    content: 'The current work needs your choice of review audience.',
    requestId: '',
    references: [],
    seq: 1,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
  // Immutable task/message presentation fixtures exercise navigation only.
  // No Runner or model execution is fabricated or admitted by this test.
  await page.route(`**/api/rooms/${room.id}/tasks?*`, (route) =>
    route.fulfill({
      json: {
        tasks: [
          {
            id: taskId,
            workItemId: item.id,
            roomId: room.id,
            goal,
            status: 'waiting_for_person',
            participants: [],
            question: {
              id: 'audience-question',
              text: 'Who should review this?',
              answer: '',
              options: [],
            },
          },
        ],
      },
    }),
  );
  await page.route(`**/api/rooms/${room.id}/messages?*`, (route) => {
    const query = new URL(route.request().url()).searchParams;
    return route.fulfill({
      json: {
        messages:
          !query.get('threadRootId') &&
          Number(query.get('after') || '0') < system.seq
            ? [system]
            : [],
      },
    });
  });
  await page.route(`**/api/rooms/${room.id}/runs?view=conversation*`, (route) =>
    route.fulfill({
      json: {
        items: [],
        tasks: [
          {
            id: taskId,
            roomId: room.id,
            workItemId: item.id,
            originMessageId: system.id,
            status: 'waiting_for_person',
          },
        ],
      },
    }),
  );
  let taskReads = 0;
  await page.route(`**/api/tasks/${taskId}?*`, (route) => {
    taskReads++;
    return route.fulfill({
      status: 404,
      json: { error: 'A linked task must open its matter.' },
    });
  });
  await errors.get(page)!.reload();
  const win = workspace(page);
  const attention = win.getByRole('region', {
    name: 'Work updates',
    exact: true,
  });
  await expect(attention).toContainText('Needs your attention');
  await attention
    .getByRole('button', { name: 'Open work', exact: true })
    .click();
  await expect(
    work(page).getByRole('region', { name: 'Work item detail', exact: true }),
  ).toContainText(goal);
  await openApp(page, 'Workspace');
  await win.getByRole('button', { name: 'Discussion', exact: true }).click();
  await win
    .locator(`[data-message-id="${system.id}"]`)
    .getByRole('button', { name: 'Open work', exact: true })
    .click();
  await expect(
    work(page).getByRole('region', { name: 'Work item detail', exact: true }),
  ).toContainText(goal);
  await openApp(page, 'Workspace');
  await win.getByRole('button', { name: 'Discussion', exact: true }).click();
  await attention
    .getByRole('button', { name: 'View work', exact: true })
    .click();
  await expect(work(page)).toContainText(goal);
  expect(taskReads).toBe(0);
});

test('a narrow Room chooser and compact actions fit long names without duplicating navigation', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(
    page,
    `Planning a shared customer onboarding guide with the entire team ${testInfo.project.name}`,
  );
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  const toolbar = win.locator('.workspace-toolbar');
  const actions = win.locator('.workspace-toolbar-actions');
  const menu = win.getByRole('button', { name: 'Room actions', exact: true });
  const library = actions.getByRole('button', { name: 'Files', exact: true });
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(chooser).toBeVisible();
    await expect(chooser).toHaveValue(room.id);
    await expect(chooser.locator('option:checked')).toHaveText(room.name);
    await expect(
      win.getByRole('button', { name: 'Rooms', exact: true }),
    ).toHaveCount(0);
    await expect(
      win.getByRole('complementary', { name: 'Workspace navigation' }),
    ).toBeHidden();
    await expect
      .poll(() =>
        toolbar.evaluate(
          (element) => element.scrollWidth - element.clientWidth,
        ),
      )
      .toBeLessThanOrEqual(1);
    const chooserBounds = (await chooser.boundingBox())!;
    const actionsBounds = (await actions.boundingBox())!;
    const menuBounds = (await menu.boundingBox())!;
    const libraryBounds = (await library.boundingBox())!;
    const windowBounds = (await win.boundingBox())!;
    expect(actionsBounds.y).toBeGreaterThanOrEqual(
      chooserBounds.y + chooserBounds.height,
    );
    expect(chooserBounds.x).toBeGreaterThanOrEqual(windowBounds.x);
    expect(chooserBounds.x + chooserBounds.width).toBeLessThanOrEqual(
      windowBounds.x + windowBounds.width,
    );
    expect(menuBounds.width).toBeLessThanOrEqual(libraryBounds.width + 12);
    expect(menuBounds.width).toBeGreaterThanOrEqual(24);
    expect(menuBounds.x + menuBounds.width).toBeCloseTo(
      actionsBounds.x + actionsBounds.width,
      0,
    );
    for (const button of await actions.getByRole('button').all()) {
      const bounds = (await button.boundingBox())!;
      expect(bounds.x).toBeGreaterThanOrEqual(windowBounds.x);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(
        windowBounds.x + windowBounds.width,
      );
    }
  }
  await expect(menu).toHaveAttribute('aria-haspopup', 'menu');
  await menu.focus();
  await menu.press('ArrowDown');
  await expect(page.getByRole('menu')).toBeFocused();
  await expect(
    page.getByRole('menuitem', { name: 'Rename Room…', exact: true }),
  ).toBeVisible();
  await page.getByRole('menu').press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(menu).toBeFocused();
  if (isMobile) await menu.tap();
  else await menu.click();
  await expect(page.getByRole('menu')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('narrow-room-menu.png') });
  await page.getByRole('menu').press('Escape');
  await expect(menu).toBeFocused();
});

test('the Room pop-up supports keyboard and touch selection while keeping private drafts', async ({
  page,
  isMobile,
}, testInfo) => {
  const first = await createRoom(page, `Orchid-${testInfo.project.name}`);
  const second = await createRoom(page, `Zephyr-${testInfo.project.name}`);
  await page.setViewportSize({ width: 390, height: 600 });
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  const popup = page.getByRole('listbox', { name: 'Room', exact: true });
  const input = win.getByRole('textbox', { name: `Message ${second.name}` });
  const draft = 'Keep these notes private while I look at another Room.';
  await expect(input).toBeEnabled();
  await input.fill(draft);

  await chooser.focus();
  await chooser.press('ArrowDown');
  await expect(chooser).toHaveAttribute('aria-expanded', 'true');
  await expect(
    popup.getByRole('option', { selected: true }),
  ).toHaveAccessibleName(second.name);
  await expect(chooser).toBeFocused();
  await chooser.pressSequentially(first.name);
  await expect(chooser).toHaveAttribute(
    'aria-activedescendant',
    (await popup
      .getByRole('option', { name: first.name, exact: true })
      .getAttribute('id'))!,
  );
  await expect(chooser).toHaveValue(second.id);
  await chooser.press('Escape');
  await expect(popup).toHaveCount(0);
  await expect(chooser).toBeFocused();
  await expect(chooser).toHaveValue(second.id);

  if (isMobile) await chooser.tap();
  else await chooser.click();
  await expect(popup).toBeVisible();
  await input.click();
  await expect(popup).toHaveCount(0);
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(draft);

  await chooser.focus();
  await chooser.press('ArrowDown');
  await chooser.press('Tab');
  await expect(popup).toHaveCount(0);
  await expect(
    win.getByRole('button', { name: 'Discussion', exact: true }),
  ).toBeFocused();

  await chooser.focus();
  await chooser.press('Enter');
  await chooser.pressSequentially(first.name);
  await chooser.press('Enter');
  await expect(popup).toHaveCount(0);
  await expect(chooser).toHaveValue(first.id);
  await expect(chooser).toBeFocused();
  await expect(
    win.getByRole('textbox', { name: `Message ${first.name}` }),
  ).toHaveValue('');

  if (isMobile) await chooser.tap();
  else await chooser.click();
  const destination = popup.getByRole('option', {
    name: second.name,
    exact: true,
  });
  if (isMobile) await destination.tap();
  else await destination.click();
  await expect(popup).toHaveCount(0);
  await expect(chooser).toHaveValue(second.id);
  await expect(chooser).toBeFocused();
  await expect(input).toHaveValue(draft);
  await errors.get(page)!.reload();
  await expect(chooser).toHaveValue(second.id);
  await expect(input).toHaveValue(draft);

  for (const room of [first, second]) {
    const messages = await (
      await page.request.get(`/api/rooms/${room.id}/messages`)
    ).json();
    expect(messages.messages).toEqual([]);
    const runs = await (
      await page.request.get(`/api/rooms/${room.id}/runs`)
    ).json();
    expect(runs.runs).toEqual([]);
  }
  await page.screenshot({ path: testInfo.outputPath('narrow-room-draft.png') });
});

test('Room selection waits for draft storage and keeps the current Room when saving fails', async ({
  page,
}, testInfo) => {
  const first = await createRoom(page, `save-first-${testInfo.project.name}`);
  const second = await createRoom(page, `save-second-${testInfo.project.name}`);
  await page.setViewportSize({ width: 390, height: 600 });
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  await chooser.selectOption(first.id);
  const input = win.getByRole('textbox', { name: `Message ${first.name}` });
  await expect(input).toBeEnabled();
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saving = false;
  let fail = false;
  await page.route('**/api/me/drafts/conversation', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    saving = true;
    if (fail) {
      await route.fulfill({
        status: 503,
        json: {
          error: 'Draft storage is unavailable. Your notes are preserved.',
        },
      });
      return;
    }
    await held;
    await route.continue();
  });
  try {
    await input.fill('Wait for this private draft before leaving.');
    await chooser.selectOption(second.id);
    await expect.poll(() => saving).toBe(true);
    await expect(chooser).toHaveValue(first.id);
    await expect(input).toHaveValue(
      'Wait for this private draft before leaving.',
    );
    release();
    await expect(chooser).toHaveValue(second.id);
    const secondInput = win.getByRole('textbox', {
      name: `Message ${second.name}`,
    });
    await expect(secondInput).toBeEnabled();
    fail = true;
    await secondInput.fill('Do not lose this draft when storage fails.');
    await chooser.selectOption(first.id);
    await expect(win.getByRole('alert')).toContainText(
      'Draft storage is unavailable.',
    );
    await expect(chooser).toHaveValue(second.id);
    await expect(secondInput).toHaveValue(
      'Do not lose this draft when storage fails.',
    );
  } finally {
    release();
    await page.unroute('**/api/me/drafts/conversation');
  }
});

test('source navigation saves input typed during the lookup before changing conversations', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `source-draft-${testInfo.project.name}`);
  const rootResponse = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'The thread containing the original source.',
    postOnly: true,
  });
  expect(rootResponse.ok()).toBe(true);
  const { message: root } = (await rootResponse.json()) as { message: Message };
  const sourceResponse = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Original threaded discussion.',
    threadRootId: root.id,
    postOnly: true,
  });
  expect(sourceResponse.ok()).toBe(true);
  const { message: source } = (await sourceResponse.json()) as {
    message: Message;
  };
  const linkedResponse = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'See the original discussion.',
    references: [{ kind: 'message', messageId: source.id }],
    postOnly: true,
  });
  expect(linkedResponse.ok()).toBe(true);
  const { message: linked } = (await linkedResponse.json()) as {
    message: Message;
  };
  await errors.get(page)!.reload();
  const win = workspace(page);
  const reference = win
    .locator(`[data-message-id="${linked.id}"]`)
    .getByRole('region', { name: 'Message references', exact: true });
  await reference.locator('summary').click();
  let requested = false;
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/api/rooms/${room.id}/messages/${source.id}`,
    async (route) => {
      requested = true;
      await held;
      await route.fulfill({ json: { message: source } });
    },
  );
  try {
    await reference
      .getByRole('button', { name: 'Open source', exact: true })
      .click();
    await expect.poll(() => requested).toBe(true);
    await page.route('**/api/me/drafts/conversation', (route) =>
      route.request().method() === 'PUT'
        ? route.fulfill({
            status: 503,
            json: { error: 'Keep the new draft until storage recovers.' },
          })
        : route.fallback(),
    );
    const input = win.getByRole('textbox', {
      name: `Message ${room.name}`,
      exact: true,
    });
    const text = 'Typed while the source lookup was still pending.';
    await input.fill(text);
    release();
    await expect(win.getByRole('alert')).toContainText(
      'Keep the new draft until storage recovers.',
    );
    await expect(input).toHaveValue(text);
    await expect(
      win.getByRole('form', { name: 'Thread composer', exact: true }),
    ).toHaveCount(0);
    await page.unroute('**/api/me/drafts/conversation');
    await win
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await win
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect(win.getByRole('alert')).toHaveCount(0);
    await reference
      .getByRole('button', { name: 'Open source', exact: true })
      .click();
    await expect(
      win.getByRole('form', { name: 'Thread composer', exact: true }),
    ).toBeVisible();
    const saved = await page.request.get(
      `/api/me/drafts/conversation?key=${encodeURIComponent(`${room.id}:room`)}`,
    );
    // The successful second navigation saves the original Room's draft before
    // entering the source Thread; no shared message is sent.
    expect(saved.ok()).toBe(true);
    expect(JSON.stringify(await saved.json())).toContain(text);
  } finally {
    release();
    await page.unroute('**/api/me/drafts/conversation');
  }
});
