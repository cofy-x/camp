import { randomUUID } from 'node:crypto';
import { expect, test, type Page, type Route } from '@playwright/test';
import type {
  Agent,
  Message,
  RunTimeline,
} from '../apps/dashboard/src/features/workspace/api';
import { createRoom, openRoomMore, signIn, workspace } from './team-helpers';
import { fixtureNotifications } from './fixture-notifications';
import { messageWindowFixture } from './message-window-fixture';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

function barrier() {
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { held, release };
}

const timestamp = '2026-11-07T08:00:00Z';
type FixtureRoom = { id: string; resourceId: string; name: string };

function message(
  room: FixtureRoom,
  content: string,
  seq = 1,
  value: Partial<Message> = {},
): Message {
  return {
    id: randomUUID(),
    roomId: room.id,
    threadRootId: '',
    authorKind: 'user',
    authorUserId: '',
    authorName: 'Avery',
    agentId: '',
    runId: '',
    content,
    requestId: '',
    references: [],
    seq,
    createdAt: timestamp,
    updatedAt: timestamp,
    ...value,
  };
}

async function fixture(page: Page, name: string) {
  const room = await createRoom(page, `${name}-${randomUUID().slice(0, 6)}`);
  const agent: Agent = {
    id: randomUUID(),
    name: 'Notes reviewer',
    handle: 'notes-reviewer',
    instructions: 'Review only when explicitly requested.',
    icon: 'document',
    avatar: 'agent-robot-teal-v1',
    access: 'write',
    enabled: true,
    revision: 1,
    createdAt: timestamp,
    updatedAt: timestamp,
  };
  const messages = [message(room, 'The saved discussion is ready to read.')];
  const reads = { members: 0, timeline: 0, fullRuns: 0, executionWrites: 0 };
  const handlers = {
    members: (route: Route) => route.fulfill({ json: { members: [agent] } }),
    timeline: (route: Route, _url: URL) =>
      route.fulfill({ json: { items: [], nextCursor: '' } }),
    messages: (route: Route, url: URL) =>
      route.fulfill({ json: messageWindowFixture(messages, url) }),
  };
  await page.route(`**/api/rooms/${room.id}/members`, (route) => {
    reads.members++;
    return handlers.members(route);
  });
  await page.route(
    (url) => url.pathname === `/api/rooms/${room.id}/runs`,
    (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get('view') !== 'conversation') {
        reads.fullRuns++;
        return route.fulfill({ json: { runs: [] } });
      }
      reads.timeline++;
      return handlers.timeline(route, url);
    },
  );
  await page.route(`**/api/rooms/${room.id}/messages**`, (route) => {
    if (route.request().method() !== 'GET') {
      reads.executionWrites++;
      return route.fulfill({
        status: 503,
        json: { error: 'This read-only fixture never sends work.' },
      });
    }
    return handlers.messages(route, new URL(route.request().url()));
  });
  return { room, agent, messages, reads, handlers };
}

async function selectRoom(page: Page, room: FixtureRoom) {
  const win = workspace(page);
  const picker = win.getByRole('combobox', { name: 'Room', exact: true });
  if (await picker.isVisible()) await picker.selectOption(room.id);
  else
    await win
      .getByRole('navigation', { name: 'Rooms', exact: true })
      .getByRole('button', { name: room.name, exact: true })
      .click();
}

async function refresh(page: Page) {
  await openRoomMore(page, 'Refresh');
}

