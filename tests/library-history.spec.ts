import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  openApp,
  signIn,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

test('Library navigation cannot show another source’s history while the new read is pending', async ({
  page,
  browserName,
}, info) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const source = await createRoom(page, `history-source-${info.project.name}`);
  const initial = await (
    await page.request.get(`/api/resources/${source.resourceId}/files`)
  ).json();
  const saved = await mutate(
    page,
    `/resources/${source.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: initial.commit,
      path: 'source-only.md',
      content: 'This version belongs only to the first source.',
    },
    'PUT',
  );
  expect(saved.ok()).toBe(true);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  const rows = library(page).locator('.camp-library-history article');
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText('source-only.md');

  await openApp(page, 'Workspace');
  const destination = await createRoom(
    page,
    `history-destination-${info.project.name}`,
  );
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  for (const endpoint of ['files', 'history']) {
    await page.route(
      `**/api/resources/${destination.resourceId}/${endpoint}`,
      async (route) => {
        await gate;
        return route.fallback();
      },
    );
  }
  try {
    await workspace(page)
      .getByRole('button', { name: 'Files', exact: true })
      .click();
    await expect(library(page).locator('.p9-window-title')).toHaveText(
      `${destination.name} — Library`,
    );
    await library(page)
      .getByRole('button', { name: 'History', exact: true })
      .click();
    await expect(
      library(page).getByRole('status').filter({ hasText: 'Loading history…' }),
    ).toBeVisible();
    await expect(rows).toHaveCount(0);
    await expect(
      library(page).getByText('source-only.md', { exact: true }),
    ).toHaveCount(0);
    release();
    await expect(
      library(page).getByText('No published changes yet.', { exact: true }),
    ).toBeVisible();
    await expect(rows).toHaveCount(0);
    await expect(library(page).locator('.p9-window-title')).toHaveText(
      `${destination.name} — Library`,
    );
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
  expect(errors.errors).toEqual([]);
});
