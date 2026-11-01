import { expect, test, type Page } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

async function editor(page: Page, name: string) {
  await signIn(page);
  const room = await createRoom(page, name);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  const win = library(page);
  await expect(
    win.getByRole('heading', { name: /^No files in/ }),
  ).toBeVisible();
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

test('a stale configuration save preserves edits and requires an explicit conflict choice', async ({
  page,
}, info) => {
  const { room, panel } = await editor(
    page,
    `config-conflict-${info.project.name}-${info.repeatEachIndex}`,
  );
  const checks = panel.getByLabel('Checks (JSON array)', { exact: true });
  const draft =
    '[{"name":"my-checks","arguments":["python3","-m","unittest"]}]';
  await checks.fill(draft);
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
    await expect(checks).toHaveValue(draft);
    await expect(
      panel.getByRole('button', { name: 'Save Environment', exact: true }),
    ).toBeDisabled();
    await page.screenshot({
      path: info.outputPath('configuration-conflict.png'),
    });
    await panel
      .getByRole('button', { name: 'Keep My Edits', exact: true })
      .click();
    await expect(checks).toHaveValue(draft);
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
      JSON.parse(draft).map((check: { name: string; arguments: string[] }) => ({
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
  const checks = panel.getByLabel('Checks (JSON array)', { exact: true });
  const draft = '[{"name":"verified-saved-settings","arguments":["true"]}]';
  await checks.fill(draft);
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
    await expect(checks).toHaveValue(draft);
    expect(writes).toBe(1);
    const current = (
      await (await page.request.get(`/api/resources/${room.resourceId}`)).json()
    ).resource;
    expect(current.revision).toBe(baseline.revision + 1);
    expect(current.environment.checks).toEqual(
      JSON.parse(draft).map((check: { name: string; arguments: string[] }) => ({
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
  const draft = '[{"name":"retained-draft","arguments":["true"]}]';
  const checks = panel.getByLabel('Checks (JSON array)', { exact: true });
  await checks.fill(draft);
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
    await expect(checks).toHaveValue(draft);
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
    await expect(checks).toHaveValue(draft);
    await expect(checks).toBeEnabled();
    expect(writes).toBe(1);
  } finally {
    await page.unrouteAll({ behavior: 'wait' });
  }
});
