import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Page, type Route } from '@playwright/test';
import type {
  Message,
  Run,
  RunEvent,
  RunActivityWindow,
} from '../apps/dashboard/src/features/workspace/api';
import { createRoom, mutate, signIn, workspace } from './team-helpers';
import { timelineFixture } from './run-timeline-fixture';
import { watchPageErrors } from './page-errors';
import { fixtureNotifications } from './fixture-notifications';

const failures = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  failures.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(failures.get(page)?.errors).toEqual([]),
);

function barrier() {
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { held, release };
}

async function fixture(
  page: Page,
  active = false,
  activityGate?: ReturnType<typeof barrier>,
) {
  const notify = await fixtureNotifications(page);
  const room = await createRoom(page, `Inspector ${randomUUID().slice(0, 6)}`);
  const posted = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Prepare the reviewed notes release.',
    postOnly: true,
  });
  expect(posted.ok()).toBe(true);
  const { message: origin } = (await posted.json()) as { message: Message };
  const now = new Date().toISOString();
  let run: Run = {
    id: `inspector-${randomUUID()}`,
    roomId: room.id,
    resourceId: room.resourceId,
    originMessageId: origin.id,
    threadRootId: '',
    agentId: 'inspector-agent',
    agentSnapshot: {
      id: 'inspector-agent',
      name: 'Notes Writer',
      handle: 'notes-writer',
      icon: 'document',
      avatar: 'agent-robot-teal-v1',
      instructions: 'Private frozen instructions.',
      access: 'write',
      enabled: true,
      revision: 1,
      createdAt: now,
      updatedAt: now,
    },
    runnerId: 'notes-runner',
    runnerName: 'Notes Runner',
    prompt: 'Frozen original request.',
    references: [
      {
        kind: 'file',
        resourceId: room.resourceId,
        path: 'brief.md',
        commit: 'a'.repeat(40),
        title: 'Frozen brief',
      },
    ],
    status: active ? 'running' : 'succeeded',
    createdAt: now,
    updatedAt: now,
    finishedAt: active ? '' : now,
    waitingReason: '',
    error: '',
    output: 'RAW_TRANSCRIPT_ONLY_IN_DETAILS\n'.repeat(16000),
    sessionId: 'private-session',
    sessionMode: 'fresh',
    sessionGeneration: 1,
    baselineCommit: 'a'.repeat(40),
    resultCommit: active ? '' : 'b'.repeat(40),
    environment: {
      kind: 'command',
      checks: [{ name: 'unit', arguments: ['node', '--test'] }],
    },
    checks: [
      {
        name: 'unit',
        command: 'node --test',
        status: active ? 'pending' : 'passed',
        exitCode: active ? null : 0,
        output: 'LARGE_CHECK_OUTPUT\n'.repeat(2000),
        startedAt: now,
        finishedAt: active ? '' : now,
      },
    ],
    files: Array.from({ length: 25 }, (_, index) => ({
      path: `note-${String(index).padStart(2, '0')}.md`,
      status: 'added',
      before: '',
      after: 'LARGE_FILE_BODY\n'.repeat(2000),
    })),
    exitCode: active ? null : 0,
    testExitCode: active ? null : 0,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    published: !active,
  };
  const reply: Message = {
    ...origin,
    id: `reply-${randomUUID()}`,
    authorKind: 'agent',
    authorUserId: '',
    authorName: '',
    agentId: run.agentId,
    runId: run.id,
    content: `# Reviewed notes\n\n${'The original reply remains available.\n\n'.repeat(40)}Exact final recommendation.`,
    seq: origin.seq + 1,
  };
  const reads = {
    summary: 0,
    reply: 0,
    full: 0,
    events: [] as number[],
    activity: [] as string[],
    threadScans: 0,
    writes: 0,
  };
  let inspecting = false;
  const handlers = {
    full: (route: Route) => route.fulfill({ json: { run } }),
    events: async (route: Route, _after: number) =>
      route.fulfill({ json: { events: [] } }),
    activity: async (route: Route) => {
      if (activityGate) await activityGate.held;
      return route.fulfill({
        json: {
          items: [],
          through: 0,
          hasOlder: false,
          hasNewer: false,
        } satisfies RunActivityWindow,
      });
    },
  };
  await page.route(`**/api/rooms/${room.id}/runs?view=conversation*`, (route) =>
    route.fulfill({ json: { items: [timelineFixture(run)], nextCursor: '' } }),
  );
  await page.route(`**/api/runs/${run.id}?view=summary`, (route) => {
    reads.summary++;
    return route.fulfill({ json: { run: timelineFixture(run) } });
  });
  await page.route(`**/api/runs/${run.id}/reply`, (route) => {
    reads.reply++;
    return route.fulfill({ json: { message: reply } });
  });
  await page.route(`**/api/runs/${run.id}`, (route) => {
    reads.full++;
    return handlers.full(route);
  });
  await page.route(`**/api/runs/${run.id}/activity?*`, (route) => {
    reads.activity.push(new URL(route.request().url()).search);
    return handlers.activity(route);
  });
  await page.route(`**/api/runs/${run.id}/events?*`, (route) => {
    const after = Number(
      new URL(route.request().url()).searchParams.get('after') || 0,
    );
    reads.events.push(after);
    return handlers.events(route, after);
  });
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (
      inspecting &&
      url.pathname === `/api/rooms/${room.id}/messages` &&
      url.searchParams.get('after') === '0'
    )
      reads.threadScans++;
    if (
      request.method() === 'POST' &&
      /^\/api\/(?:rooms\/[^/]+\/(?:messages|tasks)|runs\/)/.test(url.pathname)
    )
      reads.writes++;
  });
  await failures.get(page)!.reload();
  const win = workspace(page);
  await expect(win.getByTestId('agent-work')).toBeVisible();
  inspecting = true;
  await win
    .getByTestId('agent-work')
    .getByRole('button', { name: active ? 'Activity' : 'Details', exact: true })
    .click();
  const inspector = win.getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  await expect(
    inspector.getByRole('heading', { name: 'Notes Writer', exact: true }),
  ).toBeVisible();
  return {
    room,
    reads,
    handlers,
    inspector,
    reply,
    notifyActivity: () => notify('run.activity', room.id, run.id),
    get run() {
      return run;
    },
    async setRun(value: Partial<Run>) {
      run = { ...run, ...value };
      await notify('run.updated', room.id, run.id);
    },
  };
}

