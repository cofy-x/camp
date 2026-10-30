import { expect, test, type Page } from '@playwright/test';
import type {
  ModelSettings,
  Readiness,
} from '../apps/dashboard/src/features/settings/api';
import { openApp, settings, signIn, workspace } from './team-helpers';

const observation: Readiness = {
  ready: true,
  checkedAt: '2026-10-27T09:00:00Z',
  components: [
    {
      id: 'model',
      label: 'Model configuration',
      status: 'ready',
      detail: 'A shared model configuration is saved.',
    },
    {
      id: 'connection',
      label: 'Provider connection',
      status: 'unknown',
      detail: 'No model response has been observed.',
    },
    {
      id: 'execution',
      label: 'Runner execution reports',
      status: 'ready',
      detail: 'The execution environment passed its local checks.',
      checkedAt: '2026-10-27T09:00:00Z',
      lastSuccessAt: '2026-10-27T09:00:00Z',
      lastSuccess: 'Sandbox startup and cleanup succeeded.',
      lastFailureAt: '2026-10-26T08:00:00Z',
      lastFailure: 'The container service was unavailable.',
    },
    {
      id: 'first-task',
      label: 'Native task',
      status: 'unknown',
      detail: 'No successful native task has been recorded.',
    },
  ],
};

function preparation(page: Page) {
  return settings(page).getByRole('region', {
    name: 'Getting Ready',
    exact: true,
  });
}

function statusRow(page: Page, label: string) {
  return preparation(page)
    .locator('.camp-preparation-status > div')
    .filter({ has: page.locator('dt').getByText(label, { exact: true }) });
}

async function selectPanel(page: Page, id: string, title: string) {
  const chooser = settings(page).getByLabel('Control panel', { exact: true });
  if (await chooser.isVisible()) await chooser.selectOption(id);
  else
    await settings(page)
      .getByRole('navigation', { name: 'Settings sections' })
      .getByRole('button', { name: title, exact: true })
      .click();
}

test('model details remain keyboard accessible while locked fields explain their source without exposing the key', async ({
  page,
}) => {
  await signIn(page);
  const model: ModelSettings = {
    model: 'shared-model',
    baseUrl: 'https://provider.example/v1',
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    apiKeyConfigured: true,
    revision: 'settings-preview',
    fields: {
      model: { source: 'managed', overridden: false },
      baseUrl: { source: 'environment', overridden: true },
      apiKey: { source: 'env-file', overridden: true },
      maxTokens: { source: 'managed', overridden: false },
      maxSteps: { source: 'default', overridden: false },
      timeoutSeconds: { source: 'default', overridden: false },
    },
  };
  let writes = 0;
  await page.route('**/api/settings/models', (route) => {
    if (route.request().method() !== 'GET') writes++;
    return route.fulfill({ json: model });
  });
  await openApp(page, 'Settings');
  await selectPanel(page, 'models', 'Models');
  const app = settings(page);
  const modelInput = app.getByLabel('Model', { exact: true });
  await expect(modelInput).toHaveValue(model.model);
  await expect(modelInput).toHaveAccessibleDescription('Team settings');
  const baseURL = app.getByLabel('API base URL', { exact: true });
  await expect(baseURL).toBeDisabled();
  await expect(baseURL).toHaveAccessibleDescription(
    'Server environment · Locked',
  );
  const key = app.getByLabel('API key', { exact: true });
  await expect(key).toBeDisabled();
  await expect(key).toHaveValue('');
  await expect(key).toHaveAttribute('type', 'password');
  await expect(key).toHaveAccessibleDescription('Server .env · Locked');
  await expect(
    app.getByRole('button', { name: 'Clear Saved Key', exact: true }),
  ).toHaveCount(0);

  const limits = app.locator('details').filter({
    has: page.locator('summary').getByText('Request limits', { exact: true }),
  });
  await expect(limits).not.toHaveAttribute('open');
  await limits.locator('summary').focus();
  await limits.locator('summary').press('Enter');
  const tokens = app.getByLabel('Output tokens / request', { exact: true });
  await expect(tokens).toHaveValue('4096');
  await expect(limits).toContainText('These limits are not a billing cap.');
  await tokens.fill('0');
  await limits.locator('summary').click();
  await app
    .getByRole('button', { name: 'Save Model Settings', exact: true })
    .click();
  await expect(limits).toHaveAttribute('open');
  await expect(tokens).toBeFocused();
  expect(writes).toBe(0);
  await tokens.fill('2048');
  await limits.locator('summary').click();

  const details = app.locator('details').filter({
    has: page.locator('summary').filter({
      hasText: /^Configuration details$/,
    }),
  });
  const summary = details.locator('summary');
  await expect(details).not.toHaveAttribute('open');
  await modelInput.fill('another-model');
  await summary.focus();
  await summary.press('Enter');
  await expect(details).toHaveAttribute('open');
  await expect(summary).toBeFocused();
  await expect(details).toContainText('Configuration priority:');
  await expect(details).toContainText(
    'queued work keeps its existing settings',
  );
  await summary.press('Space');
  await expect(details).not.toHaveAttribute('open');
  await expect(modelInput).toHaveValue('another-model');
  await expect(tokens).toHaveValue('2048');
  await expect(
    app.getByRole('button', { name: 'Save Model Settings', exact: true }),
  ).toBeEnabled();
  expect(writes).toBe(0);
});

