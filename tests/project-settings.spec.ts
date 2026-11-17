import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import {
  createRoom,
  openApp,
  openWorkAction,
  signIn,
  work,
} from './team-helpers';
import type {
  WorkItemDefaults,
  ProjectSettings,
} from '../apps/dashboard/src/features/workitems/api';

test('personal project defaults keep routine Work concise and freeze the visible grant', async ({
  page,
}) => {
  await signIn(page);
  const room = await createRoom(page, `Project ${randomUUID().slice(0, 8)}`);
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
  await openWorkAction(page, 'Project settings…');
  const editor = work(page).getByRole('form', {
    name: 'Project settings',
    exact: true,
  });
  await expect(editor).toBeVisible();
  await editor.getByText('Total allowance', { exact: true }).click();
  await editor.getByLabel('Model requests', { exact: true }).fill('32');
  await editor
    .getByRole('button', { name: 'Save defaults', exact: true })
    .click();
  await expect.poll(() => writes).toBe(1);
  await editor.getByRole('button', { name: 'Done', exact: true }).click();
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
