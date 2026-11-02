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