function completedRun(
  value: Awaited<ReturnType<typeof fixture>>,
  origin: Message,
): RunTimeline {
  return {
    id: randomUUID(),
    roomId: value.room.id,
    resourceId: value.room.resourceId,
    threadRootId: '',
    originMessageId: origin.id,
    agentId: value.agent.id,
    agentSnapshot: {
      id: value.agent.id,
      name: value.agent.name,
      avatar: value.agent.avatar,
      access: value.agent.access,
    },
    status: 'succeeded',
    sessionMode: 'fresh',
    sessionGeneration: 1,
    createdAt: timestamp,
    updatedAt: timestamp,
    finishedAt: timestamp,
    error: '',
    resultCommit: '2'.repeat(40),
    published: true,
    environment: { checks: [{ name: 'unit' }] },
    checks: [{ name: 'unit', status: 'passed', exitCode: 0 }],
    testExitCode: 0,
    files: [{ path: 'notes-plan.md', status: 'added' }],
    fileCount: 1,
    filesLimited: false,
  };
}

for (const pending of ['members', 'timeline'] as const)
  test(`pending ${pending} never gates saved messages or typing`, async ({
    page,
  }, info) => {
    const value = await fixture(
      page,
      `pending-${pending}-${info.project.name}`,
    );
    const gate = barrier();
    let held = 0;
    if (pending === 'members')
      value.handlers.members = async (route) => {
        held++;
        await gate.held;
        await route.fulfill({ json: { members: [value.agent] } });
      };
    else
      value.handlers.timeline = async (route) => {
        held++;
        await gate.held;
        await route.fulfill({ json: { items: [], nextCursor: '' } });
      };
    try {
      await errors.get(page)!.reload();
      await expect.poll(() => held).toBeGreaterThan(0);
      const win = workspace(page);
      await expect(
        win.getByText(value.messages[0].content, { exact: true }),
      ).toBeVisible();
      const composer = win.getByRole('form', {
        name: 'Room composer',
        exact: true,
      });
      const input = composer.getByRole('textbox', {
        name: `Message ${value.room.name}`,
        exact: true,
      });
      await expect(input).toBeEnabled();
      const draft = 'Keep writing while optional Room activity is loading.';
      await input.fill(draft);
      const post = composer.getByRole('button', { name: 'Post', exact: true });
      if (pending === 'members') {
        // Unknown membership must not silently reinterpret normal routing.
        await expect(post).toBeDisabled();
        await composer.getByRole('button', { name: /^To:/ }).click();
        await composer
          .getByRole('button', { name: 'Post only', exact: true })
          .click();
        await expect(post).toBeEnabled();
        await composer.getByRole('button', { name: /^To:/ }).click();
        await composer
          .getByRole('button', { name: 'Room discussion', exact: true })
          .click();
        await expect(post).toBeDisabled();
      } else await expect(post).toBeEnabled();
      await win.screenshot({
        path: info.outputPath(`room-ready-pending-${pending}.png`),
      });
      gate.release();
      await expect(
        win.getByLabel('Room members', { exact: true }),
      ).toContainText(value.agent.name);
      await expect(post).toBeEnabled();
      await expect(input).toHaveValue(draft);
      expect(value.reads.fullRuns).toBe(0);
      expect(value.reads.executionWrites).toBe(0);
    } finally {
      gate.release();
      await page.unrouteAll({ behavior: 'wait' });
    }
  });

