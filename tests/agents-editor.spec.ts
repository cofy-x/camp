import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type { Agent } from '../apps/dashboard/src/features/workspace/api';
import { createRoom, mutate, openApp, signIn, workspace } from './team-helpers';

async function createAgent(page: Page, name: string) {
  const stamp = randomUUID().slice(0, 8);
  const response = await mutate(page, '/agents', {
    name: `${name} ${stamp}`,
    handle: `editor-${stamp}`,
    access: 'read',
    enabled: true,
    instructions: 'Review the requested file.',
  });
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { agent: Agent }).agent;
}

test('Agent editing retains the draft across selection and close, and saves against its original revision', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `Agent editor ${randomUUID().slice(0, 8)}`,
  );
  const first = await createAgent(page, 'Draft Agent');
  const other = await createAgent(page, 'Room Agent');
  expect(
    (
      await mutate(page, `/rooms/${room.id}/members/${other.id}`, {}, 'PUT')
    ).ok(),
  ).toBeTruthy();
  await openApp(page, 'Agents');
  const app = page.locator('[data-window-id="agents:main"]');
  const chooser = app.getByLabel('Agent', { exact: true });
  const sidebar = app.getByRole('navigation', { name: 'Agents', exact: true });
  const title = app.locator('.p9-window-title');
  const editor = app.getByRole('form', { name: 'Agent settings', exact: true });
  const name = editor.getByRole('textbox', { name: 'Name', exact: true });
  const handle = editor.getByRole('textbox', { name: 'Handle', exact: true });
  const responsibilities = editor.getByRole('textbox', {
    name: 'Responsibilities',
    exact: true,
  });
  const save = editor.getByRole('button', { name: 'Save Agent', exact: true });
  const scope = 'Changes apply in every Room this Agent joins.';
  const close = app.getByRole('button', {
    name: `Close Edit ${first.name} — Agents`,
    exact: true,
  });
  const warning = editor.getByText(
    'Save or cancel your Agent edits before leaving.',
    { exact: true },
  );

  await expect(app.locator('.panel-heading')).toBeVisible();

  async function select(agent: Agent) {
    if (await chooser.isVisible()) await chooser.selectOption(agent.id);
    else
      await sidebar
        .getByRole('button', { name: new RegExp(agent.name) })
        .click();
  }

  await select(first);
  await app.getByRole('button', { name: 'Edit Agent…', exact: true }).click();
  await expect(save).toBeInViewport();
  await expect(name).toHaveValue(first.name);
  await expect(handle).toBeVisible();
  await expect(handle).toHaveValue(first.handle);
  await expect(save).toBeDisabled();
  await expect(editor.getByText(scope, { exact: true })).toHaveCount(0);
  await name.fill(`${first.name} `);
  await expect(save).toBeDisabled();
  await name.press('Enter');
  await expect(editor).toBeVisible();
  const draft =
    'Keep these local responsibilities while I check another Agent.';
  await responsibilities.fill(draft);
  await expect(save).toBeEnabled();
  await expect(save).toHaveAccessibleDescription(scope);
  await responsibilities.fill(first.instructions);
  await expect(save).toBeDisabled();
  await expect(editor.getByText(scope, { exact: true })).toHaveCount(0);
  await responsibilities.fill(draft);
  await select(other);
  await expect(warning).toBeVisible();
  await expect(responsibilities).toHaveValue(draft);
  await expect(title).toHaveText(`Edit ${first.name} — Agents`);

  await openApp(page, 'Workspace');
  await workspace(page)
    .getByLabel('Room members')
    .getByRole('button', { name: new RegExp(other.name) })
    .click();
  await expect(app).toHaveClass(/is-active/);
  await expect(title).toHaveText(`Edit ${first.name} — Agents`);
  await close.click();
  await expect(warning).toBeVisible();
  await expect(responsibilities).toHaveValue(draft);

  const newer = await mutate(
    page,
    `/agents/${first.id}`,
    { name: `Updated ${first.name}`, revision: first.revision },
    'PATCH',
  );
  expect(newer.ok()).toBeTruthy();
  if (await chooser.isVisible()) {
    await expect(
      chooser.locator('option', { hasText: `Updated ${first.name}` }),
    ).toHaveCount(1);
  } else {
    await expect(
      sidebar.getByRole('button', {
        name: new RegExp(`Updated ${first.name}`),
      }),
    ).toBeVisible();
  }

  let releaseSave: () => void;
  const held = new Promise<void>((resolve) => {
    releaseSave = resolve;
  });
  await page.route(`**/api/agents/${first.id}`, async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    await held;
    return route.continue();
  });
  const requested = page.waitForRequest(
    (request) =>
      request.url().endsWith(`/api/agents/${first.id}`) &&
      request.method() === 'PATCH',
  );
  try {
    await save.click();
    expect((await requested).postDataJSON()).toMatchObject({
      instructions: draft,
      revision: first.revision,
    });
    await expect(
      editor.getByRole('button', { name: 'Cancel', exact: true }),
    ).toBeDisabled();
    await close.click();
    await expect(
      editor.getByText('Wait for the Agent update to finish.', { exact: true }),
    ).toBeVisible();
  } finally {
    releaseSave!();
  }
  await expect(editor.getByRole('alert')).toContainText('The agent changed.');
  await expect(responsibilities).toHaveValue(draft);
  await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
  await select(other);
  await expect(title).toHaveText(`${other.name} — Agents`);
});

test('a short Agent editor scrolls its fields above the save footer', async ({
  page,
  isMobile,
}) => {
  await page.setViewportSize({ width: isMobile ? 390 : 718, height: 520 });
  await signIn(page);
  await openApp(page, 'Agents');
  const app = page.locator('[data-window-id="agents:main"]');
  await app.getByRole('button', { name: 'Edit Agent…', exact: true }).click();
  const editor = app.getByRole('form', { name: 'Agent settings', exact: true });
  const fields = editor.locator('.workspace-agent-editor-fields');
  const footer = editor.locator('footer');
  const save = footer.getByRole('button', { name: 'Save Agent', exact: true });
  await expect(save).toBeInViewport({ ratio: 1 });
  const before = (await footer.boundingBox())!;

  const enabled = editor.getByRole('checkbox', {
    name: 'Enabled',
    exact: true,
  });
  await enabled.scrollIntoViewIfNeeded();
  await enabled.focus();
  await expect(enabled).toBeInViewport({ ratio: 1 });
  await expect(enabled).toBeFocused();
  expect(await fields.evaluate((node) => node.scrollTop)).toBeGreaterThan(0);
  const fieldBounds = (await fields.boundingBox())!;
  expect(fieldBounds.y + fieldBounds.height).toBeLessThanOrEqual(before.y + 1);
  expect((await footer.boundingBox())!.y).toBeCloseTo(before.y, 0);
  await expect(save).toBeInViewport({ ratio: 1 });
  await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(editor).toHaveCount(0);
});
