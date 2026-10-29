import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  openApp,
  owner,
  post,
  settings,
  signIn,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

test('first-run discussion, private drafts, threads and explicit agent requests', async ({
  page,
}, testInfo) => {
  for (const name of ['Workspace', 'Library', 'Agents', 'Runners', 'Settings'])
    await expect(
      page.locator('.p9-desktop-icon').getByText(name, { exact: true }),
    ).toHaveCount(1);
  await expect(page.locator('.p9-desktop-icon')).toHaveCount(5);
  if (['chromium', 'touch'].includes(testInfo.project.name))
    await page.screenshot({
      path: testInfo.outputPath('workspace-first-run.png'),
    });
  await workspace(page)
    .getByRole('button', { name: 'Set Up Agent Work…', exact: true })
    .click();
  await expect(
    settings(page).getByRole('heading', { name: 'Getting Ready', exact: true }),
  ).toBeVisible();
  await settings(page)
    .getByRole('button', { name: 'Open Workspace', exact: true })
    .click();
  const name = `discussion-${testInfo.project.name}`;
  const room = await createRoom(page, name);
  await post(page, name, 'Let us prepare a useful onboarding guide.');
  await expect(
    workspace(page).getByText(owner.name, { exact: true }),
  ).toBeVisible();
  const messages = await (
    await page.request.get(`/api/rooms/${room.id}/messages`)
  ).json();
  expect(messages.messages).toHaveLength(1);
  expect(messages.messages[0].runId).toBe('');
  expect(messages.messages[0].authorName).toBe(owner.name);

  const input = page.getByLabel(`Message ${name}`, { exact: true });
  await input.fill('Remember our three preparation steps.');
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `/api/me/drafts/conversation?key=${room.id}:room`,
            )
          ).json()
        ).draft.value?.draft,
    )
    .toBe('Remember our three preparation steps.');
  await errors.get(page)!.reload();
  await expect(input).toHaveValue('Remember our three preparation steps.');
  await input.fill('准备一份入门指南。');
  await input.dispatchEvent('compositionstart');
  await input.dispatchEvent('keydown', {
    key: 'Enter',
    ctrlKey: true,
    isComposing: true,
    bubbles: true,
  });
  await input.dispatchEvent('compositionend', { data: '准备一份入门指南。' });
  await expect(input).toHaveValue('准备一份入门指南。');
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/messages`)).json())
      .messages,
  ).toHaveLength(1);
  await input.press('ControlOrMeta+a');
  await input.press('Backspace');
  await expect(input).toHaveValue('');
  await workspace(page)
    .getByRole('button', { name: 'Reply in thread', exact: true })
    .click();
  await post(page, 'thread', 'I will take the first draft.');
  await workspace(page)
    .getByRole('button', { name: 'Back to Room', exact: true })
    .click();
  await expect(input).toBeVisible();
  await input.fill('@assistant Please draft a guide.');
  await input.press('Escape');
  await expect(
    workspace(page).getByRole('button', { name: 'Send to Agent', exact: true }),
  ).toBeDisabled();
  const runs = await (
    await page.request.get(`/api/rooms/${room.id}/runs`)
  ).json();
  expect(runs.runs).toEqual([]);
});

test('Library private editing, publication, source handoff and revision conflict', async ({
  page,
}, testInfo) => {
  const name = `writing-${testInfo.project.name}`;
  const room = await createRoom(page, name);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await newDocument(
    page,
    'guide.md',
    '# Onboarding\nPrepare an account and agree on an owner.',
  );
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `/api/me/drafts/document?key=${room.resourceId}:guide.md`,
            )
          ).json()
        ).draft.value?.content,
    )
    .toContain('Prepare an account');
  const before = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  expect(before.files).toEqual([]);
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  await expect(
    library(page).getByText(/Browser Owner.*Checks not run/),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  await expect(
    library(page).getByRole('combobox', { name: 'Use in room', exact: true }),
  ).toHaveValue(room.id);
  await library(page)
    .getByRole('button', { name: 'Add to Message', exact: true })
    .click();
  await expect(workspace(page)).toHaveClass(/is-active/);
  await expect(
    workspace(page)
      .getByRole('form', { name: 'Room composer' })
      .getByText('guide.md', { exact: true }),
  ).toBeVisible();
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/messages`)).json())
      .messages,
  ).toEqual([]);
  await post(page, name, 'Please review the attached guide with me.');
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'Edit Text', exact: true })
    .click();
  await library(page)
    .getByLabel('Document content', { exact: true })
    .fill('My local revision stays safe.');
  const files = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  const save = await mutate(
    page,
    `/resources/${room.resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: files.commit,
      path: 'guide.md',
      content: 'Another teammate changed the shared file.',
    },
    'PUT',
  );
  expect(save.ok()).toBeTruthy();
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(library(page).getByRole('alert')).toBeVisible();
  await expect(
    library(page).getByLabel('Document content', { exact: true }),
  ).toHaveValue('My local revision stays safe.');
  await library(page)
    .getByRole('button', { name: 'Compare Shared Version', exact: true })
    .click();
  await expect(
    library(page).getByText('Another teammate changed the shared file.', {
      exact: true,
    }),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'Use This Baseline', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
});

test('invited teammate, human mentions outside Workspace and account revocation', async ({
  page,
  browser,
  baseURL,
  isMobile,
}, testInfo) => {
  const createdAgent = await mutate(page, '/agents', {
    name: `Team Writer ${testInfo.project.name}`,
    handle: `team-writer-${testInfo.project.name}`,
    instructions: 'Write clear team documents when explicitly asked.',
    access: 'write',
  });
  expect(createdAgent.ok()).toBeTruthy();
  const agent = (await createdAgent.json()).agent as {
    id: string;
    name: string;
  };
  await openApp(page, 'Settings');
  if (isMobile)
    await settings(page)
      .getByLabel('Control panel', { exact: true })
      .selectOption('team');
  else
    await settings(page)
      .getByRole('button', { name: 'Team', exact: true })
      .click();
  await settings(page)
    .getByRole('button', { name: 'Create Invitation', exact: true })
    .click();
  const invite = settings(page).getByLabel('Invitation link', { exact: true });
  await expect(invite).toHaveValue(/#invite=/);
  const context = await browser.newContext({
    baseURL,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  const member = await context.newPage();
  try {
    await member.goto(await invite.inputValue());
    const username = `member-${testInfo.project.name}`;
    await member.getByLabel('Your name', { exact: true }).fill('New Teammate');
    await member.getByLabel('Username', { exact: true }).fill(username);
    await member.getByLabel(/^Password/).fill('Another private test password');
    await member
      .getByRole('button', { name: 'Join Team', exact: true })
      .click();
    await expect(workspace(member)).toBeVisible();
    const room = await createRoom(member, `team-${testInfo.project.name}`);
    await post(
      member,
      room.name,
      'The team can create rooms and discuss work.',
    );
    const personalAgent = await mutate(member, '/agents', {
      name: 'My Writer',
      handle: `my-writer-${testInfo.project.name}`,
      instructions: 'Write when asked.',
      access: 'write',
    });
    expect(personalAgent.ok()).toBeTruthy();
    expect((await personalAgent.json()).agent.ownerUserId).toBe(
      (await (await member.request.get('/api/auth/status')).json()).user.id,
    );
    await openApp(member, 'Agents');
    const agents = member.locator('[data-window-id="agents:main"]');
    if (isMobile)
      await agents.getByLabel('Agent', { exact: true }).selectOption(agent.id);
    else
      await agents
        .getByRole('navigation', { name: 'Agents', exact: true })
        .getByRole('button')
        .filter({ hasText: agent.name })
        .click();
    const membership = agents.locator('li').filter({ hasText: room.name });
    await expect(
      membership.getByRole('button', { name: 'Join Room', exact: true }),
    ).toHaveCount(0);
    const shared = await mutate(
      page,
      `/rooms/${room.id}/members/${agent.id}`,
      {},
      'PUT',
    );
    expect(shared.ok()).toBeTruthy();
    await expect(membership).toContainText('Member');
    const members = await (
      await member.request.get(`/api/rooms/${room.id}/members`)
    ).json();
    expect(members.members.map((item: { id: string }) => item.id)).toContain(
      agent.id,
    );
    await expect(
      membership.getByRole('button', { name: 'Leave Room', exact: true }),
    ).toHaveCount(0);
    await agents
      .getByRole('button', { name: 'Close Agents', exact: true })
      .click();
    await openApp(member, 'Workspace');
    await workspace(member)
      .getByRole('button', { name: /^Close .*Workspace$/ })
      .click();
    await expect(workspace(member)).toHaveCount(0);
    const notice = await mutate(page, `/rooms/${room.id}/messages`, {
      requestId: randomUUID(),
      content: `@${username} Please review our onboarding guide.`,
    });
    expect(notice.ok()).toBeTruthy();
    await member.getByRole('button', { name: /Inbox, 1 unread/ }).click();
    await member.getByRole('button', { name: /You were mentioned/ }).click();
    await expect(workspace(member)).toBeVisible();
    await expect(
      workspace(member).getByText(
        `@${username} Please review our onboarding guide.`,
        { exact: true },
      ),
    ).toBeVisible();
    if (isMobile)
      await settings(page)
        .getByLabel('Control panel', { exact: true })
        .selectOption('team');
    else
      await settings(page)
        .getByRole('button', { name: 'Team', exact: true })
        .click();
    const account = settings(page)
      .locator('li')
      .filter({ hasText: `@${username}` });
    await expect(account).toHaveCount(1);
    await account.getByRole('button', { name: 'Disable', exact: true }).click();
    const confirmation = page.getByRole('dialog', { name: 'Disable Account' });
    await expect(confirmation).toContainText(
      'their queued and active work will be canceled',
    );
    await confirmation
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    expect((await member.request.get('/api/library')).status()).toBe(200);
    await account.getByRole('button', { name: 'Disable', exact: true }).click();
    await confirmation
      .getByRole('button', { name: 'Disable Account', exact: true })
      .click();
    await expect(
      member.getByRole('button', { name: 'Sign In', exact: true }),
    ).toBeVisible();
    expect((await member.request.get('/api/library')).status()).toBe(401);
  } finally {
    await context.close();
  }
});

test('file upload preserves originals and desktop controls fit the viewport', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(page, `sources-${testInfo.project.name}`);
  const filename = `source-${testInfo.project.name}.txt`;
  await workspace(page)
    .getByLabel('Choose files to attach', { exact: true })
    .setInputFiles({
      name: filename,
      mimeType: 'text/plain',
      buffer: Buffer.from('Original customer notes.\n'),
    });
  const composer = workspace(page).getByRole('form', { name: 'Room composer' });
  await expect(composer.getByText(filename, { exact: true })).toBeVisible();
  await post(page, room.name, 'These are the original customer notes.');
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  const original = library(page)
    .getByRole('complementary', { name: 'Library resources' })
    .getByRole('button')
    .filter({ hasText: filename });
  if (isMobile)
    await library(page)
      .getByRole('button', { name: 'Sources', exact: true })
      .tap();
  await original.click();
  await expect(
    library(page).getByRole('button', {
      name: 'Create Working Copy…',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    library(page).getByRole('button', { name: 'New Document', exact: true }),
  ).toBeDisabled();
  await expect(
    library(page).getByText('Original customer notes.', { exact: false }),
  ).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('library-original.png') });
  await library(page)
    .getByRole('button', { name: 'Close Library', exact: true })
    .click();
  const win = workspace(page);
  const viewport = page.viewportSize()!;
  const rect = (await win.boundingBox())!;
  expect(rect.x).toBeGreaterThanOrEqual(0);
  expect(rect.x + rect.width).toBeLessThanOrEqual(viewport.width);
  expect(rect.y + rect.height).toBeLessThanOrEqual(viewport.height);
  if (isMobile) {
    await win
      .getByRole('combobox', { name: 'Room', exact: true })
      .selectOption(room.id);
  } else {
    const title = (await win.locator('.p9-window-titlebar').boundingBox())!;
    await page.mouse.move(
      title.x + title.width / 2,
      title.y + title.height / 2,
    );
    await page.mouse.down();
    await page.mouse.move(
      title.x + title.width / 2 + 20,
      title.y + title.height / 2 + 15,
      { steps: 5 },
    );
    await page.mouse.up();
    const moved = (await win.boundingBox())!;
    expect(moved.x).toBe(rect.x + 20);
    expect(moved.y).toBe(rect.y + 15);
  }
  const collapse = win.getByRole('button', { name: /^Collapse .*Workspace$/ });
  if (isMobile) await collapse.tap();
  else await collapse.click();
  await expect(win).toHaveClass(/is-shaded/);
  const expand = win.getByRole('button', { name: /^Expand .*Workspace$/ });
  if (isMobile) await expand.tap();
  else await expand.click();
  await expect(
    page.getByLabel(`Message ${room.name}`, { exact: true }),
  ).toBeVisible();
});

test('publication keeps newer typing private and immediate close flushes the editor', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `autosave-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await newDocument(page, 'memo.md', 'The submitted document.');
  let release = () => {};
  const hold = new Promise<void>((resolve) => {
    release = resolve;
  });
  let committed = false;
  await page.route(
    `**/api/resources/${room.resourceId}/files`,
    async (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      const response = await route.fetch();
      expect(response.ok()).toBeTruthy();
      committed = true;
      await hold;
      await route.fulfill({ response });
    },
  );
  try {
    await library(page)
      .getByRole('button', { name: 'Publish Changes', exact: true })
      .click();
    await expect.poll(() => committed).toBe(true);
    await library(page)
      .getByLabel('Document content', { exact: true })
      .fill('Newer text remains my private draft.');
    release();
    await expect(
      library(page).getByText(
        'Earlier text published · newer private changes remain',
        { exact: true },
      ),
    ).toBeVisible();
    await page.unrouteAll({ behavior: 'wait' });
    await library(page)
      .getByLabel('Document content', { exact: true })
      .fill('Last keystrokes before closing.');
    await library(page)
      .getByRole('button', { name: 'Close Library', exact: true })
      .click();
    await expect(library(page)).toHaveCount(0);
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(
                `/api/me/drafts/document?key=${room.resourceId}:memo.md`,
              )
            ).json()
          ).draft.value?.content,
      )
      .toBe('Last keystrokes before closing.');
    await workspace(page)
      .getByRole('button', { name: 'Library', exact: true })
      .click();
    await library(page)
      .getByRole('button', { name: 'Edit Text', exact: true })
      .click();
    await expect(
      library(page).getByLabel('Document content', { exact: true }),
    ).toHaveValue('Last keystrokes before closing.');
    const files = await (
      await page.request.get(`/api/resources/${room.resourceId}/files`)
    ).json();
    const shared = await page.request.get(
      `/api/resources/${room.resourceId}/file?path=memo.md&commit=${files.commit}`,
    );
    expect(await shared.text()).toBe('The submitted document.');
  } finally {
    release();
  }
});

