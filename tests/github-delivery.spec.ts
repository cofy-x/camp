import { randomUUID } from 'node:crypto';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import {
  createRoom,
  inboxToggle,
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
  await settings(page)
    .getByRole('button', { name: 'Close GitHub — Settings', exact: true })
    .click();
  await expect(
    settings(page).getByLabel('GitHub token', { exact: true }),
  ).toHaveValue(secret);
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
  await work(page).getByText('Delivery', { exact: true }).click();
  expect(reads).toBe(0);
  await work(page)
    .getByRole('combobox', { name: 'Destination', exact: true })
    .selectOption({ label: 'GitHub' });
  await work(page)
    .getByRole('combobox', { name: 'Repository', exact: true })
    .selectOption(repository.id);
  await work(page)
    .getByRole('combobox', { name: 'After the goal is met', exact: true })
    .selectOption('release');
  await work(page).getByLabel('Release tag', { exact: true }).fill('v0.1.1');
  await work(page).getByText('Delivery', { exact: true }).click();
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

test('Needs me opens uncertain GitHub delivery for receipt recovery @work-loop', async ({
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
  await page.route(`**/api/work-items/${value.id}?view=current`, (route) =>
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
  // Keep the attention row on the shared browser route used by the Inbox and
  // Work. Delivery writes and the receipt check are separate operations.
  await page.route(
    (url) => url.pathname === '/api/attention',
    (route) => {
      const pending = value.status === 'needs_attention';
      return route.fulfill({
        json: {
          items: pending
            ? [
                {
                  id: value.id,
                  kind: 'exception',
                  source: 'work_item',
                  roomId: room.id,
                  roomName: room.name,
                  title: value.goal,
                  summary: value.githubDelivery?.reason,
                  status: value.status,
                  reason: 'github_delivery_unknown',
                  priority: 1,
                  createdAt: value.createdAt,
                  updatedAt: value.updatedAt,
                  target: {
                    app: 'work',
                    roomId: room.id,
                    workItemId: value.id,
                  },
                },
              ]
            : [],
          total: pending ? 1 : 0,
          counts: { judgment: 0, review: 0, exception: pending ? 1 : 0 },
          nextCursor: '',
          hasMore: false,
        },
      });
    },
  );
  await page.reload();
  await inboxToggle(page).click();
  await page
    .getByRole('region', { name: 'Your inbox', exact: true })
    .getByRole('button', { name: /Improve the notes app/ })
    .click();
  await expect(
    work(page).getByRole('heading', { name: value.goal, exact: true }),
  ).toBeVisible();
  expect(checks).toBe(0);
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

async function deliveryInterruption(page: Page, roomName: string) {
  await signIn(page);
  const room = await createRoom(page, roomName);
  const response = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal: 'Prepare a release brief',
  });
  const item = (await response.json()).item as WorkItem;
  item.status = 'needs_attention';
  item.completionMode = 'automatic';
  item.dueAt = '2026-11-26';
  item.result = {
    taskId: 'release-brief-task',
    resourceId: room.resourceId,
    commit: 'd'.repeat(40),
    published: true,
    files: [{ path: 'release-brief.md', status: 'added' }],
    summary: 'The release brief is ready.',
  };
  item.budget = {
    ...item.budget,
    maxTasks: 3,
    maxModelRequests: 8,
    maxActions: 5,
  };
  await page.route('**/api/workspace/status', (route) =>
    route.fulfill({ json: { available: true, reason: '' } }),
  );
  item.githubDelivery = {
    id: 'delivery-one',
    revision: 5,
    repository: repository.repository,
    spec: {
      connectionId: repository.id,
      connectionRevision: repository.revision,
      baseBranch: 'main',
      mode: 'merge',
      maxRepairs: 1,
    },
    stage: 'checks',
    status: 'blocked',
    failureCode: 'ci_inspection',
    reason:
      'A CI workflow was cancelled. Rerun it on the current pull request, then check again.',
    pullRequestUrl: 'https://github.com/example/notes/pull/12',
    checkUrl: 'https://github.com/example/notes/actions/runs/19/attempts/2',
  };
  await page.route(`**/api/work-items/${item.id}?view=current`, (route) =>
    route.fulfill({ json: { item } }),
  );

  async function open() {
    await openApp(page, 'Work');
    await work(page)
      .getByRole('button', { name: /^Prepare a release brief/ })
      .filter({ hasText: room.name })
      .click();
    return work(page).getByRole('region', { name: 'GitHub delivery' });
  }

  return { item, open };
}

async function checkCancelledWorkflow(
  page: Page,
  info: TestInfo,
  stayInActivity = false,
) {
  const { item, open } = await deliveryInterruption(
    page,
    `Release planning ${info.project.name}${stayInActivity ? ' activity' : ''}`,
  );
  let finishCheck!: () => void;
  const checkResponse = new Promise<void>((resolve) => {
    finishCheck = resolve;
  });
  let checks = 0;
  await page.route(`**/api/work-items/${item.id}/actions/*`, async (route) => {
    expect(route.request().url()).toMatch(/\/actions\/retry_delivery$/);
    expect(route.request().postDataJSON()).toMatchObject({
      revision: item.revision,
      deliveryRevision: 5,
    });
    checks++;
    await checkResponse;
    item.status = 'waiting';
    item.githubDelivery = {
      ...item.githubDelivery!,
      revision: 7,
      status: 'waiting',
      checkUrl: undefined,
      failureCode: undefined,
      reason: 'Waiting for CI.',
    };
    return route.fulfill({ json: { item } });
  });
  const status = await open();
  await expect(
    status.getByRole('link', { name: 'Open workflow run' }),
  ).toHaveAttribute(
    'href',
    'https://github.com/example/notes/actions/runs/19/attempts/2',
  );
  await expect(status.getByRole('button')).toHaveCount(1);
  await expect(
    status.getByRole('button', { name: 'Check again' }),
  ).toBeVisible();
  await expect(
    work(page).getByText('Needs attention', { exact: true }),
  ).toHaveCount(0);
  await expect(
    work(page).getByText('Due 2026-11-26', { exact: true }),
  ).toBeVisible();
  await expect(
    work(page).getByRole('button', { name: 'Start work…', exact: true }),
  ).toHaveCount(0);
  expect(checks).toBe(0);
  if (!stayInActivity)
    await work(page).screenshot({ path: info.outputPath('ci-inspection.png') });
  const backToItem = work(page).getByRole('button', { name: 'Back to item' });
  await status.getByRole('button', { name: 'Check again' }).focus();
  await page.keyboard.press('Enter');
  try {
    const checking = status.getByRole('button', { name: 'Checking…' });
    await expect(checking).toBeFocused();
    await expect(checking).toBeDisabled();
    await expect.poll(() => checks).toBe(1);
    await page.keyboard.press('Enter');
    if (stayInActivity) {
      await work(page)
        .getByRole('button', { name: 'Activity', exact: true })
        .click();
      await backToItem.focus();
    }
  } finally {
    finishCheck();
  }
  if (stayInActivity) {
    await expect(work(page).locator('.work-item-delivery')).toContainText(
      'Waiting for CI · example/notes',
    );
    await expect(backToItem).toBeFocused();
    expect(checks).toBe(1);
    return;
  }
  await expect(
    status.getByText('Waiting for CI · example/notes'),
  ).toBeVisible();
  const pullRequest = status.getByRole('link', { name: 'Open pull request' });
  await expect(pullRequest).toHaveAttribute(
    'href',
    'https://github.com/example/notes/pull/12',
  );
  await expect(pullRequest).toBeFocused();
  await expect(status.getByRole('button')).toHaveCount(0);
  expect(checks).toBe(1);
}

test('a cancelled workflow opens the exact run and checks the same PR again @work-loop', async ({
  page,
}, info) => {
  await checkCancelledWorkflow(page, info);
});

test('CI recovery preserves newer Work navigation @work-loop', async ({
  page,
}, info) => {
  await checkCancelledWorkflow(page, info, true);
});

test('a changed repository identity opens My Work defaults @work-loop', async ({
  page,
}, info) => {
  const { item, open } = await deliveryInterruption(
    page,
    `Release repository changed ${info.project.name}`,
  );
  item.githubDelivery = {
    ...item.githubDelivery!,
    failureCode: 'repository_changed',
    reason: 'The repository identity changed. Authorize fresh Work.',
    checkUrl: undefined,
  };
  const status = await open();
  await expect(status.getByRole('button')).toHaveCount(1);
  await status
    .getByRole('button', { name: 'Review My Work defaults…' })
    .click();
  await expect(
    work(page).getByRole('form', { name: 'My Work defaults' }),
  ).toBeVisible();
  await expect(
    work(page)
      .getByRole('combobox', { name: 'Room' })
      .locator('option:checked'),
  ).toHaveText(`Release repository changed ${info.project.name}`);
});
