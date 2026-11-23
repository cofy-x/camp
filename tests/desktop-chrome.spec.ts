import { expect, test } from '@playwright/test';
import {
  createRoom,
  inboxToggle,
  library,
  openApp,
  settings,
  signIn,
  work,
  workspace,
} from './team-helpers';

test('Control Strip shortcuts reopen the current app windows and fit beside Inbox', async ({
  page,
  isMobile,
}, info) => {
  await signIn(page);
  const room = await createRoom(page, `Shortcut room ${info.project.name}`);
  const strip = page.getByRole('group', { name: 'Control Strip', exact: true });
  const shortcut = (name: string) =>
    strip.getByRole('button', { name: `Open ${name}`, exact: true });
  const launch = async (name: string) => {
    if (isMobile) await shortcut(name).tap();
    else await shortcut(name).press('Enter');
  };
  const composer = workspace(page).getByLabel(`Message ${room.name}`, {
    exact: true,
  });
  await composer.fill('Keep this Room draft while switching apps.');
  await expect(shortcut('Workspace')).toHaveAttribute('aria-current', 'true');
  await launch('Work');
  await expect(work(page)).toHaveClass(/is-active/);
  await expect(shortcut('Work')).toHaveAttribute('aria-current', 'true');
  await launch('Library');
  await expect(library(page)).toHaveClass(/is-active/);
  await expect(shortcut('Library')).toHaveAttribute('aria-current', 'true');
  await launch('Workspace');
  await expect(composer).toHaveValue(
    'Keep this Room draft while switching apps.',
  );
  await launch('Work');
  await expect(work(page)).toHaveCount(1);
  await launch('Library');
  await expect(library(page)).toHaveCount(1);

  if (isMobile) {
    await page.setViewportSize({ width: 320, height: 740 });
    const stripBounds = (await strip.boundingBox())!;
    const inboxBounds = (await inboxToggle(page).boundingBox())!;
    expect(stripBounds.x + stripBounds.width).toBeLessThan(inboxBounds.x);
    await launch('Workspace');
    await expect(composer).toHaveValue(
      'Keep this Room draft while switching apps.',
    );
  }

  const windows = page.getByRole('menubar').getByRole('menuitem', {
    name: 'Window',
    exact: true,
  });
  await windows.press('ArrowDown');
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(windows).toBeFocused();
});

test('Control Strip visibility survives reload and keeps shortcuts after a failed save', async ({
  page,
}) => {
  await signIn(page);
  const strip = page.getByRole('group', { name: 'Control Strip', exact: true });
  const handle = strip.getByRole('button', { name: /Control Strip/ });
  await handle.click();
  await expect(handle).toHaveAttribute('aria-expanded', 'false');
  await page.reload();
  await expect(handle).toHaveAttribute('aria-expanded', 'false');
  await expect(
    strip.getByRole('button', { name: 'Open Work', exact: true }),
  ).toHaveCount(0);
  await handle.press('Enter');
  await expect(handle).toHaveAttribute('aria-expanded', 'true');

  await page.route('**/api/preferences', (route) =>
    route.fulfill({
      status: 503,
      json: { error: 'Desktop options could not be saved.' },
    }),
  );
  await handle.click();
  await expect(page.getByRole('dialog')).toContainText(
    'Desktop options could not be saved.',
  );
  await expect(handle).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByRole('menubar').getByRole('status')).toHaveText(
    'Server connected',
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'OK', exact: true })
    .click();
  await expect(handle).toBeFocused();
});

test('The menu bar reports Server health without opening a menu', async ({
  page,
}) => {
  await page.clock.install();
  await signIn(page);
  const status = page.getByRole('menubar').getByRole('status');
  await expect(status).toHaveText('Server connected');
  await page.route('**/api/health', (route) =>
    route.fulfill({ status: 503, json: { error: 'Health read failed.' } }),
  );
  await page.clock.runFor(15_001);
  await expect(status).toHaveText('Server offline');
  await expect(status).toBeInViewport();
  await page.unroute('**/api/health');
  await page.clock.runFor(15_001);
  await expect(status).toHaveText('Server connected');
});