test('completed Overview reads only summary and exact reply, without raw execution or thread scans', async ({
  page,
}) => {
  const value = await fixture(page);
  const { inspector, reads } = value;
  await expect(
    inspector.getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  const reply = inspector.locator('.workspace-inspector-reply');
  await expect(reply).toBeVisible();
  await reply.locator('summary').click();
  await expect(reply).toContainText('Exact final recommendation.');
  expect(await reply.locator('.workspace-markdown').textContent()).toContain(
    'The original reply remains available.',
  );
  expect(reads.summary).toBeGreaterThan(0);
  expect(reads.reply).toBe(1);
  expect(reads.full).toBe(0);
  expect(reads.events).toEqual([]);
  expect(reads.threadScans).toBe(0);
  expect(reads.writes).toBe(0);
  await expect(inspector).not.toContainText('RAW_TRANSCRIPT_ONLY_IN_DETAILS');
  await expect(inspector).not.toContainText('LARGE_FILE_BODY');
});

test('Files loading and local retry preserve summary and cached reply, and leaving aborts the read', async ({
  page,
}) => {
  const value = await fixture(page);
  const { inspector, reads } = value;
  await expect(inspector.locator('.workspace-inspector-reply')).toBeVisible();
  const held = barrier();
  let mode: 'pending' | 'failure' | 'success' = 'pending';
  value.handlers.full = async (route) => {
    if (mode === 'pending') await held.held;
    return mode === 'success'
      ? route.fulfill({ json: { run: value.run } })
      : route.fulfill({
          status: 503,
          json: { error: 'The file details could not be read.' },
        });
  };
  await page.evaluate((id) => {
    const state = window as typeof window & { inspectorAborts: number };
    state.inspectorAborts = 0;
    const original = window.fetch.bind(window);
    window.fetch = (input, init) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        location.href,
      );
      if (url.pathname === `/api/runs/${id}` && !url.search)
        (
          init?.signal ?? (input instanceof Request ? input.signal : null)
        )?.addEventListener(
          'abort',
          () => {
            state.inspectorAborts++;
          },
          { once: true },
        );
      return original(input, init);
    };
  }, value.run.id);
  try {
    await inspector.getByRole('tab', { name: /^Files/ }).click();
    await expect(
      inspector.getByText('Loading execution details…', { exact: true }),
    ).toBeVisible();
    await expect.poll(() => reads.full).toBe(1);
    await expect(inspector.locator('.workspace-run-outcome')).toContainText(
      'Completed',
    );
    await inspector.getByRole('tab', { name: 'Overview', exact: true }).click();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as typeof window & { inspectorAborts: number })
              .inspectorAborts,
        ),
      )
      .toBe(1);
    held.release();
    await expect(inspector.locator('.workspace-inspector-reply')).toBeVisible();
    mode = 'failure';
    await inspector.getByRole('tab', { name: /^Files/ }).click();
    const failure = inspector
      .getByRole('alert')
      .filter({ hasText: 'The file details could not be read.' });
    await expect(failure).toBeVisible();
    await expect(inspector.locator('.workspace-run-outcome')).toContainText(
      'Completed',
    );
    mode = 'success';
    await failure
      .getByRole('button', { name: 'Retry details', exact: true })
      .click();
    await expect(
      inspector.locator('.workspace-file-change > summary'),
    ).toHaveCount(25);
    await inspector.locator('.workspace-file-change > summary').first().click();
    await expect(
      inspector
        .getByRole('link', { name: 'Download File', exact: true })
        .first(),
    ).toHaveAttribute('href', new RegExp(`commit=${value.run.resultCommit}`));
    await expect(failure).toHaveCount(0);
    expect(reads.full).toBe(3);
    expect(reads.events).toEqual([]);
    await inspector.getByRole('tab', { name: 'Overview', exact: true }).click();
    await expect(inspector.locator('.workspace-inspector-reply')).toBeVisible();
    expect(reads.reply).toBe(1);
  } finally {
    held.release();
  }
});

