import { expect, test, type Locator, type Page } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

async function editor(page: Page, name: string) {
  await signIn(page);
  const room = await createRoom(page, name);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const win = library(page);
  await expect(win.getByText('No files yet', { exact: true })).toBeVisible();
  await win
    .getByRole('button', { name: 'More Library actions', exact: true })
    .click();
  await page
    .getByRole('menuitem', { name: 'Environment…', exact: true })
    .click();
  const panel = win.getByRole('region', { name: 'Environment and checks' });
  await panel
    .getByRole('combobox', { name: 'Environment', exact: true })
    .selectOption('command');
  return { room, panel };
}

async function addCommandCheck(panel: Locator, name: string, args: string[]) {
  await panel.getByRole('button', { name: 'Add check', exact: true }).click();
  const check = panel.getByRole('group', { name: 'Check 1', exact: true });
  await check.getByRole('textbox', { name: 'Name', exact: true }).fill(name);
  await check
    .getByRole('textbox', { name: 'Executable', exact: true })
    .fill(args[0]);
  for (let index = 1; index < args.length; index++) {
    await check
      .getByRole('button', { name: 'Add argument', exact: true })
      .click();
    await check
      .getByRole('textbox', { name: `Argument ${index}`, exact: true })
      .fill(args[index]);
  }
  return check.getByRole('textbox', { name: 'Name', exact: true });
}

test('a stale configuration save preserves edits and requires an explicit conflict choice', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `config-conflict-${info.project.name}-${info.repeatEachIndex}`,
  );
  const draft = [
    { name: 'my-checks', arguments: ['python3', '-m', 'unittest'] },
  ];
  const checks = await addCommandCheck(
    panel,
    draft[0].name,
    draft[0].arguments,
  );
  const baseline = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  // Keep both browser read paths stale while an independent client saves.
  // Otherwise the detail refresh can detect the conflict before any PATCH,
  // and this database-CAS case would wait for a request the UI correctly blocks.
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/library', async (route) => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  await page.route(`**/api/resources/${room.resourceId}`, async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  try {
    const saved = await mutate(
      page,
      `/resources/${room.resourceId}`,
      {
        expectedRevision: baseline.revision,
        environment: {
          kind: 'command',
          checks: [{ name: 'other-admin', arguments: ['true'] }],
        },
      },
      'PATCH',
    );
    expect(saved.ok()).toBeTruthy();
    const conflict = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' &&
        response.url().endsWith(`/resources/${room.resourceId}`),
    );
    await panel
      .getByRole('button', { name: 'Save Environment', exact: true })
      .click();
    expect((await conflict).status()).toBe(409);
    // The stale write has now reached storage. Let the conflict recovery read
    // retrieve the saved version for comparison without releasing our draft.
    release();
    await expect(
      panel.getByRole('button', { name: 'Keep My Edits', exact: true }),
    ).toBeVisible();
    await expect(panel.locator('pre')).toContainText('other-admin');
    await expect(checks).toHaveValue(draft[0].name);
    await expect(
      panel.getByRole('button', { name: 'Save Environment', exact: true }),
    ).toBeDisabled();
    await page.screenshot({
      path: info.outputPath('configuration-conflict.png'),
    });
    await panel
      .getByRole('button', { name: 'Keep My Edits', exact: true })
      .click();
    await expect(checks).toHaveValue(draft[0].name);
    await panel
      .getByRole('button', { name: 'Save Environment', exact: true })
      .click();
    await expect(
      panel.getByText('Environment saved.', { exact: true }),
    ).toBeVisible();
    const current = (
      await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
    ).resource;
    expect(current.revision).toBe(baseline.revision + 2);
    expect(current.environment.checks).toEqual(
      draft.map((check) => ({
        ...check,
        script: '',
      })),
    );
    expect(current.currentCommit).toBe(baseline.currentCommit);
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a committed save with a lost response is read back without another write', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `config-uncertain-${info.project.name}`,
  );
  const draft = [{ name: 'verified-saved-settings', arguments: ['true'] }];
  const checks = await addCommandCheck(
    panel,
    draft[0].name,
    draft[0].arguments,
  );
  const baseline = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  let writes = 0;
  await page.route(`**/api/resources/${room.resourceId}`, async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    writes++;
    const response = await route.fetch();
    try {
      expect(response.ok(), await response.text()).toBeTruthy();
    } finally {
      await route.abort('failed');
    }
  });
  try {
    await panel
      .getByRole('button', { name: 'Save Environment', exact: true })
      .click();
    await expect(
      panel.getByText('Environment saved.', { exact: true }),
    ).toBeVisible();
    await expect(checks).toHaveValue(draft[0].name);
    expect(writes).toBe(1);
    const current = (
      await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
    ).resource;
    expect(current.revision).toBe(baseline.revision + 1);
    expect(current.environment.checks).toEqual(
      draft.map((check) => ({
        ...check,
        script: '',
      })),
    );
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('an uncertain save keeps its payload until the user can verify saved settings', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `config-readback-${info.project.name}`,
  );
  const draft = [{ name: 'retained-draft', arguments: ['true'] }];
  const checks = await addCommandCheck(
    panel,
    draft[0].name,
    draft[0].arguments,
  );
  let readsFail = false;
  let writes = 0;
  await page.route(`**/api/resources/${room.resourceId}`, async (route) => {
    if (route.request().method() === 'PATCH') {
      writes++;
      const response = await route.fetch();
      try {
        expect(response.ok(), await response.text()).toBeTruthy();
      } finally {
        readsFail = true;
        await route.abort('failed');
      }
    } else if (readsFail) await route.abort('failed');
    else await route.continue();
  });
  try {
    await panel
      .getByRole('button', { name: 'Save Environment', exact: true })
      .click();
    const check = panel.getByRole('button', {
      name: 'Check Saved Settings',
      exact: true,
    });
    await expect(check).toBeVisible();
    await expect(checks).toHaveValue(draft[0].name);
    await expect(checks).toBeDisabled();
    await expect(
      panel.getByRole('button', { name: 'Save Environment', exact: true }),
    ).toBeDisabled();
    // The successful write emits a background Library refresh even though its
    // response and the direct readback are lost. Replacing the frozen draft
    // here could later label a different form as the confirmed saved payload.
    await expect(
      panel.getByRole('button', { name: 'Reload Saved Settings', exact: true }),
    ).toBeDisabled();
    await expect(
      panel.getByRole('button', { name: 'Keep My Edits', exact: true }),
    ).toBeDisabled();
    readsFail = false;
    await check.click();
    await expect(
      panel.getByText('Environment saved.', { exact: true }),
    ).toBeVisible();
    await expect(checks).toHaveValue(draft[0].name);
    await expect(checks).toBeEnabled();
    expect(writes).toBe(1);
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('structured command checks retain literal arguments through editing and removal', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `literal-checks-${info.project.name}`,
  );
  const baseline = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  const args = [
    'node',
    '--test',
    'test folder/a.test.mjs',
    '',
    '"quoted"',
    '$HOME; echo nope',
    'first\nsecond',
    ' leading and trailing ',
  ];
  const imported = await mutate(
    page,
    `/resources/${room.resourceId}`,
    {
      expectedRevision: baseline.revision,
      environment: {
        kind: 'command',
        checks: [
          { name: 'complex', arguments: args, directory: 'packages/web' },
          { name: 'remove-me', arguments: ['true'] },
        ],
      },
    },
    'PATCH',
  );
  expect(imported.ok()).toBeTruthy();
  await panel
    .getByRole('button', { name: 'Reload Saved Settings', exact: true })
    .click();
  const first = panel.getByRole('group', { name: 'Check 1', exact: true });
  await expect(
    first.getByRole('textbox', { name: 'Executable', exact: true }),
  ).toHaveValue(args[0]);
  for (let index = 1; index < args.length; index++)
    await expect(
      first.getByRole('textbox', { name: `Argument ${index}`, exact: true }),
    ).toHaveValue(args[index]);
  await expect(first.locator('summary')).toContainText('packages/web');
  await first.locator('summary').click();
  await expect(
    first.getByRole('textbox', { name: 'Working directory', exact: true }),
  ).toHaveValue('packages/web');
  await panel
    .getByRole('button', { name: 'Remove check 2', exact: true })
    .click();
  await first
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('retained');
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    panel.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  const current = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  expect(current.environment.checks).toEqual([
    {
      name: 'retained',
      script: '',
      arguments: args,
      directory: 'packages/web',
    },
  ]);
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
  expect(
    await panel.evaluate((node) => node.scrollWidth > node.clientWidth),
  ).toBe(false);
  await page.screenshot({
    path: info.outputPath('structured-command-checks.png'),
  });
});

