import { expect, test, type Page } from '@playwright/test';
import { createRoom, signIn, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

test('a narrow Room chooser and compact actions fit long names without duplicating navigation', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(
    page,
    `Planning a shared customer onboarding guide with the entire team ${testInfo.project.name}`,
  );
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  const toolbar = win.locator('.workspace-toolbar');
  const actions = win.locator('.workspace-toolbar-actions');
  const menu = win.getByRole('button', { name: 'Room actions', exact: true });
  const library = actions.getByRole('button', { name: 'Library', exact: true });
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(chooser).toBeVisible();
    await expect(chooser).toHaveValue(room.id);
    await expect(chooser.locator('option:checked')).toHaveText(room.name);
    await expect(
      win.getByRole('button', { name: 'Rooms', exact: true }),
    ).toHaveCount(0);
    await expect(
      win.getByRole('complementary', { name: 'Workspace navigation' }),
    ).toBeHidden();
    await expect
      .poll(() =>
        toolbar.evaluate(
          (element) => element.scrollWidth - element.clientWidth,
        ),
      )
      .toBeLessThanOrEqual(1);
    const chooserBounds = (await chooser.boundingBox())!;
    const actionsBounds = (await actions.boundingBox())!;
    const menuBounds = (await menu.boundingBox())!;
    const libraryBounds = (await library.boundingBox())!;
    const windowBounds = (await win.boundingBox())!;
    expect(actionsBounds.y).toBeGreaterThanOrEqual(
      chooserBounds.y + chooserBounds.height,
    );
    expect(chooserBounds.x).toBeGreaterThanOrEqual(windowBounds.x);
    expect(chooserBounds.x + chooserBounds.width).toBeLessThanOrEqual(
      windowBounds.x + windowBounds.width,
    );
    expect(menuBounds.width).toBeLessThan(libraryBounds.width);
    expect(menuBounds.width).toBeGreaterThanOrEqual(24);
    expect(menuBounds.x + menuBounds.width).toBeCloseTo(
      actionsBounds.x + actionsBounds.width,
      0,
    );
    for (const button of await actions.getByRole('button').all()) {
      const bounds = (await button.boundingBox())!;
      expect(bounds.x).toBeGreaterThanOrEqual(windowBounds.x);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(
        windowBounds.x + windowBounds.width,
      );
    }
  }
  await expect(menu).toHaveAttribute('aria-haspopup', 'menu');
  await menu.focus();
  await menu.press('ArrowDown');
  await expect(page.getByRole('menu')).toBeFocused();
  await expect(
    page.getByRole('menuitem', { name: 'Rename Room…', exact: true }),
  ).toBeVisible();
  await page.getByRole('menu').press('Escape');
  await expect(page.getByRole('menu')).toHaveCount(0);
  await expect(menu).toBeFocused();
  if (isMobile) await menu.tap();
  else await menu.click();
  await expect(page.getByRole('menu')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('narrow-room-menu.png') });
  await page.getByRole('menu').press('Escape');
  await expect(menu).toBeFocused();
});

