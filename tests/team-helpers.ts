import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, type Page, type BrowserContext } from '@playwright/test';

export const owner = {
  username: 'browser-owner',
  name: 'Browser Owner',
  password: 'A private browser test password',
};

export const workspace = (page: Page) =>
  page.locator('[data-window-id="workspace:main"]');

export const library = (page: Page) =>
  page.locator('[data-window-id="library:main"]');

export const settings = (page: Page) =>
  page.locator('[data-window-id="settings:main"]');

let ownerCookies: Awaited<ReturnType<BrowserContext['cookies']>> = [];

export async function signIn(page: Page) {
  const manifestPath = join(
    process.cwd(),
    '.data',
    `e2e-${process.env.E2E_PORT || '13009'}.json`,
  );
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as {
    dataDir: string;
    ownerCookies?: Awaited<ReturnType<BrowserContext['cookies']>>;
  };
  // Browser projects share the runner's temporary server. Reuse its already
  // authenticated owner session instead of consuming the real sign-in limiter
  // for every browser. The private manifest is deleted when that server stops.
  if (!ownerCookies.length && manifest.ownerCookies)
    ownerCookies = manifest.ownerCookies;
  if (ownerCookies.length) {
    await page.context().addCookies(ownerCookies);
    await page.goto('/');
    await expect(workspace(page)).toBeVisible();
    return;
  }
  await page.goto('/');
  const status = await (await page.request.get('/api/auth/status')).json();
  if (status.setupRequired) {
    const token = (
      await readFile(join(manifest.dataDir, 'team-setup.token'), 'utf8')
    ).trim();
    await page
      .getByLabel('Team name', { exact: true })
      .fill('Camp browser team');
    await page.getByLabel(/^Setup token/).fill(token);
    await page.getByLabel('Your name', { exact: true }).fill(owner.name);
  }
  await page.getByLabel('Username', { exact: true }).fill(owner.username);
  await page.getByLabel(/^Password/).fill(owner.password);
  await page
    .getByRole('button', {
      name: status.setupRequired ? 'Create Team' : 'Sign In',
      exact: true,
    })
    .click();
  await expect(workspace(page)).toBeVisible();
  ownerCookies = await page.context().cookies();
  await writeFile(manifestPath, JSON.stringify({ ...manifest, ownerCookies }), {
    mode: 0o600,
  });
}

export async function openApp(page: Page, name: string) {
  await page
    .getByRole('menubar')
    .getByRole('menuitem', { name: 'Desktop', exact: true })
    .click();
  await page
    .getByRole('menu')
    .getByRole('menuitem', { name, exact: true })
    .click();
}

export async function mutate(
  page: Page,
  path: string,
  data: unknown,
  method = 'POST',
) {
  return page.request.fetch(`/api${path}`, {
    method,
    headers: { Origin: new URL(page.url()).origin },
    data,
  });
}

export async function createRoom(page: Page, name: string) {
  await workspace(page)
    .getByRole('button', { name: 'Room actions', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'New Room…', exact: true }).click();
  await page
    .getByRole('dialog')
    .getByLabel('Room name', { exact: true })
    .fill(name);
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Create Room', exact: true })
    .click();
  await expect(
    page.getByLabel(`Message ${name}`, { exact: true }),
  ).toBeVisible();
  const result = await (await page.request.get('/api/rooms')).json();
  return result.rooms.find((room: { name: string }) => room.name === name) as {
    id: string;
    resourceId: string;
    name: string;
  };
}

export async function post(page: Page, room: string, text: string) {
  const composer = page.getByRole('form', {
    name: room === 'thread' ? 'Thread composer' : 'Room composer',
  });
  await page.getByLabel(`Message ${room}`, { exact: true }).fill(text);
  await composer.getByRole('button', { name: 'Post', exact: true }).click();
  await expect(page.getByLabel(`Message ${room}`, { exact: true })).toHaveValue(
    '',
  );
  await expect(workspace(page).getByText(text, { exact: true })).toBeVisible();
}

export async function newDocument(page: Page, path: string, text: string) {
  await library(page)
    .getByRole('button', { name: 'New Document', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByLabel('File path', { exact: true })
    .fill(path);
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Create Draft', exact: true })
    .click();
  await library(page)
    .getByLabel('Document content', { exact: true })
    .fill(text);
}
