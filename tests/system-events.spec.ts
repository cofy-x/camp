import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { systemEventScene } from './system-event-helpers';
import { mutate, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

test('review events preserve exact sources, drafts and explicit work admission', async ({
  page,
  browserName,
}, info) => {
  const errors: string[] = [];
  watchPageErrors(page, browserName, errors);
  const { room, review, selfReview, commit, memberAvatar } =
    await systemEventScene(page, info.project.name);
  const self = workspace(page).locator(
    `[data-message-id="${selfReview.threadRootId}"]`,
  );
  const event = workspace(page).locator(
    `[data-message-id="${review.threadRootId}"]`,
  );
  await expect(self).toContainText('started a self-review');
  await expect(event).toHaveAttribute('aria-label', 'Document review event');
  await expect(event.getByText('Camp', { exact: true })).toHaveCount(0);
  await expect(event.locator('.workspace-message-avatar')).toHaveCount(0);
  await expect(
    event.getByRole('button', { name: 'Open Review', exact: true }),
  ).toBeVisible();
  await expect(
    event.getByRole('region', { name: 'Message references', exact: true }),
  ).toHaveCount(0);
  await expect(
    workspace(page).locator('.author-user img').first(),
  ).toHaveAttribute('src', `/api/avatars/${memberAvatar}`);

  let workSubmissions = 0;
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/api\/rooms\/[^/]+\/(messages|tasks)$/.test(
        new URL(request.url()).pathname,
      )
    )
      workSubmissions++;
  });
  const composer = workspace(page).getByRole('form', {
    name: 'Room composer',
    exact: true,
  });
  const draft = page.getByLabel(`Message ${room.name}`, { exact: true });
  await draft.fill('Keep my unfinished review question.');
  const more = event.getByRole('button', {
    name: 'More event actions',
    exact: true,
  });
  await more.focus();
  await more.press('ArrowDown');
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  await more.click();
  await page
    .getByRole('menuitem', { name: 'View referenced files', exact: true })
    .click();
  const source = event.locator('.workspace-reference-card summary');
  await expect(source).toBeFocused();
  await source.click();
  await expect(event).toContainText('Start with a small customer pilot.');
  const changed = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: commit,
      path: 'launch-plan.md',
      content: '# Launch plan\n\nA later shared revision.\n',
    },
    'PUT',
  );
  expect(changed.ok()).toBeTruthy();
  await expect(event).toContainText('Shared files use a different version');
  await expect(event).toContainText('Start with a small customer pilot.');
  await expect(event).not.toContainText('A later shared revision.');
  const download = event.getByRole('link', {
    name: 'Download launch-plan.md',
    exact: true,
  });
  await expect(download).toHaveAttribute('href', new RegExp(commit));
  await expect(draft).toHaveValue('Keep my unfinished review question.');

  await more.click();
  await page.getByRole('menuitem', { name: 'Ask agent…', exact: true }).click();
  await expect(composer).toBeVisible();
  await expect(draft).toHaveValue('Keep my unfinished review question.');
  await composer
    .getByRole('region', { name: 'Draft references', exact: true })
    .locator('summary')
    .first()
    .click();
  await expect(
    composer.getByRole('region', { name: 'Draft references', exact: true }),
  ).toContainText('Browser Owner requested Morgan');
  await more.click();
  await page
    .getByRole('menuitem', { name: 'Reply in thread', exact: true })
    .click();
  await expect(
    workspace(page).getByRole('form', { name: 'Thread composer', exact: true }),
  ).toBeVisible();
  await workspace(page)
    .getByRole('button', { name: 'More event actions', exact: true })
    .click();
  await expect(
    page.getByRole('menuitem', { name: 'Reply in thread', exact: true }),
  ).toHaveCount(0);
  await page.keyboard.press('Escape');
  await workspace(page)
    .getByRole('button', { name: 'Back to Room', exact: true })
    .click();
  await expect(draft).toHaveValue('Keep my unfinished review question.');
  await event.getByRole('button', { name: 'Open Review', exact: true }).click();
  const detail = workspace(page).getByRole('region', {
    name: 'Document review',
    exact: true,
  });
  await expect(detail).toContainText('Launch plan');
  await expect(
    detail.getByRole('button', { name: 'Approve', exact: true }),
  ).toHaveCount(0);
  expect(workSubmissions).toBe(0);
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toHaveLength(0);
  expect(errors).toEqual([]);
});

test('system handoffs retain queued work and failures before an Agent reply arrives', async ({
  page,
}, info) => {
  const { room } = await systemEventScene(page, `handoff-${info.project.name}`);
  const { agents } = await (await page.request.get('/api/agents')).json();
  const writer = agents.find(
    (agent: { name: string }) => agent.name === 'Writer',
  );
  const { messages } = await (
    await page.request.get(`/api/rooms/${room.id}/messages`)
  ).json();
  const now = new Date().toISOString();
  const eventId = randomUUID();
  const taskId = randomUUID();
  const event = {
    id: eventId,
    roomId: room.id,
    taskId,
    threadRootId: '',
    authorKind: 'system',
    authorUserId: '',
    authorName: '',
    agentId: '',
    runId: '',
    content: 'Next: Writer. Finish the checked launch plan.',
    requestId: '',
    references: [],
    seq: messages.at(-1).seq + 1,
    createdAt: now,
    updatedAt: now,
  };
  const run = {
    id: randomUUID(),
    roomId: room.id,
    taskId,
    originMessageId: eventId,
    threadRootId: '',
    agentId: writer.id,
    agentSnapshot: writer,
    resourceId: room.resourceId,
    runnerName: 'Personal Runner',
    status: 'queued',
    waitingReason: 'Personal Runner is offline.',
    prompt: '',
    references: [],
    createdAt: now,
    updatedAt: now,
    finishedAt: '',
    error: '',
    output: '',
    sessionId: '',
    sessionMode: '',
    baselineCommit: '',
    resultCommit: '',
    environment: null,
    checks: [],
    files: [],
    exitCode: null,
    testExitCode: null,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    sessionGeneration: 0,
    published: false,
  };
  await page.route(`**/api/rooms/${room.id}/messages?*`, (route) =>
    route.fulfill({ json: { messages: [...messages, event] } }),
  );
  await page.route(`**/api/rooms/${room.id}/runs`, (route) =>
    route.fulfill({ json: { runs: [run] } }),
  );
  await page.reload();
  const activity = workspace(page).locator(`[data-message-id="${eventId}"]`);
  const work = activity.getByTestId('agent-work');
  await expect(work).toHaveAttribute('data-state', 'queued');
  await expect(work).toContainText('Writer');
  await expect(work).toContainText('Personal Runner is offline.');
  await expect(
    activity.getByRole('button', { name: 'Open collaboration', exact: true }),
  ).toHaveCount(1);
  run.status = 'failed';
  run.error = 'Runner disconnected; no files were published.';
  run.updatedAt = new Date().toISOString();
  await page.reload();
  await expect(work).toHaveAttribute('data-state', 'failed');
  await expect(work).toContainText(run.error);
  await expect(
    work.getByRole('button', { name: 'Details', exact: true }),
  ).toBeVisible();
});
