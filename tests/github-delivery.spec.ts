import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  mutate,
  openApp,
  openWorkAction,
  signIn,
  work,
} from './team-helpers';
import type { GitHubRepository } from '../apps/dashboard/src/features/github-delivery/api';
import type { WorkItem } from '../apps/dashboard/src/features/workitems/api';

const repository: GitHubRepository = {
  id: 'repository-one',
  revision: 4,
  repository: 'example/notes',
  repositoryId: 10,
  actorId: 7,
  defaultBranch: 'main',
  private: false,
  ciWorkflow: 'ci.yml',
  ciWorkflowId: 11,
  enabled: true,
};
const settings = (page: Page) =>
  page.locator('[data-window-id="settings:main"]');

async function openGitHub(page: Page) {
  await openApp(page, 'Settings');
  await expect(settings(page).getByRole('main')).toBeVisible();
  const chooser = settings(page).getByLabel('Control panel', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption('github');
  else
    await settings(page)
      .getByRole('button', { name: 'GitHub', exact: true })
      .click();
  await expect(
    settings(page).getByRole('region', { name: 'GitHub repositories' }),
  ).toBeVisible();
}

test('repository credentials remain in memory while uncertain settings retain a private receipt across reload', async ({
  page,
}) => {
  await signIn(page);
  let saved = false;
  let writes = 0;
  let requestId = '';
  const secret = 'synthetic-github-credential-never-store';
  const draftBodies: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/drafts') && request.postData())
      draftBodies.push(request.postData()!);
  });
  await page.route('**/api/github-repositories', (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({
        json: { repositories: saved ? [repository] : [] },
      });
    writes++;
    const input = route.request().postDataJSON();
    expect(input.token).toBe(secret);
    expect(input.repository).toBe(repository.repository);
    requestId = input.requestId;
    saved = true;
    return route.abort('failed');
  });
  await page.route('**/api/github-repository-requests/*', (route) => {
    expect(route.request().url().endsWith(requestId)).toBe(true);
    return route.fulfill({ json: { repository } });
  });
  await openGitHub(page);
  await settings(page)
    .getByRole('button', { name: 'Connect repository…', exact: true })
    .click();
  await settings(page)
    .getByLabel('Repository', { exact: true })
    .fill(repository.repository);
  await settings(page).getByLabel('GitHub token', { exact: true }).fill(secret);
  await settings(page)
    .getByLabel('Required CI workflow', { exact: true })
    .fill('ci.yml');
  const chooser = settings(page).getByLabel('Control panel', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption('account');
  else
    await settings(page)
      .getByRole('button', { name: 'Account', exact: true })
      .click();
  await expect(
    settings(page).getByRole('form', { name: 'Repository connection' }),
  ).toBeVisible();
  await expect(
    settings(page).getByText(
      'Save or clear the GitHub credential edits before leaving.',
      { exact: true },
    ),
  ).toBeVisible();
  await settings(page)
    .getByRole('button', { name: 'Save connection', exact: true })
    .click();
  await expect(
    settings(page).getByText('Connection change not confirmed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect.poll(() => writes).toBe(1);
  await expect(
    settings(page).getByText(/Cannot reach the desktop server/),
  ).toBeVisible();
  await page.reload();
  await openGitHub(page);
  await expect(
    settings(page).getByText('Connection change not confirmed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    settings(page).getByLabel('GitHub token', { exact: true }),
  ).toHaveValue('');
  await settings(page)
    .getByRole('button', { name: 'Check status', exact: true })
    .click();
  await expect(
    settings(page).getByRole('form', { name: 'Repository connection' }),
  ).toHaveCount(0);
  await expect(
    settings(page).getByText('example/notes · Public', { exact: true }),
  ).toBeVisible();
  expect(writes).toBe(1);
  expect(draftBodies.join('\n')).not.toContain(secret);
  const browserState = await page.evaluate(() =>
    JSON.stringify({ ...localStorage, ...sessionStorage }),
  );
  expect(browserState).not.toContain(secret);
});

test('optional delivery stays quiet until opened and keeps public merge and release scope visible when collapsed', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Delivery planning ${randomUUID().slice(0, 8)}`,
  );
  let reads = 0;
  await page.route('**/api/github-repositories', (route) => {
    reads++;
    return route.fulfill({ json: { repositories: [repository] } });
  });
  await openApp(page, 'Work');
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Improve the release notes');
  await expect(work(page).locator('.work-grant-limits').first()).toBeVisible();
  expect(reads).toBe(0);
  await work(page).locator('.work-item-options > summary').click();
  await work(page)
    .getByRole('combobox', { name: 'Delivery', exact: true })
    .selectOption(repository.id);
  await work(page)
    .getByRole('combobox', { name: 'After the goal is met', exact: true })
    .selectOption('release');
  await work(page).getByLabel('Release tag', { exact: true }).fill('v0.1.1');
  await work(page).locator('.work-item-options > summary').click();
  await expect(
    work(page).getByText(
      'Public example/notes · main · Merge and release v0.1.1 after CI',
      { exact: false },
    ),
  ).toBeVisible();
  let input: Record<string, unknown> | undefined;
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal: 'Improve the release notes',
  });
  const { item } = await response.json();
  await page.route(`**/api/rooms/${room.id}/work-items`, (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    input = route.request().postDataJSON();
    return route.fulfill({ json: { item } });
  });
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  await expect.poll(() => input).toBeTruthy();
  expect(input).toMatchObject({
    completionMode: 'automatic',
    githubDelivery: {
      connectionId: repository.id,
      connectionRevision: 4,
      baseBranch: 'main',
      mode: 'release',
      tag: 'v0.1.1',
      maxRepairs: 1,
    },
  });
  expect(input).not.toHaveProperty('token');
});

test('Work presents one delivery destination and checking an unknown receipt does not resend the operation', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Code delivery ${randomUUID().slice(0, 8)}`,
  );
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal: 'Improve the notes app',
  });
  const value = (await response.json()).item as WorkItem;
  value.githubDelivery = {
    id: 'delivery-one',
    revision: 5,
    repository: repository.repository,
    spec: {
      connectionId: repository.id,
      connectionRevision: 4,
      baseBranch: 'main',
      mode: 'release',
      tag: 'v0.1.1',
    },
    stage: 'merge',
    status: 'unknown',
    reason: 'The merge response was not confirmed. Check its receipt.',
    pullRequestUrl: 'https://github.com/example/notes/pull/12',
    reconcile: true,
  };
  value.status = 'needs_attention';
  await page.route(`**/api/work-items/${value.id}`, (route) =>
    route.fulfill({ json: { item: value } }),
  );
  let checks = 0;
  await page.route(
    `**/api/work-items/${value.id}/github-delivery/check`,
    (route) => {
      checks++;
      expect(route.request().postDataJSON()).toEqual({ revision: 5 });
      value.githubDelivery = {
        ...value.githubDelivery!,
        revision: 7,
        status: 'succeeded',
        stage: 'release',
        reconcile: false,
        releaseUrl: 'https://github.com/example/notes/releases/tag/v0.1.1',
      };
      value.status = 'completed';
      value.result = {
        taskId: 'feature-task',
        resourceId: room.resourceId,
        commit: 'd'.repeat(40),
        published: true,
        files: [
          { path: 'src/export.mjs', status: 'modified' },
          { path: 'README.md', status: 'modified' },
        ],
        summary: 'The feature was implemented and reviewed.',
      };
      return route.fulfill({ json: { item: value } });
    },
  );
  await openApp(page, 'Work');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await work(page)
    .getByRole('button', { name: /Improve the notes app/ })
    .click();
  const status = work(page).getByRole('region', {
    name: 'GitHub delivery',
    exact: true,
  });
  await expect(
    status.getByText('Delivery not confirmed · example/notes', { exact: true }),
  ).toBeVisible();
  await expect(
    status.getByRole('link', { name: 'Open pull request' }),
  ).toHaveAttribute('href', 'https://github.com/example/notes/pull/12');
  await status
    .getByRole('button', { name: 'Check delivery', exact: true })
    .click();
  await expect(
    status.getByRole('link', { name: 'Open release', exact: true }),
  ).toHaveAttribute(
    'href',
    'https://github.com/example/notes/releases/tag/v0.1.1',
  );
  await expect(status.getByRole('link')).toHaveCount(1);
  await expect(
    status.getByRole('button', { name: 'Check delivery' }),
  ).toHaveCount(0);
  expect(checks).toBe(1);
  const source = work(page).getByRole('region', {
    name: 'Work result',
    exact: true,
  });
  await expect(
    source.getByRole('button', { name: 'src/export.mjs', exact: true }),
  ).toHaveCount(0);
  await expect(
    source.getByRole('button', { name: 'Source files…', exact: true }),
  ).toBeVisible();
  await openWorkAction(page, 'Refresh');
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await work(page)
      .locator('.work-app-body')
      .evaluate((element) => element.scrollWidth > element.clientWidth + 1),
  ).toBe(false);
});
