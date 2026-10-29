import { expect, test } from '@playwright/test';
import {
  createRoom,
  library,
  newDocument,
  owner,
  signIn,
  workspace,
} from './team-helpers';

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
    .getByRole('button', { name: 'Library', exact: true })
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
      value: { content: string; baseCommit: string };
    };
  const listing = (await (
    await page.request.get(`/api/resources/${room.resourceId}/files`)
  ).json()) as { commit: string };
  await expect
    .poll(async () => (await readDraft()).value?.baseCommit)
    .toBe(listing.commit);
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
      .getByRole('button', { name: 'Library', exact: true })
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
      .poll(async () => (await readDraft()).value.content)
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
    expect((await readDraft()).value.content).toBe(firstText);

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
    expect((await readDraft()).value.content).toBe(firstText);
    await comparison
      .getByRole('button', { name: 'Save My Draft', exact: true })
      .click();
    await expect
      .poll(async () => (await readDraft()).value.content)
      .toBe(secondText);
    const resolved = await readDraft();
    expect(resolved.revision).toBeGreaterThan(firstSaved.revision);
    expect(resolved.value.baseCommit).toBe(listing.commit);
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
