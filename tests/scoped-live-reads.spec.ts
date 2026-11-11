import { randomUUID } from 'node:crypto';
import { expect, test, type Page, type Request } from '@playwright/test';
import { createRoom, mutate, openApp, signIn, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(({ page }) => expect(errors.get(page)?.errors).toEqual([]));

test('a fresh desktop starts notifications at its bootstrap frontier and receives new changes', async ({
  page,
}) => {
  const room = await createRoom(
    page,
    `Live frontier ${randomUUID().slice(0, 6)}`,
  );
  let frontier = 0;
  await page.route('**/api/bootstrap?**', async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    frontier = data.workspaceEventCursor;
    await route.fulfill({ response, json: data });
  });
  await page.addInitScript(() => {
    const observed = { urls: [] as string[], ids: [] as number[] };
    (
      window as typeof window & { campLiveReadAudit: typeof observed }
    ).campLiveReadAudit = observed;
    const Source = window.EventSource;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        observed.urls.push(String(url));
        this.addEventListener('workspace', (message) => {
          observed.ids.push(
            JSON.parse((message as MessageEvent<string>).data).id,
          );
        });
      }
    };
  });
  await page.reload();
  await expect(
    workspace(page).getByLabel(`Message ${room.name}`, { exact: true }),
  ).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as typeof window & {
              campLiveReadAudit: { urls: string[]; ids: number[] };
            }
          ).campLiveReadAudit.urls.length,
      ),
    )
    .toBe(1);
  expect(frontier).toBeGreaterThan(0);
  const start = await page.evaluate(
    () =>
      (
        window as typeof window & {
          campLiveReadAudit: { urls: string[]; ids: number[] };
        }
      ).campLiveReadAudit,
  );
  expect(new URL(start.urls[0], page.url()).searchParams.get('after')).toBe(
    String(frontier),
  );
  expect(start.ids.every((id) => id > frontier)).toBe(true);
  const content = `Fresh change ${randomUUID()}`;
  const response = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content,
    postOnly: true,
  });
  expect(response.ok()).toBe(true);
  await expect(
    workspace(page).getByText(content, { exact: true }),
  ).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as typeof window & {
              campLiveReadAudit: { ids: number[] };
            }
          ).campLiveReadAudit.ids.length,
      ),
    )
    .toBeGreaterThan(0);
});

test('notifications during a slow conversation read finish once, preserve the draft and then catch up', async ({
  page,
}) => {
  const room = await createRoom(
    page,
    `Slow live read ${randomUUID().slice(0, 6)}`,
  );
  let release = () => {};
  let announce = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const started = new Promise<void>((resolve) => {
    announce = resolve;
  });
  let reads = 0;
  const readsByCursor = new Map<string, number>();
  let cancelled = 0;
  let heldRequest: Request | undefined;
  let heldRequestCancelled = false;
  page.on('requestfailed', (request) => {
    if (request === heldRequest) heldRequestCancelled = true;
    if (new URL(request.url()).pathname === `/api/rooms/${room.id}/messages`)
      cancelled++;
  });
  await page.route(
    (url) => url.pathname === `/api/rooms/${room.id}/messages`,
    async (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      reads++;
      const cursor =
        new URL(route.request().url()).searchParams.get('after') ?? '0';
      readsByCursor.set(cursor, (readsByCursor.get(cursor) ?? 0) + 1);
      if (reads !== 1) return route.continue();
      heldRequest = route.request();
      const response = await route.fetch();
      announce();
      await held;
      await route.fulfill({ response });
    },
  );
  try {
    const contents = ['First update', 'Second update', 'Final update'].map(
      (text) => `${text} ${randomUUID()}`,
    );
    expect(
      (
        await mutate(page, `/rooms/${room.id}/messages`, {
          requestId: randomUUID(),
          content: contents[0],
          postOnly: true,
        })
      ).ok(),
    ).toBe(true);
    await started;
    const draft = workspace(page).getByLabel(`Message ${room.name}`, {
      exact: true,
    });
    await draft.fill('Keep this unfinished thought.');
    for (const content of contents.slice(1)) {
      expect(
        (
          await mutate(page, `/rooms/${room.id}/messages`, {
            requestId: randomUUID(),
            content,
            postOnly: true,
          })
        ).ok(),
      ).toBe(true);
    }
    // Let the real SSE batch reach the pending read before releasing its old snapshot.
    await page.waitForTimeout(300);
    expect(reads).toBe(1);
    expect(cancelled).toBe(0);
    release();
    await expect(
      workspace(page).getByText(contents.at(-1)!, { exact: true }),
    ).toBeVisible();
    await expect(draft).toHaveValue('Keep this unfinished thought.');
    // Pagination may establish a new tail cursor, but each pending query has
    // only its original request and one catch-up, never an abort/restart loop.
    expect(Math.max(...readsByCursor.values())).toBeLessThanOrEqual(2);
    expect(heldRequestCancelled).toBe(false);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('a connected desktop does not periodically reload its open or folded windows', async ({
  page,
}) => {
  await page.clock.install();
  const pending = new Set<Request>();
  const path = (url: string) => new URL(url).pathname;
  page.on('request', (request) => {
    if (
      request.method() === 'GET' &&
      ['fetch', 'xhr'].includes(request.resourceType()) &&
      path(request.url()).startsWith('/api/') &&
      !request.url().includes('/workspace/events')
    )
      pending.add(request);
  });
  const done = (request: Request) => pending.delete(request);
  page.on('requestfinished', done);
  page.on('requestfailed', done);
  for (const name of ['Work', 'Library', 'Agents']) await openApp(page, name);
  await expect.poll(() => pending.size).toBe(0);
  const reads: string[] = [];
  page.on('request', (request) => {
    const route = path(request.url());
    if (
      request.method() === 'GET' &&
      ['fetch', 'xhr'].includes(request.resourceType()) &&
      route.startsWith('/api/') &&
      !['/api/health', '/api/workspace/events'].includes(route)
    )
      reads.push(route);
  });
  await page.clock.runFor(16000);
  await expect.poll(() => pending.size).toBe(0);
  expect(reads).toEqual([]);
  for (const [name, app] of [
    ['Workspace', 'workspace'],
    ['Work', 'work'],
    ['Library', 'library'],
    ['Agents', 'agents'],
  ]) {
    await openApp(page, name);
    await page
      .locator(`[data-app-id="${app}"]`)
      .getByRole('button', { name: /^Collapse / })
      .first()
      .click();
  }
  reads.length = 0;
  await page.clock.runFor(16000);
  await expect.poll(() => pending.size).toBe(0);
  expect(reads).toEqual([]);
});
