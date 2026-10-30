import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import type {
  Agent,
  Message,
  Reference,
  Run,
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
  await page.route(`**/api/rooms/${room.id}/runs`, (route) =>
    route.fulfill({ json: { runs } }),
  );
  for (const run of runs) {
    await page.route(`**/api/runs/${run.id}`, (route) =>
      route.fulfill({ json: { run } }),
    );
    await page.route(`**/api/runs/${run.id}/events?*`, (route) =>
      route.fulfill({ json: { events: [] } }),
    );
  }
  await reload();
  return { room, origin, replies, runs, reload };
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
  '[Read the source](camp-source:1:p2)',
].join('\n\n');

test('a long completed reply keeps its files and checks ahead of expandable, intact Markdown and sources', async ({
  page,
}, testInfo) => {
  const documentId = `source-${randomUUID()}`;
  const reference: Reference = {
    kind: 'file',
    title: 'brief.docx',
    path: 'brief.docx',
    resourceId: 'original-source',
    commit: '3'.repeat(40),
    document: {
      id: documentId,
      format: 'docx',
      status: 'ready',
      sectionCount: 2,
      warnings: [],
    },
  };
  await page.route(`**/api/documents/${documentId}`, (route) =>
    route.fulfill({
      json: {
        document: {
          id: documentId,
          resourceId: reference.resourceId,
          commit: reference.commit,
          path: reference.path,
          format: 'docx',
          status: 'ready',
          sections: [
            { id: 'p1', label: 'Paragraph 1', text: 'The original brief.' },
            {
              id: 'p2',
              label: 'Paragraph 2',
              text: 'Assign a named reviewer before delivery.',
            },
          ],
          warnings: [],
        },
      },
    }),
  );
  const { replies, runs, room, reload } = await completedWork(
    page,
    `complete-${testInfo.project.name}`,
    [
      {
        content: longReply,
        run: {
          references: [reference],
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
  const citation = message.getByRole('button', {
    name: 'Open source brief.docx · Paragraph 2',
    exact: true,
  });
  await citation.scrollIntoViewIfNeeded();
  const point = await citation.boundingBox();
  expect(point).not.toBeNull();
  await page.mouse.move(
    point!.x + point!.width / 2,
    point!.y + point!.height / 2,
  );
  await page.mouse.down();
  // A desktop update between pointer down and up must preserve the link node,
  // its focus and its pending click, including in Firefox.
  const viewport = page.viewportSize()!;
  await page.setViewportSize({ ...viewport, width: viewport.width - 1 });
  await expect(citation).toBeFocused();
  await page.mouse.up();
  const source = message.getByRole('region', {
    name: 'Cited source',
    exact: true,
  });
  await expect(
    source.getByRole('region', { name: 'Paragraph 2', exact: true }),
  ).toContainText('Assign a named reviewer before delivery.');
  await expect(
    source.getByRole('link', { name: 'Download brief.docx', exact: true }),
  ).toHaveAttribute('href', new RegExp(`commit=${reference.commit}`));
  await page.screenshot({ path: testInfo.outputPath('citation-preview.png') });
  await source
    .getByRole('button', { name: 'Close source', exact: true })
    .click();
  await citation.focus();
  await page.setViewportSize(viewport);
  await expect(citation).toBeFocused();
  await citation.press('Enter');
  await expect(
    source.getByRole('region', { name: 'Paragraph 2', exact: true }),
  ).toContainText('Assign a named reviewer before delivery.');
  await source
    .getByRole('button', { name: 'Close source', exact: true })
    .click();
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
      await expect(files).toContainText('Retained result · not published');
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
