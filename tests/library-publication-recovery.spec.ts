import { randomUUID } from 'node:crypto';
import { expect, test, type Locator } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  newDocument,
  openApp,
  openLibraryAction,
  signIn,
  workspace,
} from './team-helpers';

async function showLibrarySources(win: Locator) {
  await expect(
    win.getByRole('button', { name: 'More Library actions', exact: true }),
  ).toBeVisible();
  const sources = win.getByRole('button', { name: 'Sources', exact: true });
  if (await sources.isVisible()) await sources.click();
  return win.getByRole('complementary', { name: 'Library resources' });
}

test('a lost working copy response is recovered without another write after reload', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(page, `copy-recovery-${testInfo.project.name}`);
  const source = `copy-source-${testInfo.project.name}.md`;
  await workspace(page)
    .getByLabel('Choose files to attach', { exact: true })
    .setInputFiles({
      name: source,
      mimeType: 'text/markdown',
      buffer: Buffer.from('Original source text'),
    });
  await expect(
    workspace(page).getByText(source, { exact: true }),
  ).toBeVisible();
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const win = library(page);
  await (
    await showLibrarySources(win)
  )
    .getByRole('button', { name: source, exact: true })
    .click();

  let writes = 0;
  await page.route('**/api/resources/*/copy-file', async (route) => {
    writes++;
    const response = await route.fetch();
    expect(response.ok()).toBeTruthy();
    await route.abort('failed');
  });
  await openLibraryAction(page, 'Create Working Copy', room.name);
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('File path', { exact: true }).fill('copy.md');
  await dialog
    .getByRole('button', { name: 'Create Copy', exact: true })
    .click();
  await expect(
    win.getByText('Working copy save unconfirmed.', { exact: false }),
  ).toBeVisible();
  await page.reload();
  await openApp(page, 'Library');
  await win.getByRole('button', { name: 'Check Saved Publication' }).click();
  await expect(
    win.getByText('Working copy saved.', { exact: true }),
  ).toBeVisible();
  await win.getByRole('button', { name: 'View Saved Version' }).click();
  await expect(
    win.getByText('Original source text', { exact: true }),
  ).toBeVisible();
  expect(writes).toBe(1);
});

test('retrying an unconfirmed restore uses the original request', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `restore-recovery-${testInfo.project.name}`,
  );
  const resourcePath = `/resources/${room.resourceId}`;
  const { commit } = await (
    await page.request.get(`/api${resourcePath}/files`)
  ).json();
  const first = await mutate(
    page,
    `${resourcePath}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: commit,
      path: 'first.md',
      content: 'First saved version',
    },
    'PUT',
  );
  expect(first.ok()).toBeTruthy();
  const { publication } = await first.json();
  const second = await mutate(
    page,
    `${resourcePath}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: publication.commit,
      path: 'second.md',
      content: 'Later shared version',
    },
    'PUT',
  );
  expect(second.ok()).toBeTruthy();

  const bodies: unknown[] = [];
  await page.route('**/api/resources/*/restore', async (route) => {
    bodies.push(route.request().postDataJSON());
    if (bodies.length === 1) await route.abort('failed');
    else await route.continue();
  });
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const win = library(page);
  await win.getByRole('button', { name: 'History', exact: true }).click();
  await win
    .getByRole('article')
    .filter({ hasText: 'first.md' })
    .getByRole('button', { name: 'More version actions' })
    .click();
  await page
    .getByRole('menuitem', { name: 'Restore Version…', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Restore Version', exact: true })
    .click();
  await win.getByRole('button', { name: 'Check Saved Publication' }).click();
  await expect(
    win.getByText(/No saved publication receipt found/),
  ).toBeVisible();
  expect(bodies).toHaveLength(1);
  await win.getByRole('button', { name: 'Retry Original Request' }).click();
  await expect(
    win.getByText('Shared files restored.', { exact: true }),
  ).toBeVisible();
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toEqual(bodies[0]);
  const { files } = await (
    await page.request.get(`/api${resourcePath}/files`)
  ).json();
  expect(files).toEqual(['first.md']);
});

