import { expect, test } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  owner,
  signIn,
  workspace,
} from './team-helpers';

test('a publication receipt preserves a newer private draft saved elsewhere', async ({
  page,
}, info) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `publish-draft-race-${info.project.name}`,
  );
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await newDocument(page, 'handoff.md', 'Text submitted for publication.');
  const key = `${room.resourceId}:handoff.md`;
  const readDraft = async () =>
    (
      await (
        await page.request.get(
          `/api/me/drafts/document?key=${encodeURIComponent(key)}`,
        )
      ).json()
    ).draft;
  await expect
    .poll(async () => (await readDraft()).value?.content)
    .toBe('Text submitted for publication.');
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let acceptedCommit = '';
  await page.route(
    `**/api/resources/${room.resourceId}/files`,
    async (route) => {
      if (route.request().method() !== 'PUT') return route.fallback();
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      acceptedCommit = (await response.json()).publication.commit;
      await held;
      return route.fulfill({ response });
    },
  );
  try {
    const win = library(page);
    await win
      .getByRole('button', { name: 'Publish Changes', exact: true })
      .click();
    await expect.poll(() => acceptedCommit).not.toBe('');
    const original = await readDraft();
    const other = {
      ...original.value,
      baseCommit: acceptedCommit,
      content: 'New work from another session.',
    };
    const update = await mutate(
      page,
      '/me/drafts/document',
      { key, value: other, revision: original.revision },
      'PUT',
    );
    expect(update.ok()).toBe(true);
    release();
    await expect(win.getByRole('alert')).toContainText('saved draft changed');
    expect((await readDraft()).value).toEqual(other);
    await win
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    await expect(win.getByText(other.content, { exact: true })).toBeVisible();
    await expect(
      win.getByRole('button', { name: 'Save My Draft', exact: true }),
    ).toHaveCount(0);
    await win
      .getByRole('button', { name: 'Use Other Draft', exact: true })
      .click();
    await expect(
      win.getByLabel('Document content', { exact: true }),
    ).toHaveValue(other.content);
    const shared = await page.request.get(
      `/api/resources/${room.resourceId}/file?path=handoff.md&commit=${acceptedCommit}`,
    );
    expect(await shared.text()).toBe('Text submitted for publication.');
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('two sessions compare a private draft conflict before explicitly replacing it', async ({
  page,
  browser,
  baseURL,
  isMobile,
}, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  const room = await createRoom(page, `draft-cas-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const baseline = 'Shared baseline. Private edits must not publish this file.';
  await newDocument(page, 'memo.md', baseline);
  await library(page)
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(
    library(page).getByText('Published to shared files.', { exact: true }),
  ).toBeVisible();
  const draftURL = `/api/me/drafts/document?key=${encodeURIComponent(`${room.resourceId}:memo.md`)}`;
  const readDraft = async () =>
    (await (await page.request.get(draftURL)).json()).draft as {
      revision: number;
      value: { content: string; baseCommit: string } | null;
    };
  const listing = (await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json()) as { commit: string };
  await expect.poll(async () => (await readDraft()).value).toBeNull();
  const initial = await readDraft();

  // Sign in again so both browser contexts have independent authenticated sessions
  // and browser storage, while sharing the account's private draft identity.
  const secondContext = await browser.newContext({
    baseURL,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  const second = await secondContext.newPage();
  second.on('pageerror', (error) => errors.push(error.message));
  try {
    await second.goto('/');
    await second.getByLabel('Username', { exact: true }).fill(owner.username);
    await second.getByLabel(/^Password/).fill(owner.password);
    await second.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(workspace(second)).toBeVisible();
    if (isMobile)
      await workspace(second)
        .getByRole('combobox', { name: 'Room', exact: true })
        .selectOption(room.id);
    else
      await workspace(second)
        .getByRole('navigation', { name: 'Rooms', exact: true })
        .getByRole('button', { name: room.name, exact: false })
        .click();
    await expect(
      second.getByLabel(`Message ${room.name}`, { exact: true }),
    ).toBeVisible();
    await workspace(second)
      .getByRole('button', { name: 'Files', exact: true })
      .click();
    await library(second)
      .getByRole('button', { name: 'Edit Text', exact: true })
      .click();
    await expect(
      library(second).getByLabel('Document content', { exact: true }),
    ).toHaveValue(baseline);
    expect((await readDraft()).revision).toBe(initial.revision);

    const firstText = 'The first session saved this private version.';
    const secondText =
      'The second session keeps its divergent private version.';
    await library(page)
      .getByLabel('Document content', { exact: true })
      .fill(firstText);
    await expect
      .poll(async () => (await readDraft()).value?.content)
      .toBe(firstText);
    const firstSaved = await readDraft();
    expect(firstSaved.revision).toBeGreaterThan(initial.revision);
    await library(second)
      .getByLabel('Document content', { exact: true })
      .fill(secondText);
    await expect(library(second).getByRole('alert')).toBeVisible();
    await expect(
      library(second).getByLabel('Document content', { exact: true }),
    ).toHaveValue(secondText);
    expect((await readDraft()).value?.content).toBe(firstText);

    await library(second)
      .getByRole('button', { name: 'Compare Private Drafts', exact: true })
      .click();
    const comparison = library(second).locator('.camp-draft-comparison');
    await expect(
      comparison.getByText(firstText, { exact: true }),
    ).toBeVisible();
    await expect(
      library(second).getByLabel('Document content', { exact: true }),
    ).toHaveValue(secondText);
    expect((await readDraft()).value?.content).toBe(firstText);
    await comparison
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect
      .poll(async () => (await readDraft()).value?.content)
      .toBe(secondText);
    const resolved = await readDraft();
    expect(resolved.revision).toBeGreaterThan(firstSaved.revision);
    expect(resolved.value?.baseCommit).toBe(listing.commit);
    await expect(library(second).getByRole('alert')).toHaveCount(0);
    await expect(
      library(second).getByLabel('Document content', { exact: true }),
    ).toHaveValue(secondText);
    await expect(
      library(page).getByLabel('Document content', { exact: true }),
    ).toHaveValue(firstText);

    const after = (await (
      await page.request.get(`/api/resources/${room.resourceId}/files`)
    ).json()) as { commit: string };
    expect(after.commit).toBe(listing.commit);
    const shared = await page.request.get(
      `/api/resources/${room.resourceId}/file?path=memo.md&commit=${after.commit}`,
    );
    expect(await shared.text()).toBe(baseline);
    const history = (await (
      await page.request.get(`/api/resources/${room.resourceId}/history`)
    ).json()) as { publications: unknown[] };
    expect(history.publications).toHaveLength(1);
    const runs = (await (
      await page.request.get(`/api/rooms/${room.id}/runs`)
    ).json()) as { runs: unknown[] };
    expect(runs.runs).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    await secondContext.close();
  }
});
