import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import {
  createRoom,
  library,
  mutate,
  openLibraryAction,
  openApp,
  openRoomMore,
  signIn,
  workspace,
  work,
} from './team-helpers';
import { watchPageErrors } from './page-errors';

const failures = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  failures.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(({ page }) => expect(failures.get(page)?.errors).toEqual([]));

const history = (page: Page) =>
  page.getByRole('region', { name: 'Document reviews', exact: true });

async function fixture(page: Page, count: number, otherFiles = false) {
  const room = await createRoom(
    page,
    'Review history ' + randomUUID().slice(0, 6),
  );
  let commit = (
    await (
      await page.request.get('/api/resources/' + room.resourceId + '/files')
    ).json()
  ).commit as string;
  for (const path of ['plan.md', 'context.md']) {
    const response = await mutate(
      page,
      '/resources/' + room.resourceId + '/files',
      {
        requestId: randomUUID(),
        expectedCommit: commit,
        path,
        content: '# ' + path + '\nThe retained proposal is ready to read.',
      },
      'PUT',
    );
    expect(response.ok()).toBe(true);
    commit = (
      await (
        await page.request.get('/api/resources/' + room.resourceId + '/files')
      ).json()
    ).commit as string;
  }
  const { user } = await (await page.request.get('/api/auth/status')).json();
  for (let i = 1; i <= count; i++) {
    const create = async (path: string, title: string) => {
      const response = await mutate(page, '/rooms/' + room.id + '/reviews', {
        requestId: randomUUID(),
        title,
        resourceId: room.resourceId,
        path,
        commit,
        reviewerUserId: user.id,
      });
      expect(response.ok()).toBe(true);
    };
    await create('plan.md', 'Plan review ' + String(i).padStart(2, '0'));
    if (otherFiles && i % 5 === 0)
      await create(
        'context.md',
        'Context review ' + String(i).padStart(2, '0'),
      );
  }
  return room;
}

async function fileHistory(page: Page, path = 'plan.md') {
  await workspace(page)
    .getByRole('button', { name: 'Files', exact: true })
    .click();
  await chooseFile(page, path);
  await expect(
    library(page)
      .getByRole('region', { name: 'Selected file', exact: true })
      .getByRole('heading', { name: path, exact: true, level: 2 }),
  ).toBeVisible();
  await openLibraryAction(page, 'Review history…');
  await expect(history(page).locator('li > button').first()).toBeVisible();
}

async function chooseFile(page: Page, path: string) {
  await expect(
    library(page).getByRole('region', { name: 'Selected file', exact: true }),
  ).toBeVisible();
  const selector = library(page).getByRole('combobox', {
    name: 'File',
    exact: true,
  });
  if (await selector.isVisible()) await selector.selectOption(path);
  else
    await library(page)
      .locator('.camp-file-list button')
      .filter({ hasText: path })
      .click();
}

test('file review history reads bounded server-filtered pages and supports keyboard and touch navigation', async ({
  page,
}, testInfo) => {
  const room = await fixture(page, 47, true);
  const reads: { query: URLSearchParams; size: number; paths: string[] }[] = [];
  page.on('response', async (response) => {
    const url = new URL(response.url());
    if (url.pathname !== '/api/reviews' || !response.ok()) return;
    const raw = await response.text();
    const data = JSON.parse(raw) as { reviews: { path: string }[] };
    reads.push({
      query: url.searchParams,
      size: Buffer.byteLength(raw),
      paths: data.reviews.map((review) => review.path),
    });
  });
  await fileHistory(page);
  const list = history(page);
  await expect(list.locator('li > button')).toHaveCount(20);
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 47',
  );
  await expect(list.getByText('plan.md', { exact: true })).toHaveCount(0);
  await expect.poll(() => reads.length).toBeGreaterThan(0);
  expect(
    reads.every(
      (read) =>
        read.query.get('resourceId') === room.resourceId &&
        read.query.get('path') === 'plan.md' &&
        read.query.get('limit') === '20' &&
        read.paths.length <= 20 &&
        read.paths.every((path) => path === 'plan.md') &&
        read.size <= 320 * 1024,
    ),
  ).toBe(true);
  const earlier = list.getByRole('button', { name: 'Earlier', exact: true });
  if (testInfo.project.name === 'touch') await earlier.tap();
  else {
    await earlier.focus();
    await earlier.press('Enter');
  }
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 27',
  );
  await expect(list.locator('li > button')).toHaveCount(20);
  await expect(list.locator('li > button').first()).toBeFocused();
  await earlier.click();
  await expect(list.locator('li > button')).toHaveCount(7);
  await expect(earlier).toBeDisabled();
  await list.getByRole('button', { name: 'Newer', exact: true }).click();
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 27',
  );
  await earlier.click();
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 07',
  );
  await list.getByRole('button', { name: 'Latest', exact: true }).click();
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 47',
  );
  expect(
    (await (await page.request.get('/api/rooms/' + room.id + '/runs')).json())
      .runs,
  ).toEqual([]);
});