test('oversized document text stays local and can be shortened and saved', async ({
  page,
}, testInfo) => {
  await signIn(page);
  await createRoom(page, `large-draft-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const win = library(page);
  await win.getByRole('button', { name: 'New Document', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('File path', { exact: true }).fill('draft.md');
  await dialog
    .getByRole('button', { name: 'Create Draft', exact: true })
    .click();
  const content = win.getByRole('textbox', { name: 'Document content' });
  await expect(content).toBeVisible();
  await page.clock.install();
  const drafts: unknown[] = [];
  page.on('request', (request) => {
    if (
      request.method() === 'PUT' &&
      new URL(request.url()).pathname === '/api/me/drafts/document'
    )
      drafts.push(request.postDataJSON());
  });
  await content.fill('文'.repeat(350_000));
  await expect(win.getByRole('alert')).toContainText(
    'Document text exceeds 1 MiB',
  );
  const publish = win.getByRole('button', {
    name: 'Publish Changes',
    exact: true,
  });
  await expect(publish).toBeDisabled();
  await page.clock.runFor(700);
  expect(drafts).toHaveLength(0);
  await content.fill('A shorter draft.');
  await page.clock.runFor(700);
  await expect.poll(() => drafts.length).toBe(1);
  await expect(publish).toBeEnabled();
});

test('an uncertain document publish retries its frozen text after reload and preserves newer edits', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(
    page,
    `publish-recovery-${testInfo.project.name}`,
  );
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  const path = 'publication-draft.md';
  const submitted = 'Original submitted text';
  const subsequent = 'Newer text stays private';
  await newDocument(page, path, submitted);
  const win = library(page);
  const writes: { requestId: string; content: string }[] = [];
  await page.route('**/api/resources/*/files', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    writes.push(route.request().postDataJSON());
    if (writes.length === 1) await route.abort('failed');
    else await route.continue();
  });
  await win
    .getByRole('button', { name: 'Publish Changes', exact: true })
    .click();
  await expect(win.getByText(/Publication unconfirmed/)).toBeVisible();
  const newerDraft = page.waitForResponse((response) => {
    const request = response.request();
    return (
      request.method() === 'PUT' &&
      new URL(response.url()).pathname === '/api/me/drafts/document' &&
      request.postDataJSON().key === `${room.resourceId}:${path}` &&
      response.ok()
    );
  });
  await win.getByRole('textbox', { name: 'Document content' }).fill(subsequent);
  await newerDraft;
  expect(
    await page.evaluate(() =>
      Object.entries(sessionStorage)
        .filter(([key]) => key.startsWith('camp:receipt:'))
        .every(([, value]) => !value.includes('Original submitted text')),
    ),
  ).toBeTruthy();

  await page.reload();
  await openApp(page, 'Library');
  await (
    await showLibrarySources(win)
  )
    .getByRole('button', { name: path, exact: true })
    .click();
  const text = win.getByRole('textbox', { name: 'Document content' });
  await expect(text).toHaveValue(subsequent);
  await win.getByRole('button', { name: 'Check Saved Publication' }).click();
  await win.getByRole('button', { name: 'Retry Original Request' }).click();
  await expect(
    win.getByText('Earlier text published · newer private changes remain'),
  ).toBeVisible();
  await expect(text).toHaveValue(subsequent);
  expect(writes).toHaveLength(2);
  expect(writes[1]).toEqual(writes[0]);
  expect(writes[0].content).toBe(submitted);
  const snapshotKey = `library-publication:${room.resourceId}:${writes[0].requestId}`;
  const { draft } = await (
    await page.request.get(
      `/api/me/drafts/document?key=${encodeURIComponent(snapshotKey)}`,
    )
  ).json();
  expect(draft.value).toBeNull();
});

test('an absent publication snapshot can be fenced without losing later text', async ({
  page,
}, testInfo) => {
  await signIn(page);
  const room = await createRoom(page, `unsubmitted-${testInfo.project.name}`);
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await newDocument(page, 'unsubmitted.md', 'Original draft');
  const win = library(page);
  let snapshotKey = '';
  await page.route('**/api/me/drafts/document', async (route) => {
    const request = route.request();
    if (request.method() === 'PUT') {
      const value = request.postDataJSON() as { key: string };
      if (
        !snapshotKey &&
        value.key.startsWith(`library-publication:${room.resourceId}:`)
      ) {
        snapshotKey = value.key;
        return route.abort('failed');
      }
    }
    return route.continue();
  });
  const publish = win.getByRole('button', {
    name: 'Publish Changes',
    exact: true,
  });
  const text = win.getByRole('textbox', { name: 'Document content' });
  await publish.click();
  await expect(win.getByText(/Publication unconfirmed/)).toBeVisible();
  await text.fill('Updated private draft');
  await win.getByRole('button', { name: 'Check Saved Publication' }).click();
  await win.getByRole('button', { name: 'Retry Original Request' }).click();
  await win
    .getByRole('button', { name: 'Cancel Unsubmitted Publication' })
    .click();
  await expect(
    win.getByText(
      'Unsubmitted publication cancelled. Your draft remains private.',
    ),
  ).toBeVisible();
  await expect(text).toHaveValue('Updated private draft');
  await expect(publish).toBeEnabled();
  const { draft } = await (
    await page.request.get(
      `/api/me/drafts/document?key=${encodeURIComponent(snapshotKey)}`,
    )
  ).json();
  expect(draft.revision).toBe(1);
  expect(draft.value).toBeNull();
  expect(
    await (
      await page.request.get(`/api/resources/${room.resourceId}/history`)
    ).json(),
  ).toEqual({ publications: [] });
});
