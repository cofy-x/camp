import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { LibraryResource } from '../apps/dashboard/src/features/library/api';
import type {
  Runner,
  SourceImport,
  SourceImportInput,
} from '../apps/dashboard/src/features/runners/api';
import type { Agent, Room } from '../apps/dashboard/src/features/workspace/api';
import {
  createRoom,
  library,
  mutate,
  openApp,
  signIn,
  workspace,
} from './team-helpers';

const runners: Runner[] = [
  {
    id: 'ui-runner-a',
    name: 'Linux Studio',
    teamShared: true,
    enabled: true,
    revoked: false,
    online: false,
    lastSeenAt: '',
    runtime: 'native',
    capabilities: {
      executor: 'native',
      ready: true,
      slots: 1,
      dshVersion: 'pinned',
      layerfsVersion: 'pinned',
    },
  },
  {
    id: 'ui-runner-b',
    name: 'Protocol Fixture',
    teamShared: true,
    enabled: true,
    revoked: false,
    online: true,
    lastSeenAt: '2026-10-28T10:00:00Z',
    runtime: 'fixture',
    capabilities: { executor: 'fixture', ready: true, slots: 1 },
  },
];

async function mockRunners(page: Page) {
  await page.route('**/api/runner-installation', (route) =>
    route.fulfill({
      json: {
        platform: 'Ubuntu 24.04 amd64',
        available: false,
        downloadUrl: '',
        filename: 'camp-runner-installer-linux-amd64.tar.gz',
        bytes: 0,
      },
    }),
  );
  await page.route('**/api/runners', (route) =>
    route.fulfill({ json: { runners } }),
  );
  await page.route('**/api/runner-enrollments', (route) =>
    route.fulfill({ json: { enrollments: [] } }),
  );
  await page.route('**/api/execution-defaults', (route) =>
    route.fulfill({
      json: {
        personalRunnerId: '',
        personalRevision: 0,
        teamRunnerId: '',
        teamRevision: 0,
      },
    }),
  );
  await page.route('**/api/runners/*/agents', (route) =>
    route.fulfill({ json: { agentIds: [] } }),
  );
  await page.route('**/api/runners/*/sources', (route) =>
    route.fulfill({
      json: {
        sources: [
          {
            id: 'source-notes',
            alias: 'Team Notes',
            revision: 'source-config-4',
            available: true,
          },
        ],
      },
    }),
  );
}

const runnerApp = (page: Page) =>
  page.locator('[data-window-id="runners:main"]');

const runnerDetail = (page: Page) =>
  runnerApp(page).getByRole('region', { name: 'Runner details', exact: true });

