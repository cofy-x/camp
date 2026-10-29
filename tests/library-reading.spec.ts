import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Locator, type Page } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

test.beforeEach(async ({ page }) => {
  await signIn(page);
});

async function openLibrary(page: Page) {
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await expect(library(page)).toBeVisible();
}

async function expectNoHorizontalOverflow(win: Locator) {
  const overflow = await win.locator('.camp-library').evaluate((element) => {
    const main = element.querySelector('.camp-library-main')!;
    return {
      app: element.scrollWidth - element.clientWidth,
      main: main.scrollWidth - main.clientWidth,
    };
  });
  expect(overflow.app).toBeLessThanOrEqual(1);
  expect(overflow.main).toBeLessThanOrEqual(1);
}

async function expectOneReadingScroller(win: Locator) {
  await expect
    .poll(async () =>
      win.locator('.workspace-preview-content').evaluate((element) => {
        const scrolls: string[] = [];
        let current: HTMLElement | null = element as HTMLElement;
        while (current && !current.classList.contains('camp-library')) {
          if (
            ['auto', 'scroll'].includes(getComputedStyle(current).overflowY) &&
            current.scrollHeight > current.clientHeight + 1
          )
            scrolls.push(current.className);
          current = current.parentElement;
        }
        return scrolls.length;
      }),
    )
    .toBe(1);
}