for (const failed of ['members', 'timeline'] as const)
  test(`failed ${failed} retains the discussion and draft through explicit retry`, async ({
    page,
  }, info) => {
    const value = await fixture(page, `failed-${failed}-${info.project.name}`);
    const failure = `The ${failed} metadata could not be read.`;
    const recovery = barrier();
    let broken = true;
    if (failed === 'members')
      value.handlers.members = async (route) => {
        if (broken)
          return route.fulfill({ status: 503, json: { error: failure } });
        await recovery.held;
        return route.fulfill({ json: { members: [value.agent] } });
      };
    else
      value.handlers.timeline = async (route) => {
        if (broken)
          return route.fulfill({ status: 503, json: { error: failure } });
        await recovery.held;
        return route.fulfill({ json: { items: [], nextCursor: '' } });
      };
    try {
      await errors.get(page)!.reload();
      const win = workspace(page);
      const alert = win.getByRole('alert').filter({ hasText: failure });
      await expect(alert).toBeVisible();
      await expect(
        win.getByText(value.messages[0].content, { exact: true }),
      ).toBeVisible();
      const input = win.getByRole('textbox', {
        name: `Message ${value.room.name}`,
        exact: true,
      });
      await expect(input).toBeEnabled();
      const draft = 'This draft survives a metadata failure and its retry.';
      await input.fill(draft);
      const before = value.reads[failed];
      broken = false;
      // A background refresh may already be recovering. Hold its successful
      // response so it cannot remove Retry before the explicit click reaches it.
      await refresh(page);
      await expect.poll(() => value.reads[failed]).toBeGreaterThan(before);
      await expect(alert).toBeVisible();
      const beforeRetry = value.reads[failed];
      await alert.getByRole('button', { name: 'Retry', exact: true }).click();
      // Retry marks the accepted in-flight read dirty instead of starting a
      // duplicate or cancelling it. Its one catch-up read follows completion.
      expect(value.reads[failed]).toBe(beforeRetry);
      await expect(alert).toBeVisible();
      await expect(input).toHaveValue(draft);
      recovery.release();
      await expect.poll(() => value.reads[failed]).toBe(beforeRetry + 1);
      await expect(alert).toHaveCount(0);
      await expect(input).toHaveValue(draft);
      await expect(
        win.getByText(value.messages[0].content, { exact: true }),
      ).toBeVisible();
      expect(value.reads.fullRuns).toBe(0);
      expect(value.reads.executionWrites).toBe(0);
    } finally {
      recovery.release();
    }
  });

type BufferedReads = {
  held: string[];
  released: number;
  release: () => void;
};

async function bufferReadBodies(page: Page, roomId: string, thread?: string) {
  await page.evaluate(
    ({ roomId, thread }) => {
      const scoped = window as typeof window & {
        roomReadBodies: BufferedReads;
      };
      const original = window.fetch.bind(window);
      let resume = () => {};
      const held = new Promise<void>((resolve) => {
        resume = resolve;
      });
      let active = true;
      scoped.roomReadBodies = {
        held: [],
        released: 0,
        release: () => {
          active = false;
          resume();
        },
      };
      window.fetch = async (input, init) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          location.href,
        );
        const matching =
          active &&
          url.pathname.startsWith(`/api/rooms/${roomId}/`) &&
          (thread === undefined
            ? ['messages', 'members', 'runs'].includes(
                url.pathname.split('/').at(-1) ?? '',
              )
            : url.pathname.endsWith('/messages') &&
              url.searchParams.get('threadRootId') === thread);
        const response = await original(input, init);
        if (!matching) return response;
        const body = await response.text();
        scoped.roomReadBodies.held.push(url.pathname + url.search);
        // Resolve already-buffered bodies after navigation even when its signal
        // was aborted, exercising the continuation fence rather than only fetch.
        await held;
        scoped.roomReadBodies.released++;
        return new Response(body, {
          status: response.status,
          headers: response.headers,
        });
      };
    },
    { roomId, thread },
  );
}

async function releaseReadBodies(page: Page) {
  await page.evaluate(() =>
    (
      window as typeof window & { roomReadBodies: BufferedReads }
    ).roomReadBodies.release(),
  );
}