async function selectRunner(page: Page, runner: Runner) {
  await expect(runnerDetail(page)).toBeVisible();
  const chooser = runnerApp(page).getByLabel('Runner', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption(runner.id);
  else
    await runnerApp(page)
      .getByRole('navigation', { name: 'Runners', exact: true })
      .getByRole('button', { name: runner.name, exact: true })
      .click();
  await expect(
    runnerDetail(page).getByRole('heading', { name: runner.name, exact: true }),
  ).toBeVisible();
}

async function connectRunner(page: Page) {
  await runnerApp(page)
    .getByRole('button', { name: 'Connect Runner…', exact: true })
    .click();
  return runnerApp(page).getByRole('region', {
    name: 'Connect Runner',
    exact: true,
  });
}

async function openImport(page: Page) {
  const drawer = library(page).getByRole('button', {
    name: 'Sources',
    exact: true,
  });
  if (await drawer.isVisible()) await drawer.click();
  await library(page)
    .getByRole('button', { name: 'Import from Runner…', exact: true })
    .click();
  return library(page).getByRole('region', {
    name: 'Import from Runner',
    exact: true,
  });
}

test('Imported working files retain the initial Runner snapshot provenance', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(page, `snapshot-proof-${info.project.name}`);
  const listing = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const saved = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: listing.commit,
      path: 'notes.md',
      content: '# Imported notes\n\nAn editable Camp copy.',
    },
    'PUT',
  );
  expect(saved.ok()).toBeTruthy();
  const initial = (await saved.json()).publication.commit as string;
  const later = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: initial,
      path: 'final.md',
      content:
        '# Revised notes\n\nThis later work is not the imported snapshot.',
    },
    'PUT',
  );
  expect(later.ok()).toBeTruthy();
  await page.route('**/api/library', async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      resources: LibraryResource[];
      rooms: Room[];
    };
    await route.fulfill({
      response,
      json: {
        ...body,
        resources: body.resources.map((resource) =>
          resource.id === room.resourceId
            ? {
                ...resource,
                source: {
                  kind: 'source',
                  origin: 'runner',
                  resourceId: resource.id,
                  title: 'Team Notes',
                  commit: initial,
                  sourceRunnerId: 'runner-that-captured-source',
                  sourceImportId: 'original-import-receipt',
                  createdAt: '2026-10-28T10:00:00Z',
                },
              }
            : resource,
        ),
      },
    });
  });
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  const file = library(page).getByRole('region', { name: 'Selected file' });
  await expect(file).toContainText(
    'This later work is not the imported snapshot.',
  );
  await file.getByRole('tab', { name: 'Source', exact: true }).click();
  await expect(
    file.getByRole('heading', { name: 'Team Notes', exact: true }),
  ).toBeVisible();
  await expect(file).toContainText('do not write back');
  await expect(file).not.toContainText('runner-that-captured-source');
  await expect(file).not.toContainText('original-import-receipt');
  await expect(
    file.getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeEnabled();
  await expect(
    file.getByRole('button', { name: 'Create Working Copy…', exact: true }),
  ).toHaveCount(0);
  await file
    .getByRole('button', { name: 'Open Imported Snapshot', exact: true })
    .click();
  await expect(library(page)).toContainText('Historical version · read only');
  await expect(file).toContainText('An editable Camp copy.');
  await expect(file).not.toContainText(
    'This later work is not the imported snapshot.',
  );
  await expect(
    file.getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeDisabled();
});

