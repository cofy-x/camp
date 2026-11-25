import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import {
  createRoom,
  mutate,
  openApp,
  openWorkAction,
  signIn,
  work,
} from './team-helpers';
import type { AutomationPolicy } from '../apps/dashboard/src/features/automation/api';
import type { GitHubSource } from '../apps/dashboard/src/features/github/api';
import type {
  WorkItemDefaults,
  ProjectSettings,
} from '../apps/dashboard/src/features/workitems/api';

test('delivery defaults identify which source rule the project authorizes', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Project sources ${randomUUID().slice(0, 8)}`,
  );
  const { user } = await (await page.request.get('/api/auth/status')).json();
  const policies: AutomationPolicy[] = [];
  for (const [index, repository] of [
    'example/research',
    'example/support',
  ].entries()) {
    const response = await mutate(page, `/rooms/${room.id}/github-sources`, {
      requestId: randomUUID(),
      repository,
      secret: 'private-synthetic-project-rule-source-secret',
    });
    expect(response.ok()).toBe(true);
    const { source } = (await response.json()) as { source: GitHubSource };
    policies.push({
      id: `rule-${index}`,
      roomId: room.id,
      sourceId: source.id,
      revision: 1,
      ownerUserId: user.id,
      enabled: true,
      mode: 'auto',
      rules: 'Process the approved issue.',
      allowedActorIds: ['7'],
      participantAgentIds: [],
      firstAgentId: '',
      maxTurns: 2,
      maxModelRequests: 4,
      dailyEvaluations: 2,
      dailyTasks: 1,
      allowAnswers: false,
      allowComments: false,
      credentialConfigured: false,
      startAfter: 0,
    });
  }
  await page.route(`**/api/rooms/${room.id}/automation-policies`, (route) =>
    route.fulfill({ json: { policies } }),
  );
  await page.route('**/api/github-repositories', (route) =>
    route.fulfill({
      json: {
        repositories: [
          {
            id: 'notes',
            revision: 1,
            repository: 'example/notes',
            defaultBranch: 'main',
            private: true,
            enabled: true,
          },
        ],
      },
    }),
  );
  // Start with an account read store created after the fixture policy route.
  await page.reload();
  await openApp(page, 'Work');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await openWorkAction(page, 'My Work defaults…');
  const editor = work(page).getByRole('form', {
    name: 'My Work defaults',
    exact: true,
  });
  await editor.getByText('Delivery', { exact: true }).click();
  await editor
    .getByRole('combobox', { name: 'Destination', exact: true })
    .selectOption({ label: 'GitHub' });
  await editor
    .getByRole('combobox', { name: 'Repository', exact: true })
    .selectOption('notes');

  const rules = editor.getByRole('combobox', {
    name: 'Allow delivery from this rule',
  });
  await expect(
    rules.getByRole('option', { name: 'example/research', exact: true }),
  ).toHaveCount(1);
  await rules.selectOption({ label: 'example/support' });
  await expect(rules).toHaveValue('rule-1');

  const branch = editor.getByRole('textbox', {
    name: 'Target branch',
    exact: true,
  });
  const save = editor.getByRole('button', {
    name: 'Save defaults',
    exact: true,
  });
  await branch.fill('');
  await editor.getByText('Allowance', { exact: true }).click();
  await save.click();
  await expect(branch).toBeFocused();
  await branch.fill('main');

  await editor
    .getByRole('combobox', { name: 'Completion', exact: true })
    .selectOption('human');
  await expect(
    editor.getByText('GitHub delivery requires automatic completion.'),
  ).toBeVisible();
  await expect(save).toBeDisabled();
});

test('personal project defaults keep routine Work concise and freeze the visible grant', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(page, `Project ${randomUUID().slice(0, 8)}`);
  const otherRoom = await createRoom(
    page,
    `Other defaults ${randomUUID().slice(0, 8)}`,
  );
  const base = (
    await (
      await page.request.get(`/api/rooms/${room.id}/work-item-defaults`)
    ).json()
  ).defaults as WorkItemDefaults;
  let saved: ProjectSettings | undefined;
  let writes = 0;
  await page.route(
    `**/api/rooms/${room.id}/work-item-defaults`,
    async (route) => {
      await route.fulfill({
        json: {
          defaults: saved
            ? {
                ...base,
                budget: saved.budget,
                participantAgentIds: saved.participantAgentIds,
                firstAgentId: saved.firstAgentId,
                project: saved,
              }
            : base,
        },
      });
    },
  );
  await page.route(
    `**/api/rooms/${room.id}/project-settings`,
    async (route) => {
      writes++;
      const input = route.request().postDataJSON();
      expect(input.requestId).toBeTruthy();
      expect(input.budget.maxModelRequests).toBe(32);
      saved = { ...input, revision: 1 };
      await route.fulfill({ json: { kind: 'saved', settings: saved } });
    },
  );
  await openApp(page, 'Work');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await openWorkAction(page, 'My Work defaults…');
  const editor = work(page).getByRole('form', {
    name: 'My Work defaults',
    exact: true,
  });
  await expect(editor).toBeVisible();
  const completion = editor.getByRole('combobox', {
    name: 'Completion',
    exact: true,
  });
  await expect(completion).toBeHidden();
  const deliverySection = editor.getByText('Delivery', {
    exact: true,
  });
  await deliverySection.click();
  await expect(completion).toHaveValue('automatic');
  const allowance = editor.getByText('Allowance', { exact: true });
  const requests = editor.getByLabel('Model requests', { exact: true });
  await allowance.click();
  await expect(completion).toBeHidden();
  await requests.fill('0');
  await deliverySection.click();
  await expect(requests).toBeHidden();
  await editor
    .getByRole('button', { name: 'Save defaults', exact: true })
    .click();
  await expect(requests).toBeFocused();
  await expect(completion).toBeHidden();
  await requests.fill('32');
  const roomPicker = work(page).getByRole('combobox', {
    name: 'Room',
    exact: true,
  });
  await roomPicker.selectOption(otherRoom.id);
  await expect(work(page).locator('.p9-window-title')).toHaveText(
    `${otherRoom.name} — My Work defaults`,
  );
  await allowance.click();
  await expect(requests).not.toHaveValue('32');
  await roomPicker.selectOption(room.id);
  await expect(work(page).locator('.p9-window-title')).toHaveText(
    `${room.name} — My Work defaults`,
  );
  await expect(requests).toBeHidden();
  await allowance.click();
  await expect(requests).toHaveValue('32');
  const save = editor.getByRole('button', {
    name: 'Save defaults',
    exact: true,
  });
  await expect(save).toBeInViewport();
  await expect(
    editor.getByText('Applies to new Work in this Room.', { exact: true }),
  ).toBeVisible();
  expect(writes).toBe(0);
  await save.click();
  await expect.poll(() => writes).toBe(1);
  await expect(
    editor.getByText('Defaults saved.', { exact: true }),
  ).toBeVisible();
  await expect(save).toBeDisabled();
  await expect(
    editor.getByText('Applies to new Work in this Room.', { exact: true }),
  ).toHaveCount(0);
  await expect(
    editor.getByRole('button', { name: 'Done', exact: true }),
  ).toHaveCount(0);
  await work(page)
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await expect(work(page).locator('.work-grant-limits').first()).toContainText(
    '32 model requests',
  );
  await expect(
    work(page).getByLabel('Completion', { exact: true }),
  ).toBeHidden();
  await expect(
    work(page).getByText('Team-visible draft', { exact: true }),
  ).toBeVisible();
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Prepare a checked project note');
  const admitted = page.waitForRequest(
    (r) =>
      r.method() === 'POST' && r.url().endsWith(`/rooms/${room.id}/work-items`),
  );
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  const input = (await admitted).postDataJSON();
  expect(input).toMatchObject({
    start: false,
    completionMode: 'automatic',
    projectRevision: 1,
    budget: { maxModelRequests: 32 },
    goal: 'Prepare a checked project note',
  });
});

test('My Work defaults reveals GitHub setup on demand and saves the enabled setting explicitly', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Default controls ${randomUUID().slice(0, 8)}`,
  );
  let repositoryReads = 0;
  await page.route('**/api/github-repositories', (route) => {
    repositoryReads++;
    return route.fulfill({ json: { repositories: [] } });
  });

  await openApp(page, 'Work');
  const window = work(page);
  await window
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await openWorkAction(page, 'My Work defaults…');
  const editor = window.getByRole('form', {
    name: 'My Work defaults',
    exact: true,
  });
  await expect(editor.getByText('Execution', { exact: true })).toBeVisible();
  await expect(editor.locator('details[open]')).toHaveCount(0);
  await editor.getByText('Delivery', { exact: true }).click();
  const connect = editor.getByRole('button', {
    name: 'Connect repository…',
    exact: true,
  });
  await expect(connect).toHaveCount(0);
  expect(repositoryReads).toBe(0);

  const delivery = editor.getByRole('combobox', {
    name: 'Destination',
    exact: true,
  });
  const save = editor.getByRole('button', {
    name: 'Save defaults',
    exact: true,
  });
  await delivery.selectOption({ label: 'GitHub' });
  await expect(connect).toBeVisible();
  await expect(save).toBeDisabled();
  await delivery.selectOption({ label: 'Room files' });
  await expect(connect).toHaveCount(0);

  const useDefaults = editor.getByRole('checkbox', {
    name: 'Use my defaults',
    exact: true,
  });
  await expect(useDefaults).toBeChecked();
  await useDefaults.uncheck();
  await expect(
    editor.getByText('Save to use Room defaults for new Work.', {
      exact: true,
    }),
  ).toBeVisible();
  let releaseRead!: () => void;
  const heldRead = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  const defaultsPath = `/api/rooms/${room.id}/work-item-defaults`;
  await page.route(`**${defaultsPath}`, async (route) => {
    await heldRead;
    await route.continue();
  });
  try {
    await save.click();
    await expect
      .poll(async () => {
        const response = await page.request.get(defaultsPath);
        return (await response.json()).defaults.project?.enabled;
      })
      .toBe(false);
    await expect(
      editor.getByText('New Work uses Room defaults.'),
    ).toBeVisible();
    await expect(useDefaults).not.toBeChecked();
    await expect(save).toBeDisabled();
    await useDefaults.check();
    await expect(editor.getByText('Execution', { exact: true })).toBeVisible();
    await expect(save).toBeEnabled();
    const current = (await (await page.request.get(defaultsPath)).json())
      .defaults.project;
    expect(current.enabled).toBe(false);
  } finally {
    releaseRead();
  }
});

