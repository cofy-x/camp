import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type {
  DocumentSnapshot,
  ExtractedDocument,
  Reference,
  SendInput,
} from '../apps/dashboard/src/features/workspace/api';
import { createRoom, mutate, signIn, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

function original(path: string): Reference {
  return {
    kind: 'file',
    origin: 'upload',
    resourceId: `original-${randomUUID()}`,
    commit: 'a'.repeat(40),
    path,
    title: path,
    size: 25,
  };
}

function saved(
  reference: Reference,
  status: DocumentSnapshot['status'] = 'ready',
): ExtractedDocument {
  return {
    id: `saved-${reference.resourceId}`,
    resourceId: reference.resourceId!,
    commit: reference.commit!,
    path: reference.path!,
    format: reference.path!.endsWith('.pdf') ? 'pdf' : 'docx',
    status,
    extractorVersion: 'historical-synthetic-reader',
    sections:
      status === 'ready'
        ? [
            {
              id: 'p1',
              label: 'Page 1',
              text: `Saved text for ${reference.path}.`,
            },
          ]
        : [],
    warnings: [],
    createdAt: '2026-10-30T00:00:00Z',
    updatedAt: '2026-10-30T00:00:00Z',
  };
}

async function attach(page: Page, reference: Reference) {
  await workspace(page)
    .getByLabel('Choose files to attach', { exact: true })
    .setInputFiles({
      name: reference.path!,
      mimeType: 'application/octet-stream',
      buffer: Buffer.from('Synthetic original bytes.'),
    });
  const composer = workspace(page).getByRole('form', { name: 'Room composer' });
  await expect(
    composer.getByText(reference.path!, { exact: true }),
  ).toBeVisible();
  return composer;
}

for (const format of ['pdf', 'docx'] as const) {
  test(`new ${format.toUpperCase()} attachments send the immutable original without preparing or waiting for text`, async ({
    page,
  }, testInfo) => {
    const room = await createRoom(
      page,
      `original-${format}-${testInfo.project.name}`,
    );
    const agentResponse = await mutate(page, '/agents', {
      name: 'Attachment reviewer',
      handle: `review-${randomUUID().slice(0, 8)}`,
      instructions: 'Review provided readable sources.',
      access: 'read',
      enabled: true,
    });
    expect(agentResponse.ok()).toBeTruthy();
    const { agent } = await agentResponse.json();
    expect(
      (
        await mutate(page, `/rooms/${room.id}/members/${agent.id}`, {}, 'PUT')
      ).ok(),
    ).toBeTruthy();
    await page.route('**/api/workspace/status', async (route) => {
      const response = await route.fetch();
      await route.fulfill({
        response,
        json: { ...(await response.json()), available: true, reason: '' },
      });
    });
    // Originals are uploaded and downloaded through the actual keyless Server.
    // Agent admission and saved-text reads are synthetic; no model or Runner runs.
    let reference = original(`customer-brief.${format}`);
    const documentReads: string[] = [];
    const preparationWrites: string[] = [];
    await page.route(`**/api/resources/*/document**`, (route) => {
      if (route.request().method() === 'POST') {
        preparationWrites.push(route.request().method());
        return route.fulfill({
          json: { document: saved(reference, 'queued') },
        });
      }
      documentReads.push(route.request().url());
      return route.fulfill({ json: { document: null } });
    });
    let attempted: SendInput | undefined;
    await page.route(`**/api/rooms/${room.id}/messages`, (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      attempted = route.request().postDataJSON() as SendInput;
      return route.fulfill({
        status: 503,
        json: { error: 'Synthetic admission failure; draft preserved.' },
      });
    });
    await errors.get(page)!.reload();
    const uploaded = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/rooms/${room.id}/uploads`) &&
        response.request().method() === 'POST',
    );
    const composer = await attach(page, reference);
    const response = await uploaded;
    expect(response.ok()).toBeTruthy();
    reference = (await response.json()).reference;
    await composer
      .getByRole('textbox', { name: `Message ${room.name}` })
      .fill(`@${agent.handle} Review the attached source.`);
    const card = composer.locator('.workspace-reference-card');
    await card.locator('summary').first().click();
    await expect(
      composer.getByRole('link', {
        name: `Download ${reference.path}`,
        exact: true,
      }),
    ).toHaveAttribute('href', new RegExp(`commit=${reference.commit}`));
    await page.screenshot({
      path: testInfo.outputPath(`${format}-original-only.png`),
    });
    await expect(
      card.getByText('Original only', { exact: true }),
    ).toBeVisible();
    await expect(card.getByText('Original only', { exact: true })).toHaveCount(
      1,
    );
    await expect(
      card.getByText('Uploaded attachment', { exact: false }),
    ).toHaveCount(0);
    await expect(
      card.getByText('pinned version', { exact: false }),
    ).toHaveCount(0);
    await expect(
      card.getByText(
        'Attach a Markdown or plain-text copy for Agents to read.',
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      composer.getByRole('button', { name: 'Send to Agent', exact: true }),
    ).toBeEnabled();
    const download = page.waitForEvent('download');
    await composer
      .getByRole('link', { name: `Download ${reference.path}`, exact: true })
      .click();
    const fs = await import('node:fs/promises');
    expect(await fs.readFile((await (await download).path())!, 'utf8')).toBe(
      'Synthetic original bytes.',
    );
    await composer
      .getByRole('button', { name: 'Send to Agent', exact: true })
      .click();
    await expect(composer.getByRole('alert')).toContainText('draft preserved');
    expect(attempted?.references).toEqual([
      {
        kind: 'file',
        resourceId: reference.resourceId,
        commit: reference.commit,
        path: reference.path,
      },
    ]);
    expect(preparationWrites).toEqual([]);
    expect(documentReads).toHaveLength(1);
    const queried = new URL(documentReads[0]).searchParams;
    expect(queried.get('commit')).toBe(reference.commit);
    expect(queried.get('path')).toBe(reference.path);
  });
}

test('a late saved-text read cannot restore a removed attachment or replace its successor', async ({
  page,
}) => {
  const room = await createRoom(page, `late-text-${randomUUID().slice(0, 6)}`);
  const first = original('removed.pdf');
  const second = original('retained.pdf');
  let selected = first;
  await page.route(`**/api/rooms/${room.id}/uploads`, (route) =>
    route.fulfill({ json: { reference: selected } }),
  );
  let arrived!: () => void;
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    arrived = resolve;
  });
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/api/resources/${first.resourceId}/document?*`,
    async (route) => {
      arrived();
      await released;
      await route.fulfill({ json: { document: saved(first) } }).catch(() => {});
    },
  );
  await page.route(
    `**/api/resources/${second.resourceId}/document?*`,
    (route) => route.fulfill({ json: { document: saved(second) } }),
  );
  await page.route(`**/api/documents/saved-${second.resourceId}`, (route) =>
    route.fulfill({ json: { document: saved(second) } }),
  );
  const composer = await attach(page, first);
  try {
    await held;
    await composer
      .getByRole('button', {
        name: `Remove reference ${first.title}`,
        exact: true,
      })
      .click();
    selected = second;
    await attach(page, second);
    await composer.locator('.workspace-reference-card summary').first().click();
    await expect(
      composer.getByRole('region', { name: 'Page 1', exact: true }),
    ).toContainText('Saved text for retained.pdf.');
  } finally {
    release();
  }
  await expect(composer.getByText('removed.pdf', { exact: true })).toHaveCount(
    0,
  );
  await expect(
    composer.getByRole('region', { name: 'Page 1', exact: true }),
  ).toContainText('Saved text for retained.pdf.');
  await errors.get(page)!.reload();
  await expect(
    composer.getByText('retained.pdf', { exact: true }),
  ).toBeVisible();
  await expect(composer.getByText('removed.pdf', { exact: true })).toHaveCount(
    0,
  );
});

test('saved-text failures keep the original usable and Retry preview performs only a read', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `read-retry-${testInfo.project.name}`);
  const reference = original('historical.pdf');
  let reads = 0;
  const methods: string[] = [];
  await page.route(`**/api/rooms/${room.id}/uploads`, (route) =>
    route.fulfill({ json: { reference } }),
  );
  await page.route(
    `**/api/resources/${reference.resourceId}/document?*`,
    (route) => {
      methods.push(route.request().method());
      reads++;
      return reads === 1
        ? route.fulfill({
            status: 503,
            json: { error: 'Saved text is temporarily unavailable.' },
          })
        : route.fulfill({ json: { document: saved(reference) } });
    },
  );
  await page.route(
    `**/api/documents/saved-${reference.resourceId}`,
    (route) => {
      methods.push(route.request().method());
      return route.fulfill({ json: { document: saved(reference) } });
    },
  );
  const composer = await attach(page, reference);
  await composer
    .getByRole('textbox', { name: `Message ${room.name}` })
    .fill('Keep the original source.');
  await composer.locator('.workspace-reference-card summary').first().click();
  await expect(composer.getByRole('alert')).toContainText(
    'Saved text is temporarily unavailable.',
  );
  await expect(
    composer.getByRole('button', { name: 'Post', exact: true }),
  ).toBeEnabled();
  await expect(
    composer.getByRole('link', {
      name: 'Download historical.pdf',
      exact: true,
    }),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath('saved-text-read-error.png'),
  });
  await composer
    .getByRole('button', { name: 'Retry preview', exact: true })
    .click();
  await expect(
    composer.getByRole('region', { name: 'Page 1', exact: true }),
  ).toContainText('Saved text for historical.pdf.');
  expect(reads).toBe(2);
  expect(methods.every((method) => method === 'GET')).toBe(true);
  await expect(
    composer.getByRole('button', { name: 'Prepare text again', exact: true }),
  ).toHaveCount(0);
});
