import { expect, test, type Locator, type Page } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

function apiWrites(page: Page) {
  const writes: { method: string; path: string; body: string }[] = [];
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    // Desktop geometry saves independently of environment configuration.
    if (
      path.startsWith('/api/') &&
      path !== '/api/layout' &&
      !['GET', 'HEAD'].includes(request.method())
    )
      writes.push({
        method: request.method(),
        path,
        body: request.postData() ?? '',
      });
  });
  return writes;
}

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
  const libraryBaseline = await (await page.request.get('/api/library')).json();
  // Keep both browser read paths stale while an independent client saves.
  // Otherwise the detail refresh can detect the conflict before any PATCH,
  // and this database-CAS case would wait for a request the UI correctly blocks.
  let staleReads = true;
  await page.route('**/api/library', async (route) => {
    if (!staleReads) return route.continue();
    await route.fulfill({ json: libraryBaseline });
  });
  await page.route(`**/api/resources/${room.resourceId}`, async (route) => {
    if (route.request().method() === 'PATCH' && staleReads) {
      // Switch reads before delivering the actual storage response, so the
      // conflict recovery always sees the independently saved configuration.
      const response = await route.fetch();
      staleReads = false;
      return route.fulfill({ response });
    }
    if (route.request().method() === 'GET' && staleReads)
      return route.fulfill({ json: { resource: baseline } });
    return route.continue();
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
    await expect(
      panel.getByText('Environment saved.', { exact: true }),
    ).toHaveCount(0);
    await expect(
      panel.getByText('Unsaved changes', { exact: true }),
    ).toHaveCount(0);
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
    first.getByRole('textbox', { name: 'Name', exact: true }),
  ).toHaveCount(0);
  await first
    .getByRole('button', { name: 'Edit check 1', exact: true })
    .click();
  await expect(
    first.getByRole('textbox', { name: 'Executable', exact: true }),
  ).toHaveValue(args[0]);
  let previousArgument: { x: number; y: number; height: number } | null = null;
  for (let index = 1; index < args.length; index++) {
    const argument = first.getByRole('textbox', {
      name: `Argument ${index}`,
      exact: true,
    });
    await expect(argument).toHaveValue(args[index]);
    const fieldBounds = await argument.boundingBox();
    const removeBounds = await first
      .getByRole('button', { name: `Remove argument ${index}`, exact: true })
      .boundingBox();
    expect(fieldBounds).not.toBeNull();
    expect(removeBounds).not.toBeNull();
    expect(
      Math.abs(removeBounds!.height - removeBounds!.width),
    ).toBeLessThanOrEqual(1);
    if (fieldBounds!.height <= 27) {
      expect(
        Math.abs(fieldBounds!.height - removeBounds!.height),
      ).toBeLessThanOrEqual(1);
    } else {
      expect(removeBounds!.height).toBeLessThanOrEqual(27);
      expect(
        Math.abs(
          fieldBounds!.y +
            fieldBounds!.height / 2 -
            (removeBounds!.y + removeBounds!.height / 2),
        ),
      ).toBeLessThanOrEqual(1);
    }
    if (previousArgument) {
      expect(fieldBounds!.y).toBeGreaterThanOrEqual(
        previousArgument.y + previousArgument.height - 1,
      );
      expect(Math.abs(fieldBounds!.x - previousArgument.x)).toBeLessThanOrEqual(
        1,
      );
    }
    previousArgument = fieldBounds;
  }
  const addArgumentBounds = await first
    .getByRole('button', { name: 'Add argument', exact: true })
    .boundingBox();
  expect(addArgumentBounds!.y).toBeGreaterThanOrEqual(
    previousArgument!.y + previousArgument!.height - 1,
  );
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
    .fill('invalid script');
  await unit.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(
    unit.getByRole('textbox', { name: 'Package script', exact: true }),
  ).toBeFocused();
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

test('saved checks collapse to literal summaries and editing stays local until Save', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `compact-checks-${info.project.name}`,
  );
  const baseline = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  const args = ['node', '--test', 'test folder/notes.test.mjs', '', '"quoted"'];
  const imported = await mutate(
    page,
    `/resources/${room.resourceId}`,
    {
      expectedRevision: baseline.revision,
      environment: {
        kind: 'command',
        checks: [
          { name: 'unit', arguments: args },
          { name: 'remove-me', arguments: ['true'] },
        ],
      },
    },
    'PATCH',
  );
  expect(imported.ok()).toBe(true);
  await panel
    .getByRole('button', { name: 'Reload Saved Settings', exact: true })
    .click();
  const first = panel.getByRole('group', { name: 'Check 1', exact: true });
  const name = first.getByRole('textbox', { name: 'Name', exact: true });
  const edit = first.getByRole('button', { name: 'Edit check 1', exact: true });
  await expect(edit).toHaveAttribute('aria-expanded', 'false');
  await expect(name).toHaveCount(0);
  await expect(first).toContainText('"test folder/notes.test.mjs"');
  await expect(first).toContainText('""');
  await expect(
    panel.getByText('Saved environment', { exact: true }),
  ).toHaveCount(0);
  await expect(panel.getByText('Unsaved changes', { exact: true })).toHaveCount(
    0,
  );
  const writes = apiWrites(page);
  await edit.click();
  await expect(
    first.getByRole('button', { name: 'Done', exact: true }),
  ).toHaveAttribute('aria-expanded', 'true');
  await name.fill('changed-unit');
  await expect(
    panel.getByText('Unsaved changes', { exact: true }),
  ).toBeVisible();
  await name.fill('unit');
  await expect(panel.getByText('Unsaved changes', { exact: true })).toHaveCount(
    0,
  );
  await name.fill('changed-unit');
  await first.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(name).toHaveCount(0);
  await expect(edit).toBeFocused();
  await expect(first).toContainText('changed-unit');
  expect(writes).toEqual([]);
  await edit.click();
  await expect(name).toHaveValue('changed-unit');
  for (let index = 1; index < args.length; index++)
    await expect(
      first.getByRole('textbox', { name: `Argument ${index}`, exact: true }),
    ).toHaveValue(args[index]);
  await first.getByRole('button', { name: 'Done', exact: true }).click();
  const remove = panel.getByRole('button', {
    name: 'Remove check 2',
    exact: true,
  });
  await remove.focus();
  await remove.press('Enter');
  await expect(edit).toBeFocused();
  expect(writes).toEqual([]);
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    panel.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  expect(writes).toHaveLength(1);
  expect(writes[0]).toMatchObject({
    method: 'PATCH',
    path: `/api/resources/${room.resourceId}`,
  });
  const current = (
    await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
  ).resource;
  expect(current.environment.checks).toEqual([
    { name: 'changed-unit', script: '', arguments: args },
  ]);
  expect(current.currentCommit).toBe(baseline.currentCommit);
});

