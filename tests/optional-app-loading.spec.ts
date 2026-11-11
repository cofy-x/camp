import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import {
  createRoom,
  openApp,
  settings,
  signIn,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

for (const changedBuild of [false, true])
  test(`an optional app ${changedBuild ? 'does not mix a newer build after a chunk failure' : 'retries a failed chunk'} without reloading or losing another window draft`, async ({
    page,
    browserName,
  }) => {
    const errors = watchPageErrors(page, browserName);
    await signIn(page);
    const room = await createRoom(
      page,
      `Optional app recovery ${randomUUID().slice(0, 8)}`,
    );
    const composer = workspace(page).getByRole('textbox', {
      name: `Message ${room.name}`,
      exact: true,
      includeHidden: true,
    });
    const draft = 'Keep this private draft while another window retries.';
    await composer.fill(draft);

    let chunkReads = 0;
    let documentReads = 0;
    const submissions: string[] = [];
    page.on('request', (request) => {
      if (
        request.isNavigationRequest() &&
        request.resourceType() === 'document'
      )
        documentReads++;
    });
    await page.route('**/api/rooms/*/messages', (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      submissions.push(route.request().url());
      return route.abort('blockedbyclient');
    });
    await page.route(/\/assets\/Settings-[^/?]+\.js(?:\?.*)?$/, (route) => {
      chunkReads++;
      return chunkReads === 1 ? route.abort('failed') : route.continue();
    });
    if (changedBuild)
      await page.route('**/.vite/manifest.json', async (route) => {
        const response = await route.fetch();
        const manifest = await response.json();
        manifest['index.html'].file = 'assets/newer-document-entry.js';
        await route.fulfill({ response, json: manifest });
      });

    await openApp(page, 'Settings');
    await expect(settings(page).getByRole('alert')).toContainText(
      'Could not open this window.',
    );
    await expect(composer).toHaveValue(draft);
    await settings(page)
      .getByRole('button', { name: 'Try Again', exact: true })
      .click();
    if (changedBuild) {
      await expect(settings(page).getByRole('alert')).toContainText(
        'Camp was updated. Refresh Camp to open this window.',
      );
      expect(chunkReads).toBe(1);
    } else {
      await expect(settings(page).getByRole('main')).toBeVisible();
      await expect(settings(page).getByRole('alert')).toHaveCount(0);
      expect(chunkReads).toBe(2);
      await settings(page)
        .getByRole('button', { name: /^Close / })
        .first()
        .click();
      await openApp(page, 'Settings');
      await expect(settings(page).getByRole('main')).toBeVisible();
      expect(chunkReads).toBe(2);
    }
    await expect(composer).toHaveValue(draft);
    expect(documentReads).toBe(0);
    expect(submissions).toEqual([]);
    expect(errors.errors).toEqual([]);
  });