test('an empty resource can configure its environment without starting agent work', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(page, `environment-${testInfo.project.name}`);
  await openLibrary(page);
  const win = library(page);
  await expect(
    win.getByRole('heading', { name: /^No files in/ }),
  ).toBeVisible();
  const actions = win.getByRole('button', {
    name: 'More Library actions',
    exact: true,
  });
  await expect(
    win.locator('.camp-library-toolbar > .app-actions').getByRole('button'),
  ).toHaveCount(3);
  await actions.focus();
  await actions.press('ArrowDown');
  await expect(page.getByRole('menu')).toBeFocused();
  await page.getByRole('menu').press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(actions).toBeFocused();
  if (isMobile) await actions.tap();
  else await actions.click();
  await expectNoHorizontalOverflow(win);
  await page.screenshot({ path: testInfo.outputPath('library-actions.png') });
  await page
    .getByRole('menuitem', { name: 'Environment…', exact: true })
    .click();
  const environment = win.getByRole('region', {
    name: 'Environment and checks',
  });
  await expect(
    environment.getByText('Not configured', { exact: true }),
  ).toBeVisible();
  await environment
    .getByRole('combobox', { name: 'Environment', exact: true })
    .selectOption('node');
  await environment
    .getByRole('combobox', { name: 'Package manager', exact: true })
    .selectOption('pnpm@12.11.2');
  await environment
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    environment.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  await environment
    .getByRole('combobox', { name: 'Package manager', exact: true })
    .selectOption('node');
  await expect(
    environment.getByText('Environment saved.', { exact: true }),
  ).toHaveCount(0);
  await expectNoHorizontalOverflow(win);
  await page.screenshot({
    path: testInfo.outputPath('library-environment.png'),
  });

  const changed = await mutate(
    page,
    `/resources/${room.resourceId}`,
    { environment: { kind: 'command', checks: [] } },
    'PATCH',
  );
  expect(changed.ok()).toBeTruthy();
  await expect(
    environment.getByRole('button', {
      name: 'Reload Saved Settings',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    environment.getByRole('combobox', { name: 'Environment', exact: true }),
  ).toHaveValue('node');
  await expect(
    environment.getByRole('combobox', { name: 'Package manager', exact: true }),
  ).toHaveValue('node');
  await expect(
    environment.getByRole('button', { name: 'Save Environment', exact: true }),
  ).toBeDisabled();
  await environment
    .getByRole('button', { name: 'Reload Saved Settings', exact: true })
    .click();
  await expect(
    environment.getByRole('combobox', { name: 'Environment', exact: true }),
  ).toHaveValue('command');

  await environment
    .getByRole('combobox', { name: 'Environment', exact: true })
    .selectOption('none');
  await environment
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    environment.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  await environment
    .getByRole('button', { name: 'Back to Files', exact: true })
    .click();
  await expect(
    win.getByRole('heading', { name: /^No files in/ }),
  ).toBeVisible();
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
  const resources = (await (await page.request.get('/api/library')).json())
    .resources;
  expect(
    resources.find(
      (resource: { id: string }) => resource.id === room.resourceId,
    ).environment,
  ).toEqual({});
});

test('Library renders readable Markdown and keeps file actions safe in small windows', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(page, `reading-${testInfo.project.name}`);
  const path = 'handoff/customer-onboarding-guide.md';
  const content = [
    '# Customer handoff',
    '',
    '| Role | Responsibility |',
    '| --- | --- |',
    '| Onboarding Lead | Own the plan and remove blockers. |',
    '| Customer Champion | Confirm the handoff with the team. |',
    '',
    '- [x] Account prepared',
    '- [ ] Review requested',
    '',
    ...Array.from(
      { length: 24 },
      (_, index) =>
        `## Handoff step ${index + 1}\n\nRecord the owner, source material and next action. Keep the original evidence available to the reviewer.`,
    ),
    '',
    'Document end marker.',
  ].join('\n');
  const listing = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const published = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: listing.commit,
      path,
      content,
    },
    'PUT',
  );
  expect(published.ok()).toBeTruthy();
  await openLibrary(page);
  const win = library(page);
  const preview = win.getByRole('region', { name: `Preview ${path}` });
  await expect(preview.getByRole('table')).toBeVisible();
  await expect(
    preview.getByRole('columnheader', { name: 'Responsibility' }),
  ).toBeVisible();
  await expect(
    preview.getByRole('cell', { name: 'Own the plan and remove blockers.' }),
  ).toBeVisible();
  const checks = preview.getByRole('checkbox');
  await expect(checks).toHaveCount(2);
  await expect(checks.nth(0)).toBeChecked();
  await expect(checks.nth(1)).not.toBeChecked();
  await expect(checks.nth(0)).toBeDisabled();
  await expectNoHorizontalOverflow(win);
  await expectOneReadingScroller(win);
  await page.screenshot({ path: testInfo.outputPath('library-reading.png') });

  await preview
    .getByRole('button', { name: 'Show source', exact: true })
    .click();
  await expect(preview.locator('pre')).toHaveText(content);
  await expect(preview.getByRole('table')).toHaveCount(0);
  const readingPane = win.locator('.camp-file-tab:not([hidden])');
  const readingPosition = await readingPane.evaluate((element) => {
    element.scrollTop = 180;
    return element.scrollTop;
  });
  expect(readingPosition).toBeGreaterThan(0);
  const previewTab = win.getByRole('tab', { name: 'Preview', exact: true });
  await previewTab.focus();
  await previewTab.press('ArrowRight');
  const reviewsTab = win.getByRole('tab', {
    name: 'Reviews & Delivery',
    exact: true,
  });
  await expect(reviewsTab).toBeFocused();
  await expect(
    win.getByRole('region', { name: 'Document reviews' }),
  ).toContainText('No reviews yet.');
  await reviewsTab.press('ArrowLeft');
  await expect(previewTab).toBeFocused();
  await expect(preview.locator('pre')).toHaveText(content);
  expect(await readingPane.evaluate((element) => element.scrollTop)).toBe(
    readingPosition,
  );
  await preview
    .getByRole('button', { name: 'Show preview', exact: true })
    .click();
  await expect(preview.getByRole('table')).toBeVisible();

  await win
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  const request = win.getByRole('form', { name: 'Request document review' });
  await expect(request).toContainText(path);
  await request.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(request).toHaveCount(0);
  expect(
    (await (await page.request.get(`/api/reviews?roomId=${room.id}`)).json())
      .reviews,
  ).toEqual([]);

  // No private document draft exists: reusing a filename must read its saved bytes.
  await win.getByRole('button', { name: 'New Document', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByLabel('File path', { exact: true })
    .fill(path);
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Create Draft', exact: true })
    .click();
  await expect(win.getByLabel('Document content', { exact: true })).toHaveValue(
    content,
  );
  await win
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
  await expect(preview.getByRole('table')).toBeVisible();

  if (isMobile) {
    await page.setViewportSize({ width: 390, height: 480 });
  } else {
    const edge = (await win.locator('[data-resize="se"]').boundingBox())!;
    const before = (await win.boundingBox())!;
    await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      edge.x + edge.width / 2 - (before.width - 560),
      edge.y + edge.height / 2 - (before.height - 360),
      { steps: 5 },
    );
    await page.mouse.up();
  }
  await expectNoHorizontalOverflow(win);
  await expectOneReadingScroller(win);
  const end = preview.getByText('Document end marker.', { exact: true });
  await end.scrollIntoViewIfNeeded();
  await expect(end).toBeInViewport();
  await win
    .getByRole('button', { name: 'Request Review…', exact: true })
    .scrollIntoViewIfNeeded();
  await expect(
    win.getByRole('button', { name: 'Request Review…', exact: true }),
  ).toBeInViewport();
  await page.screenshot({
    path: testInfo.outputPath('library-short-window.png'),
  });
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});

