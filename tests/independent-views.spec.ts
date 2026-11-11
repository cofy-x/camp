import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { Agent } from '../apps/dashboard/src/features/workspace/api';
import type { AutomationPolicy } from '../apps/dashboard/src/features/automation/api';
import type {
  WorkItem,
  WorkListItem,
} from '../apps/dashboard/src/features/workitems/api';
import {
  createRoom,
  library,
  mutate,
  openApp,
  openInboxActivity,
  openRoomMore,
  inboxToggle,
  signIn,
  work,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';
import type { InboxItem } from '../apps/dashboard/src/team/api';

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

async function publishedDocument(page: Page, name: string, path = 'notes.md') {
  const room = await createRoom(page, `${name}-${randomUUID().slice(0, 6)}`);
  const original = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const content = `# ${name}\n\nThis accepted file remains independent from version history.`;
  const response = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: original.commit,
      path,
      content,
    },
    'PUT',
  );
  expect(response.ok()).toBe(true);
  const { publication } = await response.json();
  return { room, path, heading: name, commit: publication.commit };
}

async function openFiles(page: Page) {
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
}

async function selectResource(page: Page, name: string) {
  const win = library(page);
  await expect(
    win.getByRole('button', { name: 'More Library actions', exact: true }),
  ).toBeVisible();
  const sources = win.getByRole('complementary', {
    name: 'Library resources',
    exact: true,
  });
  if (!(await sources.isVisible()))
    await win.getByRole('button', { name: 'Sources', exact: true }).click();
  await sources
    .getByRole('button')
    .filter({ hasText: `${name} files` })
    .click();
}

