import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { createRoom, library, mutate, signIn, workspace } from './team-helpers';

test.beforeEach(async ({ page }) => {
  await signIn(page);
});

async function publish(page: Page, resourceId: string, content: string) {
  const listing = await (
    await page.request.get(`/api/resources/${resourceId}/files`)
  ).json();
  const response = await mutate(
    page,
    `/resources/${resourceId}/files`,
    {
      requestId: randomUUID(),
      expectedCommit: listing.commit,
      path: 'notes.md',
      content,
    },
    'PUT',
  );
  expect(response.ok()).toBeTruthy();
}

test('Library loading failures stop pending states and recover through explicit retries', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `loading-${testInfo.project.name}`);
  await publish(page, room.resourceId, 'Recovered document content.');
  // Recover only after the actual Retry click. A live refresh between changing
  // a Node-side flag and clicking can otherwise remove the target first.
  await page.evaluate(() => {
    const recovery = { library: false, files: false };
    (
      window as typeof window & { campLibraryRecovery: typeof recovery }
    ).campLibraryRecovery = recovery;
    document.addEventListener(
      'click',
      (event) => {
        const button = (event.target as HTMLElement).closest('button');
        if (button?.textContent === 'Retry Library') recovery.library = true;
        if (button?.textContent === 'Retry Files') recovery.files = true;
      },
      true,
    );
  });

  await page.route('**/api/library', async (route) => {
    if (
      await page.evaluate(
        () =>
          (
            window as typeof window & {
              campLibraryRecovery: { library: boolean };
            }
          ).campLibraryRecovery.library,
      )
    )
      return route.continue();
    await route.fulfill({
      status: 503,
      json: { error: 'Library is temporarily unavailable.' },
    });
  });
  await page.route(
    `**/api/resources/${room.resourceId}/files`,
    async (route) => {
      if (
        await page.evaluate(
          () =>
            (
              window as typeof window & {
                campLibraryRecovery: { files: boolean };
              }
            ).campLibraryRecovery.files,
        )
      )
        return route.continue();
      await route.fulfill({
        status: 503,
        json: { error: 'File listing is temporarily unavailable.' },
      });
    },
  );
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();
  await expect(library(page).getByRole('alert')).toContainText(
    'Library is temporarily unavailable.',
  );
  await expect(library(page).getByText('Opening Library…')).toHaveCount(0);

  await library(page)
    .getByRole('button', { name: 'Retry Library', exact: true })
    .click();
  await expect(library(page).getByRole('alert')).toContainText(
    'File listing is temporarily unavailable.',
  );
  await expect(library(page).getByText('Loading shared files…')).toHaveCount(0);
  await expect(
    library(page).getByText('Library is temporarily unavailable.'),
  ).toHaveCount(0);

  await library(page)
    .getByRole('button', { name: 'Retry Files', exact: true })
    .click();
  await expect(
    library(page).getByText('Recovered document content.', { exact: true }),
  ).toBeVisible();
  await expect(library(page).getByRole('alert')).toHaveCount(0);
});

test('a delayed editor read cannot replace a different resource or History', async ({
  page,
  isMobile,
}, testInfo) => {
  const first = await createRoom(page, `read-first-${testInfo.project.name}`);
  await publish(
    page,
    first.resourceId,
    'Only the first resource owns this text.',
  );
  const second = await createRoom(page, `read-second-${testInfo.project.name}`);
  await publish(
    page,
    second.resourceId,
    'The second resource has its own text.',
  );
  await workspace(page)
    .getByRole('button', { name: 'Library', exact: true })
    .click();

  async function selectResource(name: string) {
    if (isMobile)
      await library(page)
        .getByRole('button', { name: 'Sources', exact: true })
        .tap();
    await library(page)
      .getByRole('complementary', { name: 'Library resources' })
      .getByRole('button')
      .filter({ hasText: `${name} files` })
      .click();
  }

  async function delayEditorRead(resourceId: string) {
    let release = () => {};
    const hold = new Promise<void>((resolve) => {
      release = resolve;
    });
    let received = false;
    await page.route(`**/api/resources/${resourceId}/file?*`, async (route) => {
      const response = await route.fetch();
      received = true;
      await hold;
      await route.fulfill({ response });
    });
    await library(page)
      .getByRole('button', { name: 'Edit Text', exact: true })
      .click();
    await expect.poll(() => received).toBe(true);
    return release;
  }

  async function settleRead(release: () => void) {
    release();
    await page.unrouteAll({ behavior: 'wait' });
    await page.evaluate(
      () =>
        new Promise<void>((resolve) => {
          requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
        }),
    );
  }

  await selectResource(first.name);
  await expect(
    library(page).getByText('Only the first resource owns this text.', {
      exact: true,
    }),
  ).toBeVisible();
  const releaseFirst = await delayEditorRead(first.resourceId);
  try {
    await selectResource(second.name);
    await expect(
      library(page).getByText('The second resource has its own text.', {
        exact: true,
      }),
    ).toBeVisible();
    await settleRead(releaseFirst);
    await expect(library(page).getByLabel('Document content')).toHaveCount(0);
    await expect(library(page).getByRole('alert')).toHaveCount(0);
  } finally {
    releaseFirst();
  }

  const releaseSecond = await delayEditorRead(second.resourceId);
  try {
    await library(page)
      .getByRole('button', { name: 'History', exact: true })
      .click();
    await settleRead(releaseSecond);
    await expect(
      library(page).getByRole('heading', { name: 'Shared version history' }),
    ).toBeVisible();
    await expect(library(page).getByLabel('Document content')).toHaveCount(0);
    await library(page)
      .getByRole('button', { name: 'History', exact: true })
      .click();
    await library(page)
      .getByRole('button', { name: 'Edit Text', exact: true })
      .click();
    await expect(library(page).getByLabel('Document content')).toHaveValue(
      'The second resource has its own text.',
    );
  } finally {
    releaseSecond();
  }
});