test('late Room message and metadata bodies cannot replace a newer Room or surface its old error', async ({
  page,
}, info) => {
  const old = await fixture(page, `old-room-${info.project.name}`);
  old.messages[0].content =
    'This old Room response must never paint in the new Room.';
  const failure = 'The superseded Room membership failed.';
  old.handlers.members = (route) =>
    route.fulfill({ status: 503, json: { error: failure } });
  const current = await fixture(page, `current-room-${info.project.name}`);
  current.messages[0].content = 'The current Room stays selected.';
  await errors.get(page)!.reload();
  await expect(
    workspace(page).getByText(current.messages[0].content, { exact: true }),
  ).toBeVisible();
  await bufferReadBodies(page, old.room.id);
  try {
    await selectRoom(page, old.room);
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { roomReadBodies: BufferedReads })
              .roomReadBodies.held.length,
        ),
      )
      .toBeGreaterThanOrEqual(2);
    await selectRoom(page, current.room);
    const win = workspace(page);
    const input = win.getByRole('textbox', {
      name: `Message ${current.room.name}`,
      exact: true,
    });
    await expect(
      win.getByText(current.messages[0].content, { exact: true }),
    ).toBeVisible();
    const draft = 'Keep the newer Room draft when the old reads finish.';
    await input.fill(draft);
    await releaseReadBodies(page);
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { roomReadBodies: BufferedReads })
              .roomReadBodies.released,
        ),
      )
      .toBeGreaterThanOrEqual(2);
    await expect(input).toHaveValue(draft);
    await expect(
      win.getByText(current.messages[0].content, { exact: true }),
    ).toBeVisible();
    await expect(
      win.getByText(old.messages[0].content, { exact: true }),
    ).toHaveCount(0);
    await expect(
      win.getByRole('alert').filter({ hasText: failure }),
    ).toHaveCount(0);
    expect(old.reads.fullRuns + current.reads.fullRuns).toBe(0);
    expect(old.reads.executionWrites + current.reads.executionWrites).toBe(0);
  } finally {
    await releaseReadBodies(page);
  }
});

test('a late Thread body cannot replace a newer Thread or its unfinished reply', async ({
  page,
}, info) => {
  const value = await fixture(page, `thread-switch-${info.project.name}`);
  const first = value.messages[0];
  first.content = 'Discuss the first customer note.';
  const second = message(value.room, 'Discuss the second customer note.', 2);
  const oldReply = message(
    value.room,
    'This old Thread reply must not replace the newer Thread.',
    3,
    { threadRootId: first.id },
  );
  const reply = message(
    value.room,
    'The second Thread reply stays visible.',
    4,
    { threadRootId: second.id },
  );
  value.messages.push(second, oldReply, reply);
  await errors.get(page)!.reload();
  const win = workspace(page);
  await expect(win.getByText(second.content, { exact: true })).toBeVisible();
  await bufferReadBodies(page, value.room.id, first.id);
  try {
    await win
      .locator(`[data-message-id="${first.id}"]`)
      .getByRole('button', { name: 'Reply in thread', exact: true })
      .click();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { roomReadBodies: BufferedReads })
              .roomReadBodies.held.length,
        ),
      )
      .toBeGreaterThan(0);
    await selectRoom(page, value.room);
    await win
      .locator(`[data-message-id="${second.id}"]`)
      .getByRole('button', { name: 'Reply in thread', exact: true })
      .click();
    await expect(win.getByText(reply.content, { exact: true })).toBeVisible();
    const input = win.getByRole('textbox', {
      name: 'Message thread',
      exact: true,
    });
    const draft = 'Keep my unfinished reply in the newer Thread.';
    await input.fill(draft);
    await releaseReadBodies(page);
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { roomReadBodies: BufferedReads })
              .roomReadBodies.released,
        ),
      )
      .toBeGreaterThan(0);
    await expect(input).toHaveValue(draft);
    await expect(win.getByText(reply.content, { exact: true })).toBeVisible();
    await expect(win.getByText(oldReply.content, { exact: true })).toHaveCount(
      0,
    );
    expect(value.reads.fullRuns).toBe(0);
    expect(value.reads.executionWrites).toBe(0);
  } finally {
    await releaseReadBodies(page);
  }
});