test('New Work switches between personal defaults and a private one-time override', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(page, `Work modes ${randomUUID().slice(0, 8)}`);
  const defaultsPath = `/api/rooms/${room.id}/work-item-defaults`;
  const base = (await (await page.request.get(defaultsPath)).json())
    .defaults as WorkItemDefaults;
  const result = await mutate(page, `/rooms/${room.id}/project-settings`, {
    requestId: randomUUID(),
    roomId: room.id,
    revision: 0,
    enabled: true,
    completionMode: 'automatic',
    participantAgentIds: base.participantAgentIds,
    firstAgentId: base.firstAgentId,
    budget: { ...base.budget, maxModelRequests: 32 },
  });
  expect(result.ok()).toBe(true);

  await openApp(page, 'Work');
  const window = work(page);
  const openDraft = async () => {
    await window
      .getByRole('button', { name: 'New work…', exact: true })
      .click();
    await window
      .getByRole('combobox', { name: 'Room', exact: true })
      .selectOption(room.id);
  };
  await openDraft();
  const form = window.getByRole('form', { name: 'New work', exact: true });
  const inherited = form.getByRole('radio', {
    name: 'Use my defaults',
    exact: true,
  });
  const custom = form.getByRole('radio', {
    name: 'This Work only',
    exact: true,
  });
  const goal = form.getByLabel('What needs to happen?', { exact: true });
  const completion = form.getByRole('combobox', {
    name: 'Completion',
    exact: true,
  });
  const options = form.getByRole('group', {
    name: 'Work options',
    exact: true,
  });
  const delivery = options.getByText('Delivery', { exact: true });
  const allowance = options.getByText('Allowance', { exact: true });
  const modelRequests = form.getByLabel('Model requests', { exact: true });
  await expect(inherited).toBeChecked();
  await expect(options.getByText('Details', { exact: true })).toBeVisible();
  await expect(delivery).toHaveCount(0);
  await expect(
    form.getByText('32 model requests total', { exact: true }),
  ).toBeVisible();
  await goal.fill('Keep these choices scoped to the current Work');

  await custom.check();
  await expect(delivery).toBeVisible();
  await expect(completion).toBeHidden();
  await delivery.click();
  await completion.selectOption('human');
  await allowance.click();
  await expect(completion).toBeHidden();
  await modelRequests.fill('18');

  await inherited.check();
  await expect(
    form.getByText('32 model requests total', { exact: true }),
  ).toBeVisible();
  await expect(
    form.getByText("You'll confirm completion", { exact: true }),
  ).toHaveCount(0);
  await expect(goal).toHaveValue(
    'Keep these choices scoped to the current Work',
  );
  await window
    .getByRole('button', { name: 'Back to Work', exact: true })
    .click();
  await openDraft();
  await expect(inherited).toBeChecked();
  await custom.check();
  await expect(
    form.getByText("You'll confirm completion", { exact: true }),
  ).toBeVisible();
  await expect(
    form.getByText('18 model requests total', { exact: true }),
  ).toBeVisible();

  const record = form.getByRole('button', { name: 'Keep track', exact: true });
  await allowance.click();
  await modelRequests.fill('0');
  await allowance.click();
  await expect(modelRequests).toBeHidden();
  await record.click();
  await expect(modelRequests).toBeFocused();
  await expect(modelRequests).toBeInViewport();
  await modelRequests.fill('18');

  const posted = page.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      request.url().endsWith(`/rooms/${room.id}/work-items`),
  );
  await record.click();
  const body = (await posted).postDataJSON();
  expect(body).toMatchObject({
    completionMode: 'human',
    budget: { maxModelRequests: 18 },
    goal: 'Keep these choices scoped to the current Work',
  });
  expect(body).not.toHaveProperty('projectRevision');
  expect(body).not.toHaveProperty('customDefaults');
  const after = (await (await page.request.get(defaultsPath)).json()).defaults;
  expect(after.project).toMatchObject({
    enabled: true,
    revision: 1,
    completionMode: 'automatic',
    budget: { maxModelRequests: 32 },
  });
});

