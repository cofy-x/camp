import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { Task } from '../apps/dashboard/src/features/collaboration/api';
import type { Message } from '../apps/dashboard/src/features/workspace/api';
import { createRoom, mutate, signIn, work, workspace } from './team-helpers';

async function fixture(page: Page, projected: boolean) {
  await signIn(page);
  const room = await createRoom(
    page,
    `Work history ${randomUUID().slice(0, 8)}`,
  );
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const goal = 'Build and release the shared notes application.';
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
  });
  expect(response.ok()).toBe(true);
  const { item } = await response.json();
  const now = new Date().toISOString();
  const messages: Message[] = Array.from({ length: 4 }, (_, index) => ({
    id: `origin-${randomUUID()}`,
    roomId: room.id,
    taskId: `task-${randomUUID()}`,
    threadRootId: '',
    authorKind: 'user',
    authorUserId: user.id,
    authorName: index === 3 ? 'Recorded owner' : '',
    agentId: '',
    runId: '',
    content: goal,
    requestId: '',
    references: [],
    seq: index + 1,
    createdAt: now,
    updatedAt: now,
    ...(projected ? { workOrigin: { workItemId: item.id } } : {}),
  }));
  const tasks: Task[] = messages.map((message) => ({
    id: message.taskId!,
    workItemId: item.id,
    roomId: room.id,
    resourceId: room.resourceId,
    threadRootId: message.id,
    originMessageId: message.id,
    requestedByUserId: user.id,
    goal,
    status: 'completed',
    revision: 1,
    participants: [],
    currentAgentId: '',
    activeRunId: '',
    baseSharedCommit: '',
    checkpointCommit: '',
    publishedCommit: '',
    error: '',
    summary: '',
    turnsUsed: 1,
    maxTurns: 6,
    modelRequestsUsed: 2,
    maxModelRequests: 24,
    inputRevision: 0,
    inputBytes: 0,
    coveredInputRevision: 0,
    createdAt: now,
    updatedAt: now,
  }));
  const root = messages[0];
  const answer: Message = {
    ...root,
    id: `answer-${randomUUID()}`,
    threadRootId: root.id,
    authorName: 'Recorded colleague',
    content: 'Use the browser-first release.',
    workOrigin: undefined,
    seq: 6,
  };
  const ordinary: Message = {
    ...root,
    id: `discussion-${randomUUID()}`,
    authorName: 'Recorded colleague',
    taskId: undefined,
    workOrigin: undefined,
    seq: 5,
  };
  await page.route(`**/api/rooms/${room.id}/messages?*`, (route) => {
    const thread = new URL(route.request().url()).searchParams.get(
      'threadRootId',
    );
    return route.fulfill({
      json: {
        messages: thread ? [root, answer] : [...messages, ordinary],
        through: Math.max(
          root.seq,
          answer.seq,
          ordinary.seq,
          ...messages.map((message) => message.seq),
        ),
      },
    });
  });
  await page.route(`**/api/rooms/${room.id}/messages/${root.id}`, (route) =>
    route.fulfill({ json: { message: root } }),
  );
  await page.route(`**/api/rooms/${room.id}/runs?*`, (route) =>
    route.fulfill({ json: { items: [], tasks } }),
  );
  await page.route(`**/api/rooms/${room.id}/tasks?*`, (route) =>
    projected
      ? route.fulfill({
          status: 503,
          json: { error: 'Task history unavailable.' },
        })
      : route.fulfill({ json: { tasks } }),
  );
  await page.reload();
  return { room, root, answer, ordinary, goal };
}

test('derived Work origins stay compact without Task history and retain original source attribution', async ({
  page,
}, info) => {
  const value = await fixture(page, true);
  const events = workspace(page).getByRole('article', {
    name: 'Work started',
    exact: true,
  });
  await expect(events).toHaveCount(4);
  await expect(events.locator('.workspace-message-avatar')).toHaveCount(0);
  await expect(events.filter({ hasText: 'Team member' })).toHaveCount(0);
  await expect(events.last()).toContainText('Recorded owner started work');
  await expect(events.first()).not.toContainText('Browser Owner');
  await expect(
    events.first().getByRole('button', { name: 'Open work', exact: true }),
  ).toHaveCount(1);
  const ordinary = workspace(page).locator(
    `[data-message-id="${value.ordinary.id}"]`,
  );
  await expect(ordinary.locator('.workspace-message-avatar')).toBeVisible();
  await expect(ordinary).not.toContainText('started work');

  await workspace(page).screenshot({
    path: info.outputPath('work-origin-events.png'),
  });
  const draft = page.getByLabel(`Message ${value.room.name}`, { exact: true });
  await draft.fill('Keep my unfinished question.');
  await events
    .first()
    .getByRole('button', { name: 'More event actions', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Ask agent…', exact: true }).click();
  await expect(draft).toHaveValue('Keep my unfinished question.');
  const reference = workspace(page).getByRole('region', {
    name: 'Draft references',
    exact: true,
  });
  await expect(reference).toContainText("Team member's message");
  await reference.locator('summary').first().click();
  await expect(reference.locator('blockquote')).toHaveText(value.goal);
  await reference
    .getByRole('button', { name: 'Open source', exact: true })
    .click();
  const origin = workspace(page).locator(
    `[data-message-id="${value.root.id}"]`,
  );
  await expect(origin).toHaveClass(/is-source/);
  await expect(origin).toBeFocused();
  await origin.getByRole('button', { name: 'Open work', exact: true }).click();
  await expect(work(page)).toBeVisible();
  await expect(work(page)).toContainText(value.goal);
});

test('legacy task lookup requires the exact origin and keeps user inputs as messages', async ({
  page,
}) => {
  const value = await fixture(page, false);
  const origin = workspace(page).locator(
    `[data-message-id="${value.root.id}"]`,
  );
  await expect(origin).toHaveAttribute('aria-label', 'Work started');
  await origin
    .getByRole('button', { name: 'More event actions', exact: true })
    .click();
  await page
    .getByRole('menuitem', { name: 'Reply in thread', exact: true })
    .click();
  const answer = workspace(page).locator(
    `[data-message-id="${value.answer.id}"]`,
  );
  await expect(answer).toBeVisible();
  await expect(answer).toHaveAttribute('aria-label', 'Recorded colleague');
  await expect(answer.locator('.workspace-message-avatar')).toBeVisible();
  await expect(answer).toContainText(value.answer.content);
  await expect(answer).not.toContainText('started work');
});
