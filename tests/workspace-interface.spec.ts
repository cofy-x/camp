import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import type {
  Agent,
  Run,
  SendInput,
} from '../apps/dashboard/src/features/workspace/api';
import { createRoom, mutate, post, signIn, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

const errors = new WeakMap<Page, ReturnType<typeof watchPageErrors>>();
test.beforeEach(async ({ page, browserName }) => {
  errors.set(page, watchPageErrors(page, browserName));
  await signIn(page);
});
test.afterEach(async ({ page }) =>
  expect(errors.get(page)?.errors).toEqual([]),
);

async function createAgent(page: Page, name: string, enabled = true) {
  const response = await mutate(page, '/agents', {
    name: `${name} ${randomUUID().slice(0, 6)}`,
    handle: `interface-${randomUUID().slice(0, 8)}`,
    instructions: 'Review documents when explicitly asked.',
    access: 'read',
    enabled,
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()).agent as Agent;
}

test('recipient labels match selected and mentioned Agents, while Post only suppresses all Agent work', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `routing-${testInfo.project.name}`);
  const writer = await createAgent(page, 'Selected Writer');
  const reviewer = await createAgent(page, 'Mentioned Reviewer');
  for (const agent of [writer, reviewer]) {
    const response = await mutate(
      page,
      `/rooms/${room.id}/members/${agent.id}`,
      {},
      'PUT',
    );
    expect(response.ok()).toBeTruthy();
  }
  // Exercise submission without enabling an actual model or execution runtime.
  await page.route('**/api/rooms', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const result = await response.json();
    await route.fulfill({
      response,
      json: {
        ...result,
        rooms: result.rooms.map((item: { id: string }) =>
          item.id === room.id
            ? { ...item, runnerId: 'interface-runner', runnerRevision: 1 }
            : item,
        ),
      },
    });
  });
  await page.route('**/api/workspace/status', async (route) => {
    const response = await route.fetch();
    await route.fulfill({
      response,
      json: { ...(await response.json()), available: true, reason: '' },
    });
  });
  let attempted: SendInput | null = null;
  await page.route(`**/api/rooms/${room.id}/messages`, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const body = route.request().postDataJSON() as SendInput;
    if (body.postOnly) return route.continue();
    attempted = body;
    await route.fulfill({
      status: 503,
      json: {
        error: 'The test request was not sent. Your draft is preserved.',
      },
    });
  });
  await errors.get(page)!.reload();
  const composer = workspace(page).getByRole('form', { name: 'Room composer' });
  const input = composer.getByRole('textbox', { name: `Message ${room.name}` });
  await expect(input).toBeEnabled();
  await composer.getByRole('button', { name: /^To:/ }).click();
  await composer
    .getByRole('checkbox', { name: writer.name, exact: true })
    .check();
  await composer.getByRole('button', { name: /^To:/ }).click();
  const text = `@${reviewer.handle} Please review this with the writer.`;
  await input.fill(text);
  await expect(composer.getByRole('button', { name: /^To:/ })).toHaveText(
    `To: ${writer.name}, ${reviewer.name} ▾`,
  );
  await composer
    .getByRole('button', { name: 'Send to Agent', exact: true })
    .click();
  await expect(composer.getByRole('alert')).toContainText('draft is preserved');
  expect(attempted).toMatchObject({
    content: text,
    recipientAgentIds: [writer.id, reviewer.id],
  });
  await expect(input).toHaveValue(text);

  await composer.getByRole('button', { name: /^To:/ }).click();
  await composer
    .getByRole('button', { name: 'Post only', exact: true })
    .click();
  await expect(input).toHaveAttribute('placeholder', 'Write a message…');
  const consequence = composer.getByText('Agent mentions will not start work', {
    exact: true,
  });
  await expect(consequence).toBeVisible();
  const bounds = await consequence.boundingBox();
  const windowBounds = await workspace(page).boundingBox();
  expect(bounds).not.toBeNull();
  expect(windowBounds).not.toBeNull();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(
    windowBounds!.x + windowBounds!.width,
  );
  await composer.getByRole('button', { name: 'Post', exact: true }).click();
  await expect(input).toHaveValue('');
  await expect(workspace(page).getByText(text, { exact: true })).toBeVisible();
  const messages = await (
    await page.request.get(`/api/rooms/${room.id}/messages`)
  ).json();
  expect(messages.messages).toHaveLength(1);
  expect(messages.messages[0].content).toBe(text);
  expect(messages.messages[0].runId).toBe('');
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});