test('summary notifications reach terminal state while Activity is blocked and leaving Activity stops its reads', async ({
  page,
}) => {
  await page.clock.install();
  const held = barrier();
  const value = await fixture(page, true, held);
  const { inspector, reads } = value;
  try {
    await expect.poll(() => reads.activity.length).toBeGreaterThan(0);
    await expect(
      inspector.getByText('Loading activity…', { exact: true }),
    ).toBeVisible();
    await value.setRun({
      status: 'succeeded',
      updatedAt: new Date(Date.now() + 1000).toISOString(),
      finishedAt: new Date().toISOString(),
      resultCommit: 'b'.repeat(40),
      published: true,
    });
    await expect(inspector.locator('.workspace-run-outcome')).toContainText(
      'Completed',
    );
    await inspector.getByRole('tab', { name: 'Overview', exact: true }).click();
    held.release();
    await expect(inspector.locator('.workspace-inspector-reply')).toBeVisible();
    const count = reads.activity.length;
    // Advancing browser time proves the inactive owner has no polling timer.
    await page.clock.fastForward(3600);
    expect(reads.activity).toHaveLength(count);
    expect(reads.full).toBe(0);
  } finally {
    held.release();
  }
});

test('diagnostics wait for every log page and certify completeness only from a terminal pre-log snapshot', async ({
  page,
}) => {
  const value = await fixture(page, true);
  const { inspector, reads } = value;
  await inspector.getByRole('tab', { name: 'Overview', exact: true }).click();
  const tail = barrier();
  const events: RunEvent[] = Array.from({ length: 501 }, (_, index) => ({
    runId: value.run.id,
    seq: index + 1,
    type: 'tool_output',
    text: `Retained event ${index + 1}: ${'x'.repeat(512)}`,
    createdAt: value.run.createdAt,
  }));
  value.handlers.events = async (route, after) => {
    if (after === 500) await tail.held;
    return route.fulfill({
      json: {
        events: events.filter((event) => event.seq > after).slice(0, 500),
      },
    });
  };
  const downloads: string[] = [];
  page.on('download', (download) =>
    downloads.push(download.suggestedFilename()),
  );
  try {
    const downloaded = page.waitForEvent('download');
    await inspector
      .getByRole('button', { name: 'Download diagnostics', exact: true })
      .click();
    await expect.poll(() => reads.events.includes(500)).toBe(true);
    await expect(
      inspector.getByRole('button', {
        name: 'Preparing diagnostics…',
        exact: true,
      }),
    ).toBeDisabled();
    expect(downloads).toEqual([]);
    await value.setRun({
      status: 'succeeded',
      updatedAt: new Date(Date.now() + 1000).toISOString(),
      finishedAt: new Date().toISOString(),
      resultCommit: 'b'.repeat(40),
      published: true,
    });
    await expect(inspector.locator('.workspace-run-outcome')).toContainText(
      'Completed',
    );
    tail.release();
    const first = await downloaded;
    const initial = JSON.parse(await readFile((await first.path())!, 'utf8'));
    expect(initial.activity).toEqual(events);
    expect(initial.activityComplete).toBe(false);
    expect(initial.rawOutput).toBe(value.run.output);
    const completedDownload = page.waitForEvent('download');
    await inspector
      .getByRole('button', { name: 'Download diagnostics', exact: true })
      .click();
    const completed = JSON.parse(
      await readFile((await (await completedDownload).path())!, 'utf8'),
    );
    expect(completed.activity).toEqual(events);
    expect(completed.activityComplete).toBe(true);
    expect(reads.full).toBe(2);
    expect(reads.writes).toBe(0);
  } finally {
    tail.release();
  }
});

