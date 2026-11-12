import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Locator, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  openLibraryAction,
  mutate,
  post,
  signIn,
  workspace,
} from './team-helpers';

test.beforeEach(async ({ page }) => {
  await signIn(page);
});

async function openLibrary(page: Page) {
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
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
  await expect(win.locator('.p9-window-title')).toHaveText(
    `${room.name} — Library`,
  );
  const empty = win.locator('.camp-library-empty');
  const emptyText = empty.getByText('No files yet', { exact: true });
  await expect(emptyText).toBeVisible();
  const create = win.getByRole('button', { name: 'New Document', exact: true });
  await expect(create).toBeVisible();
  await expect(create).toBeEnabled();
  await expect(empty.getByRole('button')).toHaveCount(0);
  const [emptyBounds, iconBounds, textBounds, paneBounds] = await Promise.all([
    empty.boundingBox(),
    empty.locator('.p9-pixel-icon').boundingBox(),
    emptyText.boundingBox(),
    win.locator('.camp-file-selection').boundingBox(),
  ]);
  for (const bounds of [emptyBounds, iconBounds, textBounds, paneBounds])
    expect(bounds).not.toBeNull();
  const center = (bounds: NonNullable<typeof emptyBounds>) =>
    bounds.x + bounds.width / 2;
  expect(
    Math.abs(center(iconBounds!) - center(textBounds!)),
  ).toBeLessThanOrEqual(1);
  expect(
    Math.abs(center(iconBounds!) - center(paneBounds!)),
  ).toBeLessThanOrEqual(1);
  await create.click();
  const newDocument = page.getByRole('dialog');
  await expect(
    newDocument.getByLabel('File path', { exact: true }),
  ).toBeVisible();
  await newDocument
    .getByLabel('File path', { exact: true })
    .fill('not-created.md');
  await newDocument
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(newDocument).toHaveCount(0);
  await expect(emptyText).toBeVisible();
  await expect(
    win
      .getByRole('navigation', { name: 'Resource files', exact: true })
      .getByRole('button'),
  ).toHaveCount(0);
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
    environment.getByRole('combobox', { name: 'Environment', exact: true }),
  ).toHaveValue('none');
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
    {
      expectedRevision: (
        await (
          await page.request.get(`/api/resources/${room.resourceId}`)
        ).json()
      ).resource.revision,
      environment: { kind: 'command', checks: [] },
    },
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
  await page.screenshot({
    path: testInfo.outputPath('library-environment-conflict.png'),
  });
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
  await expect(win.getByText('No files yet', { exact: true })).toBeVisible();
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