test('members manage their own Agents and cannot share another owner’s Agent', async ({
  page,
  browser,
  baseURL,
  isMobile,
}, testInfo) => {
  const otherAgent = await createAgent(page, 'Another owner’s Reviewer');
  const invitation = await mutate(page, '/team/invites', {
    expiresInHours: 24,
  });
  expect(invitation.ok()).toBeTruthy();
  const { token } = await invitation.json();
  const context = await browser.newContext({
    baseURL,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  const member = await context.newPage();
  member.on('pageerror', (error) =>
    errors.get(page)?.errors.push(error.message),
  );
  try {
    await member.goto(`/#invite=${encodeURIComponent(token)}`);
    await member.getByLabel('Your name', { exact: true }).fill('Room Teammate');
    await member
      .getByLabel('Username', { exact: true })
      .fill(`room-member-${randomUUID().slice(0, 8)}`);
    await member
      .getByLabel(/^Password/)
      .fill('A private room interface test password');
    await member
      .getByRole('button', { name: 'Join Team', exact: true })
      .click();
    await expect(workspace(member)).toBeVisible();
    const agent = await createAgent(member, 'My Room Reviewer');
    const disabled = await createAgent(member, 'My Disabled Reviewer', false);
    const room = await createRoom(
      member,
      `membership-${testInfo.project.name}`,
    );
    await workspace(member)
      .getByRole('button', { name: 'Agents', exact: true })
      .click();
    const panel = workspace(member).getByRole('region', {
      name: 'Room agents',
      exact: true,
    });
    const back = panel.getByRole('button', {
      name: 'Back to Chat',
      exact: true,
    });
    await expect(back).toBeEnabled();
    const available = panel.getByRole('region', {
      name: 'Available agents',
      exact: true,
    });
    const otherRow = available
      .getByRole('listitem')
      .filter({ has: member.getByText(otherAgent.name, { exact: true }) });
    await expect(
      otherRow.getByRole('button', { name: 'Join Room', exact: true }),
    ).toHaveCount(0);
    const disabledRow = available
      .getByRole('listitem')
      .filter({ has: member.getByText(disabled.name, { exact: true }) });
    await expect(
      disabledRow.getByRole('button', { name: 'Join Room', exact: true }),
    ).toBeDisabled();
    const candidate = available
      .getByRole('listitem')
      .filter({ has: member.getByText(agent.name, { exact: true }) });
    const responsibilities = candidate.getByText(agent.instructions, {
      exact: true,
    });
    await expect(responsibilities).not.toBeVisible();
    await candidate.getByText('Responsibilities', { exact: true }).focus();
    await member.keyboard.press('Enter');
    await expect(responsibilities).toBeVisible();
    await candidate.getByText('Responsibilities', { exact: true }).click();
    await expect(responsibilities).not.toBeVisible();
    await candidate
      .getByRole('button', { name: 'Join Room', exact: true })
      .click();
    const joined = panel
      .getByRole('region', { name: 'In this room', exact: true })
      .getByRole('listitem')
      .filter({ has: member.getByText(agent.name, { exact: true }) });
    await expect(joined).toBeVisible();
    await expect(
      joined.getByRole('button', { name: 'Leave Room', exact: true }),
    ).toBeVisible();
    await expect(
      joined.getByRole('button', { name: 'Edit', exact: true }),
    ).toBeVisible();
    await expect(
      panel.getByRole('button', { name: 'New Agent…', exact: true }),
    ).toBeVisible();
    const members = await (
      await member.request.get(`/api/rooms/${room.id}/members`)
    ).json();
    expect(members.members.map((entry: Agent) => entry.id)).toContain(agent.id);
    expect(members.members.map((entry: Agent) => entry.id)).not.toContain(
      disabled.id,
    );
    expect(
      (
        await mutate(
          member,
          `/rooms/${room.id}/members/${otherAgent.id}`,
          {},
          'DELETE',
        )
      ).status(),
    ).toBe(403);
    await back.click();
    await expect(
      workspace(member).getByRole('form', { name: 'Room composer' }),
    ).toBeVisible();
    await post(member, room.name, 'The reviewer is ready for a later request.');
    expect(
      (await (await member.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
  } finally {
    await context.close();
  }
});

test('removing a Room Agent confirms the work consequence and preserves existing discussion', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `leaving-${testInfo.project.name}`);
  const agent = await createAgent(page, 'Leaving Reviewer');
  const response = await mutate(
    page,
    `/rooms/${room.id}/members/${agent.id}`,
    {},
    'PUT',
  );
  expect(response.ok()).toBeTruthy();
  await post(
    page,
    room.name,
    'Keep this discussion after the reviewer leaves.',
  );
  await workspace(page)
    .getByRole('button', { name: 'Agents', exact: true })
    .click();
  const panel = workspace(page).getByRole('region', {
    name: 'Room agents',
    exact: true,
  });
  const joined = panel
    .getByRole('region', { name: 'In this room', exact: true })
    .getByRole('listitem')
    .filter({ has: page.getByText(agent.name, { exact: true }) });
  await joined.getByRole('button', { name: 'Leave Room', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText(
    'Queued and active requests in this Room will stop.',
  );
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(joined).toBeVisible();
  const retained = await (
    await page.request.get(`/api/rooms/${room.id}/members`)
  ).json();
  expect(retained.members.map((entry: Agent) => entry.id)).toContain(agent.id);
  await joined.getByRole('button', { name: 'Leave Room', exact: true }).click();
  await dialog.getByRole('button', { name: 'Leave Room', exact: true }).click();
  await expect(joined).toHaveCount(0);
  const members = await (
    await page.request.get(`/api/rooms/${room.id}/members`)
  ).json();
  expect(members.members.map((entry: Agent) => entry.id)).not.toContain(
    agent.id,
  );
  await panel
    .getByRole('button', { name: 'Back to Chat', exact: true })
    .click();
  await expect(
    workspace(page).getByText(
      'Keep this discussion after the reviewer leaves.',
      { exact: true },
    ),
  ).toBeVisible();
});

test('work details lead with results, preserve failure evidence, and keep technical records in diagnostics', async ({
  page,
}, testInfo) => {
  const room = await createRoom(page, `results-${testInfo.project.name}`);
  const agent = await createAgent(page, 'Result Reviewer');
  const now = new Date().toISOString();
  const runs: Run[] = [];
  for (const kind of ['captured', 'uncaptured', 'unchanged']) {
    const response = await mutate(page, `/rooms/${room.id}/messages`, {
      requestId: randomUUID(),
      content: `Inspect ${kind} result.`,
      postOnly: true,
    });
    expect(response.ok()).toBeTruthy();
    const { message } = await response.json();
    runs.push({
      id: `interface-${kind}-${randomUUID()}`,
      roomId: room.id,
      threadRootId: '',
      originMessageId: message.id,
      agentId: agent.id,
      resourceId: room.resourceId,
      runnerId: `runner-${kind}-${randomUUID()}`,
      runnerName: 'Design Studio Runner',
      attemptId: `attempt-${kind}-${randomUUID()}`,
      prompt: message.content,
      references: [],
      status: kind === 'unchanged' ? 'succeeded' : 'failed',
      createdAt: now,
      updatedAt: now,
      finishedAt: now,
      error:
        kind === 'uncaptured'
          ? 'Result capture could not be confirmed.'
          : kind === 'captured'
            ? 'The document check failed.'
            : '',
      output: `Retained ${kind} execution output.`,
      sessionId: '',
      sessionMode: 'fresh',
      baselineCommit: '1'.repeat(40),
      resultCommit: kind === 'uncaptured' ? '' : '2'.repeat(40),
      agentSnapshot: {
        ...agent,
        access: 'write',
        name:
          kind === 'captured'
            ? 'Collaboration Writer Preparing the Team Onboarding Guide'
            : `Result ${kind}`,
      },
      environment:
        kind !== 'unchanged'
          ? {
              kind: 'command',
              checks: [{ name: 'Document check', script: 'check-document' }],
            }
          : null,
      checks:
        kind !== 'unchanged'
          ? [
              {
                name: 'Document check',
                command: 'check-document',
                status: 'failed',
                exitCode: 1,
                output: 'Missing required owner.',
                startedAt: now,
                finishedAt: now,
              },
            ]
          : [],
      files:
        kind === 'captured'
          ? [
              {
                path: 'proposal.md',
                status: 'modified',
                before: 'Earlier proposal.',
                after: 'Retained proposal revision.',
              },
            ]
          : [],
      exitCode: kind === 'uncaptured' ? null : 0,
      testExitCode: kind !== 'unchanged' ? 1 : null,
      maxTokens: 4096,
      maxSteps: 8,
      timeoutSeconds: 180,
      sessionGeneration: 1,
      published: false,
    });
  }
  await page.route(`**/api/rooms/${room.id}/runs`, (route) =>
    route.fulfill({ json: { runs } }),
  );
  for (const run of runs) {
    await page.route(`**/api/runs/${run.id}`, (route) =>
      route.fulfill({ json: { run } }),
    );
    await page.route(`**/api/runs/${run.id}/events?*`, (route) =>
      route.fulfill({
        json: {
          events: [
            {
              runId: run.id,
              seq: 1,
              type: 'tool_output',
              text: run.output,
              createdAt: now,
            },
            {
              runId: run.id,
              seq: 2,
              type: 'tool_result',
              text: 'Recorded tool result, retained in diagnostics.',
              createdAt: now,
            },
            {
              runId: run.id,
              seq: 3,
              type: 'tool_call',
              text: JSON.stringify({
                name: 'read',
                arguments: { path: 'proposal.md' },
              }),
              createdAt: now,
            },
            {
              runId: run.id,
              seq: 4,
              type: 'status',
              text: 'checking',
              createdAt: now,
            },
            {
              runId: run.id,
              seq: 5,
              type: 'error',
              text: 'The tool process stopped unexpectedly.',
              createdAt: now,
            },
          ],
        },
      }),
    );
  }
  await errors.get(page)!.reload();
  const inspector = workspace(page).getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  for (const run of runs) {
    await workspace(page)
      .getByTestId('agent-work')
      .filter({ hasText: run.agentSnapshot.name })
      .getByRole('button', { name: 'Details', exact: true })
      .click();
    await expect(inspector.getByRole('heading', { level: 2 })).toContainText(
      run.agentSnapshot.name,
    );
    expect(
      await inspector
        .getByRole('button', { name: 'Back to Chat', exact: true })
        .evaluate((button) => button.scrollWidth <= button.clientWidth),
    ).toBe(true);
    await expect(
      inspector.getByRole('tab', { name: 'Overview', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    await expect(inspector.getByText('Design Studio Runner')).toBeVisible();
    // The execution transcript is not an Agent-authored completion message.
    await expect(inspector.getByText(run.output, { exact: true })).toHaveCount(
      0,
    );
    await expect(
      inspector.locator('summary').filter({ hasText: /^Agent reply$/ }),
    ).toHaveCount(0);
    await expect(
      inspector.getByRole('link', { name: 'Download Patch', exact: true }),
    ).toHaveCount(0);
    await expect(
      inspector.getByText(run.prompt, { exact: true }),
    ).not.toBeVisible();
    for (const label of [
      'Execution identity',
      'Execution details',
      'Budget',
      'Runtime exit',
      'Starting version',
      'Result version',
    ]) {
      await expect(inspector.getByText(label, { exact: true })).toHaveCount(0);
    }
    for (const identity of [
      run.id,
      run.runnerId!,
      run.attemptId!,
      run.baselineCommit,
      run.resultCommit,
    ]) {
      if (identity)
        await expect(
          inspector.getByText(identity, { exact: true }),
        ).toHaveCount(0);
    }
    if (run.error)
      await expect(inspector.getByRole('alert')).toContainText(run.error);
    await expect(
      inspector.getByRole('button', { name: 'Open in Library', exact: true }),
    ).toHaveCount(0);
    if (run.files.length) {
      await inspector.screenshot({
        path: testInfo.outputPath('work-overview.png'),
      });
      const bounds = await inspector.boundingBox();
      expect(bounds).not.toBeNull();
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(
        page.viewportSize()!.width,
      );
      expect(
        await inspector.evaluate(
          (element) => element.scrollWidth <= element.clientWidth,
        ),
      ).toBeTruthy();
    }
    await inspector
      .locator('summary')
      .filter({ hasText: /^Original request$/ })
      .click();
    await expect(
      inspector.getByText(run.prompt, { exact: true }),
    ).toBeVisible();
    await inspector.getByRole('tab', { name: 'Activity', exact: true }).click();
    const activity = inspector.getByRole('list', {
      name: 'Execution activity',
      exact: true,
    });
    await expect(activity.getByRole('listitem')).toHaveCount(3);
    await expect(activity).toContainText('Reading a file');
    await expect(activity).toContainText('Checking');
    await expect(activity).toContainText(
      'The tool process stopped unexpectedly.',
    );
    await expect(activity).not.toContainText('Tool output recorded');
    await expect(activity).not.toContainText('Received tool output');
    await expect(activity.getByText(run.output, { exact: true })).toHaveCount(
      0,
    );
    await expect(activity.locator('summary')).toHaveCount(0);
    if (run.error)
      await expect(inspector.getByRole('alert')).toContainText(run.error);

    const downloadPromise = page.waitForEvent('download');
    await inspector
      .getByRole('button', { name: 'Download diagnostics', exact: true })
      .click();
    const diagnosticsDownload = await downloadPromise;
    const diagnosticsPath = await diagnosticsDownload.path();
    expect(diagnosticsPath).not.toBeNull();
    const diagnostics = JSON.parse(await readFile(diagnosticsPath!, 'utf8'));
    expect(diagnostics.run).toMatchObject({
      id: run.id,
      runnerId: run.runnerId,
      attemptId: run.attemptId,
      baselineCommit: run.baselineCommit,
      resultCommit: run.resultCommit,
      configuredLimits: {
        maxOutputTokensPerRequest: run.maxTokens,
        maxObservedModelSteps: run.maxSteps,
        timeoutSeconds: run.timeoutSeconds,
      },
    });
    expect(diagnostics.rawOutput).toBe(run.output);
    expect(diagnostics.activityComplete).toBe(true);
    expect(diagnostics.activity).toEqual([
      expect.objectContaining({ runId: run.id, seq: 1, text: run.output }),
      expect.objectContaining({
        seq: 2,
        type: 'tool_result',
        text: 'Recorded tool result, retained in diagnostics.',
      }),
      expect.objectContaining({ seq: 3, type: 'tool_call' }),
      expect.objectContaining({ seq: 4, type: 'status', text: 'checking' }),
      expect.objectContaining({
        seq: 5,
        type: 'error',
        text: 'The tool process stopped unexpectedly.',
      }),
    ]);
    await inspector.getByRole('tab', { name: /^Files \(/ }).click();
    const patch = inspector.getByRole('link', {
      name: 'Download Patch',
      exact: true,
    });
    if (run.files.length) {
      await expect(patch).toHaveAttribute('href', `/api/runs/${run.id}/patch`);
      await expect(patch).toHaveAttribute('download', `${run.id}.patch`);
      await expect(
        inspector.getByRole('button', { name: 'Open in Library', exact: true }),
      ).toHaveCount(0);
      await inspector.locator('.workspace-file-change > summary').click();
      await expect(
        inspector.getByText(run.files[0].after, { exact: true }),
      ).toBeVisible();
      const download = inspector.getByRole('link', {
        name: 'Download File',
        exact: true,
      });
      await expect(download).toHaveAttribute(
        'href',
        new RegExp(`commit=${run.resultCommit}`),
      );
      await expect(
        inspector.getByText('Changes retained separately', { exact: true }),
      ).toBeVisible();
    } else {
      await expect(patch).toHaveCount(0);
      await expect(
        inspector.getByText('No file changes were collected.', { exact: true }),
      ).toBeVisible();
    }
    await inspector.getByRole('tab', { name: 'Checks', exact: true }).click();
    await expect(patch).toHaveCount(0);
    if (run.checks?.length) {
      await expect(
        inspector.getByText('Failed', { exact: true }).last(),
      ).toBeVisible();
      await expect(
        inspector.getByText('Missing required owner.', { exact: true }),
      ).toBeVisible();
      await expect(
        inspector.locator('summary').filter({ hasText: 'Document check' }),
      ).toContainText('exit 1');
    } else {
      await expect(
        inspector.getByText('Not configured', { exact: true }),
      ).toBeVisible();
    }
    await inspector
      .getByRole('button', { name: 'Back to Chat', exact: true })
      .click();
  }
  // The displayed runs are deterministic browser fixtures, not model execution.
  expect(
    (await (await page.request.get(`/api/rooms/${room.id}/runs`)).json()).runs,
  ).toEqual([]);
});

test('a member can stop work from details, recover a failed request, and keep their chosen tab during updates', async ({
  page,
  browser,
  baseURL,
  isMobile,
  browserName,
}, testInfo) => {
  const agent = await createAgent(page, 'Cancellable Writer');
  const invitation = await mutate(page, '/team/invites', {
    expiresInHours: 24,
  });
  expect(invitation.ok()).toBeTruthy();
  const { token } = await invitation.json();
  const context = await browser.newContext({
    baseURL,
    viewport: page.viewportSize() ?? undefined,
    ...(isMobile
      ? { hasTouch: true, isMobile: true, deviceScaleFactor: 2 }
      : {}),
  });
  const member = await context.newPage();
  const memberNavigation = watchPageErrors(
    member,
    browserName,
    errors.get(page)!.errors,
  );
  try {
    await member.goto(`/#invite=${encodeURIComponent(token)}`);
    await member
      .getByLabel('Your name', { exact: true })
      .fill('Work Requester');
    await member
      .getByLabel('Username', { exact: true })
      .fill(`work-member-${randomUUID().slice(0, 8)}`);
    await member
      .getByLabel(/^Password/)
      .fill('A private work interface test password');
    await member
      .getByRole('button', { name: 'Join Team', exact: true })
      .click();
    await expect(workspace(member)).toBeVisible();
    const authentication = await (
      await member.request.get('/api/auth/status')
    ).json();
    expect(authentication.user.role).toBe('member');
    const room = await createRoom(member, `stopping-${testInfo.project.name}`);
    const submitted = await mutate(member, `/rooms/${room.id}/messages`, {
      requestId: randomUUID(),
      content: 'Write a concise team handbook.',
      postOnly: true,
    });
    expect(submitted.ok()).toBeTruthy();
    const { message } = await submitted.json();
    const now = new Date().toISOString();
    let run: Run = {
      id: `interface-stopping-${randomUUID()}`,
      roomId: room.id,
      threadRootId: '',
      originMessageId: message.id,
      agentId: agent.id,
      resourceId: room.resourceId,
      runnerId: `runner-${randomUUID()}`,
      runnerName: 'Editorial Runner',
      attemptId: `attempt-${randomUUID()}`,
      prompt: message.content,
      references: [],
      status: 'running',
      createdAt: now,
      updatedAt: now,
      finishedAt: '',
      error: '',
      output: '',
      sessionId: '',
      sessionMode: 'fresh',
      baselineCommit: 'a'.repeat(40),
      resultCommit: '',
      agentSnapshot: { ...agent, access: 'write' },
      environment: null,
      checks: [],
      files: [],
      exitCode: null,
      testExitCode: null,
      maxTokens: 4096,
      maxSteps: 8,
      timeoutSeconds: 180,
      sessionGeneration: 1,
      published: false,
    };
    await member.route(`**/api/rooms/${room.id}/runs`, (route) =>
      route.fulfill({ json: { runs: [run] } }),
    );
    await member.route(`**/api/runs/${run.id}`, (route) =>
      route.fulfill({ json: { run } }),
    );
    await member.route(`**/api/runs/${run.id}/events?*`, (route) =>
      route.fulfill({ json: { events: [] } }),
    );
    let cancelRequests = 0;
    await member.route(`**/api/runs/${run.id}/cancel`, async (route) => {
      expect(route.request().method()).toBe('POST');
      cancelRequests += 1;
      if (cancelRequests === 1) {
        await route.fulfill({
          status: 503,
          json: { error: 'Stopping could not be confirmed. Please try again.' },
        });
        return;
      }
      run = {
        ...run,
        status: 'cancelling',
        updatedAt: new Date().toISOString(),
      };
      await route.fulfill({ json: { run } });
    });
    await memberNavigation.reload();
    await workspace(member)
      .getByTestId('agent-work')
      .filter({ hasText: agent.name })
      .getByRole('button', { name: 'Activity', exact: true })
      .click();
    const inspector = workspace(member).getByRole('region', {
      name: 'Work details',
      exact: true,
    });
    await expect(
      inspector.getByRole('tab', { name: 'Activity', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    const overview = inspector.getByRole('tab', {
      name: 'Overview',
      exact: true,
    });
    await overview.click();
    run = { ...run, status: 'checking', updatedAt: new Date().toISOString() };
    await expect(
      inspector.getByText('Checking', { exact: true }),
    ).toBeVisible();
    await expect(overview).toHaveAttribute('aria-selected', 'true');

    const checks = inspector.getByRole('tab', { name: 'Checks', exact: true });
    await checks.click();
    const stop = inspector.getByRole('button', { name: 'Stop', exact: true });
    await expect(stop).toBeEnabled();
    await stop.click();
    await expect(inspector.getByRole('alert')).toContainText(
      'Stopping could not be confirmed.',
    );
    await expect(stop).toBeEnabled();
    await expect(checks).toHaveAttribute('aria-selected', 'true');
    await stop.click();
    await expect(
      inspector.getByText('Stopping', { exact: true }),
    ).toBeVisible();
    await expect(
      inspector.getByRole('button', { name: 'Stopping…', exact: true }),
    ).toBeDisabled();
    await expect(inspector.getByRole('alert')).toHaveCount(0);
    await expect(checks).toHaveAttribute('aria-selected', 'true');
    expect(cancelRequests).toBe(2);
    await inspector.screenshot({
      path: testInfo.outputPath('work-stopping.png'),
    });

    run = {
      ...run,
      status: 'cancelled',
      updatedAt: new Date().toISOString(),
      finishedAt: new Date().toISOString(),
    };
    await expect(
      inspector.getByText('Cancelled', { exact: true }),
    ).toBeVisible();
    await expect(checks).toHaveAttribute('aria-selected', 'true');
    await expect(inspector.getByRole('button', { name: /^Stop/ })).toHaveCount(
      0,
    );
    await expect(
      inspector.getByRole('button', { name: 'Ask agent…', exact: true }),
    ).toBeEnabled();
    expect(cancelRequests).toBe(2);
    // Only view state and the cancellation response are fixtures; no model runs.
    expect(
      (await (await member.request.get(`/api/rooms/${room.id}/runs`)).json())
        .runs,
    ).toEqual([]);
  } finally {
    await context.close();
  }
});