test('execution Activity opens one recent window and preserves frozen history during updates and retry', async ({
  page,
}, info) => {
  await page.clock.install();
  const value = await fixture(page);
  let frontier = 20000;
  const queries: URLSearchParams[] = [];
  let failOlder = false;
  value.handlers.activity = (route) => {
    const query = new URL(route.request().url()).searchParams;
    queries.push(query);
    if (failOlder && query.has('before'))
      return route.fulfill({
        status: 503,
        json: { error: 'The earlier activity could not be read.' },
      });
    const through = Number(query.get('through') ?? frontier);
    const before = Number(query.get('before') ?? through + 1);
    const after = Number(query.get('after') ?? 0);
    const limit = Number(query.get('limit') ?? 50);
    let items = Array.from(
      { length: Math.floor(frontier / 40) },
      (_, index) => ({
        seq: (index + 1) * 40,
        type: 'tool_call',
        name: 'read_file',
        detail: `note-${(index + 1) * 40}.md`,
        createdAt: value.run.createdAt,
      }),
    ).filter(
      (item) => item.seq <= through && item.seq < before && item.seq > after,
    );
    items = query.has('after') ? items.slice(0, limit) : items.slice(-limit);
    return route.fulfill({
      json: {
        items,
        through,
        hasOlder: (items[0]?.seq ?? 0) > 40,
        hasNewer: (items.at(-1)?.seq ?? through) < through,
      } satisfies RunActivityWindow,
    });
  };
  const { inspector, reads } = value;
  await inspector.getByRole('tab', { name: 'Activity', exact: true }).click();
  const rows = inspector.locator('.workspace-event-log > li');
  await expect(rows).toHaveCount(50);
  await expect(rows.last()).toContainText('note-18040.md');
  await expect(rows.first()).toContainText('note-20000.md');
  expect(queries).toHaveLength(1);
  expect(reads.events).toEqual([]);
  expect(reads.full).toBe(0);
  await inspector.screenshot({
    path: info.outputPath('execution-recent-activity.png'),
  });
  await rows.last().scrollIntoViewIfNeeded();
  await expect(
    inspector.getByRole('button', { name: 'Earlier activity', exact: true }),
  ).toBeInViewport();
  failOlder = true;
  await inspector
    .getByRole('button', { name: 'Earlier activity', exact: true })
    .click();
  await expect(inspector.getByRole('alert')).toContainText(
    'The earlier activity could not be read.',
  );
  await expect(rows.first()).toContainText('note-20000.md');
  failOlder = false;
  await inspector
    .getByRole('button', { name: 'Retry activity', exact: true })
    .click();
  await expect(rows.last()).toContainText('note-16040.md');
  await expect(rows.first()).toContainText('note-18000.md');
  await expect(rows.first()).toBeInViewport();
  frontier = 20040;
  const count = queries.length;
  await value.notifyActivity();
  await page.clock.fastForward(500);
  expect(queries).toHaveLength(count);
  await expect(rows.last()).toContainText('note-16040.md');
  await inspector
    .getByRole('button', { name: 'Later activity', exact: true })
    .click();
  await expect(rows.first()).toContainText('note-20000.md');
  expect(queries.at(-1)?.get('through')).toBe('20000');
  await inspector
    .getByRole('button', { name: 'Latest activity', exact: true })
    .click();
  await expect(rows.first()).toContainText('note-20040.md');
  expect(queries.at(-1)?.has('through')).toBe(false);
  await expect(rows).toHaveCount(50);
  expect(reads.events).toEqual([]);
  expect(reads.writes).toBe(0);
});