test('the Room pop-up supports keyboard and touch selection while keeping private drafts', async ({
  page,
  isMobile,
}, testInfo) => {
  const first = await createRoom(page, `Orchid-${testInfo.project.name}`);
  const second = await createRoom(page, `Zephyr-${testInfo.project.name}`);
  await page.setViewportSize({ width: 390, height: 600 });
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  const popup = page.getByRole('listbox', { name: 'Room', exact: true });
  const input = win.getByRole('textbox', { name: `Message ${second.name}` });
  const draft = 'Keep these notes private while I look at another Room.';
  await expect(input).toBeEnabled();
  await input.fill(draft);

  await chooser.focus();
  await chooser.press('ArrowDown');
  await expect(chooser).toHaveAttribute('aria-expanded', 'true');
  await expect(
    popup.getByRole('option', { selected: true }),
  ).toHaveAccessibleName(second.name);
  await expect(chooser).toBeFocused();
  await chooser.pressSequentially(first.name);
  await expect(chooser).toHaveAttribute(
    'aria-activedescendant',
    (await popup
      .getByRole('option', { name: first.name, exact: true })
      .getAttribute('id'))!,
  );
  await expect(chooser).toHaveValue(second.id);
  await chooser.press('Escape');
  await expect(popup).toHaveCount(0);
  await expect(chooser).toBeFocused();
  await expect(chooser).toHaveValue(second.id);

  if (isMobile) await chooser.tap();
  else await chooser.click();
  await expect(popup).toBeVisible();
  await input.click();
  await expect(popup).toHaveCount(0);
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(draft);

  await chooser.focus();
  await chooser.press('ArrowDown');
  await chooser.press('Tab');
  await expect(popup).toHaveCount(0);
  await expect(
    win.getByRole('button', { name: 'Collaborations', exact: true }),
  ).toBeFocused();

  await chooser.focus();
  await chooser.press('Enter');
  await chooser.pressSequentially(first.name);
  await chooser.press('Enter');
  await expect(popup).toHaveCount(0);
  await expect(chooser).toHaveValue(first.id);
  await expect(chooser).toBeFocused();
  await expect(
    win.getByRole('textbox', { name: `Message ${first.name}` }),
  ).toHaveValue('');

  if (isMobile) await chooser.tap();
  else await chooser.click();
  const destination = popup.getByRole('option', {
    name: second.name,
    exact: true,
  });
  if (isMobile) await destination.tap();
  else await destination.click();
  await expect(popup).toHaveCount(0);
  await expect(chooser).toHaveValue(second.id);
  await expect(chooser).toBeFocused();
  await expect(input).toHaveValue(draft);
  await errors.get(page)!.reload();
  await expect(chooser).toHaveValue(second.id);
  await expect(input).toHaveValue(draft);

  for (const room of [first, second]) {
    const messages = await (
      await page.request.get(`/api/rooms/${room.id}/messages`)
    ).json();
    expect(messages.messages).toEqual([]);
    const runs = await (
      await page.request.get(`/api/rooms/${room.id}/runs`)
    ).json();
    expect(runs.runs).toEqual([]);
  }
  await page.screenshot({ path: testInfo.outputPath('narrow-room-draft.png') });
});

test('Room selection waits for draft storage and keeps the current Room when saving fails', async ({
  page,
}, testInfo) => {
  const first = await createRoom(page, `save-first-${testInfo.project.name}`);
  const second = await createRoom(page, `save-second-${testInfo.project.name}`);
  await page.setViewportSize({ width: 390, height: 600 });
  const win = workspace(page);
  const chooser = win.getByRole('combobox', { name: 'Room', exact: true });
  await chooser.selectOption(first.id);
  const input = win.getByRole('textbox', { name: `Message ${first.name}` });
  await expect(input).toBeEnabled();
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saving = false;
  let fail = false;
  await page.route('**/api/me/drafts/conversation', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    saving = true;
    if (fail) {
      await route.fulfill({
        status: 503,
        json: {
          error: 'Draft storage is unavailable. Your notes are preserved.',
        },
      });
      return;
    }
    await held;
    await route.continue();
  });
  try {
    await input.fill('Wait for this private draft before leaving.');
    await chooser.selectOption(second.id);
    await expect.poll(() => saving).toBe(true);
    await expect(chooser).toHaveValue(first.id);
    await expect(input).toHaveValue(
      'Wait for this private draft before leaving.',
    );
    release();
    await expect(chooser).toHaveValue(second.id);
    const secondInput = win.getByRole('textbox', {
      name: `Message ${second.name}`,
    });
    await expect(secondInput).toBeEnabled();
    fail = true;
    await secondInput.fill('Do not lose this draft when storage fails.');
    await chooser.selectOption(first.id);
    await expect(win.getByRole('alert')).toContainText(
      'Draft storage is unavailable.',
    );
    await expect(chooser).toHaveValue(second.id);
    await expect(secondInput).toHaveValue(
      'Do not lose this draft when storage fails.',
    );
  } finally {
    release();
    await page.unroute('**/api/me/drafts/conversation');
  }
});
