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