test('opening a review and returning retains its history page, selected row and reading position', async ({
  page,
}) => {
  await fixture(page, 47, true);
  await openRoomMore(page, 'File reviews…');
  const list = history(page);
  const earlier = list.getByRole('button', { name: 'Earlier', exact: true });
  await expect(list.locator('li > button')).toHaveCount(20);
  await earlier.click();
  await expect(
    list.getByRole('button', { name: 'Newer', exact: true }),
  ).toBeEnabled();
  await expect(list.locator('li > button').first()).toContainText(
    'Context review 30',
  );
  await earlier.click();
  await expect(list.locator('li > button')).toHaveCount(16);
  const selected = list.locator('li > button').nth(9);
  await selected.scrollIntoViewIfNeeded();
  const before = await selected.evaluate((button) => {
    const ul = button.closest('ul')!;
    return {
      id: (button as HTMLElement).dataset.reviewId!,
      top: ul.scrollTop,
      offset:
        button.getBoundingClientRect().top - ul.getBoundingClientRect().top,
    };
  });
  await selected.click();
  const detail = workspace(page).getByRole('region', {
    name: 'Document review',
    exact: true,
  });
  await expect(detail).toBeVisible();
  await expect(list).toHaveCount(0);
  await detail
    .getByRole('button', { name: 'Back to Reviews', exact: true })
    .click();
  await expect(list.locator('li > button')).toHaveCount(16);
  const restored = list.locator('button[data-review-id="' + before.id + '"]');
  await expect(restored).toBeFocused();
  const offset = await restored.evaluate(
    (button) =>
      button.getBoundingClientRect().top -
      button.closest('ul')!.getBoundingClientRect().top,
  );
  expect(Math.abs(offset - before.offset)).toBeLessThan(3);
  expect(
    Math.abs(
      (await list.locator('ul').evaluate((ul) => ul.scrollTop)) - before.top,
    ),
  ).toBeLessThan(3);
  await expect(earlier).toBeDisabled();
});