test('live Activity pins a reading gesture and resumes current observations only through Latest', async ({
  page,
}) => {
  await page.clock.install();
  const value = await fixture(page, true);
  let frontier = 3;
  value.handlers.activity = (route) => {
    const query = new URL(route.request().url()).searchParams;
    const through = Number(query.get('through') ?? frontier);
    const items = Array.from(
      { length: Math.min(through, frontier) },
      (_, i) => ({
        seq: i + 1,
        type: 'tool_call',
        name: 'read_file',
        detail: `note-${i + 1}.md`,
        createdAt: value.run.createdAt,
      }),
    );
    return route.fulfill({
      json: {
        items,
        through,
        hasOlder: false,
        hasNewer: false,
      } satisfies RunActivityWindow,
    });
  };
  await value.notifyActivity();
  const { inspector, reads } = value;
  const rows = inspector.locator('.workspace-event-log > li');
  await expect(rows).toHaveCount(3);
  await expect(rows.first()).toContainText('note-3.md');
  await rows.first().locator('p').click();
  await expect
    .poll(() =>
      reads.activity.some(
        (path) => new URLSearchParams(path).get('through') === '3',
      ),
    )
    .toBe(true);
  const count = reads.activity.filter((path) =>
    new URLSearchParams(path).has('through'),
  ).length;
  frontier = 4;
  await value.notifyActivity();
  await page.clock.fastForward(500);
  await expect(rows).toHaveCount(3);
  await expect(rows.first()).toContainText('note-3.md');
  expect(
    reads.activity.filter((path) => new URLSearchParams(path).has('through')),
  ).toHaveLength(count);
  await inspector
    .getByRole('button', { name: 'Latest activity', exact: true })
    .click();
  await expect(rows).toHaveCount(4);
  await expect(rows.first()).toContainText('note-4.md');
  expect(reads.events).toEqual([]);
  expect(reads.writes).toBe(0);
});