test('Help offers Getting Ready only to administrators', async ({ page }) => {
  await signIn(page);
  const help = page
    .getByRole('menubar')
    .getByRole('menuitem', { name: 'Help', exact: true });
  const gettingReady = page.getByRole('menuitem', {
    name: 'Getting Ready',
    exact: true,
  });
  await help.click();
  await expect(gettingReady).toBeVisible();
  await gettingReady.click();
  await expect(settings(page).locator('.p9-window-title')).toHaveText(
    'Getting Ready — Settings',
  );

  const status = await (await page.request.get('/api/auth/status')).json();
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({
      json: { ...status, user: { ...status.user, role: 'member' } },
    }),
  );
  await page.reload();
  await help.click();
  await expect(gettingReady).toHaveCount(0);
  await page
    .getByRole('menuitem', { name: 'About This Desktop…', exact: true })
    .click();

  const about = page.getByRole('region', {
    name: 'About This Desktop',
    exact: true,
  });
  const favicon = await page
    .locator('link[rel="icon"]')
    .evaluate((link: HTMLLinkElement) => link.href);
  await expect
    .poll(() =>
      about
        .locator('img')
        .evaluate((image: HTMLImageElement) =>
          image.naturalWidth > 0 ? image.currentSrc : null,
        ),
    )
    .toBe(favicon);
});

test('Inbox palette restores focus and File names the selected desktop application', async ({
  page,
  isMobile,
}, testInfo) => {
  await signIn(page);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const toggle = inboxToggle(page);
  const panel = page.getByRole('region', { name: 'Your inbox', exact: true });
  const activate = async (element: typeof toggle) =>
    isMobile ? element.tap() : element.click();
  await activate(toggle);
  await expect(panel).toBeVisible();
  const close = panel.getByRole('button', { name: 'Close inbox', exact: true });
  await expect(close).toBeFocused();
  await close.press('Escape');
  await expect(panel).toHaveCount(0);
  await expect(toggle).toBeFocused();
  await toggle.press('Enter');
  await activate(close);
  await expect(toggle).toBeFocused();
  await activate(toggle);
  const menu = page
    .getByRole('menubar')
    .getByRole('menuitem', { name: 'File', exact: true });
  await activate(menu);
  await expect(panel).toHaveCount(0);
  await expect(page.getByRole('menu')).toBeVisible();
  await page.keyboard.press('Escape');
  await workspace(page)
    .getByRole('button', { name: /^Close .*Workspace$/ })
    .click();
  await activate(
    page
      .getByRole('group', { name: 'Desktop icons' })
      .getByRole('button', { name: 'Settings', exact: true }),
  );
  await activate(menu);
  await expect(
    page.getByRole('menuitem', { name: 'Open Settings', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('menuitem', { name: 'Open Settings', exact: true })
    .click();
  await expect(settings(page)).toBeVisible();
  await activate(menu);
  await page
    .getByRole('menuitem', { name: 'Close Window', exact: true })
    .click();
  await expect(settings(page)).toHaveCount(0);
  await activate(
    page
      .getByRole('group', { name: 'Desktop icons' })
      .getByRole('button', { name: 'Library', exact: true }),
  );
  await activate(menu);
  await expect(
    page.getByRole('menuitem', { name: 'Open Library', exact: true }),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await openApp(page, 'Workspace');
  await activate(toggle);
  await page.screenshot({ path: testInfo.outputPath('inbox-palette.png') });
  expect(errors).toEqual([]);
});

test('Room Work opens its application and Library history provides an explicit return', async ({
  page,
}, testInfo) => {
  await signIn(page);
  await createRoom(page, `Selected views ${testInfo.project.name}`);
  const discussion = workspace(page).getByRole('button', {
    name: 'Discussion',
    exact: true,
  });
  const workShortcut = workspace(page).getByRole('button', {
    name: 'Work',
    exact: true,
  });
  await expect(discussion).toHaveAttribute('aria-pressed', 'true');
  await expect(workShortcut).not.toHaveAttribute('aria-pressed');
  await workShortcut.click();
  await expect(work(page)).toHaveClass(/is-active/);
  await openApp(page, 'Workspace');
  await discussion.press('Enter');
  await expect(discussion).toHaveAttribute('aria-pressed', 'true');
  await expect(workShortcut).not.toHaveAttribute('aria-pressed');
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const history = library(page).getByRole('button', {
    name: 'History',
    exact: true,
  });
  await expect(history).toBeEnabled();
  await history.click();
  const back = library(page).getByRole('button', {
    name: 'Back to Files',
    exact: true,
  });
  await expect(back).toHaveCount(1);
  await expect(back).not.toHaveAttribute('aria-pressed');
  await expect(library(page).locator('.camp-library-history')).toBeVisible();
  await back.press('Enter');
  await expect(history).toBeVisible();
  await expect(library(page).locator('.camp-library-history')).toHaveCount(0);
});