test('a failed earlier window keeps the current messages and draft, then retries without an old-page read acknowledgment', async ({
  page,
}, info) => {
  const notify = await fixtureNotifications(page);
  const value = await fixture(page, `message-windows-${info.project.name}`);
  value.messages.splice(
    0,
    value.messages.length,
    ...Array.from({ length: 120 }, (_, i) =>
      message(value.room, `Retained discussion ${i + 1}`, i + 1),
    ),
  );
  let frontier = 120;
  await page.route('**/api/rooms', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    return route.fulfill({
      response,
      json: {
        ...body,
        rooms: body.rooms.map((room: FixtureRoom) =>
          room.id === value.room.id
            ? { ...room, lastMessageSeq: frontier }
            : room,
        ),
      },
    });
  });
  let earlierFails = true;
  const windows: string[] = [];
  const acknowledgments: number[] = [];
  await page.route(`**/api/me/room-reads/${value.room.id}`, (route) => {
    acknowledgments.push(route.request().postDataJSON().seq);
    return route.fulfill({ json: {} });
  });
  value.handlers.messages = async (route, url) => {
    windows.push(url.searchParams.toString());
    if (url.searchParams.has('before') && earlierFails)
      return route.fulfill({
        status: 503,
        json: { error: 'The earlier discussion failed.' },
      });
    return route.fulfill({ json: messageWindowFixture(value.messages, url) });
  };
  await errors.get(page)!.reload();
  const win = workspace(page);
  await expect(win.locator('.workspace-message')).toHaveCount(50);
  const input = win.getByRole('textbox', {
    name: `Message ${value.room.name}`,
    exact: true,
  });
  await input.fill('Keep this unfinished message through history navigation.');
  await expect.poll(() => acknowledgments).toContain(120);
  const original = await input.elementHandle();
  const initial = windows.length;
  await win
    .getByRole('navigation', { name: 'Message history' })
    .getByRole('button', { name: 'Earlier', exact: true })
    .click();
  const error = win
    .getByRole('alert')
    .filter({ hasText: 'The earlier discussion failed.' });
  await expect(error).toBeVisible();
  await expect(win.locator('.workspace-message')).toHaveCount(50);
  await expect(input).toHaveValue(
    'Keep this unfinished message through history navigation.',
  );
  const before = acknowledgments.length;
  earlierFails = false;
  await error.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(error).toHaveCount(0);
  await expect(win.locator('.workspace-message').last()).toContainText(
    'Retained discussion 70',
  );
  await expect(win.locator('.workspace-message')).toHaveCount(50);
  expect(acknowledgments.length).toBe(before);
  expect(
    await input.evaluate((node, original) => node === original, original),
  ).toBe(true);
  expect(
    windows.slice(initial).every((query) => query.includes('before=71')),
  ).toBe(true);
  const windowsBeforeNotice = windows.length;
  value.messages.push(
    message(value.room, 'New activity while reading older history', 121),
  );
  frontier = 121;
  await notify('message.created', value.room.id, value.messages.at(-1)!.id);
  await expect.poll(() => windows.length).toBeGreaterThan(windowsBeforeNotice);
  await expect(win.locator('.workspace-message').last()).toContainText(
    'Retained discussion 70',
  );
  expect(acknowledgments.length).toBe(before);
  await win
    .getByRole('navigation', { name: 'Message history' })
    .getByRole('button', { name: 'Latest', exact: true })
    .click();
  await expect(win.locator('.workspace-message').last()).toContainText(
    'New activity while reading older history',
  );
  await expect.poll(() => acknowledgments).toContain(121);
  await expect(input).toHaveValue(
    'Keep this unfinished message through history navigation.',
  );
  expect(value.reads.fullRuns).toBe(0);
  expect(value.reads.executionWrites).toBe(0);
});

