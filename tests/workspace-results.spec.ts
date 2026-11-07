import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type {
  Agent,
  Message,
  Run,
} from '../apps/dashboard/src/features/workspace/api';
import {
  openRoomMore,
  createRoom,
  mutate,
  openApp,
  signIn,
  workspace,
} from './team-helpers';
import { watchPageErrors } from './page-errors';
import { timelineFixture } from './run-timeline-fixture';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

type ResultFixture = {
  content: string;
  run?: Partial<Run>;
  thread?: boolean;
  access?: Agent['access'];
};

async function completedWork(
  page: Page,
  name: string,
  fixtures: ResultFixture[],
) {
  const room = await createRoom(page, `${name}-${randomUUID().slice(0, 8)}`);
  const reload = () => errors.get(page)!.reload();
  const response = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Prepare and review a document for this team.',
    postOnly: true,
  });
  expect(response.ok()).toBeTruthy();
  const { message: origin } = (await response.json()) as {
    message: Message;
  };
  const now = new Date().toISOString();
  const agent: Agent = {
    id: `result-agent-${randomUUID()}`,
    name: 'Result Writer',
    handle: 'result-writer',
    icon: 'document',
    avatar: 'agent-robot-teal-v1',
    instructions: 'Write and review team documents when asked.',
    access: 'write',
    enabled: true,
    revision: 1,
    createdAt: now,
    updatedAt: now,
  };
  const runs: Run[] = fixtures.map((fixture) => ({
    id: `completion-${randomUUID()}`,
    roomId: room.id,
    threadRootId: fixture.thread ? origin.id : '',
    originMessageId: origin.id,
    agentId: agent.id,
    resourceId: room.resourceId,
    prompt: origin.content,
    references: [],
    status: 'succeeded',
    createdAt: now,
    updatedAt: now,
    finishedAt: now,
    error: '',
    output: 'The deterministic browser fixture completed.',
    sessionId: '',
    sessionMode: 'fresh',
    baselineCommit: '1'.repeat(40),
    resultCommit: '2'.repeat(40),
    agentSnapshot: { ...agent, access: fixture.access ?? agent.access },
    environment: null,
    checks: [],
    files: [],
    exitCode: 0,
    testExitCode: null,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    sessionGeneration: 1,
    published: false,
    ...fixture.run,
  }));
  const replies: Message[] = fixtures.map((fixture, index) => ({
    id: `reply-${randomUUID()}`,
    roomId: room.id,
    threadRootId: runs[index].threadRootId,
    authorKind: 'agent',
    authorUserId: '',
    authorName: '',
    agentId: agent.id,
    runId: runs[index].id,
    content: fixture.content,
    requestId: '',
    references: [],
    seq: origin.seq + index + 1,
    createdAt: now,
    updatedAt: now,
  }));
  const messages = [origin, ...replies];
  // Persist only the human discussion. Completed runs are explicit presentation
  // fixtures; these tests never invoke a model or fabricate server execution.
  await page.route(`**/api/rooms/${room.id}/messages?*`, (route) => {
    const requested = new URL(route.request().url()).searchParams;
    const thread = requested.get('threadRootId') || '';
    const after = Number(requested.get('after') || '0');
    return route.fulfill({
      json: {
        messages: messages.filter(
          (message) => message.threadRootId === thread && message.seq > after,
        ),
      },
    });
  });
  await page.route(`**/api/rooms/${room.id}/messages/*`, (route) => {
    const id = new URL(route.request().url()).pathname.split('/').at(-1);
    const message = messages.find((item) => item.id === id);
    return message ? route.fulfill({ json: { message } }) : route.continue();
  });
  await page.route(`**/api/rooms/${room.id}/runs?view=timeline*`, (route) =>
    route.fulfill({
      json: { items: runs.map(timelineFixture), nextCursor: '' },
    }),
  );
  const fullRunReads: string[] = [];
  for (const run of runs) {
    await page.route(`**/api/runs/${run.id}`, (route) => {
      fullRunReads.push(run.id);
      return route.fulfill({ json: { run } });
    });
    await page.route(`**/api/runs/${run.id}/events?*`, (route) =>
      route.fulfill({ json: { events: [] } }),
    );
  }
  await reload();
  return { room, origin, replies, runs, reload, fullRunReads };
}