test('a slow or failed earlier page retains accepted rows and retries without losing keyboard focus', async ({
  page,
  isMobile,
}) => {
  await page.setViewportSize({ width: isMobile ? 390 : 900, height: 520 });
  await fixture(page, 26);
  await fileHistory(page);
  let release = () => {};
  let announce = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const started = new Promise<void>((resolve) => {
    announce = resolve;
  });
  await page.evaluate(() => {
    const state = { retry: false };
    (
      window as typeof window & { reviewHistoryRecovery: typeof state }
    ).reviewHistoryRecovery = state;
    document.addEventListener(
      'click',
      (event) => {
        if (
          (event.target as HTMLElement).closest('button')?.textContent ===
          'Retry reviews'
        )
          state.retry = true;
      },
      true,
    );
  });
  await page.route('**/api/reviews?*', async (route) => {
    if (!new URL(route.request().url()).searchParams.has('cursor'))
      return route.fallback();
    const retry = await page.evaluate(
      () =>
        (
          window as typeof window & {
            reviewHistoryRecovery: { retry: boolean };
          }
        ).reviewHistoryRecovery.retry,
    );
    if (retry) return route.fallback();
    announce();
    await held;
    return route.fulfill({
      status: 503,
      json: {
        code: 'unavailable',
        error: 'Review history is temporarily unavailable.',
      },
    });
  });
  try {
    const list = history(page);
    await list.locator('li > button').last().scrollIntoViewIfNeeded();
    const earlier = list.getByRole('button', { name: 'Earlier', exact: true });
    await expect(earlier).toBeInViewport();
    await earlier.focus();
    await earlier.press('Enter');
    await started;
    await expect(list.locator('li > button')).toHaveCount(20);
    await expect(list.locator('li > button').first()).toContainText(
      'Plan review 26',
    );
    await expect(
      list.getByRole('button', { name: 'Back to Preview', exact: true }),
    ).toBeVisible();
    release();
    await expect(list.getByRole('alert')).toContainText(
      'temporarily unavailable',
    );
    await expect(list.getByRole('alert')).toBeInViewport();
    await expect(list.locator('li > button')).toHaveCount(20);
    const retry = list.getByRole('button', {
      name: 'Retry reviews',
      exact: true,
    });
    await expect(retry).toBeInViewport();
    await retry.focus();
    await retry.press('Enter');
    await expect(list.locator('li > button')).toHaveCount(6);
    await expect(list.getByRole('alert')).toHaveCount(0);
    await expect(list.locator('li > button').first()).toContainText(
      'Plan review 06',
    );
    await expect(list.locator('li > button').first()).toBeFocused();
    await expect(list.locator('li > button').first()).toBeInViewport();
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a late older page cannot replace a newer selected file and its scoped history', async ({
  page,
}) => {
  await fixture(page, 26, true);
  await fileHistory(page);
  let release = () => {};
  let announce = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const started = new Promise<void>((resolve) => {
    announce = resolve;
  });
  await page.route('**/api/reviews?*', async (route) => {
    const url = new URL(route.request().url());
    if (
      url.searchParams.get('path') !== 'plan.md' ||
      !url.searchParams.has('cursor')
    )
      return route.fallback();
    const response = await route.fetch();
    announce();
    await held;
    return route.fulfill({ response });
  });
  try {
    await history(page)
      .getByRole('button', { name: 'Earlier', exact: true })
      .click();
    await started;
    await history(page)
      .getByRole('button', { name: 'Back to Preview', exact: true })
      .click();
    await chooseFile(page, 'context.md');
    await expect(
      library(page)
        .getByRole('region', { name: 'Selected file', exact: true })
        .getByRole('heading', { name: 'context.md', exact: true, level: 2 }),
    ).toBeVisible();
    await openLibraryAction(page, 'Review history…');
    await expect(history(page).locator('li > button')).toHaveCount(5);
    release();
    await expect(history(page)).not.toContainText('Plan review');
    await expect(history(page).locator('li > button').first()).toContainText(
      'Context review',
    );
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a delayed history page preserves another application focus and resumes when visible', async ({
  page,
}) => {
  await fixture(page, 26);
  await fileHistory(page);
  let release = () => {};
  let announce = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const started = new Promise<void>((resolve) => {
    announce = resolve;
  });
  let complete = () => {};
  const delivered = new Promise<void>((resolve) => {
    complete = resolve;
  });
  await page.route('**/api/reviews?*', async (route) => {
    if (!new URL(route.request().url()).searchParams.has('cursor'))
      return route.fallback();
    const response = await route.fetch();
    announce();
    await held;
    await route.fulfill({ response });
    complete();
  });
  try {
    await history(page)
      .getByRole('button', { name: 'Earlier', exact: true })
      .click();
    await started;
    await openApp(page, 'Work');
    const view = work(page).getByRole('combobox', {
      name: 'View',
      exact: true,
    });
    await view.focus();
    release();
    await delivered;
    const backgroundList = library(page).getByRole('region', {
      name: 'Document reviews',
      exact: true,
      includeHidden: true,
    });
    await expect(backgroundList.locator('li > button')).toHaveCount(
      (await library(page).isVisible()) ? 6 : 20,
    );
    await expect(work(page)).toHaveClass(/is-active/);
    await expect(view).toBeFocused();
    await openApp(page, 'Library');
    await expect(history(page).locator('li > button')).toHaveCount(6);
    await expect(
      history(page).getByRole('button', { name: 'Earlier', exact: true }),
    ).toBeDisabled();
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('review status updates keep their page, row order and reading focus', async ({
  page,
}) => {
  const room = await fixture(page, 47);
  await fileHistory(page);
  const list = history(page);
  await list.getByRole('button', { name: 'Earlier', exact: true }).click();
  await expect(list.locator('li > button').first()).toContainText(
    'Plan review 27',
  );
  const rows = list.locator('li > button');
  const before = await rows.evaluateAll((buttons) =>
    buttons.map((button) => (button as HTMLElement).dataset.reviewId!),
  );
  const selected = rows.nth(6);
  await selected.scrollIntoViewIfNeeded();
  await selected.focus();
  const position = await list.locator('ul').evaluate((ul) => ul.scrollTop);
  const response = await mutate(page, '/reviews/' + before[6] + '/actions', {
    requestId: randomUUID(),
    revision: 1,
    action: 'approve',
    comment: 'This exact submitted version is accepted.',
  });
  expect(response.ok()).toBe(true);
  await expect(selected.locator('.camp-review-state')).toHaveText('Approved');
  expect(
    await rows.evaluateAll((buttons) =>
      buttons.map((button) => (button as HTMLElement).dataset.reviewId!),
    ),
  ).toEqual(before);
  await expect(selected).toBeFocused();
  expect(
    Math.abs(
      (await list.locator('ul').evaluate((ul) => ul.scrollTop)) - position,
    ),
  ).toBeLessThan(3);
  expect(
    (await (await page.request.get('/api/rooms/' + room.id + '/runs')).json())
      .runs,
  ).toEqual([]);
});

test('short review windows keep return and paging controls reachable while reading older rows', async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 520 });
  await fixture(page, 26);
  await fileHistory(page);
  const list = history(page);
  await list.locator('li > button').last().scrollIntoViewIfNeeded();
  await expect(
    list.getByRole('button', { name: 'Back to Preview', exact: true }),
  ).toBeInViewport();
  await expect(
    list.getByRole('button', { name: 'Earlier', exact: true }),
  ).toBeInViewport();
  await list.getByRole('button', { name: 'Earlier', exact: true }).click();
  await expect(list.locator('li > button')).toHaveCount(6);
  await expect(list.locator('li > button').first()).toBeInViewport();
  await expect(
    list.getByRole('button', { name: 'Newer', exact: true }),
  ).toBeInViewport();
});
