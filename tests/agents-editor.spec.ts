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
  const responsibilities = editor.getByRole('textbox', {
    name: 'Responsibilities',
    exact: true,
  });
  const save = editor.getByRole('button', { name: 'Save Agent', exact: true });
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
  const draft =
    'Keep these local responsibilities while I check another Agent.';
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