test('history inspects exact versions and downloads differences without restoring', async ({
  page,
  isMobile,
}, testInfo) => {
  const room = await createRoom(page, `history-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await newDocument(page, 'proposal.md', 'First published draft.');
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  await library(page)
    .getByLabel('Document content', { exact: true })
    .fill('Revised published draft.');
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  const latest = await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json();
  await library(page)
    .getByRole('button', { name: 'Back to Preview', exact: true })
    .click();
  if (isMobile)
    await library(page)
      .getByRole('button', { name: 'Sources', exact: true })
      .tap();
  await library(page)
    .getByRole('complementary', { name: 'Library resources' })
    .getByRole('button')
    .filter({ hasText: `${room.name} files` })
    .click();
  await expect(
    library(page).getByText('Revised published draft.', { exact: true }),
  ).toBeVisible();
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  const versions = library(page).locator('.camp-library-history article');
  await expect(versions).toHaveCount(2);
  await expect(
    versions.first().getByText('Current version', { exact: true }),
  ).toBeVisible();
  await expect(
    versions.first().getByRole('button', { name: /^Restore Version/ }),
  ).toHaveCount(0);
  await expect(
    versions.nth(1).getByRole('button', { name: /^Restore Version/ }),
  ).toBeEnabled();
  await expect(
    library(page).getByRole('link', { name: 'Download Patch', exact: true }),
  ).toHaveCount(0);
  await versions
    .nth(1)
    .getByRole('button', { name: 'View Version', exact: true })
    .click();
  await expect(
    library(page).getByText('Historical version · read only', { exact: true }),
  ).toBeVisible();
  await expect(
    library(page).getByText('First published draft.', { exact: true }),
  ).toBeVisible();
  await expect(
    library(page).getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeDisabled();
  await expect(
    library(page).getByRole('button', { name: 'New Document', exact: true }),
  ).toBeDisabled();
  const originalDownload = page.waitForEvent('download');
  await library(page)
    .getByRole('link', { name: 'Download proposal.md', exact: true })
    .click();
  const original = await originalDownload;
  expect(await readFile((await original.path())!, 'utf8')).toBe(
    'First published draft.',
  );
  expect(
    (
      await (
        await page.request.get(`/api/resources/${room.resourceId}/files`)
      ).json()
    ).commit,
  ).toBe(latest.commit);
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  await versions
    .first()
    .getByRole('button', { name: 'Compare with Previous', exact: true })
    .click();
  const changes = library(page).getByLabel('Version differences', {
    exact: true,
  });
  await expect(changes).toContainText('-First published draft.');
  await expect(changes).toContainText('+Revised published draft.');
  const patchDownload = page.waitForEvent('download');
  await library(page)
    .getByRole('link', { name: 'Download Patch', exact: true })
    .click();
  const patch = await patchDownload;
  const text = await readFile((await patch.path())!, 'utf8');
  const response = await page.request.get(
    (await library(page)
      .getByRole('link', { name: 'Download Patch', exact: true })
      .getAttribute('href'))!,
  );
  expect(response.ok()).toBeTruthy();
  expect(text).toBe(await response.text());
  expect(text).toContain('-First published draft.');
  expect(text).toContain('+Revised published draft.');
  await library(page)
    .getByRole('button', { name: 'Back to History', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'History', exact: true })
    .click();
  await library(page)
    .getByRole('button', { name: 'Return to Latest', exact: true })
    .click();
  await expect(
    library(page).getByText('Revised published draft.', { exact: true }),
  ).toBeVisible();
  await expect(
    library(page).getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeEnabled();
  const history = await (
    await page.request.get(`/api/resources/${room.resourceId}/history`)
  ).json();
  expect(history.publications).toHaveLength(2);
  expect(history.publications[0].commit).toBe(latest.commit);
});

test('Platinum control panels stay usable in a narrow window with keyboard navigation', async ({
  page,
  isMobile,
}, testInfo) => {
  await openApp(page, 'Settings');
  const win = settings(page);
  const chooser = win.getByLabel('Control panel', { exact: true });
  if (!isMobile) {
    await expect(chooser).toBeHidden();
    const edge = (await win.locator('[data-resize="e"]').boundingBox())!;
    const before = (await win.boundingBox())!;
    await page.mouse.move(edge.x + edge.width / 2, edge.y + edge.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      edge.x + edge.width / 2 - (before.width - 520),
      edge.y + edge.height / 2,
      { steps: 5 },
    );
    await page.mouse.up();
  }
  await expect(chooser).toBeVisible();
  await chooser.focus();
  await chooser.press('End');
  await chooser.press('Enter');
  await expect(chooser).toHaveValue('account');
  await chooser.press('Home');
  await chooser.press('Enter');
  await expect(chooser).toHaveValue('readiness');
  await chooser.press('ArrowDown');
  await chooser.press('ArrowDown');
  await chooser.press('Enter');
  await expect(chooser).toHaveValue('models');
  await expect(
    win.getByRole('heading', { name: 'Model Connection' }),
  ).toBeVisible();
  await expect(win.getByLabel('API key', { exact: true })).toHaveValue('');
  for (const section of [
    'environments',
    'storage',
    'team',
    'appearance',
    'account',
  ]) {
    await chooser.selectOption(section);
    const overflow = await win
      .locator('.camp-settings')
      .evaluate((element) => element.scrollWidth > element.clientWidth);
    expect(overflow, `${section} should fit the window width`).toBe(false);
  }
  await page.screenshot({
    path: testInfo.outputPath('platinum-control-panel.png'),
  });
});