test('check and argument controls retain keyboard focus without saving', async ({
  page,
}, info) => {
  const { panel } = await editor(page, `check-focus-${info.project.name}`);
  const writes = apiWrites(page);
  const add = panel.getByRole('button', { name: 'Add check', exact: true });
  await add.focus();
  await add.press('Enter');
  const check = panel.getByRole('group', { name: 'Check 1', exact: true });
  const name = check.getByRole('textbox', { name: 'Name', exact: true });
  await expect(name).toBeFocused();
  await name.fill('unit');
  await check
    .getByRole('textbox', { name: 'Executable', exact: true })
    .fill('node');
  const addArgument = check.getByRole('button', {
    name: 'Add argument',
    exact: true,
  });
  await addArgument.click();
  const first = check.getByRole('textbox', { name: 'Argument 1', exact: true });
  await expect(first).toBeFocused();
  await first.fill('first');
  await addArgument.click();
  await expect(
    check.getByRole('textbox', { name: 'Argument 2', exact: true }),
  ).toBeFocused();
  await check
    .getByRole('textbox', { name: 'Argument 2', exact: true })
    .fill('second');
  const removeFirst = check.getByRole('button', {
    name: 'Remove argument 1',
    exact: true,
  });
  await removeFirst.focus();
  await removeFirst.press('Enter');
  await expect(first).toBeFocused();
  await expect(first).toHaveValue('second');
  const removeLast = check.getByRole('button', {
    name: 'Remove argument 1',
    exact: true,
  });
  await removeLast.focus();
  await removeLast.press('Enter');
  await expect(addArgument).toBeFocused();
  await check.getByRole('button', { name: 'Done', exact: true }).click();
  const removeCheck = check.getByRole('button', {
    name: 'Remove check 1',
    exact: true,
  });
  await removeCheck.focus();
  await removeCheck.press('Enter');
  await expect(add).toBeFocused();
  await expect(
    panel.getByRole('group', { name: 'Check 1', exact: true }),
  ).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('Done and Save retain an invalid check editor and focus its required field', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `check-validity-${info.project.name}`,
  );
  const writes = apiWrites(page);
  await panel.getByRole('button', { name: 'Add check', exact: true }).click();
  const check = panel.getByRole('group', { name: 'Check 1', exact: true });
  const name = check.getByRole('textbox', { name: 'Name', exact: true });
  const executable = check.getByRole('textbox', {
    name: 'Executable',
    exact: true,
  });
  await executable.fill('node');
  await check.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(name).toBeFocused();
  await expect(
    check.getByRole('button', { name: 'Done', exact: true }),
  ).toBeVisible();
  expect(writes).toEqual([]);
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(name).toBeFocused();
  expect(writes).toEqual([]);
  await name.fill('invalid check');
  await check.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(name).toBeFocused();
  expect(writes).toEqual([]);
  await name.fill('unit');
  await executable.fill('');
  await check.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(executable).toBeFocused();
  expect(writes).toEqual([]);
  await executable.fill('node');
  await check.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(
    check.getByRole('button', { name: 'Edit check 1', exact: true }),
  ).toBeFocused();
  expect(writes).toEqual([]);
  // Native constraints can become invalid while the fields are collapsed.
  // The form must reveal the actual control before the browser focuses it.
  await check.getByLabel('Name', { exact: true }).evaluate((element) => {
    (element as HTMLInputElement).setCustomValidity(
      'Review this field before saving.',
    );
  });
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(name).toBeFocused();
  await expect(name).toHaveValue('unit');
  expect(writes).toEqual([]);
  await name.evaluate((element) =>
    (element as HTMLInputElement).setCustomValidity(''),
  );
  await panel
    .getByRole('button', { name: 'Save Environment', exact: true })
    .click();
  await expect(
    panel.getByText('Environment saved.', { exact: true }),
  ).toBeVisible();
  expect(writes).toHaveLength(1);
  expect(writes[0]).toMatchObject({
    method: 'PATCH',
    path: `/api/resources/${room.resourceId}`,
  });
});
