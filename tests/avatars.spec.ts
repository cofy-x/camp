import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  mutate,
  openApp,
  settings,
  signIn,
  workspace,
} from './team-helpers';
import type { User } from '../apps/dashboard/src/team/api';
import type { Agent } from '../apps/dashboard/src/features/workspace/api';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) => {
  expect(errors.get(page)?.errors).toEqual([]);
});

async function account(page: Page) {
  await openApp(page, 'Settings');
  const chooser = settings(page).getByLabel('Control panel', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption('account');
  else
    await settings(page)
      .getByRole('button', { name: 'Account', exact: true })
      .click();
}

async function profile(page: Page) {
  return (await (await page.request.get('/api/auth/status')).json())
    .user as User;
}

async function catalog(page: Page, kind: 'member' | 'agent') {
  const { avatars } = (await (
    await page.request.get('/api/avatars')
  ).json()) as { avatars: { id: string; name: string; kind: string }[] };
  return avatars.filter((entry) => entry.kind === kind);
}

test('a saved avatar stays stable and updates an existing conversation in another session', async ({
  page,
  context,
}) => {
  const before = await profile(page);
  expect(before.avatar).toMatch(/^member-/);
  const choices = await catalog(page, 'member');
  const target =
    choices[
      (choices.findIndex((entry) => entry.id === before.avatar) + 1) %
        choices.length
    ];
  const room = await createRoom(
    page,
    `Avatar Room ${randomUUID().slice(0, 8)}`,
  );
  const message = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Keep this discussion and its author.',
    postOnly: true,
  });
  expect(message.ok()).toBe(true);
  const receipt = await message.json();
  const other = await context.newPage();
  await other.goto('/');
  await expect(workspace(other)).toBeVisible();
  const roomPicker = workspace(other).getByLabel('Room', { exact: true });
  if (await roomPicker.isVisible()) await roomPicker.selectOption(room.id);
  else
    await workspace(other)
      .getByRole('button', { name: room.name, exact: true })
      .click();
  const turn = workspace(other).locator(
    `[data-message-id="${receipt.message.id}"]`,
  );
  await expect(turn).toBeVisible();
  await expect(turn.locator('.p9-avatar img')).toHaveAttribute(
    'src',
    `/api/avatars/${before.avatar}`,
  );
  // Native keyboard radio navigation belongs to the foreground browser page.
  await page.bringToFront();
  await account(page);
  const changeAvatar = settings(page).getByRole('button', {
    name: 'Change Avatar…',
    exact: true,
  });
  await changeAvatar.focus();
  await changeAvatar.press('Enter');
  const current = settings(page).getByRole('radio', {
    name: choices.find((entry) => entry.id === before.avatar)!.name,
    exact: true,
  });
  await expect(current).toBeFocused();
  await current.press('ArrowRight');
  const radio = settings(page).getByRole('radio', {
    name: target.name,
    exact: true,
  });
  await expect(radio).toBeChecked();
  await radio.press('Tab');
  await page.keyboard.press('Shift+Tab');
  await expect(radio).toBeFocused();
  expect(
    await radio.evaluate(
      (input) => getComputedStyle(input.closest('label')!).outlineStyle,
    ),
  ).toBe('solid');
  await settings(page)
    .getByRole('button', { name: 'Save Avatar', exact: true })
    .click();
  await expect(
    settings(page).getByText('Avatar saved.', { exact: true }),
  ).toBeVisible();
  await expect(
    settings(page).getByRole('button', { name: 'Change Avatar…', exact: true }),
  ).toBeFocused();
  await expect.poll(async () => (await profile(page)).avatar).toBe(target.id);
  await expect(turn.locator('.p9-avatar img')).toHaveAttribute(
    'src',
    `/api/avatars/${target.id}`,
  );
  await expect(turn).toContainText(before.name);
  await expect(turn.locator('.workspace-message-avatar')).toBeVisible();
  await errors.get(page)!.reload();
  expect((await profile(page)).avatar).toBe(target.id);
  expect(
    await other.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    ),
  ).toBe(false);
  await other.close();
});