const longReply = [
  'The proposal identifies an owner for each launch milestone.',
  '## Delivery checklist',
  '- [x] Assign an owner\n- [ ] Confirm the launch date',
  '| Milestone | Owner |\n| --- | --- |\n| Review | Morgan |\n| Delivery | Alex |',
  ...Array.from(
    { length: 9 },
    (_, index) =>
      `Detailed consideration ${index + 1}: record the decision, its owner, and the evidence needed before delivery. Keep the source document available for the next reviewer.`,
  ),
  'The final recommendation remains part of the complete reply.',
  '[Read the source](https://example.com/review-guide)',
].join('\n\n');

for (const outcome of ['arrives', 'fails'] as const) {
  test(`initial work metadata ${outcome} without replacing an expanded reply, its selection or focused link`, async ({
    page,
  }, info) => {
    const { room, replies, runs, reload } = await completedWork(
      page,
      `initial-metadata-${outcome}-${info.project.name}`,
      [{ content: longReply, run: { published: true } }],
    );
    let release!: () => void;
    const waiting = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route(
      `**/api/rooms/${room.id}/runs?view=timeline*`,
      async (route) => {
        await waiting;
        return outcome === 'arrives'
          ? route.fulfill({
              json: { items: runs.map(timelineFixture), nextCursor: '' },
            })
          : route.fulfill({
              status: 503,
              json: { error: 'Initial work activity unavailable.' },
            });
      },
    );
    try {
      await reload();
      const message = workspace(page).locator(
        `[data-message-id="${replies[0].id}"]`,
      );
      const reply = message.locator('.workspace-agent-reply');
      await expect(reply).toBeVisible();
      await expect(reply).not.toHaveAttribute('open');
      await expect(message.locator('.workspace-result-facts')).toHaveCount(0);
      await reply.locator(':scope > summary').click();
      await expect(reply).toHaveAttribute('open');
      const source = message.getByRole('link', {
        name: 'Read the source',
        exact: true,
      });
      const paragraph = reply.getByText(
        'The proposal identifies an owner for each launch milestone.',
        { exact: true },
      );
      await paragraph.evaluate((node) => {
        const range = document.createRange();
        range.selectNodeContents(node);
        const selection = window.getSelection()!;
        selection.removeAllRanges();
        selection.addRange(range);
      });
      await source.focus();
      await expect(source).toBeFocused();
      const selection = await page.evaluate(() =>
        window.getSelection()?.toString(),
      );
      expect(selection).toBe(
        'The proposal identifies an owner for each launch milestone.',
      );
      const originalLink = await source.elementHandle();
      expect(originalLink).not.toBeNull();
      release();
      if (outcome === 'arrives') {
        await expect(message.locator('.workspace-result-facts')).toContainText(
          'Saved to room files',
        );
      } else {
        await expect(workspace(page)).toContainText(
          'Initial work activity unavailable.',
        );
        await expect(message.locator('.workspace-result-facts')).toHaveCount(0);
      }
      await expect(reply).toHaveAttribute('open');
      await expect(source).toBeFocused();
      expect(
        await source.evaluate(
          (node, original) => node === original,
          originalLink,
        ),
      ).toBe(true);
      expect(await page.evaluate(() => window.getSelection()?.toString())).toBe(
        selection,
      );
      await expect(reply).toContainText(
        'The final recommendation remains part of the complete reply.',
      );
    } finally {
      release();
    }
  });
}

