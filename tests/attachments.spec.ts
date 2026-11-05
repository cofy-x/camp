import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import type {
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

async function attach(
  page: Page,
  reference: Reference,
  bytes = Buffer.from('# Source\nExact original bytes.\n'),
) {
  await workspace(page)
    .getByLabel('Choose files to attach', { exact: true })
    .setInputFiles({
      name: reference.path!,
      mimeType: 'application/octet-stream',
      buffer: bytes,
    });
  const composer = workspace(page).getByRole('form', { name: 'Room composer' });
  await expect(
    composer.getByText(reference.path!, { exact: true }),
  ).toBeVisible();
  return composer;
}

for (const fixture of [
  {
    label: 'Markdown',
    path: 'customer-brief.md',
    bytes: Buffer.from('# Customer brief\nConfirm **exact source bytes**.\n'),
  },
  {
    label: 'binary',
    path: 'customer-brief.bin',
    bytes: Buffer.from([0, 255, 128, 1, 2, 3]),
  },
])
  test(`${fixture.label} attachments retain exact originals and versioned references across an uncertain send`, async ({
    page,
  }, testInfo) => {
    const room = await createRoom(
      page,
      `attachment-${fixture.label}-${testInfo.project.name}`,
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
      return route.fulfill({
        response,
        json: { ...(await response.json()), available: true, reason: '' },
      });
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
    const composer = await attach(page, original(fixture.path), fixture.bytes);
    const response = await uploaded;
    expect(response.ok()).toBeTruthy();
    const reference = (await response.json()).reference as Reference;
    await composer
      .getByRole('textbox', { name: `Message ${room.name}` })
      .fill(`@${agent.handle} Review the attached source.`);
    const resourceReads: string[] = [];
    page.on('request', (request) => {
      if (
        new URL(request.url()).pathname.startsWith(
          `/api/resources/${reference.resourceId}/`,
        )
      ) {
        expect(request.method()).toBe('GET');
        resourceReads.push(request.url());
      }
    });
    const card = composer.locator('.workspace-reference-card');
    await card.locator('summary').first().click();
    const downloadLink = card.getByRole('link', {
      name: `Download ${reference.path}`,
      exact: true,
    });
    await expect(downloadLink).toHaveText('Download');
    await expect(downloadLink).toHaveAttribute('download', fixture.path);
    await expect(downloadLink).toHaveAttribute(
      'href',
      new RegExp(`commit=${reference.commit}`),
    );
    if (fixture.label === 'Markdown') {
      await expect(
        card.getByRole('heading', { name: 'Customer brief', exact: true }),
      ).toBeVisible();
      await expect(card.locator('strong')).toHaveText('exact source bytes');
    } else {
      await expect(
        card.getByText('This file has no text preview.', { exact: false }),
      ).toBeVisible();
      await expect(
        card.getByRole('button', { name: 'Show source', exact: true }),
      ).toHaveCount(0);
    }
    expect(resourceReads).toHaveLength(1);
    const requested = new URL(resourceReads[0]);
    expect(requested.pathname).toBe(
      `/api/resources/${reference.resourceId}/file`,
    );
    expect(requested.searchParams.get('commit')).toBe(reference.commit);
    expect(requested.searchParams.get('path')).toBe(reference.path);
    await page.screenshot({
      path: testInfo.outputPath(
        `${fixture.label.toLowerCase()}-attachment.png`,
      ),
    });
    const download = page.waitForEvent('download');
    await downloadLink.click();
    expect(await readFile((await (await download).path())!)).toEqual(
      fixture.bytes,
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
    await errors.get(page)!.reload();
    await expect(
      composer.getByText(reference.path!, { exact: true }),
    ).toBeVisible();
    await expect(
      composer.getByRole('textbox', { name: `Message ${room.name}` }),
    ).toHaveValue(`@${agent.handle} Review the attached source.`);
  });

test('a late original-file read cannot restore a removed attachment or replace its successor', async ({
  page,
}) => {
  const room = await createRoom(
    page,
    `late-preview-${randomUUID().slice(0, 6)}`,
  );
  const first = original('removed.md');
  const second = original('retained.md');
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
    `**/api/resources/${first.resourceId}/file?*`,
    async (route) => {
      arrived();
      await released;
      await route
        .fulfill({
          contentType: 'text/plain',
          body: '# Removed\nStale original.',
        })
        .catch(() => {});
    },
  );
  await page.route(`**/api/resources/${second.resourceId}/file?*`, (route) =>
    route.fulfill({
      contentType: 'text/plain',
      body: '# Retained\nCurrent original.',
    }),
  );
  const composer = await attach(page, first);
  await composer.locator('.workspace-reference-card summary').first().click();
  try {
    await held;
    await expect(
      composer.getByRole('link', { name: 'Download removed.md', exact: true }),
    ).toHaveText('Download');
    await expect(
      composer.getByRole('button', { name: 'Show source', exact: true }),
    ).toHaveCount(0);
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
      composer.getByRole('heading', { name: 'Retained', exact: true }),
    ).toBeVisible();
  } finally {
    release();
  }
  await expect(composer.getByText('removed.md', { exact: true })).toHaveCount(
    0,
  );
  await expect(
    composer.getByText('Stale original.', { exact: true }),
  ).toHaveCount(0);
  await expect(
    composer.getByText('Current original.', { exact: true }),
  ).toBeVisible();
  await errors.get(page)!.reload();
  await expect(
    composer.getByText('retained.md', { exact: true }),
  ).toBeVisible();
  await expect(composer.getByText('removed.md', { exact: true })).toHaveCount(
    0,
  );
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});

test('a failed original-file read keeps the draft and exact download while retry reads the same version', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `read-retry-${testInfo.project.name}`);
  const reference = original('source.md');
  let reads = 0;
  const methods: string[] = [];
  await page.route(`**/api/rooms/${room.id}/uploads`, (route) =>
    route.fulfill({ json: { reference } }),
  );
  await page.route(
    `**/api/resources/${reference.resourceId}/file?*`,
    (route) => {
      methods.push(route.request().method());
      const url = new URL(route.request().url());
      expect(url.searchParams.get('commit')).toBe(reference.commit);
      expect(url.searchParams.get('path')).toBe(reference.path);
      return ++reads === 1
        ? route.fulfill({ status: 503, body: 'Synthetic read unavailable.' })
        : route.fulfill({
            contentType: 'text/plain',
            body: '# Source\nExact original bytes.',
          });
    },
  );
  const composer = await attach(page, reference);
  await composer
    .getByRole('textbox', { name: `Message ${room.name}` })
    .fill('Keep the original source.');
  await composer.locator('.workspace-reference-card summary').first().click();
  await expect(composer.getByRole('alert')).toContainText(
    'This file could not be loaded.',
  );
  await expect(
    composer.getByRole('button', { name: 'Post', exact: true }),
  ).toBeEnabled();
  await expect(
    composer.getByRole('link', { name: 'Download source.md', exact: true }),
  ).toHaveText('Download');
  await expect(
    composer.getByRole('link', { name: 'Download source.md', exact: true }),
  ).toHaveAttribute('download', 'source.md');
  await page.screenshot({
    path: testInfo.outputPath('original-read-error.png'),
  });
  await composer
    .getByRole('button', { name: 'Retry preview', exact: true })
    .click();
  await expect(
    composer.getByRole('heading', { name: 'Source', exact: true }),
  ).toBeVisible();
  expect(reads).toBe(2);
  expect(methods).toEqual(['GET', 'GET']);
  await expect(
    composer.getByRole('textbox', { name: `Message ${room.name}` }),
  ).toHaveValue('Keep the original source.');
});