test('Agent defaults and chosen avatars remain consistent in the editor and Room', async ({
  page,
}, info) => {
  const room = await createRoom(
    page,
    `Agent Avatar ${randomUUID().slice(0, 8)}`,
  );
  const created = await mutate(page, '/agents', {
    name: `Avatar Helper ${randomUUID().slice(0, 8)}`,
    handle: `avatar-${randomUUID().slice(0, 8)}`,
    access: 'read',
  });
  expect(created.ok()).toBe(true);
  const { agent } = (await created.json()) as { agent: Agent };
  expect(agent.avatar).toMatch(/^agent-/);
  const target = (await catalog(page, 'agent')).find(
    (entry) => entry.id !== agent.avatar,
  )!;
  expect(
    (
      await mutate(page, `/rooms/${room.id}/members/${agent.id}`, {}, 'PUT')
    ).ok(),
  ).toBe(true);
  await openApp(page, 'Agents');
  const app = page.locator('[data-window-id="agents:main"]');
  const chooser = app.getByLabel('Agent', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption(agent.id);
  else
    await app
      .getByRole('navigation', { name: 'Agents', exact: true })
      .getByRole('button', { name: new RegExp(agent.name) })
      .click();
  await expect(app.locator('.panel-heading .p9-avatar img')).toHaveAttribute(
    'src',
    `/api/avatars/${agent.avatar}`,
  );
  await app.getByRole('button', { name: 'Edit Agent…', exact: true }).click();
  const editor = app.getByRole('form', { name: 'Agent settings' });
  await editor.locator('.camp-avatar-disclosure > summary').click();
  const radio = editor.getByRole('radio', { name: target.name, exact: true });
  if (info.project.name === 'touch') await radio.tap();
  else await radio.click();
  await editor.getByRole('button', { name: 'Save Agent', exact: true }).click();
  await expect(editor).toHaveCount(0);
  await expect(app.locator('.panel-heading .p9-avatar img')).toHaveAttribute(
    'src',
    `/api/avatars/${target.id}`,
  );
  await openApp(page, 'Workspace');
  await expect(
    workspace(page).locator(
      `.workspace-members img[src="/api/avatars/${target.id}"]`,
    ),
  ).toBeVisible();
  const saved = (
    await (await page.request.get('/api/agents')).json()
  ).agents.find((item: Agent) => item.id === agent.id) as Agent;
  expect(saved.avatar).toBe(target.id);
  expect(saved.revision).toBe(agent.revision + 1);
});

test('failed catalog loads, lost saves and concurrent edits retain an explicit recovery path', async ({
  page,
}) => {
  const original = await profile(page);
  const choices = (await catalog(page, 'member')).filter(
    (entry) => entry.id !== original.avatar,
  );
  const [wanted, newer] = choices;
  let failCatalog = true;
  await page.route('**/api/avatars', (route) =>
    failCatalog
      ? route.fulfill({
          status: 503,
          json: { error: 'Avatars are temporarily unavailable.' },
        })
      : route.continue(),
  );
  await account(page);
  await settings(page)
    .getByRole('button', { name: 'Change Avatar…', exact: true })
    .click();
  await expect(settings(page).getByRole('alert')).toContainText(
    'Avatars are temporarily unavailable.',
  );
  failCatalog = false;
  await settings(page)
    .getByRole('button', { name: 'Retry Avatars', exact: true })
    .click();
  await settings(page)
    .getByRole('radio', { name: wanted.name, exact: true })
    .check();
  let loseResponse = true;
  await page.route('**/api/me/avatar', async (route) => {
    if (!loseResponse) return route.continue();
    loseResponse = false;
    expect((await route.fetch()).ok()).toBe(true);
    await route.abort('failed');
  });
  await settings(page)
    .getByRole('button', { name: 'Save Avatar', exact: true })
    .click();
  await expect(settings(page).getByRole('alert')).toBeVisible();
  await expect(
    settings(page).getByRole('radio', { name: wanted.name, exact: true }),
  ).toBeChecked();
  await expect(settings(page)).not.toContainText('Avatar saved.');
  await settings(page)
    .getByRole('button', { name: 'Save Avatar', exact: true })
    .click();
  await expect(
    settings(page).getByText('Avatar saved.', { exact: true }),
  ).toBeVisible();
  await settings(page)
    .getByRole('button', { name: 'Change Avatar…', exact: true })
    .click();
  const originalChoice = (await catalog(page, 'member')).find(
    (entry) => entry.id === original.avatar,
  )!;
  await settings(page)
    .getByRole('radio', { name: originalChoice.name, exact: true })
    .check();
  expect(
    (
      await mutate(
        page,
        '/me/avatar',
        { avatar: newer.id, expectedAvatar: wanted.id },
        'PATCH',
      )
    ).ok(),
  ).toBe(true);
  await settings(page)
    .getByRole('button', { name: 'Save Avatar', exact: true })
    .click();
  await expect(settings(page).getByRole('alert')).toContainText(
    'another session',
  );
  await expect(
    settings(page).getByRole('radio', {
      name: originalChoice.name,
      exact: true,
    }),
  ).toBeChecked();
  expect((await profile(page)).avatar).toBe(newer.id);
  await settings(page)
    .getByRole('button', { name: 'Reload Avatar', exact: true })
    .click();
  await expect(
    settings(page).getByRole('radio', { name: newer.name, exact: true }),
  ).toBeChecked();
  await expect(
    settings(page).getByRole('radio', { name: newer.name, exact: true }),
  ).toBeFocused();
  await expect(
    settings(page).getByRole('button', { name: 'Save Avatar', exact: true }),
  ).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(
    settings(page).getByRole('button', { name: 'Change Avatar…', exact: true }),
  ).toBeFocused();
});