test('remote environment settings show Server concurrency without inventing Runner limits', async ({
  page,
}) => {
  await signIn(page);
  await page.route('**/api/settings/environments', (route) =>
    route.fulfill({
      json: {
        platform: 'Remote Linux runners',
        nodeVersion: 'Runner capability',
        packageManager: 'Runner capability',
        commandNetwork: false,
        concurrency: 2,
        checks: 'Resource-specific checks on the assigned runner.',
        limits: {
          maxFiles: 10000,
          maxFileBytes: 8388608,
          maxProjectBytes: 134217728,
        },
      },
    }),
  );
  await openApp(page, 'Settings');
  await selectPanel(page, 'environments', 'Environments');
  const app = settings(page);
  await expect(
    app.getByText('Remote Linux runners', { exact: true }),
  ).toBeVisible();
  await expect(
    app.locator('dt').getByText('Server concurrency', { exact: true }),
  ).toBeVisible();
  await expect(app.locator('dd').getByText('2', { exact: true })).toBeVisible();
  await expect(
    app.getByText('Per-command limits', { exact: true }),
  ).toHaveCount(0);
  await expect(app.getByText('Command network', { exact: true })).toHaveCount(
    0,
  );
  await expect(app.getByText('Runner capability', { exact: true })).toHaveCount(
    0,
  );
  await app.getByRole('button', { name: 'Open Runners', exact: true }).click();
  await expect(page.locator('[data-window-id="runners:main"]')).toBeVisible();
});

test('Getting Ready guides model, Runner and task setup without starting work', async ({
  page,
}) => {
  await signIn(page);
  let stage = 'model';
  const mutations: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      /\/api\/(agents|runner-enrollments|rooms\/[^/]+\/messages)/.test(
        request.url(),
      )
    )
      mutations.push(request.url());
  });
  await page.route('**/api/settings/readiness', (route) =>
    route.fulfill({
      json: {
        ...observation,
        ready: ['task', 'done'].includes(stage),
        components: observation.components.map((component) => ({
          ...component,
          status:
            component.id === 'model'
              ? stage === 'model'
                ? 'missing'
                : 'ready'
              : component.id === 'execution'
                ? ['task', 'done'].includes(stage)
                  ? 'ready'
                  : 'missing'
                : component.id === 'first-task'
                  ? stage === 'done'
                    ? 'ready'
                    : 'unknown'
                  : component.status,
        })),
      },
    }),
  );
  await openApp(page, 'Settings');
  const next = preparation(page).getByRole('region', {
    name: 'Next setup step',
  });
  await expect(next.getByRole('heading')).toHaveText('Connect a model');
  await next
    .getByRole('button', { name: 'Configure Model…', exact: true })
    .focus();
  await page.keyboard.press('Enter');
  await expect(
    settings(page).getByRole('heading', {
      name: 'Model Connection',
      exact: true,
    }),
  ).toBeVisible();
  stage = 'runner';
  await selectPanel(page, 'readiness', 'Getting Ready');
  await expect(next.getByRole('heading')).toHaveText('Connect a Runner');
  await next
    .getByRole('button', { name: 'Connect Runner…', exact: true })
    .click();
  await expect(
    page.getByRole('region', { name: 'Connect Runner', exact: true }),
  ).toBeVisible();
  stage = 'task';
  await openApp(page, 'Settings');
  await expect(next.getByRole('heading')).toHaveText(
    'Try your first Agent task',
  );
  await expect(next).toContainText('This makes a model request.');
  await next
    .getByRole('button', { name: 'Choose Runner…', exact: true })
    .click();
  await expect(
    page.getByRole('region', { name: 'Connect Runner', exact: true }),
  ).not.toBeVisible();
  stage = 'done';
  await openApp(page, 'Settings');
  await expect(next.getByRole('heading')).toHaveText('Start working');
  expect(mutations).toEqual([]);
});