test('file handoff retains the selected version through a background publication and stays unsent', async ({
  page,
}, info) => {
  const room = await createRoom(page, `exact-handoff-${info.project.name}`);
  const path = 'plan.md';
  const listing = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const first = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: listing.commit,
      path,
      content: '# Original plan\n\nKeep this exact version.',
    },
    'PUT',
  );
  expect(first.ok()).toBeTruthy();
  const { publication } = await first.json();
  let reviewReads = 0;
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/reviews') reviewReads++;
  });
  await openLibrary(page);
  const win = library(page);
  await expect(
    win.getByRole('region', { name: `Preview ${path}` }),
  ).toContainText('Original plan');
  const more = win.getByRole('button', {
    name: 'More Library actions',
    exact: true,
  });
  await more.click();
  await page
    .getByRole('menuitem', { name: 'Add to Message…', exact: true })
    .click();
  await page
    .getByRole('dialog', { name: 'Add to Message', exact: true })
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(more).toBeFocused();
  await more.focus();
  await more.press('ArrowDown');
  await page
    .getByRole('menuitem', { name: 'Add to Message…', exact: true })
    .press('Enter');
  const handoff = page.getByRole('dialog', {
    name: 'Add to Message',
    exact: true,
  });
  const newer = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: publication.commit,
      path,
      content: '# Newer plan\n\nA later revision.',
    },
    'PUT',
  );
  expect(newer.ok()).toBeTruthy();
  await expect(
    win.getByRole('region', { name: `Preview ${path}` }),
  ).toContainText('Newer plan');
  const chooser = handoff.getByRole('combobox', { name: 'Room', exact: true });
  if (info.project.name === 'touch') {
    await chooser.tap();
    await page
      .getByRole('listbox', { name: 'Room', exact: true })
      .getByRole('option', { name: room.name, exact: true })
      .tap();
    await handoff
      .getByRole('button', { name: 'Add to Message', exact: true })
      .tap();
  } else {
    await chooser.focus();
    await chooser.press('End');
    await chooser.press('Enter');
    await expect(chooser).toHaveValue(room.id);
    await handoff
      .getByRole('button', { name: 'Add to Message', exact: true })
      .click();
  }
  await expect(workspace(page)).toHaveClass(/is-active/);
  await expect(
    workspace(page)
      .getByRole('form', { name: 'Room composer' })
      .getByText(path, { exact: true }),
  ).toBeVisible();
  const messages = async () =>
    (await (await page.request.get(`/api/rooms/${room.id}/messages`)).json())
      .messages;
  expect(await messages()).toEqual([]);
  expect(reviewReads).toBe(0);
  await post(page, room.name, 'Keep the selected original plan.');
  const sent = (await messages()).at(-1);
  expect(sent.references[0]).toMatchObject({
    resourceId: room.resourceId,
    path,
    commit: publication.commit,
  });
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
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
  const saved = (await published.json()) as {
    publication: { commit: string };
  };
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
  const downloadLink = preview.getByRole('link', {
    name: 'Download customer-onboarding-guide.md',
    exact: true,
  });
  await expect(downloadLink).toHaveText('Download');
  await expect(downloadLink).toHaveAttribute(
    'download',
    'customer-onboarding-guide.md',
  );
  const downloadURL = new URL(
    (await downloadLink.getAttribute('href'))!,
    page.url(),
  );
  expect(downloadURL.pathname).toBe(`/api/resources/${room.resourceId}/file`);
  expect(downloadURL.searchParams.get('path')).toBe(path);
  expect(downloadURL.searchParams.get('commit')).toBe(saved.publication.commit);
  const [actionsBounds, downloadBounds] = await Promise.all([
    preview.locator('.workspace-preview-actions').boundingBox(),
    downloadLink.boundingBox(),
  ]);
  expect(actionsBounds).not.toBeNull();
  expect(downloadBounds).not.toBeNull();
  expect(
    Math.abs(
      actionsBounds!.x +
        actionsBounds!.width -
        downloadBounds!.x -
        downloadBounds!.width,
    ),
  ).toBeLessThanOrEqual(1);
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
  await expect(
    win.getByRole('tab', { name: 'Reviews & Delivery', exact: true }),
  ).toHaveCount(0);
  await expect(
    win.getByRole('button', { name: 'Request Review…', exact: true }),
  ).toHaveCount(0);
  await expect(
    win.getByRole('combobox', { name: 'Use in room', exact: true }),
  ).toHaveCount(0);
  await openLibraryAction(page, 'Review history…');
  await expect(
    win.getByRole('region', { name: 'Document reviews' }),
  ).toContainText('No reviews yet.');
  await win
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
  await expect(
    win.getByRole('button', { name: 'More Library actions', exact: true }),
  ).toBeFocused();
  await expect(preview.locator('pre')).toHaveText(content);
  expect(await readingPane.evaluate((element) => element.scrollTop)).toBe(
    readingPosition,
  );
  await preview
    .getByRole('button', { name: 'Show preview', exact: true })
    .click();
  await expect(preview.getByRole('table')).toBeVisible();

  await openLibraryAction(page, 'Request Review…');
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
    .getByRole('button', { name: 'Edit Text', exact: true })
    .scrollIntoViewIfNeeded();
  await expect(
    win.getByRole('button', { name: 'Edit Text', exact: true }),
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
  await expect(win.locator('.p9-window-title')).toHaveText(
    `${filename} — Library`,
  );
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
  await openLibraryAction(page, 'Create Working Copy', room.name);
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
