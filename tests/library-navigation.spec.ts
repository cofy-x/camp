import { expect, test, type Locator } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  settings,
  signIn,
  workspace,
} from './team-helpers';

test('Library Sources preserves focus and keeps navigation accessible in a short window', async ({
  page,
  isMobile,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(page, `navigation-${testInfo.project.name}`);
  let destination = '';
  for (let index = 0; index < 7; index++) {
    destination = `navigation-source-${testInfo.project.name}-${index}`;
    const response = await mutate(page, '/rooms', {
      name: destination,
      description: '',
    });
    expect(response.ok()).toBeTruthy();
  }
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await page.setViewportSize({ width: 390, height: 430 });
  const win = library(page);
  const sources = win.getByRole('button', { name: 'Sources', exact: true });
  const panel = win.getByRole('complementary', {
    name: 'Library resources',
  });
  const selected = panel.locator('button[aria-current="true"]');
  const activate = (target: Locator) =>
    isMobile ? target.tap() : target.click();

  await expect(win.locator('.camp-library-toolbar')).toContainText(
    `${room.name} files`,
  );
  await activate(sources);
  await expect(sources).toHaveAttribute('aria-expanded', 'true');
  await expect(selected).toContainText(`${room.name} files`);
  await expect(selected).toBeFocused();
  await expect(selected).toBeInViewport();
  await selected.press('Escape');
  await expect(panel).toBeHidden();
  await expect(sources).toHaveAttribute('aria-expanded', 'false');
  await expect(sources).toBeFocused();

  await activate(sources);
  await activate(panel.getByRole('button', { name: 'Done', exact: true }));
  await expect(panel).toBeHidden();
  await expect(sources).toBeFocused();

  await activate(sources);
  const backdrop = win.getByRole('button', {
    name: 'Close Library sources',
    exact: true,
  });
  const bounds = (await backdrop.boundingBox())!;
  const position = { x: bounds.width - 4, y: bounds.height / 2 };
  if (isMobile) await backdrop.tap({ position });
  else await backdrop.click({ position });
  await expect(panel).toBeHidden();
  await expect(sources).toBeFocused();

  await activate(sources);
  const storageAction = panel.getByRole('button', {
    name: 'Storage Settings…',
    exact: true,
  });
  await storageAction.focus();
  await storageAction.press('Tab');
  await expect(panel).toBeHidden();
  await expect(sources).toBeFocused();

  await activate(sources);
  const list = panel.locator('.camp-library-source-list');
  const overflow = await list.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
    return element.scrollHeight - element.clientHeight;
  });
  expect(overflow).toBeGreaterThan(0);
  const storage = panel.getByRole('button', {
    name: 'Storage Settings…',
    exact: true,
  });
  await expect(storage).toBeInViewport();
  await expect(
    panel.getByRole('button', { name: 'Done', exact: true }),
  ).toBeInViewport();
  await page.screenshot({
    path: testInfo.outputPath('library-sources-short.png'),
  });

  await activate(
    panel.getByRole('button').filter({ hasText: `${destination} files` }),
  );
  await expect(panel).toBeHidden();
  await expect(sources).toBeFocused();
  await expect(win.locator('.camp-library-toolbar')).toContainText(
    `${destination} files`,
  );
  await activate(sources);
  await expect(selected).toContainText(`${destination} files`);
  await expect(selected).toBeFocused();
  await expect(selected).toBeInViewport();
  await activate(storage);
  await expect(
    settings(page).getByRole('heading', { name: 'Storage', exact: true }),
  ).toBeVisible();
});

test('a delayed source change does not steal focus after leaving the drawer', async ({
  page,
}, testInfo) => {
  await signIn(page);
  await createRoom(page, `source-focus-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await page.setViewportSize({ width: 390, height: 600 });
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saving = false;
  await page.route('**/api/me/drafts/document', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    saving = true;
    await held;
    await route.fulfill({ response });
  });
  try {
    await newDocument(page, 'focus.md', 'Keep this private draft.');
    const win = library(page);
    await win.getByRole('button', { name: 'Sources', exact: true }).click();
    const panel = win.getByRole('complementary', { name: 'Library resources' });
    await panel.locator('button[aria-current="true"]').click();
    await expect.poll(() => saving).toBe(true);
    const destination = win.getByRole('button', {
      name: 'More Library actions',
      exact: true,
    });
    await destination.focus();
    await expect(panel).toBeHidden();
    release();
    await expect(
      win.getByLabel('Document content', { exact: true }),
    ).toHaveCount(0);
    await expect(destination).toBeFocused();
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('Library actions preserve a private document while refreshing and wait for its save before changing panels', async ({
  page,
  isMobile,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(page, `actions-draft-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  const win = library(page);
  const actions = win.getByRole('button', {
    name: 'More Library actions',
    exact: true,
  });
  const editor = win.getByLabel('Document content', { exact: true });
  const environment = win.getByRole('region', {
    name: 'Environment and checks',
  });
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saving = false;
  await page.route('**/api/me/drafts/document', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    saving = true;
    await held;
    await route.fulfill({ response });
  });
  try {
    await newDocument(page, 'private.md', 'Keep this private until I publish.');
    await expect.poll(() => saving).toBe(true);
    if (isMobile) await actions.tap();
    else await actions.click();
    await page.getByRole('menuitem', { name: 'Refresh', exact: true }).click();
    await expect(editor).toHaveValue('Keep this private until I publish.');
    await expect(actions).toBeFocused();

    await actions.press('Enter');
    await page.getByRole('menu').press('Enter');
    await expect(page.getByRole('menu')).toHaveCount(0);
    await expect(editor).toHaveValue('Keep this private until I publish.');
    await expect(environment).toHaveCount(0);
    release();
    await expect(environment).toBeVisible();
    await expect(editor).toHaveCount(0);
    const saved = await (
      await page.request.get(
        `/api/me/drafts/document?key=${encodeURIComponent(`${room.resourceId}:private.md`)}`,
      )
    ).json();
    expect(saved.draft.value.content).toBe(
      'Keep this private until I publish.',
    );
    expect(
      (
        await (
          await page.request.get(`/api/resources/${room.resourceId}/files`)
        ).json()
      ).files,
    ).toEqual([]);
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});