test('accepted files and exact-version downloads never wait for an unopened or slow history', async ({
  page,
}, info) => {
  const value = await publishedDocument(
    page,
    `Independent file ${info.project.name}`,
  );
  const gate = barrier();
  let historyReads = 0;
  await page.route(
    `**/api/resources/${value.room.resourceId}/history`,
    async (route) => {
      historyReads++;
      const response = await route.fetch();
      await gate.held;
      await route.fulfill({ response });
    },
  );
  try {
    await openFiles(page);
    const win = library(page);
    await expect(
      win.getByRole('heading', { name: value.heading, exact: true }),
    ).toBeVisible();
    const download = win.getByRole('link', {
      name: `Download ${value.path}`,
      exact: true,
    });
    await expect(download).toHaveAttribute(
      'href',
      new RegExp(`commit=${value.commit}`),
    );
    expect(historyReads).toBe(0);
    await win.getByRole('button', { name: 'History', exact: true }).click();
    await expect.poll(() => historyReads).toBeGreaterThan(0);
    await expect(
      win.getByText('Loading history…', { exact: true }),
    ).toBeVisible();
    await win
      .getByRole('button', { name: 'Back to Files', exact: true })
      .click();
    await expect(
      win.getByRole('heading', { name: value.heading, exact: true }),
    ).toBeVisible();
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
    await expect(
      win.getByRole('heading', { name: value.heading, exact: true }),
    ).toBeVisible();
    await expect(download).toHaveAttribute(
      'href',
      new RegExp(`commit=${value.commit}`),
    );
    await expect(
      win.getByRole('region', { name: 'Shared version history', exact: true }),
    ).toHaveCount(0);
    await win.screenshot({
      path: info.outputPath('library-files-independent-history.png'),
    });
  } finally {
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('history failure has a local retry and never marks an accepted file unavailable', async ({
  page,
}, info) => {
  const value = await publishedDocument(
    page,
    `History recovery ${info.project.name}`,
  );
  let broken = true;
  let fileReads = 0;
  await page.route(
    `**/api/resources/${value.room.resourceId}/files`,
    (route) => {
      fileReads++;
      return route.fallback();
    },
  );
  await page.route(
    `**/api/resources/${value.room.resourceId}/history`,
    (route) =>
      broken
        ? route.fulfill({
            status: 503,
            json: { error: 'Version history is temporarily unavailable.' },
          })
        : route.fallback(),
  );
  await openFiles(page);
  const win = library(page);
  await expect(
    win.getByRole('heading', { name: value.heading, exact: true }),
  ).toBeVisible();
  await win.getByRole('button', { name: 'History', exact: true }).click();
  const history = win.getByRole('region', {
    name: 'Shared version history',
    exact: true,
  });
  await expect(history.getByRole('alert')).toContainText(
    'Version history is temporarily unavailable.',
  );
  await expect(win.getByText('Files unavailable', { exact: true })).toHaveCount(
    0,
  );
  const beforeRetry = fileReads;
  broken = false;
  await history
    .getByRole('button', { name: 'Retry History', exact: true })
    .click();
  await expect(history.locator('article')).toHaveCount(1);
  await expect(history.getByRole('alert')).toHaveCount(0);
  expect(fileReads).toBe(beforeRetry);
  await win.getByRole('button', { name: 'Back to Files', exact: true }).click();
  await expect(
    win.getByRole('heading', { name: value.heading, exact: true }),
  ).toBeVisible();
  await expect(
    win.getByRole('link', { name: `Download ${value.path}`, exact: true }),
  ).toHaveAttribute('href', new RegExp(`commit=${value.commit}`));
});

test('late history cannot paint another resource or replace its preview', async ({
  page,
}, info) => {
  const first = await publishedDocument(
    page,
    `First source ${info.project.name}`,
    'first-only.md',
  );
  const second = await publishedDocument(
    page,
    `Second source ${info.project.name}`,
    'second-only.md',
  );
  const gate = barrier();
  let pending = 0;
  await page.route(
    `**/api/resources/${first.room.resourceId}/history`,
    async (route) => {
      const response = await route.fetch();
      pending++;
      await gate.held;
      await route.fulfill({ response });
    },
  );
  try {
    await openFiles(page);
    const win = library(page);
    await selectResource(page, first.room.name);
    await expect(
      win.getByRole('heading', { name: first.heading, exact: true }),
    ).toBeVisible();
    await win.getByRole('button', { name: 'History', exact: true }).click();
    await expect.poll(() => pending).toBeGreaterThan(0);
    await selectResource(page, second.room.name);
    await expect(
      win.getByRole('heading', { name: second.heading, exact: true }),
    ).toBeVisible();
    await win.getByRole('button', { name: 'History', exact: true }).click();
    const history = win.getByRole('region', {
      name: 'Shared version history',
      exact: true,
    });
    await expect(history.locator('article')).toContainText('second-only.md');
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
    await expect(history.locator('article')).toHaveCount(1);
    await expect(history).not.toContainText('first-only.md');
    await win
      .getByRole('button', { name: 'Back to Files', exact: true })
      .click();
    await expect(
      win.getByRole('heading', { name: second.heading, exact: true }),
    ).toBeVisible();
  } finally {
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

async function matter(page: Page, name: string, question = true) {
  const room = await createRoom(page, `${name}-${randomUUID().slice(0, 6)}`);
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal: 'Confirm the Notes release date',
  });
  expect(response.ok()).toBe(true);
  const body = await response.json();
  const item: WorkItem = {
    ...body.item,
    ...(question
      ? {
          status: 'waiting_for_person',
          question: 'Should we release on Tuesday or Thursday?',
        }
      : {}),
  };
  const stamp = new Date().toISOString();
  const agent: Agent = {
    id: randomUUID(),
    name: `Notes developer ${name}`,
    handle: `notes-${randomUUID().slice(0, 8)}`,
    icon: 'document',
    avatar: 'agent-robot-teal-v1',
    instructions: '',
    access: 'write',
    enabled: true,
    revision: 1,
    createdAt: stamp,
    updatedAt: stamp,
  };
  const listItem: WorkListItem = {
    seq: item.seq,
    id: item.id,
    roomId: item.roomId,
    ownerUserId: item.ownerUserId,
    goal: item.goal,
    status: item.status,
    revision: item.revision,
    question: item.question,
    roomName: room.name,
    needsYou: question,
    createdAt: item.createdAt,
    updatedAt: item.updatedAt,
  };
  const reads = { detail: 0, writes: 0, context: 0 };

  type ContextReply = {
    status: number;
    json: { members?: Agent[]; policies?: AutomationPolicy[]; error?: string };
  };

  const context: {
    members: () => Promise<ContextReply>;
    policies: () => Promise<ContextReply>;
  } = {
    members: async () => ({ status: 200, json: { members: [agent] } }),
    policies: async () => ({ status: 200, json: { policies: [] } }),
  };
  await page.route('**/api/work-items?*', (route) =>
    route.fulfill({
      json: { items: [listItem], nextCursor: '', hasMore: false },
    }),
  );
  await page.route(`**/api/work-items/${item.id}`, (route) => {
    reads.detail++;
    return route.fulfill({ json: { item } });
  });
  await page.route(`**/api/work-items/${item.id}/actions/*`, (route) => {
    reads.writes++;
    return route.fulfill({
      status: 503,
      json: { error: 'This fixture never starts or changes real work.' },
    });
  });
  await page.route(`**/api/rooms/${room.id}/members`, async (route) => {
    reads.context++;
    await route.fulfill(await context.members());
  });
  await page.route(
    `**/api/rooms/${room.id}/automation-policies`,
    async (route) => {
      reads.context++;
      await route.fulfill(await context.policies());
    },
  );
  await page.route(`**/api/rooms/${room.id}/work-item-defaults*`, (route) =>
    route.fulfill({
      json: {
        defaults: {
          budget: item.budget,
          expiresAt: item.expiresAt,
          participantAgentIds: [agent.id],
          firstAgentId: agent.id,
          execution: { maxTurns: 6, maxModelRequests: 24 },
        },
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
  return { room, item, agent, listItem, reads, context };
}

async function openMatter(page: Page, item: WorkItem) {
  // Install the complete fixture before a cold shared-read cache is created.
  await page.reload();
  await openApp(page, 'Work');
  await work(page)
    .locator('.work-item-list')
    .getByRole('button')
    .filter({ hasText: item.goal })
    .click();
  return work(page).getByRole('region', {
    name: 'Work item detail',
    exact: true,
  });
}

for (const pending of ['members', 'policies'] as const)
  test(`Work facts and answers do not wait for ${pending}, while allowance edits retain their context gate`, async ({
    page,
  }, info) => {
    const value = await matter(page, `pending-${pending}-${info.project.name}`);
    const gate = barrier();
    const original = value.context[pending];
    let held = 0;
    value.context[pending] = async () => {
      held++;
      await gate.held;
      return original();
    };
    try {
      const detail = await openMatter(page, value.item);
      await expect.poll(() => held).toBeGreaterThan(0);
      await expect(
        detail.getByRole('heading', { name: value.item.goal, exact: true }),
      ).toBeVisible();
      await expect(
        detail.getByRole('heading', {
          name: value.item.question!,
          exact: true,
        }),
      ).toBeVisible();
      const answer = detail.getByRole('textbox', {
        name: 'Your answer',
        exact: true,
      });
      const draft = 'Thursday, retaining my answer while settings are loading.';
      await expect(answer).toBeEnabled();
      await answer.fill(draft);
      await expect(
        detail.getByRole('button', { name: 'Send answer', exact: true }),
      ).toBeEnabled();
      await detail.getByRole('tab', { name: 'Manage', exact: true }).click();
      const allowance = detail.getByRole('region', {
        name: 'Allowance and access',
        exact: true,
      });
      await expect(
        allowance.getByRole('button', { name: 'Save allowance', exact: true }),
      ).toBeDisabled();
      await expect(allowance.getByRole('spinbutton').first()).toBeDisabled();
      await detail.getByRole('tab', { name: 'Overview', exact: true }).click();
      await expect(answer).toHaveValue(draft);
      await work(page).screenshot({
        path: info.outputPath(`work-facts-pending-${pending}.png`),
      });
      gate.release();
      await expect(
        work(page).getByText('Loading Room settings…', { exact: true }),
      ).toHaveCount(0);
      await expect(answer).toHaveValue(draft);
      await detail.getByRole('tab', { name: 'Manage', exact: true }).click();
      await expect(
        allowance.getByRole('button', { name: 'Save allowance', exact: true }),
      ).toBeEnabled();
      expect(value.reads.detail).toBeGreaterThan(0);
      expect(value.reads.writes).toBe(0);
    } finally {
      gate.release();
      await page.unrouteAll({ behavior: 'wait' });
    }
  });

test('a failed Room policy read keeps the question and draft with a local settings retry', async ({
  page,
}, info) => {
  const value = await matter(page, `context-retry-${info.project.name}`);
  await page.addInitScript(() => {
    const recovery = { clicked: false };
    (
      window as typeof window & { campWorkContextRetry: typeof recovery }
    ).campWorkContextRetry = recovery;
    document.addEventListener(
      'click',
      (event) => {
        const button =
          event.target instanceof Element
            ? event.target.closest('button')
            : null;
        if (button?.textContent?.trim() === 'Retry Room settings')
          recovery.clicked = true;
      },
      true,
    );
  });
  value.context.policies = async () => {
    // A global event can retry context while the pointer is approaching the
    // control. Only delivery of the explicit Retry click permits success.
    const clicked = await page.evaluate(
      () =>
        (
          window as typeof window & {
            campWorkContextRetry: { clicked: boolean };
          }
        ).campWorkContextRetry.clicked,
    );
    return clicked
      ? { status: 200, json: { policies: [] } }
      : {
          status: 503,
          json: { error: 'Room policies are temporarily unavailable.' },
        };
  };
  const detail = await openMatter(page, value.item);
  const failure = work(page)
    .getByRole('alert')
    .filter({ hasText: 'Room policies are temporarily unavailable.' });
  await expect(failure).toBeVisible();
  const answer = detail.getByRole('textbox', {
    name: 'Your answer',
    exact: true,
  });
  const draft = 'Thursday. Preserve this reply through the settings retry.';
  await answer.fill(draft);
  await expect(
    detail.getByRole('button', { name: 'Send answer', exact: true }),
  ).toBeEnabled();
  await failure
    .getByRole('button', { name: 'Retry Room settings', exact: true })
    .click();
  await expect(failure).toHaveCount(0);
  await expect(answer).toHaveValue(draft);
  expect(value.reads.writes).toBe(0);
});

test('a prepared execution waits for matching context without guessing Agents or losing its note', async ({
  page,
}, info) => {
  const value = await matter(page, `grant-context-${info.project.name}`, false);
  const gate = barrier();
  const original = value.context.members;
  value.context.members = async () => {
    await gate.held;
    return original();
  };
  try {
    const detail = await openMatter(page, value.item);
    await expect(
      detail.getByRole('heading', { name: value.item.goal, exact: true }),
    ).toBeVisible();
    await detail
      .getByRole('button', { name: 'Start work…', exact: true })
      .click();
    const next = detail.getByRole('form', { name: 'Next step', exact: true });
    const note = next.getByRole('textbox', {
      name: 'What should happen next? (optional)',
      exact: true,
    });
    const draft = 'Finish the Notes release summary after routing is known.';
    await note.fill(draft);
    await expect(
      next.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeDisabled();
    await expect(
      next.getByText('No available Agents in this Room.', { exact: true }),
    ).toHaveCount(0);
    gate.release();
    await expect(
      next.getByText(value.agent.name, { exact: false }).first(),
    ).toBeVisible();
    await expect(
      next.getByRole('button', { name: 'Start work', exact: true }),
    ).toBeEnabled();
    await expect(note).toHaveValue(draft);
    expect(value.reads.writes).toBe(0);
  } finally {
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a late Room context cannot replace another Work item or enable its grants from the wrong Room', async ({
  page,
}, info) => {
  const first = await matter(page, `first-context-${info.project.name}`);
  const second = await matter(page, `second-context-${info.project.name}`);
  const gate = barrier();
  const original = first.context.policies;
  let held = 0;
  first.context.policies = async () => {
    held++;
    await gate.held;
    return original();
  };
  await page.route('**/api/work-items?*', (route) =>
    route.fulfill({
      json: {
        items: [first.listItem, second.listItem],
        nextCursor: '',
        hasMore: false,
      },
    }),
  );
  try {
    await page.reload();
    await openApp(page, 'Work');
    const win = work(page);
    const list = win.locator('.work-item-list');
    await list.getByRole('button').filter({ hasText: first.room.name }).click();
    await expect.poll(() => held).toBeGreaterThan(0);
    let detail = win.getByRole('region', {
      name: 'Work item detail',
      exact: true,
    });
    await detail
      .getByRole('textbox', { name: 'Your answer', exact: true })
      .fill('This is the first Room draft.');
    await detail
      .getByRole('button', { name: 'Back to Work', exact: true })
      .click();
    await list
      .getByRole('button')
      .filter({ hasText: second.room.name })
      .click();
    detail = win.getByRole('region', { name: 'Work item detail', exact: true });
    const answer = detail.getByRole('textbox', {
      name: 'Your answer',
      exact: true,
    });
    const draft = 'This answer belongs only to the second Room.';
    await answer.fill(draft);
    await detail.getByRole('tab', { name: 'Manage', exact: true }).click();
    const save = detail.getByRole('button', {
      name: 'Save allowance',
      exact: true,
    });
    await expect(save).toBeEnabled();
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
    await expect(save).toBeEnabled();
    await expect(win.locator('.p9-window-title')).toHaveText(
      `${second.room.name} — Work`,
    );
    await detail.getByRole('tab', { name: 'Overview', exact: true }).click();
    await expect(answer).toHaveValue(draft);
    expect(first.reads.writes + second.reads.writes).toBe(0);
  } finally {
    gate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

function activity(room: { id: string; name: string }): InboxItem {
  return {
    id: randomUUID(),
    kind: 'mention',
    roomId: room.id,
    roomName: room.name,
    messageId: '',
    runId: '',
    reviewTitle: '',
    title: 'The Notes plan is ready',
    summary:
      'A retained activity entry remains separate from pending judgment.',
    createdAt: '2026-11-07T08:00:00Z',
    readAt: '',
  };
}

test('Activity has an honest initial loading state and accepts rows while unrelated team members are pending', async ({
  page,
}, info) => {
  const room = await createRoom(page, `inbox-independent-${info.project.name}`);
  const item = activity(room);
  const membersGate = barrier(),
    inboxGate = barrier();
  let membersHeld = 0,
    activityHeld = 0;
  await page.route('**/api/team/members', async (route) => {
    const response = await route.fetch();
    membersHeld++;
    await membersGate.held;
    await route.fulfill({ response });
  });
  await page.route('**/api/me/inbox', async (route) => {
    activityHeld++;
    await inboxGate.held;
    await route.fulfill({ json: { items: [item] } });
  });
  try {
    await errors.get(page)!.reload();
    await expect.poll(() => membersHeld).toBeGreaterThan(0);
    await expect.poll(() => activityHeld).toBeGreaterThan(0);
    const panel = await openInboxActivity(page);
    await expect(
      panel.getByText('Loading activity…', { exact: true }),
    ).toBeVisible();
    await expect(
      panel.getByText('No activity yet.', { exact: true }),
    ).toHaveCount(0);
    inboxGate.release();
    await expect(panel.getByText(item.title!, { exact: true })).toBeVisible();
    await expect(
      panel.getByText('Loading activity…', { exact: true }),
    ).toHaveCount(0);
    await page.screenshot({
      path: info.outputPath('activity-independent-team-members.png'),
    });
  } finally {
    inboxGate.release();
    membersGate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('failed Activity refresh retains rows and the discussion draft through local retry and read navigation', async ({
  page,
}, info) => {
  const room = await createRoom(page, `inbox-retry-${info.project.name}`);
  const item = activity(room);
  let phase: 'ready' | 'failed' | 'retrying' | 'revoked' = 'ready';
  let acknowledgments = 0;
  const retryGate = barrier();
  let retryHeld = 0;
  await page.route('**/api/me/inbox', async (route) => {
    if (phase === 'revoked')
      return route.fulfill({
        status: 403,
        json: { error: 'Activity access is no longer available.' },
      });
    if (phase === 'failed')
      return route.fulfill({
        status: 503,
        json: { error: 'Activity is temporarily unavailable.' },
      });
    if (phase === 'retrying') {
      retryHeld++;
      await retryGate.held;
    }
    return route.fulfill({ json: { items: [item] } });
  });
  await page.route(`**/api/me/inbox/${item.id}/read`, (route) => {
    acknowledgments++;
    item.readAt = new Date().toISOString();
    return route.fulfill({ json: {} });
  });
  await page.route('**/api/attention?*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: 'pending-judgment',
            kind: 'judgment',
            source: 'work_item',
            title: 'Choose the Notes release date',
            summary: 'A decision is still required.',
            roomId: room.id,
            roomName: room.name,
            status: 'waiting_for_person',
            reason: 'Question pending',
            priority: 1,
            createdAt: item.createdAt,
            updatedAt: item.createdAt,
            target: {
              app: 'work',
              roomId: room.id,
              workItemId: 'pending-judgment-matter',
            },
          },
        ],
        total: 1,
        counts: { judgment: 1, review: 0, exception: 0 },
        nextCursor: '',
        hasMore: false,
      },
    }),
  );
  try {
    await errors.get(page)!.reload();
    const input = workspace(page).getByRole('textbox', {
      name: `Message ${room.name}`,
      exact: true,
    });
    const draft = 'Keep my unfinished discussion while Activity recovers.';
    await input.fill(draft);
    let panel = await openInboxActivity(page);
    await expect(panel.getByText(item.title!, { exact: true })).toBeVisible();
    await panel
      .getByRole('button', { name: 'Close inbox', exact: true })
      .click();
    phase = 'failed';
    await openRoomMore(page, 'Refresh');
    panel = await openInboxActivity(page);
    const failure = panel
      .getByRole('alert')
      .filter({ hasText: 'Activity is temporarily unavailable.' });
    await expect(failure).toBeVisible();
    await expect(panel.getByText(item.title!, { exact: true })).toBeVisible();
    await expect(
      panel.getByText('No activity yet.', { exact: true }),
    ).toHaveCount(0);
    await expect(input).toHaveValue(draft);
    phase = 'retrying';
    await failure
      .getByRole('button', { name: 'Retry Activity', exact: true })
      .click();
    await expect.poll(() => retryHeld).toBeGreaterThan(0);
    await expect(failure).toBeVisible();
    await expect(panel.getByText(item.title!, { exact: true })).toBeVisible();
    retryGate.release();
    await expect(failure).toHaveCount(0);
    await expect(input).toHaveValue(draft);
    await panel.getByRole('button').filter({ hasText: item.title! }).click();
    await expect(panel).toHaveCount(0);
    await expect(workspace(page)).toHaveClass(/is-active/);
    await expect(input).toHaveValue(draft);
    await expect.poll(() => acknowledgments).toBe(1);
    await expect(inboxToggle(page)).toContainText('Needs me (1)');
    phase = 'revoked';
    await openRoomMore(page, 'Refresh');
    panel = await openInboxActivity(page);
    await expect(panel.getByRole('alert')).toContainText(
      'Activity access is no longer available.',
    );
    await expect(panel.getByText(item.title!, { exact: true })).toHaveCount(0);
    await expect(
      panel.getByText('No activity yet.', { exact: true }),
    ).toHaveCount(0);
    await expect(input).toHaveValue(draft);
  } finally {
    retryGate.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});