test('a late project proposal preserves a goal typed during loading', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Late defaults ${randomUUID().slice(0, 8)}`,
  );
  const defaults = (
    await (
      await page.request.get(`/api/rooms/${room.id}/work-item-defaults`)
    ).json()
  ).defaults as WorkItemDefaults;
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/api/rooms/${room.id}/work-item-defaults`,
    async (route) => {
      await held;
      await route.fulfill({
        json: {
          defaults: {
            ...defaults,
            project: {
              roomId: room.id,
              revision: 3,
              enabled: true,
              participantAgentIds: defaults.participantAgentIds,
              firstAgentId: defaults.firstAgentId,
              budget: { ...defaults.budget, maxModelRequests: 28 },
              completionMode: 'human',
            },
          },
        },
      });
    },
  );
  await openApp(page, 'Work');
  await work(page)
    .getByRole('combobox', { name: 'Room', exact: true })
    .selectOption(room.id);
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  await work(page)
    .getByLabel('What needs to happen?', { exact: true })
    .fill('Keep this authored goal');
  await expect(
    work(page).getByRole('button', { name: 'Keep track', exact: true }),
  ).toBeDisabled();
  release();
  await expect(
    work(page).getByText("You'll confirm completion", { exact: true }),
  ).toBeVisible();
  await expect(
    work(page).getByLabel('What needs to happen?', { exact: true }),
  ).toHaveValue('Keep this authored goal');
  await expect(work(page).getByText(/28 model requests/)).toBeVisible();
  const posted = page.waitForRequest(
    (r) =>
      r.method() === 'POST' && r.url().endsWith(`/rooms/${room.id}/work-items`),
  );
  await work(page)
    .getByRole('button', { name: 'Keep track', exact: true })
    .click();
  expect((await posted).postDataJSON()).toMatchObject({
    goal: 'Keep this authored goal',
    projectRevision: 3,
    completionMode: 'human',
    budget: { maxModelRequests: 28 },
  });
});
