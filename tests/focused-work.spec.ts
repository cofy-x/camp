import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import {
  createRoom,
  mutate,
  openApp,
  signIn,
  work,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

test('global creation preserves the goal and isolates Room settings without replacing scoped drafts', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  // Do not visit this Room first: its accepted context must not already be in
  // the shared read cache when the settings response is held below.
  const createdRoom = await mutate(page, '/rooms', {
    name: `Goal first ${randomUUID().slice(0, 8)}`,
  });
  expect(createdRoom.ok()).toBe(true);
  const { room: first } = await createdRoom.json();
  const second = await createRoom(
    page,
    `Goal second ${randomUUID().slice(0, 8)}`,
  );
  const scopedKey = `work-item-create:${first.id}:manual`;
  const scoped = { goal: 'Keep this separate Room draft.' };
  expect(
    (
      await mutate(
        page,
        '/me/drafts/conversation',
        {
          key: scopedKey,
          revision: 0,
          value: scoped,
        },
        'PUT',
      )
    ).ok(),
  ).toBe(true);
  const globalKey = 'work-item-create::manual';
  const draftUrl = `/api/me/drafts/conversation?key=${encodeURIComponent(globalKey)}`;
  const scopedUrl = `/api/me/drafts/conversation?key=${encodeURIComponent(scopedKey)}`;
  await openApp(page, 'Work');
  await work(page)
    .getByRole('button', { name: 'New work…', exact: true })
    .click();
  const room = work(page).getByRole('combobox', { name: 'Room', exact: true });
  const goal = work(page).getByLabel('What needs to happen?', { exact: true });
  const request = 'Prepare the customer release brief.';
  await expect(room).toHaveValue('');
  await expect(goal).toBeEditable();
  await goal.fill(request);
  await expect(
    work(page).getByRole('button', { name: 'Start work', exact: true }),
  ).toBeDisabled();
  await expect(
    work(page).getByRole('button', { name: 'Keep track', exact: true }),
  ).toBeDisabled();
  await expect(work(page).locator('.work-grant')).toHaveCount(0);
  await expect
    .poll(
      async () =>
        (await (await page.request.get(draftUrl)).json()).draft.value?.goal,
    )
    .toBe(request);

  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/api/rooms/${first.id}/members`, async (route) => {
    await held;
    await route.continue();
  });
  try {
    await room.selectOption(first.id);
    await expect(goal).toHaveValue(request);
    await expect(
      work(page).getByRole('button', { name: 'Keep track', exact: true }),
    ).toBeDisabled();
    await goal.fill(`${request} Include the checklist.`);
    await expect(goal).toBeFocused();
    release();
    await expect(work(page).locator('.work-grant')).toContainText(
      'Team-visible draft',
    );
    await expect(goal).toBeFocused();
    await work(page).getByText('Options', { exact: true }).click();
    await work(page).getByText('Allowance and access', { exact: true }).click();
    const requests = work(page).getByLabel('Model requests', { exact: true });
    await requests.fill('7');
    await expect(work(page).locator('.work-grant-limits')).toContainText(
      '7 model requests',
    );
    await room.selectOption(second.id);
    await expect(goal).toHaveValue(`${request} Include the checklist.`);
    await expect(requests).not.toHaveValue('7');
    await room.selectOption(first.id);
    await expect(requests).toHaveValue('7');
    expect(
      (await (await page.request.get(scopedUrl)).json()).draft.value,
    ).toEqual(scoped);
    await work(page)
      .getByRole('button', { name: 'Back to Work', exact: true })
      .click();
    await work(page)
      .getByRole('button', { name: 'New work…', exact: true })
      .click();
    await expect(goal).toHaveValue(`${request} Include the checklist.`);
    await expect(room).toHaveValue('');
    await room.selectOption(first.id);
    await expect(
      work(page).getByRole('button', { name: 'Keep track', exact: true }),
    ).toBeEnabled();
    await work(page)
      .getByRole('button', { name: 'Keep track', exact: true })
      .click();
    await expect(
      work(page).getByRole('region', { name: 'Work item detail', exact: true }),
    ).toContainText(`${request} Include the checklist.`);
    expect(
      (await (await page.request.get(draftUrl)).json()).draft.value,
    ).toBeNull();
    expect(
      (await (await page.request.get(scopedUrl)).json()).draft.value,
    ).toEqual(scoped);
    const { items } = await (
      await page.request.get(`/api/rooms/${first.id}/work-items`)
    ).json();
    expect(items).toHaveLength(1);
    expect(items[0].budget.maxModelRequests).toBe(7);
    const { item: recorded } = await (
      await page.request.get(`/api/work-items/${items[0].id}`)
    ).json();
    expect(recorded.tasks).toHaveLength(0);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
  }
});

test('a long matter goal keeps the current question reachable and retains full-text reading across views', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Readable goal ${randomUUID().slice(0, 8)}`,
  );
  const goal =
    'Prepare the customer release brief. ' +
    'Use the existing requirements, confirm the launch date and verify the saved document before completion. '.repeat(
      15,
    );
  const created = await mutate(page, `/rooms/${room.id}/work-items`, {
    requestId: randomUUID(),
    goal,
  });
  expect(created.ok()).toBe(true);
  const { item } = await created.json();
  const question = 'Should we propose Tuesday or Thursday?';
  expect(
    (
      await mutate(page, `/work-items/${item.id}/decisions`, {
        requestId: randomUUID(),
        revision: item.revision,
        kind: 'request_judgment',
        reason: 'The release date needs confirmation.',
        question,
      })
    ).ok(),
  ).toBe(true);
  await workspace(page)
    .getByRole('button', { name: 'Work', exact: true })
    .click();
  await work(page)
    .getByRole('button', {
      name: new RegExp('^Prepare the customer release brief'),
    })
    .click();
  const detail = work(page).getByRole('region', {
    name: 'Work item detail',
    exact: true,
  });
  await expect(
    detail.getByRole('heading', { name: goal, exact: true }),
  ).toBeVisible();
  await expect(
    detail.getByRole('heading', { name: question, exact: true }),
  ).toBeInViewport();
  const expand = detail.getByRole('button', {
    name: 'Show full goal',
    exact: true,
  });
  await expand.click();
  const collapse = detail.getByRole('button', {
    name: 'Show less',
    exact: true,
  });
  await expect(collapse).toHaveAttribute('aria-expanded', 'true');
  await detail.getByRole('button', { name: 'Activity', exact: true }).click();
  await expect(collapse).toHaveAttribute('aria-expanded', 'true');
  await detail
    .getByRole('button', { name: 'Back to item', exact: true })
    .click();
  await collapse.click();
  await expect(expand).toBeFocused();
  await expect(
    detail.getByRole('heading', { name: question, exact: true }),
  ).toBeInViewport();
  expect(errors.errors).toEqual([]);
});

test('message overflow is keyboard accessible and opening it preserves the composer', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Message actions ${randomUUID().slice(0, 8)}`,
  );
  const posted = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Prepare the release checklist.',
    postOnly: true,
  });
  expect(posted.ok()).toBe(true);
  const { message } = await posted.json();
  const row = workspace(page).locator(`[data-message-id="${message.id}"]`);
  await expect(
    row.getByRole('button', { name: 'Reply in thread', exact: true }),
  ).toBeVisible();
  await expect(
    row.getByRole('button', { name: 'Ask agent…', exact: true }),
  ).toHaveCount(0);
  const input = workspace(page).getByLabel(`Message ${room.name}`, {
    exact: true,
  });
  await input.fill('Keep this unfinished reply.');
  const more = row.getByRole('button', {
    name: 'More message actions',
    exact: true,
  });
  await more.focus();
  await more.press('ArrowDown');
  for (const name of ['Ask agent…', 'Follow up…', 'Remember this…'])
    await expect(
      page.getByRole('menuitem', { name, exact: true }),
    ).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  await expect(input).toHaveValue('Keep this unfinished reply.');
  expect(errors.errors).toEqual([]);
});