test('Runner navigation separates connection codes from machine details and retains the private connection draft', async ({
  page,
}, info) => {
  await mockRunners(page);
  let enrollments = 0;
  let revoked = false;
  await page.route('**/api/runner-enrollments', async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { enrollments: [] } });
    enrollments++;
    expect(route.request().postDataJSON()).toMatchObject({
      name: 'Second Linux machine',
    });
    await route.fulfill({
      json: {
        enrollment: {
          id: 'enrollment-ui',
          name: 'Second Linux machine',
          expiresAt: '2030-01-01T00:00:00Z',
        },
        token: 'private-browser-test-credential',
      },
    });
  });
  await page.route(
    '**/api/runner-enrollments/enrollment-ui/revoke',
    (route) => {
      revoked = true;
      return route.fulfill({ json: {} });
    },
  );
  await signIn(page);
  await openApp(page, 'Runners');
  const app = runnerApp(page);
  const detail = runnerDetail(page);
  await selectRunner(page, runners[0]);
  await expect(detail).toContainText('Offline');
  await expect(app.getByLabel('Runner name', { exact: true })).toBeHidden();
  const chooser = app.getByLabel('Runner', { exact: true });
  if (info.project.name === 'touch') {
    await chooser.tap();
    await page
      .getByRole('listbox', { name: 'Runner', exact: true })
      .getByRole('option', { name: runners[1].name, exact: true })
      .tap();
  } else {
    const target = app
      .getByRole('navigation', { name: 'Runners', exact: true })
      .getByRole('button', { name: runners[1].name, exact: true });
    await target.focus();
    await page.keyboard.press('Enter');
  }
  await expect(
    detail.getByRole('heading', { name: runners[1].name, exact: true }),
  ).toBeVisible();
  await expect(detail).toContainText('Test executor');
  await expect(detail).not.toContainText('Execution ready');
  await expect(detail.getByText('pinned', { exact: true })).toHaveCount(0);
  await detail.getByRole('tab', { name: 'Overview', exact: true }).focus();
  await page.keyboard.press('End');
  await expect(
    detail.getByRole('tab', { name: 'Diagnostics', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(detail).toContainText('Team Notes');
  await page.keyboard.press('Home');
  await expect(
    detail.getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(detail.getByText('Team Notes', { exact: true })).toHaveCount(0);

  await expect(
    detail.getByRole('button', { name: 'Run First Task…', exact: true }),
  ).toHaveCount(0);
  if (['chromium', 'touch'].includes(info.project.name))
    await app.screenshot({ path: info.outputPath('runners-overview.png') });

  if (info.project.name !== 'touch') {
    const edge = (await app.locator('[data-resize="e"]').boundingBox())!;
    const before = (await app.boundingBox())!;
    await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      edge.x + edge.width / 2 - (before.width - 520),
      edge.y + edge.height / 2,
      { steps: 5 },
    );
    await page.mouse.up();
  }
  await expect(chooser).toBeVisible();
  await expect(chooser).toHaveValue(runners[1].id);
  await chooser.focus();
  await chooser.press('Home');
  await chooser.press('Enter');
  await expect(
    detail.getByRole('heading', { name: runners[0].name, exact: true }),
  ).toBeVisible();
  await chooser.press('End');
  await chooser.press('Enter');
  await expect(
    detail.getByRole('heading', { name: runners[1].name, exact: true }),
  ).toBeVisible();
  expect(
    await detail.evaluate(
      (element) => element.scrollWidth > element.clientWidth,
    ),
  ).toBe(false);

  const connect = await connectRunner(page);
  await connect
    .getByLabel('Runner name', { exact: true })
    .fill('Second Linux machine');
  await app
    .getByRole('button', { name: 'Back to Runners', exact: true })
    .click();
  await expect(
    detail.getByRole('heading', { name: runners[1].name, exact: true }),
  ).toBeVisible();
  await app.getByRole('button', { name: 'Refresh', exact: true }).click();
  await connectRunner(page);
  await expect(connect.getByLabel('Runner name', { exact: true })).toHaveValue(
    'Second Linux machine',
  );
  await connect
    .getByRole('button', { name: 'Create Connection Code', exact: true })
    .click();
  await expect(
    connect.getByLabel('Connection code', { exact: true }),
  ).toHaveValue('private-browser-test-credential');
  expect(enrollments).toBe(1);
  expect(
    await page.evaluate(() =>
      JSON.stringify({ ...localStorage, ...sessionStorage }),
    ),
  ).not.toContain('private-browser-test-credential');
  await app
    .getByRole('button', { name: 'Back to Runners', exact: true })
    .click();
  await connectRunner(page);
  await expect(
    connect.getByLabel('Connection code', { exact: true }),
  ).toHaveValue('private-browser-test-credential');
  if (['chromium', 'touch'].includes(info.project.name))
    await app.screenshot({
      path: info.outputPath('runners-connect.png'),
      mask: [connect.getByLabel('Connection code', { exact: true })],
    });
  await connect
    .getByRole('button', { name: 'Revoke Code', exact: true })
    .click();
  await expect(
    connect.getByText('Connection code revoked.', { exact: true }),
  ).toBeVisible();
  expect(revoked).toBe(true);
  await expect(
    connect.getByLabel('Connection code', { exact: true }),
  ).toHaveCount(0);
  await app
    .getByRole('button', { name: 'Back to Runners', exact: true })
    .click();
  await detail
    .getByRole('button', { name: 'Runner actions', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Revoke…', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Revoke Runner Access' });
  await expect(dialog).toContainText(runners[1].name);
  await expect(dialog).toContainText('new private data directory');
  await dialog
    .getByRole('button', { name: 'Keep Access', exact: true })
    .click();
});

test('Connect Runner uses the fixed public release and exposes offline verification with the keyboard', async ({
  page,
}, info) => {
  await mockRunners(page);
  const base =
    'https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.1/';
  await page.route('**/api/runner-installation', (route) =>
    route.fulfill({
      json: {
        platform: 'Ubuntu 24.04 amd64',
        version: 'v0.1.0-alpha.1',
        source: 'github',
        available: true,
        filename: 'camp-runner-linux-amd64.tar.gz',
        downloadUrl: `${base}camp-runner-linux-amd64.tar.gz`,
        checksumUrl: `${base}camp-runner-linux-amd64.tar.gz.sha256`,
        bytes: 12_000_000,
        runtime: { bytes: 310_000_000, unpackedBytes: 920_000_000 },
        offline: {
          available: true,
          filename: 'camp-runner-offline-linux-amd64.tar.gz',
          downloadUrl: `${base}camp-runner-offline-linux-amd64.tar.gz`,
          checksumUrl: `${base}camp-runner-offline-linux-amd64.tar.gz.sha256`,
          bytes: 322_000_000,
        },
      },
    }),
  );
  await signIn(page);
  await openApp(page, 'Runners');
  const panel = await connectRunner(page);
  await expect(panel).toContainText('v0.1.0-alpha.1.');
  await expect(panel).toContainText(
    'Requires Ubuntu 24.04 amd64, Docker, FUSE and Bubblewrap.',
  );
  await expect(panel).toContainText(
    'as the same root account, using the same private data directory.',
  );
  await expect(panel).toContainText('Rootless execution is unsupported.');
  await expect(
    panel.getByRole('link', {
      name: 'Download for Linux (12 MB)',
      exact: true,
    }),
  ).toHaveAttribute('href', `${base}camp-runner-linux-amd64.tar.gz`);
  const details = panel.getByText('Download verification and offline install', {
    exact: true,
  });
  await details.focus();
  await details.press('Enter');
  await expect(
    panel.getByRole('link', {
      name: 'Full offline package (322 MB)',
      exact: true,
    }),
  ).toHaveAttribute('href', `${base}camp-runner-offline-linux-amd64.tar.gz`);
  await expect(
    panel.getByRole('link', { name: 'Offline checksum', exact: true }),
  ).toHaveAttribute(
    'href',
    `${base}camp-runner-offline-linux-amd64.tar.gz.sha256`,
  );
  await expect(panel).not.toContainText('--runtime-source server');
  if (['chromium', 'touch'].includes(info.project.name))
    await runnerApp(page).screenshot({
      path: info.outputPath('runners-public-release.png'),
    });
});

test('Connect Runner distinguishes the small installer from runtime downloads and forgotten codes stay revocable', async ({
  page,
}, info) => {
  await mockRunners(page);
  await page.route('**/api/runner-installation', (route) =>
    route.fulfill({
      json: {
        platform: 'Ubuntu 24.04 amd64',
        version: 'v0.1.0-alpha.1',
        source: 'server',
        available: true,
        filename: 'camp-runner-linux-amd64.tar.gz',
        downloadUrl: '/api/runner-installation/download',
        checksumUrl: '/api/runner-installation/checksum',
        bytes: 12_000_000,
        runtime: { bytes: 310_000_000, unpackedBytes: 920_000_000 },
        offline: {
          available: true,
          filename: 'camp-runner-offline-linux-amd64.tar.gz',
          downloadUrl: '/api/runner-installation/download?variant=offline',
          checksumUrl: '/api/runner-installation/checksum?variant=offline',
          bytes: 322_000_000,
        },
      },
    }),
  );
  let saved:
    | { id: string; name: string; expiresAt: string; revoked?: boolean }
    | undefined;
  let revoked = false;
  await page.route('**/api/runner-enrollments', (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { enrollments: saved ? [saved] : [] } });
    saved = {
      id: 'forgotten-code',
      name: 'Private machine',
      expiresAt: '2030-01-01T00:00:00Z',
    };
    return route.fulfill({
      json: { enrollment: saved, token: 'discard-this-one-time-code' },
    });
  });
  await page.route(
    '**/api/runner-enrollments/forgotten-code/revoke',
    (route) => {
      saved = { ...saved!, revoked: true };
      revoked = true;
      return route.fulfill({ json: {} });
    },
  );
  await signIn(page);
  await openApp(page, 'Runners');
  const panel = await connectRunner(page);
  await expect(
    panel.getByRole('link', {
      name: 'Download for Linux (12 MB)',
      exact: true,
    }),
  ).toHaveAttribute('href', '/api/runner-installation/download');
  await expect(panel).toContainText(
    'Setup downloads 310 MB of execution tools once.',
  );
  if (['chromium', 'touch'].includes(info.project.name))
    await runnerApp(page).screenshot({
      path: info.outputPath('runners-connect-start.png'),
    });

  await expect(
    panel.getByRole('link', {
      name: 'Full offline package (322 MB)',
      exact: true,
    }),
  ).toBeHidden();
  await panel
    .getByText('Download verification and offline install', { exact: true })
    .click();
  await expect(
    panel.getByRole('link', {
      name: 'Full offline package (322 MB)',
      exact: true,
    }),
  ).toHaveAttribute(
    'href',
    '/api/runner-installation/download?variant=offline',
  );
  await expect(panel).toContainText(
    'sha256sum -c camp-runner-linux-amd64.tar.gz.sha256',
  );
  await expect(panel).toContainText('--runtime-source server');
  await expect(panel).toContainText('920 MB on disk');
  await panel
    .getByLabel('Runner name', { exact: true })
    .fill('Private machine');
  await panel
    .getByRole('button', { name: 'Create Connection Code', exact: true })
    .click();
  await expect(
    panel.getByLabel('Connection code', { exact: true }),
  ).toHaveValue('discard-this-one-time-code');
  await expect(panel).toContainText('save it before hiding');
  await panel.getByRole('button', { name: 'Hide Code', exact: true }).click();
  await expect(
    panel.getByLabel('Connection code', { exact: true }),
  ).toHaveCount(0);
  await panel
    .getByText('Pending connection codes (1)', { exact: true })
    .click();
  await expect(panel).toContainText('Hidden codes cannot be shown again');
  if (['chromium', 'touch'].includes(info.project.name))
    await runnerApp(page).screenshot({
      path: info.outputPath('runners-installer.png'),
    });
  await panel.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(panel).toContainText('Connection code revoked.');
  await expect(
    panel.getByText('Pending connection codes (1)', { exact: true }),
  ).toHaveCount(0);
  expect(revoked).toBe(true);
});

