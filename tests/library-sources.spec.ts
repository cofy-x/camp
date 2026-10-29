import { expect, test } from '@playwright/test';
import { library, openApp, signIn, workspace } from './team-helpers';
import type { Runner } from '../apps/dashboard/src/features/runners/api';

test('a member can import from owned Runners while shared execution does not grant source access', async ({
  page,
}) => {
  await signIn(page);
  const status = await (await page.request.get('/api/auth/status')).json();
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({
      json: { ...status, user: { ...status.user, role: 'member' } },
    }),
  );
  const owned: Runner = {
    id: 'owned-runner',
    name: 'My Linux Runner',
    ownerUserId: status.user.id,
    enabled: true,
    revoked: false,
    online: true,
    lastSeenAt: '',
    runtime: 'native',
    capabilities: { ready: true },
  };
  let runners: Runner[] = [
    owned,
    {
      ...owned,
      id: 'shared',
      name: 'Shared Runner',
      ownerUserId: 'another-member',
      teamShared: true,
    },
    { ...owned, id: 'legacy', name: 'Team Runner', ownerUserId: '' },
    { ...owned, id: 'disabled', name: 'Disabled Runner', enabled: false },
    { ...owned, id: 'revoked', name: 'Revoked Runner', revoked: true },
  ];
  const sourceRequests: string[] = [];
  await page.route('**/api/runners', (route) =>
    route.fulfill({ json: { runners } }),
  );
  await page.route('**/api/source-imports', (route) =>
    route.fulfill({ json: { sourceImports: [] } }),
  );
  await page.route('**/api/runners/*/sources', (route) => {
    sourceRequests.push(new URL(route.request().url()).pathname);
    return route.fulfill({
      json: {
        sources: [
          {
            id: 'briefs',
            alias: 'Team briefs',
            revision: '1',
            available: true,
          },
        ],
      },
    });
  });
  await page.reload();
  await expect(workspace(page)).toBeVisible();
  await openApp(page, 'Library');
  const win = library(page);
  const openImport = async () => {
    const sources = win.getByRole('button', { name: 'Sources', exact: true });
    if (await sources.isVisible()) await sources.click();
    await expect(
      win.getByRole('button', { name: 'Storage Settings…', exact: true }),
    ).toHaveCount(0);
    await win
      .getByRole('button', { name: 'Import from Runner…', exact: true })
      .click();
    return win.getByRole('region', { name: 'Import from Runner', exact: true });
  };
  let panel = await openImport();
  const picker = panel.getByLabel('Source Runner', { exact: true });
  await expect(picker.locator('option')).toHaveText([
    'Choose Runner',
    'My Linux Runner · Online',
  ]);
  await picker.selectOption(owned.id);
  await panel
    .getByLabel('Registered directory', { exact: true })
    .selectOption('briefs');
  await panel
    .getByLabel('New Room name', { exact: true })
    .fill('Imported briefs');
  await expect(
    panel.getByRole('button', { name: 'Import Snapshot', exact: true }),
  ).toBeEnabled();
  await expect(panel).not.toContainText('wait until the Runner reconnects');
  expect(sourceRequests).toEqual(['/api/runners/owned-runner/sources']);
  expect(
    await panel.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);

  await panel
    .getByRole('button', { name: 'Back to Files', exact: true })
    .click();
  runners = [];
  panel = await openImport();
  await expect(
    panel.getByRole('button', { name: 'Open Runners', exact: true }),
  ).toBeVisible();
  await expect(
    panel.getByRole('button', { name: 'Import Snapshot', exact: true }),
  ).toBeDisabled();
});