test('Getting Ready separates capabilities and remains usable when another settings panel fails', async ({
  page,
}, testInfo) => {
  await signIn(page);
  let storageReads = 0;
  let checks = 0;
  await page.route('**/api/settings/readiness', (route) =>
    route.fulfill({ json: observation }),
  );
  await page.route('**/api/settings/readiness/recheck', (route) => {
    checks++;
    return route.fulfill({ json: observation });
  });
  await page.route('**/api/settings/storage', (route) => {
    storageReads++;
    return route.fulfill({
      status: 503,
      json: { error: 'Storage settings are temporarily unavailable.' },
    });
  });
  await openApp(page, 'Settings');
  await expect(statusRow(page, 'Model settings')).toContainText('Configured');
  await expect(statusRow(page, 'Model connection')).toContainText(
    'Not verified',
  );
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  await expect(statusRow(page, 'Document reading')).toHaveCount(0);
  await expect(statusRow(page, 'Native task')).toContainText(
    'Not yet verified',
  );
  const openWorkspace = preparation(page).getByRole('button', {
    name: 'Open Workspace',
    exact: true,
  });
  await expect(openWorkspace).toBeInViewport();
  const overflow = await settings(page)
    .locator('.camp-settings-content')
    .evaluate((element) => element.scrollWidth > element.clientWidth);
  expect(overflow).toBe(false);
  expect(storageReads).toBe(0);
  expect(checks).toBe(0);
  await page.screenshot({
    path: testInfo.outputPath('getting-ready-capabilities.png'),
  });
  await openWorkspace.click();
  await expect(workspace(page)).toHaveClass(/is-active/);

  await openApp(page, 'Settings');
  await selectPanel(page, 'storage', 'Storage');
  await expect(settings(page).getByRole('alert')).toContainText(
    'Storage settings are temporarily unavailable.',
  );
  await selectPanel(page, 'readiness', 'Getting Ready');
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  await expect(preparation(page).getByRole('alert')).toHaveCount(0);
  expect(storageReads).toBe(1);
  expect(checks).toBe(0);
});

test('status refresh uses GET, keeps work available, and retains observations after failure', async ({
  page,
}) => {
  await signIn(page);
  let reads = 0;
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/settings/readiness', async (route) => {
    expect(route.request().method()).toBe('GET');
    reads++;
    if (reads === 2) {
      await held;
      return route.fulfill({
        status: 503,
        json: { error: 'Status refresh failed.' },
      });
    }
    return route.fulfill({ json: observation });
  });
  await openApp(page, 'Settings');
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  expect(reads).toBe(1);
  await preparation(page)
    .getByRole('button', { name: 'Refresh Status', exact: true })
    .click();
  const checking = preparation(page).getByRole('button', {
    name: 'Refreshing Status…',
    exact: true,
  });
  try {
    await expect(checking).toBeDisabled();
    await expect(statusRow(page, 'Agent work')).toContainText('Available');
    await expect(preparation(page)).not.toContainText('New work waits');
    await checking.press('Enter');
    await expect.poll(() => reads).toBe(2);
  } finally {
    release();
  }
  await expect(preparation(page).getByRole('alert')).toContainText(
    'Status refresh failed. Previous observations are shown below.',
  );
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  await preparation(page)
    .getByText(/^Diagnostic details/)
    .click();
  const runtime = preparation(page)
    .locator('.camp-readiness > li')
    .filter({ hasText: 'Runner execution reports' });
  await runtime.getByText('Previous observations', { exact: true }).click();
  await expect(runtime).toContainText('Sandbox startup and cleanup succeeded.');
  await expect(runtime).toContainText('The container service was unavailable.');
  const previousReads = reads;
  await preparation(page)
    .getByRole('button', { name: 'Retry Status', exact: true })
    .click();
  await expect(preparation(page).getByRole('alert')).toHaveCount(0);
  await expect.poll(() => reads).toBe(previousReads + 1);
  await expect(runtime).toContainText('Sandbox startup and cleanup succeeded.');
  await expect(runtime).toContainText('The container service was unavailable.');
});