test('binary working copies keep exact bytes and do not offer text editing', async ({
  page,
  isMobile,
  request,
}, testInfo) => {
  const room = await createRoom(page, `binary-${testInfo.project.name}`);
  const filename = `diagram-${testInfo.project.name}.png`;
  const bytes = Buffer.from([
    0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0x80,
  ]);
  await workspace(page)
    .getByLabel('Choose files to attach', { exact: true })
    .setInputFiles({ name: filename, mimeType: 'image/png', buffer: bytes });
  await expect(
    workspace(page)
      .getByRole('form', { name: 'Room composer' })
      .getByText(filename, { exact: true }),
  ).toBeVisible();
  await openLibrary(page);
  const win = library(page);
  if (isMobile)
    await win.getByRole('button', { name: 'Sources', exact: true }).tap();
  await win
    .getByRole('complementary', { name: 'Library resources' })
    .getByRole('button')
    .filter({ hasText: filename })
    .click();
  await expect(
    win.getByRole('button', { name: 'New Document', exact: true }),
  ).toBeDisabled();
  await win
    .getByRole('button', { name: 'More Library actions', exact: true })
    .click();
  await expect(
    page.getByRole('menuitem', { name: 'Environment…', exact: true }),
  ).toHaveCount(0);
  await page.getByRole('menu').press('Escape');
  const originalURL = await win
    .getByRole('link', { name: `Download ${filename}`, exact: true })
    .getAttribute('href');
  expect(originalURL).toBeTruthy();
  await win
    .getByRole('button', { name: 'Create Working Copy…', exact: true })
    .click();
  const dialog = page.getByRole('dialog');
  await dialog
    .getByLabel('File path', { exact: true })
    .fill('assets/reference.png');
  await dialog
    .getByRole('button', { name: 'Create Copy', exact: true })
    .click();
  await expect(
    win.getByRole('heading', { name: 'assets/reference.png', exact: true }),
  ).toBeVisible();
  await expect(
    win.getByText('This file has no text preview.', { exact: false }),
  ).toBeVisible();
  await expect(
    win.getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeDisabled();
  const downloadLink = win.getByRole('link', {
    name: 'Download reference.png',
    exact: true,
  });
  const downloadURL = await downloadLink.getAttribute('href');
  const download = page.waitForEvent('download');
  await downloadLink.click();
  expect(await readFile((await (await download).path())!)).toEqual(bytes);
  expect(await (await page.request.get(originalURL!)).body()).toEqual(bytes);
  expect((await request.get(downloadURL!)).status()).toBe(401);
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
  await expectNoHorizontalOverflow(win);
  await page.screenshot({ path: testInfo.outputPath('library-binary.png') });
});