test('a delayed source lookup cannot close a newer Automation view or replace its draft', async ({
  page,
}, testInfo) => {
  const { origin, replies, room, runs } = await completedWork(
    page,
    `source-navigation-${testInfo.project.name}`,
    [{ content: 'A saved proposal is ready for review.' }],
  );
  const created = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    threadRootId: origin.id,
    content: 'The original source remains in its Thread.',
    postOnly: true,
  });
  expect(created.ok()).toBe(true);
  const { message: source } = (await created.json()) as { message: Message };
  runs[0].originMessageId = source.id;
  const win = workspace(page);
  await win
    .locator(`[data-message-id="${replies[0].id}"]`)
    .getByRole('button', { name: /^Details/ })
    .click();
  const inspector = win.getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  await inspector.getByText('Original request', { exact: true }).click();
  let requested = false;
  let release = () => {};
  let held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(
    `**/api/rooms/${room.id}/messages/${source.id}`,
    async (route) => {
      requested = true;
      await held;
      await route.fulfill({ json: { message: source } });
    },
  );
  try {
    await inspector
      .getByRole('button', { name: 'View request in chat', exact: true })
      .click();
    await expect.poll(() => requested).toBe(true);
    await openRoomMore(page, 'Sources & rules…');
    const automation = win.getByRole('region', {
      name: 'Room management',
      exact: true,
    });
    await automation
      .getByRole('tab', { name: 'Sources & rules', exact: true })
      .click();
    const rules = automation.getByLabel('Rules', { exact: true });
    const draft =
      'Keep my new policy draft open while the old source resolves.';
    await expect(rules).toBeEnabled();
    await rules.fill(draft);
    const response = page.waitForResponse((value) =>
      value.url().endsWith(`/messages/${source.id}`),
    );
    release();
    await (await response).finished();
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(automation).toBeVisible();
    await expect(rules).toHaveValue(draft);
    await expect(
      automation.getByRole('tab', { name: 'Sources & rules', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    await automation
      .getByRole('button', { name: 'Back to Discussion' })
      .click();
    await win
      .locator(`[data-message-id="${replies[0].id}"]`)
      .getByRole('button', { name: /^Details/ })
      .click();
    await inspector.getByText('Original request', { exact: true }).click();
    held = new Promise<void>((resolve) => {
      release = resolve;
    });
    requested = false;
    await inspector
      .getByRole('button', { name: 'View request in chat', exact: true })
      .click();
    await expect.poll(() => requested).toBe(true);
    const selection = () =>
      page.evaluate(() =>
        Object.fromEntries(
          Object.entries(sessionStorage).filter(([key]) =>
            /^camp:rooms:[^:]+:(room|thread)$/.test(key),
          ),
        ),
      );
    const beforeClose = await selection();
    await win
      .getByRole('button', {
        name: /^Close .+ — Workspace$/,
      })
      .click();
    await expect(win).toHaveCount(0);
    const late = page.waitForResponse((value) =>
      value.url().endsWith(`/messages/${source.id}`),
    );
    release();
    await (await late).finished();
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    expect(await selection()).toEqual(beforeClose);
    await openApp(page, 'Workspace');
    await expect(
      win.getByRole('form', { name: 'Room composer', exact: true }),
    ).toBeVisible();
    await expect(
      win.getByRole('form', { name: 'Thread composer', exact: true }),
    ).toHaveCount(0);
  } finally {
    release();
  }
});

test('a long completed reply keeps its files and checks ahead of expandable, intact Markdown and sources', async ({
  page,
}, testInfo) => {
  await page.context().route('https://example.com/review-guide', (route) =>
    route.fulfill({
      contentType: 'text/plain',
      body: 'A named reviewer confirms delivery.',
    }),
  );
  const { replies, runs, room, reload } = await completedWork(
    page,
    `complete-${testInfo.project.name}`,
    [
      {
        content: longReply,
        run: {
          published: true,
          files: [
            {
              path: 'proposal.md',
              status: 'added',
              before: '',
              after: '# Proposal\nThe saved result is version-pinned.',
            },
          ],
          environment: {
            kind: 'command',
            checks: [{ name: 'Document check', script: 'check-document' }],
          },
          checks: [
            {
              name: 'Document check',
              command: 'check-document',
              status: 'passed',
              exitCode: 0,
              output: 'The required sections are present.',
              startedAt: new Date().toISOString(),
              finishedAt: new Date().toISOString(),
            },
          ],
          testExitCode: 0,
        },
      },
    ],
  );
  const message = workspace(page).locator(
    `[data-message-id="${replies[0].id}"]`,
  );
  const file = message.getByRole('button', {
    name: 'Open proposal.md',
    exact: true,
  });
  await expect(file).toBeVisible();
  await expect(
    message.getByText('Saved to room files', { exact: true }),
  ).toBeVisible();
  await expect(
    message
      .getByRole('region', { name: 'Work result', exact: true })
      .getByText('Checks: Passed', { exact: true }),
  ).toBeVisible();
  const disclosure = message.locator('.workspace-agent-reply');
  const toggle = disclosure.locator(':scope > summary');
  await expect(toggle).toHaveText('Read full reply');
  await expect(disclosure).not.toHaveAttribute('open');
  await expect(
    message.getByText(
      'The final recommendation remains part of the complete reply.',
      { exact: true },
    ),
  ).not.toBeVisible();
  expect(
    await file.evaluate((element) => {
      const toggle = element
        .closest('article')!
        .querySelector('.workspace-agent-reply > summary')!;
      return Boolean(
        element.compareDocumentPosition(toggle) &
        Node.DOCUMENT_POSITION_FOLLOWING,
      );
    }),
  ).toBeTruthy();

  await toggle.focus();
  await toggle.press('Enter');
  const collapse = message.getByRole('button', {
    name: 'Collapse reply',
    exact: true,
  });
  await expect(disclosure).toHaveAttribute('open');
  await expect(toggle).toHaveText('Full reply');
  await expect(message.getByRole('table')).toContainText('Morgan');
  await expect(message.getByRole('checkbox').first()).toBeChecked();
  await expect(message.getByRole('checkbox').last()).not.toBeChecked();
  await expect(
    message.getByText(
      'The final recommendation remains part of the complete reply.',
      { exact: true },
    ),
  ).toBeVisible();
  await reload();
  await expect(disclosure).toHaveAttribute('open');
  await expect(toggle).toHaveText('Full reply');
  await expect(message.getByRole('table')).toContainText('Morgan');
  const source = message.getByRole('link', {
    name: 'Read the source',
    exact: true,
  });
  await source.scrollIntoViewIfNeeded();
  const point = await source.boundingBox();
  expect(point).not.toBeNull();
  await page.mouse.move(
    point!.x + point!.width / 2,
    point!.y + point!.height / 2,
  );
  await page.mouse.down();
  // Preserve the actual Markdown link across a desktop update between pointer
  // down and up, including Safari's OS-dependent mouse focus preference.
  await source.focus();
  const viewport = page.viewportSize()!;
  await page.setViewportSize({ ...viewport, width: viewport.width - 1 });
  await expect(source).toBeFocused();
  const opened = page.waitForEvent('popup');
  await page.mouse.up();
  const linked = await opened;
  await expect(linked).toHaveURL('https://example.com/review-guide');
  await linked.close();
  await page.bringToFront();
  await collapse.focus();
  await collapse.press('Space');
  await expect(toggle).toBeFocused();
  await expect(disclosure).not.toHaveAttribute('open');

  let fetchedVersion = '';
  await page.route(`**/api/resources/${room.resourceId}/file?*`, (route) => {
    fetchedVersion =
      new URL(route.request().url()).searchParams.get('commit') ?? '';
    return route.fulfill({
      contentType: 'text/plain',
      body: runs[0].files[0].after,
    });
  });
  await file.click();
  const preview = message.getByRole('region', {
    name: 'Preview proposal.md',
    exact: true,
  });
  await expect(
    preview.getByText('The saved result is version-pinned.', { exact: true }),
  ).toBeVisible();
  expect(fetchedVersion).toBe(runs[0].resultCommit);
  await expect(
    preview.getByRole('link', { name: 'Download proposal.md', exact: true }),
  ).toHaveAttribute('href', new RegExp(`commit=${runs[0].resultCommit}`));
  await page.screenshot({
    path: testInfo.outputPath('markdown-file-preview.png'),
  });
  await page.setViewportSize(viewport);
  await expect(disclosure).not.toHaveAttribute('open');
  await reload();
  await expect(disclosure).not.toHaveAttribute('open');
  await expect(toggle).toHaveText('Read full reply');
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});

test('short replies stay inline and absent or read-only checks are never reported as passed', async ({
  page,
}, testInfo) => {
  const { replies } = await completedWork(
    page,
    `short-${testInfo.project.name}`,
    [
      { content: 'The next step is to confirm the owner with Morgan.' },
      {
        content: 'The proposal names an owner for every milestone.',
        access: 'read',
        run: {
          environment: {
            kind: 'command',
            checks: [{ name: 'Document check', script: 'check-document' }],
          },
        },
      },
    ],
  );
  for (const [index, reply] of replies.entries()) {
    const message = workspace(page).locator(`[data-message-id="${reply.id}"]`);
    await expect(
      message.getByText(reply.content, { exact: true }),
    ).toBeVisible();
    await expect(message.locator('.workspace-agent-reply')).toHaveCount(0);
    const result = message.getByRole('region', {
      name: 'Work result',
      exact: true,
    });
    await expect(result).toContainText(
      index ? 'Read-only work' : 'No files changed',
    );
    await expect(result).toContainText(
      index ? 'Checks: Not run · read-only work' : 'Checks: Not configured',
    );
    await expect(result).not.toContainText('Passed');
    await expect(
      message.getByRole('region', { name: 'Result files', exact: true }),
    ).toHaveCount(0);
  }
});

test('failed, cancelled and interrupted work keeps its error and check facts visible without expanding the reply', async ({
  page,
}, testInfo) => {
  const fixtures: ResultFixture[] = [
    {
      content: longReply,
      run: {
        status: 'failed',
        error: 'The document check failed: an owner is missing.',
        files: [
          {
            path: 'proposal.md',
            status: 'modified',
            before: 'Original',
            after: 'Retained draft',
          },
        ],
        environment: {
          kind: 'command',
          checks: [{ name: 'Document check', script: 'check-document' }],
        },
        checks: [
          {
            name: 'Document check',
            command: 'check-document',
            status: 'failed',
            exitCode: 1,
            output: 'Missing owner.',
            startedAt: '',
            finishedAt: '',
          },
        ],
        testExitCode: 1,
      },
    },
    {
      content: longReply,
      run: {
        status: 'cancelled',
        error: 'The requester stopped this work.',
        resultCommit: '',
        files: [
          {
            path: 'unconfirmed.md',
            status: 'added',
            before: '',
            after: 'Not captured',
          },
        ],
        environment: {
          kind: 'command',
          checks: [{ name: 'Document check', script: 'check-document' }],
        },
      },
    },
    {
      content: longReply,
      run: {
        status: 'interrupted',
        error: 'The server restarted before the result could be saved.',
        resultCommit: '',
        environment: {
          kind: 'command',
          checks: [{ name: 'Document check', script: 'check-document' }],
        },
        checks: [
          {
            name: 'Document check',
            command: 'check-document',
            status: 'interrupted',
            exitCode: null,
            output: 'Checking references…',
            startedAt: '',
            finishedAt: '',
          },
        ],
      },
    },
  ];
  const { replies, runs } = await completedWork(
    page,
    `outcomes-${testInfo.project.name}`,
    fixtures,
  );
  for (const [index, reply] of replies.entries()) {
    const message = workspace(page).locator(`[data-message-id="${reply.id}"]`);
    const result = message.getByRole('region', {
      name: 'Work result',
      exact: true,
    });
    await expect(result).toBeVisible();
    await expect(
      message.getByText(runs[index].error, { exact: true }),
    ).toBeVisible();
    await expect(message.locator('.workspace-agent-reply')).not.toHaveAttribute(
      'open',
    );
    await expect(result).not.toContainText('Saved to room files');
    await expect(result).not.toContainText('Passed');
    await expect(result).toContainText(
      ['Checks: Failed', 'Checks: Not run', 'Checks: Interrupted'][index],
    );
    const files = message.getByRole('region', {
      name: 'Result files',
      exact: true,
    });
    if (index === 0) {
      await expect(result).toContainText('Changes retained separately');
      await expect(
        files.getByRole('button', { name: 'Open proposal.md', exact: true }),
      ).toBeVisible();
      await expect(files).toContainText('Saved files · not published');
    } else {
      await expect(result).toContainText('No result files captured');
      await expect(files).toHaveCount(0);
      await expect(result).not.toContainText('retained separately');
    }
  }
});

for (const context of ['room', 'room details', 'thread'] as const) {
  test(`${context} results hand off the full reply in the same conversation while preserving a draft and requiring explicit sending`, async ({
    page,
  }, testInfo) => {
    const threaded = context === 'thread';
    const rawOutput =
      'Tool transport transcript: {"type":"tool_result","sessionId":"internal-runtime-record"}';
    const { replies, origin, room } = await completedWork(
      page,
      `handoff-${context}-${testInfo.project.name}`,
      [{ content: longReply, thread: threaded, run: { output: rawOutput } }],
    );
    if (threaded)
      await workspace(page)
        .locator(`[data-message-id="${origin.id}"]`)
        .getByRole('button', { name: /^Open thread/ })
        .click();
    const message = workspace(page).locator(
      `[data-message-id="${replies[0].id}"]`,
    );
    await expect(
      message.getByRole('region', { name: 'Work result', exact: true }),
    ).toBeVisible();
    await expect(message.locator('.workspace-agent-reply')).not.toHaveAttribute(
      'open',
    );
    const composer = workspace(page).getByRole('form', {
      name: threaded ? 'Thread composer' : 'Room composer',
      exact: true,
    });
    const input = composer.getByRole('textbox', {
      name: threaded ? 'Message thread' : `Message ${room.name}`,
      exact: true,
    });
    const draft = 'Review this proposal against the original brief.';
    await input.fill(draft);
    if (context === 'room details') {
      await message.getByRole('button', { name: /^Details/ }).click();
      const inspector = workspace(page).getByRole('region', {
        name: 'Work details',
        exact: true,
      });
      await expect(inspector).toBeVisible();
      await expect(
        inspector.getByRole('tab', { name: 'Overview', exact: true }),
      ).toHaveAttribute('aria-selected', 'true');
      await expect(inspector.getByText(rawOutput, { exact: true })).toHaveCount(
        0,
      );
      const fullReply = inspector.locator('.workspace-inspector-reply');
      await expect(fullReply).toBeVisible();
      await expect(fullReply).not.toHaveAttribute('open');
      await fullReply.locator(':scope > summary').click();
      await expect(
        fullReply.getByRole('heading', {
          name: 'Delivery checklist',
          exact: true,
        }),
      ).toBeVisible();
      await expect(fullReply.getByRole('table')).toContainText('Morgan');
      await expect(
        fullReply.getByText(
          'The final recommendation remains part of the complete reply.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(inspector.getByText(rawOutput, { exact: true })).toHaveCount(
        0,
      );
      await expect(
        workspace(page).getByRole('region', { name: 'Room Runner' }),
      ).toBeHidden();
      await expect(workspace(page).getByLabel('Room members')).toHaveCount(0);
      await expect(
        workspace(page).locator('.workspace-unavailable'),
      ).toHaveCount(0);
      await inspector
        .getByRole('button', { name: 'Ask agent…', exact: true })
        .click();
    } else {
      await message
        .getByRole('button', { name: 'Ask agent…', exact: true })
        .click();
    }
    await expect(input).toHaveValue(draft);
    await expect(
      workspace(page).getByRole('region', { name: 'Room Runner' }),
    ).toHaveCount(0);
    await expect(workspace(page).getByLabel('Room members')).toBeVisible();
    await expect(
      workspace(page).getByRole('form', {
        name: threaded ? 'Room composer' : 'Thread composer',
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      composer.getByText('Choose a recipient to continue', { exact: true }),
    ).toBeVisible();
    const references = composer.getByRole('region', {
      name: 'Draft references',
      exact: true,
    });
    await references
      .locator('summary')
      .filter({ hasText: "Result Writer's message" })
      .click();
    expect(await references.locator('blockquote').textContent()).toBe(
      longReply,
    );
    await expect(message.locator('.workspace-agent-reply')).not.toHaveAttribute(
      'open',
    );
    await expect
      .poll(async () => {
        const saved = await (
          await page.request.get(
            `/api/me/drafts/conversation?key=${room.id}:${threaded ? origin.id : 'room'}`,
          )
        ).json();
        return JSON.parse(saved.draft.value?.references || '[]');
      })
      .toContainEqual(
        expect.objectContaining({
          kind: 'message',
          messageId: replies[0].id,
          content: longReply,
          threadRootId: threaded ? origin.id : '',
        }),
      );
    const persisted = await (
      await page.request.get(`/api/rooms/${room.id}/messages`)
    ).json();
    expect(persisted.messages).toHaveLength(1);
    expect(persisted.messages[0].id).toBe(origin.id);
    expect(
      (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
  });
}

test('discussion keeps the complete file count while previewing only the bounded timeline paths', async ({
  page,
}, info) => {
  const files = Array.from({ length: 25 }, (_, index) => ({
    path: `notes-${String(index + 1).padStart(2, '0')}.md`,
    status: 'added' as const,
    before: '',
    after: `# Note ${index + 1}\nThis full file body belongs only to explicit details.`,
  }));
  const { replies, fullRunReads } = await completedWork(
    page,
    `bounded-paths-${info.project.name}`,
    [{ content: 'The complete set of notes is retained.', run: { files } }],
  );
  const win = workspace(page);
  const result = win.locator(`[data-message-id="${replies[0].id}"]`);
  const details = result.getByRole('button', {
    name: 'Details · 25 files',
    exact: true,
  });
  await expect(details).toBeVisible();
  const documents = result.getByRole('region', {
    name: 'Result files',
    exact: true,
  });
  await expect(
    documents.getByRole('button', { name: /^Open notes-/ }),
  ).toHaveCount(3);
  await documents
    .getByRole('button', { name: 'Show 20 files', exact: true })
    .click();
  await expect(
    documents.getByRole('button', { name: /^Open notes-/ }),
  ).toHaveCount(20);
  await expect(
    documents.getByRole('button', { name: 'Open notes-25.md', exact: true }),
  ).toHaveCount(0);
  await expect(result).not.toContainText(
    'This full file body belongs only to explicit details.',
  );
  expect(fullRunReads).toEqual([]);
  await details.click();
  const inspector = win.getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  await expect(inspector).toBeVisible();
  await inspector.getByRole('tab', { name: /^Files/ }).click();
  await expect(
    inspector
      .locator('.workspace-file-change > summary')
      .filter({ hasText: 'notes-25.md' }),
  ).toBeVisible();
  expect(fullRunReads).toHaveLength(1);
});

for (const timeline of ['ready', 'pending'] as const) {
  test(`Ask agent loads frozen sources only on request and preserves their exact versions in the draft (${timeline} timeline)`, async ({
    page,
  }, info) => {
    const frozenCommit = '3'.repeat(40);
    const resultCommit = '4'.repeat(40);
    const { room, replies, runs, fullRunReads, reload } = await completedWork(
      page,
      `lazy-frozen-handoff-${timeline}-${info.project.name}`,
      [
        {
          content: 'The reviewed result should travel with its original brief.',
          run: {
            references: [
              {
                kind: 'file',
                resourceId: 'frozen-original-resource',
                commit: frozenCommit,
                path: 'original-brief.md',
                title: 'Original brief',
              },
            ],
            resultCommit,
            files: [
              {
                path: 'result.md',
                status: 'added',
                before: '',
                after: '# Retained result',
              },
            ],
          },
        },
      ],
    );
    let release = () => {};
    if (timeline === 'pending') {
      const waiting = new Promise<void>((resolve) => {
        release = resolve;
      });
      await page.route(
        `**/api/rooms/${room.id}/runs?view=timeline*`,
        async (route) => {
          await waiting;
          return route.fulfill({
            json: { items: runs.map(timelineFixture), nextCursor: '' },
          });
        },
      );
      await reload();
    }
    const executionWrites: string[] = [];
    page.on('request', (request) => {
      if (
        request.method() === 'POST' &&
        /\/api\/rooms\/[^/]+\/(?:messages|tasks)$/.test(
          new URL(request.url()).pathname,
        )
      )
        executionWrites.push(request.url());
    });
    try {
      const win = workspace(page);
      const reply = win.locator(`[data-message-id="${replies[0].id}"]`);
      await expect(
        reply.getByText(
          'The reviewed result should travel with its original brief.',
          { exact: true },
        ),
      ).toBeVisible();
      expect(fullRunReads).toEqual([]);
      if (timeline === 'pending')
        await expect(reply.locator('.workspace-result-facts')).toHaveCount(0);
      const input = win.getByRole('textbox', {
        name: `Message ${room.name}`,
        exact: true,
      });
      const draft =
        'Review this against the original brief, retaining my text.';
      await input.fill(draft);
      await reply
        .getByRole('button', { name: 'Ask agent…', exact: true })
        .click();
      await expect(
        win.getByText('Choose a recipient to continue', { exact: true }),
      ).toBeVisible();
      await expect(input).toHaveValue(draft);
      expect(fullRunReads).toEqual([runs[0].id]);
      if (timeline === 'pending')
        await expect(reply.locator('.workspace-result-facts')).toHaveCount(0);
      await expect
        .poll(async () => {
          const body = await (
            await page.request.get(
              `/api/me/drafts/conversation?key=${room.id}:room`,
            )
          ).json();
          return JSON.parse(body.draft.value?.references || '[]');
        })
        .toEqual(
          expect.arrayContaining([
            expect.objectContaining({
              kind: 'file',
              resourceId: 'frozen-original-resource',
              commit: frozenCommit,
              path: 'original-brief.md',
            }),
            expect.objectContaining({
              kind: 'file',
              resourceId: room.resourceId,
              commit: resultCommit,
              path: 'result.md',
            }),
            expect.objectContaining({
              kind: 'message',
              messageId: replies[0].id,
            }),
          ]),
        );
      const persisted = await (
        await page.request.get(`/api/rooms/${room.id}/messages`)
      ).json();
      expect(persisted.messages).toHaveLength(1);
      expect(
        (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json())
          .runs,
      ).toEqual([]);
      expect(executionWrites).toEqual([]);
    } finally {
      release();
    }
  });
}