test('personal Runner defaults expose a concurrent revision without overwriting it', async ({
  page,
}) => {
  await mockRunners(page);
  let saved = {
    personalRunnerId: runners[0].id,
    personalRevision: 3,
    teamRunnerId: '',
    teamRevision: 1,
  };
  let writes = 0;
  await page.route('**/api/execution-defaults', async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: saved });
    const body = route.request().postDataJSON();
    writes++;
    if (writes === 1) {
      expect(body).toEqual({
        scope: 'personal',
        runnerId: runners[1].id,
        revision: 3,
      });
      saved = {
        ...saved,
        personalRunnerId: runners[1].id,
        personalRevision: 4,
      };
      return route.fulfill({
        status: 409,
        json: {
          code: 'revision_conflict',
          error: 'Default Runner changed in another window.',
        },
      });
    }
    expect(body).toEqual({
      scope: 'personal',
      runnerId: runners[0].id,
      revision: 4,
    });
    saved = { ...saved, personalRunnerId: body.runnerId, personalRevision: 5 };
    return route.fulfill({ json: saved });
  });
  await signIn(page);
  await openApp(page, 'Runners');
  const panel = page.locator('[data-window-id="runners:main"]');
  const detail = runnerDetail(page);
  await selectRunner(page, runners[0]);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await expect(detail).toContainText('Your default');
  await selectRunner(page, runners[1]);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await detail
    .getByRole('button', { name: 'Use as My Default', exact: true })
    .click();
  await expect(detail.getByRole('alert')).toContainText(
    'changed in another window',
  );
  await detail
    .getByRole('button', { name: 'Refresh Access', exact: true })
    .click();
  await expect(detail).toContainText('Your default');
  await panel.getByRole('button', { name: 'Refresh', exact: true }).click();
  await selectRunner(page, runners[0]);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await detail
    .getByRole('button', { name: 'Use as My Default', exact: true })
    .click();
  await expect(detail).toContainText('Your default');
  expect(writes).toBe(2);
});