test('delayed and failed related activity preserves accepted status and results until retry', async ({
  page,
}, info) => {
  const value = await fixture(page, `activity-pages-${info.project.name}`);
  const origin = value.messages[0];
  const complete = completedRun(value, origin);
  const result = message(
    value.room,
    'The reviewed Notes plan is available.',
    2,
    {
      authorKind: 'agent',
      agentId: value.agent.id,
      runId: complete.id,
      authorName: '',
    },
  );
  const nextOrigin = message(
    value.room,
    'Review the next set of customer notes.',
    3,
  );
  const queued: RunTimeline = {
    ...complete,
    id: randomUUID(),
    originMessageId: nextOrigin.id,
    status: 'queued',
    finishedAt: '',
    resultCommit: '',
    published: false,
    files: [],
    fileCount: 0,
    checks: [],
    testExitCode: null,
  };
  value.messages.push(result, nextOrigin);
  const gate = barrier();
  let updating = false;
  let failUpdate = true;
  let updateReads = 0;
  value.handlers.timeline = async (route, url) => {
    expect(url.searchParams.get('cursor')).toBeNull();
    if (!updating)
      return route.fulfill({ json: { items: [complete], tasks: [] } });
    updateReads++;
    await gate.held;
    return failUpdate
      ? route.fulfill({
          status: 503,
          json: { error: 'The related work activity failed.' },
        })
      : route.fulfill({ json: { items: [complete, queued], tasks: [] } });
  };

  try {
    await errors.get(page)!.reload();
    const win = workspace(page);
    const reply = win.locator(`[data-message-id="${result.id}"]`);
    await expect(reply.locator('.workspace-result-status')).toHaveText(
      'Completed',
    );
    await expect(
      reply.getByText('Checks: Passed', { exact: true }),
    ).toBeVisible();
    await expect(
      reply.getByRole('button', { name: 'Open notes-plan.md', exact: true }),
    ).toBeVisible();
    const input = win.getByRole('textbox', {
      name: `Message ${value.room.name}`,
      exact: true,
    });
    const draft =
      'Do not discard my draft or accepted results while activity loads.';
    await input.fill(draft);
    updating = true;
    await refresh(page);
    await expect.poll(() => updateReads).toBeGreaterThan(0);
    await expect(reply.locator('.workspace-result-status')).toHaveText(
      'Completed',
    );
    await expect(
      reply.getByRole('button', { name: 'Open notes-plan.md', exact: true }),
    ).toBeVisible();
    await expect(input).toHaveValue(draft);
    await win.screenshot({
      path: info.outputPath('room-retains-results-during-pagination.png'),
    });
    gate.release();
    const error = win
      .getByRole('alert')
      .filter({ hasText: 'The related work activity failed.' });
    await expect(error).toBeVisible();
    await expect(reply.locator('.workspace-result-status')).toHaveText(
      'Completed',
    );
    await expect(
      reply.getByText('Checks: Passed', { exact: true }),
    ).toBeVisible();
    await expect(input).toHaveValue(draft);
    failUpdate = false;
    await error.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(error).toHaveCount(0);
    await expect(win.getByTestId('agent-work')).toHaveAttribute(
      'data-state',
      'queued',
    );
    await expect(reply.locator('.workspace-result-status')).toHaveText(
      'Completed',
    );
    await expect(
      reply.getByRole('button', { name: 'Open notes-plan.md', exact: true }),
    ).toBeVisible();
    await expect(input).toHaveValue(draft);
    expect(updateReads).toBeGreaterThanOrEqual(2);
    expect(value.reads.fullRuns).toBe(0);
    expect(value.reads.executionWrites).toBe(0);
  } finally {
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('ten thousand retained messages open one window and historical references seek directly while preserving the draft', async ({
  page,
}, info) => {
  const value = await fixture(page, `long-history-${info.project.name}`);
  value.messages.splice(
    0,
    value.messages.length,
    ...Array.from({ length: 10000 }, (_, i) =>
      message(value.room, `Retained note ${i + 1}`, i + 1),
    ),
  );
  const old = value.messages[10];
  old.content = Array.from(
    { length: 15 },
    (_, i) => `Historical paragraph ${i + 1}.`,
  ).join('\n\n');
  old.authorKind = 'agent';
  old.agentId = value.agent.id;
  const original = completedRun(value, value.messages[9]);
  old.runId = original.id;
  const last = value.messages.at(-1)!;
  last.references = [
    {
      kind: 'message',
      messageId: old.id,
      title: 'Original launch brief',
      content: 'Historical brief',
    },
  ];
  const reads: string[] = [];
  const batches: string[][] = [];
  value.handlers.messages = (route, url) => {
    reads.push(url.search);
    return route.fulfill({ json: messageWindowFixture(value.messages, url) });
  };
  value.handlers.timeline = (route, url) => {
    const ids = (url.searchParams.get('messageIds') ?? '').split(',');
    batches.push(ids);
    return route.fulfill({
      json: { items: ids.includes(old.id) ? [original] : [], tasks: [] },
    });
  };
  await page.route(
    `**/api/rooms/${value.room.id}/messages/${old.id}`,
    (route) => route.fulfill({ json: { message: old } }),
  );
  await errors.get(page)!.reload();
  const win = workspace(page);
  const input = win.getByRole('textbox', {
    name: `Message ${value.room.name}`,
    exact: true,
  });
  await expect(win.locator('.workspace-message')).toHaveCount(50);
  await expect(win.locator('.workspace-message').last()).toContainText(
    'Retained note 10000',
  );
  await expect.poll(() => batches.length).toBe(1);
  expect(reads).toHaveLength(1);
  expect(batches[0]).toHaveLength(50);
  await input.fill('Keep the draft while I read the old brief.');
  await win.screenshot({ path: info.outputPath('recent-message-window.png') });
  await win.getByText('Original launch brief', { exact: true }).click();
  await win.getByRole('button', { name: 'Open source', exact: true }).click();
  const target = win.locator(`[data-message-id="${old.id}"]`);
  await expect(target).toBeFocused();
  await expect(target).toHaveClass(/is-source/);
  await expect(target.locator('.workspace-result-facts')).toContainText(
    'Checks: Passed',
  );
  await expect(win.locator('.workspace-message')).toHaveCount(50);
  expect(reads.filter((query) => query.includes('around='))).toHaveLength(1);
  expect(reads.some((query) => query.includes('after='))).toBe(false);
  expect(batches.every((ids) => ids.length <= 51)).toBe(true);
  await expect(input).toHaveValue('Keep the draft while I read the old brief.');
  const reply = target.locator('.workspace-agent-reply');
  await reply.locator(':scope > summary').click();
  await expect(reply).toHaveAttribute('open');
  await win.screenshot({
    path: info.outputPath('direct-historical-source.png'),
  });
  await win
    .getByRole('navigation', { name: 'Message history' })
    .getByRole('button', { name: 'Latest', exact: true })
    .click();
  await expect(win.locator('.workspace-message').last()).toContainText(
    'Retained note 10000',
  );
  await expect(input).toHaveValue('Keep the draft while I read the old brief.');
  await win.getByText('Original launch brief', { exact: true }).click();
  await win.getByRole('button', { name: 'Open source', exact: true }).click();
  await expect(reply).toHaveAttribute('open');
  await expect(target).toBeFocused();
  const offset = await target.evaluate(
    (node) => node.getBoundingClientRect().top,
  );
  const rooms = (await (await page.request.get('/api/rooms')).json())
    .rooms as FixtureRoom[];
  const other = rooms.find((room) => room.id !== value.room.id)!;
  await selectRoom(page, other);
  await expect(
    win.getByRole('textbox', { name: `Message ${other.name}`, exact: true }),
  ).toBeVisible();
  await selectRoom(page, value.room);
  await expect(reply).toHaveAttribute('open');
  await expect(input).toHaveValue('Keep the draft while I read the old brief.');
  await expect
    .poll(async () =>
      Math.abs(
        (await target.evaluate((node) => node.getBoundingClientRect().top)) -
          offset,
      ),
    )
    .toBeLessThan(4);
  expect(value.reads.fullRuns).toBe(0);
  expect(value.reads.executionWrites).toBe(0);
});