test('status loading can be retried without running checks and refreshes after returning from Models', async ({
  page,
}) => {
  await signIn(page);
  let failStatus = true;
  let reads = 0;
  let checks = 0;
  let current: Readiness = {
    ...observation,
    ready: false,
    components: observation.components.map((item) =>
      item.id === 'model' || item.id === 'execution'
        ? {
            ...item,
            status: 'missing',
            detail: 'The shared execution setup is incomplete.',
          }
        : item,
    ),
  };
  await page.route('**/api/settings/readiness', (route) => {
    reads++;
    return failStatus
      ? route.fulfill({
          status: 503,
          json: { error: 'Setup status is temporarily unavailable.' },
        })
      : route.fulfill({ json: current });
  });
  await page.route('**/api/settings/readiness/recheck', (route) => {
    checks++;
    return route.fulfill({ json: current });
  });
  await openApp(page, 'Settings');
  await expect(preparation(page).getByRole('alert')).toContainText(
    'Setup status is temporarily unavailable.',
  );
  await expect(
    preparation(page).getByRole('button', {
      name: 'Open Workspace',
      exact: true,
    }),
  ).toBeEnabled();
  failStatus = false;
  await preparation(page)
    .getByRole('button', { name: 'Retry Status', exact: true })
    .click();
  await expect(statusRow(page, 'Model connection')).toContainText(
    'Not verified',
  );
  await expect(statusRow(page, 'Model settings')).toContainText(
    'Setup required',
  );
  await expect(statusRow(page, 'Agent work')).toContainText('Unavailable');
  await expect(
    preparation(page).getByRole('button', {
      name: 'Open Workspace',
      exact: true,
    }),
  ).toBeEnabled();
  await preparation(page)
    .getByRole('button', { name: 'Model Settings', exact: true })
    .click();
  await expect(
    settings(page).getByRole('heading', { name: 'Model Connection' }),
  ).toBeVisible();
  current = {
    ...observation,
    components: observation.components.map((item) =>
      item.id === 'connection'
        ? {
            ...item,
            status: 'ready',
            checkedAt: '2026-10-27T10:00:00Z',
            detail: 'A response was observed for an explicit Agent request.',
          }
        : item.id === 'first-task'
          ? {
              ...item,
              status: 'ready',
              checkedAt: '2026-10-27T09:30:00Z',
              detail: 'A native task succeeded previously.',
            }
          : item,
    ),
  };
  const previousReads = reads;
  await selectPanel(page, 'readiness', 'Getting Ready');
  await expect(statusRow(page, 'Model connection')).toContainText(
    'Response observed',
  );
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  await expect(statusRow(page, 'Native task')).toContainText(
    'Succeeded previously',
  );
  await preparation(page)
    .getByText(/^Diagnostic details/)
    .click();
  const nativeTask = preparation(page)
    .locator('.camp-readiness > li')
    .filter({ hasText: 'Native task' });
  const recordedAt = await page.evaluate(() =>
    new Date('2026-10-27T09:30:00Z').toLocaleString(),
  );
  await expect(nativeTask).toContainText(`Observed ${recordedAt}`);
  await expect.poll(() => reads).toBe(previousReads + 1);
  expect(checks).toBe(0);
});

test('panel navigation refreshes status and ignores an older pending response', async ({
  page,
}) => {
  await signIn(page);
  let reads = 0;
  let releaseOlderRead = () => {};
  const heldRead = new Promise<void>((resolve) => {
    releaseOlderRead = resolve;
  });
  const newerObservation: Readiness = {
    ...observation,
    components: observation.components.map((item) =>
      item.id === 'connection'
        ? {
            ...item,
            status: 'ready',
            checkedAt: '2026-10-27T10:00:00Z',
            detail: 'A response was observed for an explicit Agent request.',
          }
        : item,
    ),
  };
  await page.route('**/api/settings/readiness', async (route) => {
    expect(route.request().method()).toBe('GET');
    const number = ++reads;
    if (number === 2) await heldRead;
    await route.fulfill({
      json: number >= 3 ? newerObservation : observation,
    });
  });
  await openApp(page, 'Settings');
  await expect(statusRow(page, 'Agent work')).toContainText('Available');
  await preparation(page)
    .getByRole('button', { name: 'Refresh Status', exact: true })
    .click();
  try {
    await expect.poll(() => reads).toBe(2);
    await selectPanel(page, 'models', 'Models');
    await selectPanel(page, 'readiness', 'Getting Ready');
    await expect(statusRow(page, 'Model connection')).toContainText(
      'Response observed',
    );
    expect(reads).toBe(3);
    const olderResponse = page.waitForResponse('**/api/settings/readiness');
    releaseOlderRead();
    await (await olderResponse).finished();
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(statusRow(page, 'Model connection')).toContainText(
      'Response observed',
    );
    await expect(preparation(page).getByRole('alert')).toHaveCount(0);
  } finally {
    releaseOlderRead();
  }
});