test('Node checks keep named package scripts separate from default node tests', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `node-checks-${info.project.name}`,
  );
  await panel
    .getByRole('combobox', { name: 'Environment', exact: true })
    .selectOption('node');
  await panel
    .getByRole('combobox', { name: 'Package manager', exact: true })
    .selectOption('pnpm@12.11.2');
  await panel.getByRole('button', { name: 'Add check', exact: true }).click();
  const unit = panel.getByRole('group', { name: 'Check 1', exact: true });
  await unit.getByRole('textbox', { name: 'Name', exact: true }).fill('unit');
  await unit
    .getByRole('textbox', { name: 'Package script', exact: true })
    .fill('test:unit');
  await unit.locator('summary').click();
  await unit
    .getByRole('textbox', { name: 'Working directory', exact: true })
    .fill('web');
  await unit
    .getByRole('textbox', { name: 'Working directory', exact: true })
    .fill('');
  await expect(
    unit.getByRole('textbox', { name: 'Working directory', exact: true }),
  ).toBeVisible();
  await unit
    .getByRole('textbox', { name: 'Working directory', exact: true })
    .fill('web');
  await panel.getByRole('button', { name: 'Add check', exact: true }).click();
  await panel
    .getByRole('group', { name: 'Check 2', exact: true })
    .getByRole('textbox', { name: 'Name', exact: true })
    .fill('default-tests');
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    panel.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  const current = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  expect(current.environment).toMatchObject({
    kind: 'node',
    packageManager: 'pnpm@12.11.2',
    checks: [
      { name: 'unit', script: 'test:unit', directory: 'web' },
      { name: 'default-tests', script: '' },
    ],
  });
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});