test('Runner Access grants only the selected machine and keeps management private to its owner', async ({
  page,
}, info) => {
  await mockRunners(page);
  await signIn(page);
  const status = await (await page.request.get('/api/auth/status')).json();
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({
      json: { ...status, user: { ...status.user, role: 'member' } },
    }),
  );
  const owned = {
    ...runners[0],
    ownerUserId: status.user.id,
    teamShared: false,
  };
  const other = {
    ...runners[1],
    ownerUserId: 'other-owner',
    teamShared: false,
  };
  await page.route('**/api/runners', (route) =>
    route.fulfill({ json: { runners: [owned, other] } }),
  );
  await page.route('**/api/agents', (route) =>
    route.fulfill({
      json: {
        agents: [
          {
            id: 'guest-agent',
            name: 'Shared Researcher',
            handle: 'shared-researcher',
            ownerUserId: 'other-owner',
            access: 'read',
            enabled: true,
            revision: 1,
            instructions: '',
            icon: 'agent',
            createdAt: '',
            updatedAt: '',
          },
        ],
      },
    }),
  );
  const grants: string[] = [];
  const changes: { method: string; path: string }[] = [];
  await page.route(`**/api/runners/${owned.id}/agents`, (route) =>
    route.fulfill({ json: { agentIds: grants } }),
  );
  await page.route(`**/api/runners/${owned.id}/agents/guest-agent`, (route) => {
    const method = route.request().method();
    changes.push({ method, path: new URL(route.request().url()).pathname });
    if (method === 'PUT') grants.push('guest-agent');
    else grants.splice(0);
    return route.fulfill({ json: { agentIds: grants } });
  });
  await page.reload();
  await openApp(page, 'Runners');
  const detail = runnerDetail(page);
  await selectRunner(page, other);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await expect(
    detail.getByRole('button', { name: 'Runner actions', exact: true }),
  ).toHaveCount(0);
  await expect(
    detail.getByRole('checkbox', {
      name: 'Allow all Agents',
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(
    detail.getByRole('button', { name: 'Use as My Default', exact: true }),
  ).toHaveCount(0);
  await selectRunner(page, owned);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await expect(
    detail.getByRole('button', { name: 'Use as My Default', exact: true }),
  ).toBeVisible();
  await expect(
    detail.getByRole('button', { name: 'Use as Team Default', exact: true }),
  ).toHaveCount(0);
  const grant = detail.getByRole('checkbox', {
    name: 'Shared Researcher',
    exact: true,
  });
  await expect(grant).not.toBeChecked();
  await grant.click();
  await expect(grant).toBeChecked();
  await selectRunner(page, other);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await expect(detail.getByRole('checkbox')).toHaveCount(0);
  await selectRunner(page, owned);
  await detail.getByRole('tab', { name: 'Access', exact: true }).click();
  await expect(grant).toBeChecked();
  if (['chromium', 'touch'].includes(info.project.name))
    await runnerApp(page).screenshot({
      path: info.outputPath('runners-access.png'),
    });
  await grant.click();
  await expect(grant).not.toBeChecked();
  expect(changes).toEqual([
    { method: 'PUT', path: `/api/runners/${owned.id}/agents/guest-agent` },
    { method: 'DELETE', path: `/api/runners/${owned.id}/agents/guest-agent` },
  ]);
});

test('Runner imports recover an uncertain receipt after closing the panel and cancel without losing identity', async ({
  page,
}, info) => {
  await mockRunners(page);
  await signIn(page);
  await createRoom(page, `runner-import-${info.project.name}`);
  const requests: SourceImportInput[] = [];
  let value: SourceImport = {
    id: 'ui-import',
    runnerId: runners[0].id,
    sourceId: 'source-notes',
    sourceAlias: 'Team Notes',
    roomName: 'Imported notes',
    status: 'capturing',
    createdAt: '2026-10-28T10:00:00Z',
    updatedAt: '2026-10-28T10:00:00Z',
  };
  await page.route('**/api/source-imports', async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { sourceImports: [] } });
    requests.push(route.request().postDataJSON() as SourceImportInput);
    if (requests.length === 1)
      return route.fulfill({
        status: 503,
        json: {
          error: 'The receipt connection was interrupted.',
          code: 'unavailable',
        },
      });
    await route.fulfill({ json: { sourceImport: value } });
  });
  await page.route('**/api/source-imports/ui-import', (route) =>
    route.fulfill({ json: { sourceImport: value } }),
  );
  await page.route('**/api/source-imports/ui-import/cancel', (route) => {
    value = {
      ...value,
      status: 'cancelled',
      updatedAt: '2026-10-28T10:00:01Z',
    };
    return route.fulfill({ json: { sourceImport: value } });
  });
  await openApp(page, 'Library');
  let panel = await openImport(page);
  await panel
    .getByLabel('Source Runner', { exact: true })
    .selectOption(runners[0].id);
  await panel
    .getByLabel('Registered directory', { exact: true })
    .selectOption('source-notes');
  await panel
    .getByLabel('New Room name', { exact: true })
    .fill('Imported notes');
  await expect(panel).toContainText('original directory stays unchanged');
  await panel
    .getByRole('button', { name: 'Import Snapshot', exact: true })
    .click();
  await expect(panel.getByRole('alert')).toContainText(
    'receipt connection was interrupted',
  );
  await panel
    .getByRole('button', { name: 'Back to Files', exact: true })
    .click();
  panel = await openImport(page);
  await panel
    .getByRole('button', { name: 'Check Import Receipt', exact: true })
    .click();
  await expect(panel.getByRole('status')).toContainText('Capturing snapshot');
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(requests[0]).toMatchObject({
    sourceId: 'source-notes',
    expectedSourceRevision: 'source-config-4',
    roomName: 'Imported notes',
  });
  expect(requests[0]).not.toHaveProperty('sourcePath');
  await panel
    .getByRole('button', { name: 'Cancel Import', exact: true })
    .click();
  await expect(panel.getByRole('status')).toContainText('Import cancelled');
  await expect(
    panel.getByRole('button', { name: 'Import Another Snapshot', exact: true }),
  ).toBeEnabled();
});

test('Runner onboarding reports observed readiness and explicitly sends the first task through a shared Agent', async ({
  page,
}, info) => {
  await mockRunners(page);
  await signIn(page);
  const room = await createRoom(page, `first-runner-task-${info.project.name}`);
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const runner = {
    ...runners[0],
    ownerUserId: user.id,
    online: true,
    earlierTaskRunId: 'earlier-success-without-runtime-provenance',
    earlierTaskSucceededAt: '2026-10-27T10:00:00Z',
    modelConfigured: true,
    modelCheckedAt: '2026-10-28T10:00:00Z',
    capabilities: {
      ...runners[0].capabilities,
      readiness: {
        ready: true,
        checkedAt: '2026-10-28T10:00:00Z',
        checks: [
          { id: 'execution', label: 'Isolated execution', status: 'passed' },
        ],
      },
    },
  };
  await page.route('**/api/runners', (route) =>
    route.fulfill({ json: { runners: [runner] } }),
  );
  const requests: { path: string; body: Record<string, unknown> }[] = [];
  let createdAgent: Agent | undefined;
  await page.route('**/api/agents', async (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({
        json: { agents: createdAgent ? [createdAgent] : [] },
      });
    const body = route.request().postDataJSON();
    requests.push({ path: 'agent', body });
    createdAgent = {
      ...body,
      id: 'first-task-agent',
      ownerUserId: user.id,
      revision: 1,
      createdAt: '',
      updatedAt: '',
    };
    return route.abort();
  });
  await page.route(
    `**/api/rooms/${room.id}/members/first-task-agent`,
    (route) => {
      requests.push({ path: 'share', body: route.request().postDataJSON() });
      return route.fulfill({ json: { ok: true } });
    },
  );
  await page.route(`**/api/rooms/${room.id}/messages`, (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    requests.push({ path: 'send', body: route.request().postDataJSON() });
    if (requests.filter((item) => item.path === 'send').length === 1)
      return route.abort();
    return route.fulfill({ json: { runs: [] } });
  });
  await openApp(page, 'Runners');
  const app = page.locator('[data-window-id="runners:main"]');
  const detail = runnerDetail(page);
  await expect(detail.getByText('Ready', { exact: true })).toBeVisible();
  await expect(detail).toContainText('Configured on Server');
  await detail.getByRole('tab', { name: 'Diagnostics', exact: true }).click();
  await expect(detail).toContainText('Earlier success · runtime not recorded');
  await expect(
    detail.getByRole('button', { name: 'View Earlier Task', exact: true }),
  ).toBeVisible();
  await expect(detail).not.toContainText('Verified by completed work');
  await detail.getByRole('tab', { name: 'Overview', exact: true }).click();
  const readiness = app.getByRole('region', {
    name: 'Linux Studio readiness',
    exact: true,
  });
  expect(requests).toEqual([]);
  if (['chromium', 'touch'].includes(info.project.name))
    await app.screenshot({
      path: info.outputPath('runners-overview-native.png'),
    });
  await readiness
    .getByRole('button', { name: 'Run First Task…', exact: true })
    .click();
  await readiness.getByLabel('Room', { exact: true }).selectOption(room.id);
  await expect(readiness).toContainText('Assistant shared with this Room');
  await expect(readiness).toContainText('Uses the Server model');
  if (['chromium', 'touch'].includes(info.project.name))
    await app.screenshot({
      path: info.outputPath('runners-first-task.png'),
    });
  await readiness
    .getByRole('button', { name: 'Run First Task', exact: true })
    .click();
  await expect(readiness.getByRole('alert')).toBeVisible();
  expect(requests.map((item) => item.path)).toEqual(['agent']);
  await page.reload();
  await openApp(page, 'Runners');
  await readiness
    .getByRole('button', { name: 'Run First Task…', exact: true })
    .click();
  await expect(readiness.getByLabel('Room', { exact: true })).toBeDisabled();
  await readiness
    .getByRole('button', { name: 'Retry First Task', exact: true })
    .click();
  await expect(readiness.getByRole('alert')).toBeVisible();
  await expect.poll(() => requests.length).toBe(3);
  await page.reload();
  await openApp(page, 'Runners');
  await readiness
    .getByRole('button', { name: 'Run First Task…', exact: true })
    .click();
  await readiness
    .getByRole('button', { name: 'Retry First Task', exact: true })
    .click();
  await expect.poll(() => requests.length).toBe(5);
  expect(requests[4]).toEqual(requests[2]);

  expect(requests.map((item) => item.path)).toEqual([
    'agent',
    'share',
    'send',
    'share',
    'send',
  ]);
  expect(requests[0].body.preferredRunnerId).toBe(runner.id);
  expect(requests[2].body.recipientAgentIds).toEqual(['first-task-agent']);
  expect(requests[2].body.content).toContain('Read the file back using a tool');
});
