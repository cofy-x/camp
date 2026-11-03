import { createHmac, randomUUID } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';
import type {
  AutomationPolicy,
  SourceEventPage,
  Delivery,
  Subscription,
  Evaluation,
  SourceEvent,
} from '../apps/dashboard/src/features/automation/api';
import type { Task } from '../apps/dashboard/src/features/collaboration/api';
import { createRoom, mutate, signIn, workspace } from './team-helpers';
import { watchPageErrors } from './page-errors';

const panel = (page: Page) =>
  workspace(page).getByRole('region', { name: 'Room automation', exact: true });

async function openActivity(page: Page) {
  await workspace(page)
    .getByRole('button', { name: 'Automation', exact: true })
    .click();
  await expect(
    panel(page).getByRole('tab', { name: 'Activity', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    panel(page).getByLabel('Activity source', { exact: true }),
  ).toBeVisible();
}

async function openAutomation(page: Page) {
  await openAutomationForRecovery(page);
  await expect(
    panel(page).getByRole('button', { name: 'Save Policy', exact: true }),
  ).toBeEnabled();
}

test('policy drafts survive navigation and uncertain saves recover the committed receipt', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Automation recovery ${randomUUID().slice(0, 8)}`,
  );
  await openAutomation(page);
  await expect(
    panel(page).getByRole('radio', { name: 'Off', exact: true }),
  ).toBeChecked();
  const rules =
    'Only prepare a concise proposal when a teammate asks. 保留草稿。';
  await panel(page).getByLabel('Rules', { exact: true }).fill(rules);
  await panel(page)
    .getByRole('radio', { name: 'Observe', exact: true })
    .check();
  await panel(page).getByRole('tab', { name: 'Activity', exact: true }).click();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeHidden();
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    rules,
  );
  await expect(
    panel(page).getByRole('radio', { name: 'Observe', exact: true }),
  ).toBeChecked();
  await panel(page).getByRole('radio', { name: 'Off', exact: true }).check();
  await panel(page)
    .getByRole('button', { name: 'Back to Chat', exact: true })
    .click();
  await expect(panel(page)).toHaveCount(0);
  await openAutomation(page);
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    rules,
  );
  let submissions = 0;
  await page.route(
    `**/api/rooms/${room.id}/automation-policies/room`,
    async (route) => {
      if (route.request().method() !== 'PUT') return route.fallback();
      submissions++;
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      return route.abort('failed');
    },
  );
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toBeVisible();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeDisabled();
  await panel(page).getByRole('tab', { name: 'Activity', exact: true }).click();
  await expect(
    panel(page).getByLabel('Activity source', { exact: true }),
  ).toBeVisible();
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toBeVisible();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeDisabled();
  expect(submissions).toBe(1);
  await errors.reload();
  await openAutomationForRecovery(page);
  await panel(page)
    .getByRole('button', { name: 'Check Saved Operation' })
    .click();
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toHaveCount(0);
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    rules,
  );
  expect(submissions).toBe(1);
  const saved = (await (
    await page.request.get(`/api/rooms/${room.id}/automation-policies`)
  ).json()) as { policies: AutomationPolicy[] };
  expect(saved.policies).toHaveLength(1);
  expect(saved.policies[0]).toMatchObject({
    enabled: false,
    mode: 'observe',
    revision: 1,
    rules,
  });
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/tasks?view=summary`)
      ).json()
    ).tasks,
  ).toEqual([]);
  expect(errors.errors).toEqual([]);
});

test('the latest Workspace navigation wins while Automation saves a private draft', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Automation navigation ${randomUUID().slice(0, 8)}`,
  );
  const obsoleteTaskId = `obsolete-navigation-task-${randomUUID()}`;
  let taskReads = 0;
  await page.route(`**/api/tasks/${obsoleteTaskId}?*`, (route) => {
    taskReads++;
    return route.fulfill({
      status: 404,
      json: { error: 'No task was opened.' },
    });
  });
  await page.route(`**/api/rooms/${room.id}/automation-events?*`, (route) => {
    const event: SourceEvent = {
      seq: 1,
      id: 'fixture-navigation-event',
      roomId: room.id,
      sourceId: 'room',
      threadKey: `${room.id}:room`,
      kind: 'room.message',
      actorId: 'fixture-member',
      actorLogin: 'A teammate',
      actorKind: 'user',
      content: 'Context for the earlier collaboration.',
      occurredAt: '2026-11-03T02:00:00Z',
      status: 'context',
      taskId: obsoleteTaskId,
    };
    return route.fulfill({
      json: { items: [event], through: 1, nextBefore: null, hasMore: false },
    });
  });
  await openAutomation(page);
  let release = () => {};
  let saving = false;
  let held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const draftKey = `automation-policy:${room.id}:room`;
  await page.route('**/api/me/drafts/conversation', async (route) => {
    if (
      route.request().method() !== 'PUT' ||
      (route.request().postDataJSON() as { key: string }).key !== draftKey
    )
      return route.fallback();
    saving = true;
    await held;
    await route.continue();
  });

  async function finishSaving() {
    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        response.url().endsWith('/api/me/drafts/conversation') &&
        (response.request().postDataJSON() as { key: string }).key === draftKey,
    );
    release();
    await (await saved).finished();
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
  }

  try {
    const rules = panel(page).getByLabel('Rules', { exact: true });
    const draft = 'Preserve this private draft while navigation is pending.';
    await rules.fill(draft);
    await panel(page).getByRole('button', { name: 'Back to Chat' }).click();
    await expect.poll(() => saving).toBe(true);
    await workspace(page)
      .getByRole('button', { name: 'Automation', exact: true })
      .click();
    await finishSaving();
    await expect(panel(page)).toBeVisible();
    await expect(
      panel(page).getByRole('tab', { name: 'Settings', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    await expect(rules).toHaveValue(draft);

    saving = false;
    held = new Promise<void>((resolve) => {
      release = resolve;
    });
    await rules.fill(`${draft} Keep the second change too.`);
    await panel(page)
      .getByRole('tab', { name: 'Activity', exact: true })
      .click();
    const events = panel(page).getByRole('region', {
      name: 'Automation events',
    });
    await events.getByText('Details', { exact: true }).click();
    await events.getByRole('button', { name: 'Open Collaboration' }).click();
    await expect.poll(() => saving).toBe(true);
    await workspace(page)
      .getByRole('button', { name: 'Automation', exact: true })
      .click();
    await finishSaving();
    await expect(panel(page)).toBeVisible();
    await expect(
      panel(page).getByRole('tab', { name: 'Activity', exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    expect(taskReads).toBe(0);
    await panel(page)
      .getByRole('tab', { name: 'Settings', exact: true })
      .click();
    await expect(rules).toHaveValue(`${draft} Keep the second change too.`);
    expect(errors.errors).toEqual([]);
  } finally {
    release();
  }
});

async function openAutomationForRecovery(page: Page) {
  await openActivity(page);
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(
    panel(page).getByRole('form', { name: 'Automation policy' }),
  ).toBeVisible();
}

test('a late private draft conflict stays reachable in Settings and blocks leaving until resolved', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Hidden draft recovery ${randomUUID().slice(0, 8)}`,
  );
  const { user } = (await (
    await page.request.get('/api/auth/status')
  ).json()) as { user: { id: string } };
  const key = `automation-policy:${room.id}:room`;
  const path = '/me/drafts/conversation';
  const draftURL = `/api${path}?key=${encodeURIComponent(key)}`;
  const localRules = 'Keep my unsaved local policy draft.';
  const remoteRules = 'The other session saved this private policy draft.';
  const makeDraft = (rules: string) => ({
    revision: 0,
    actors: '',
    values: {
      enabled: false,
      mode: 'observe',
      rules,
      allowedActorIds: [],
      participantAgentIds: [],
      firstAgentId: '',
      maxTurns: 6,
      maxModelRequests: 24,
      dailyEvaluations: 20,
      dailyTasks: 5,
      allowAnswers: false,
      allowComments: false,
    },
  });
  const created = await mutate(
    page,
    path,
    { key, revision: 0, value: makeDraft(remoteRules) },
    'PUT',
  );
  expect(created.ok()).toBe(true);
  const { draft: remote } = (await created.json()) as {
    draft: { revision: number };
  };
  await page.evaluate(
    ({ storageKey, value }) => {
      sessionStorage.setItem(
        storageKey,
        JSON.stringify({ revision: 0, value }),
      );
    },
    {
      storageKey: `camp:private:${user.id}:conversation:${key}`,
      value: makeDraft(localRules),
    },
  );

  let releaseRestore!: () => void;
  const restored = new Promise<void>((resolve) => {
    releaseRestore = resolve;
  });
  let sawRestore!: () => void;
  const restoring = new Promise<void>((resolve) => {
    sawRestore = resolve;
  });
  let held = false;
  let privateWrites = 0;
  await page.route('**/api/me/drafts/conversation*', async (route) => {
    const request = route.request();
    if (request.method() === 'PUT') {
      if ((request.postDataJSON() as { key: string }).key === key)
        privateWrites++;
      return route.fallback();
    }
    if (new URL(request.url()).searchParams.get('key') !== key || held)
      return route.fallback();
    held = true;
    sawRestore();
    await restored;
    return route.continue();
  });
  await openActivity(page);
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await restoring;
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeDisabled();
  await panel(page).getByRole('tab', { name: 'Activity', exact: true }).click();
  await expect(
    panel(page).getByRole('tab', { name: 'Activity', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  releaseRestore();
  const comparison = panel(page).locator('.camp-draft-comparison');
  await expect(comparison).toHaveCount(1);
  await expect(comparison).toBeHidden();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    localRules,
  );
  expect(privateWrites).toBe(0);

  // A blocked writer must still fence leaving Automation. Mounted tabs must
  // remain reachable so the user can resolve the hidden conflict instead.
  await panel(page)
    .getByRole('button', { name: 'Back to Chat', exact: true })
    .click();
  await expect(panel(page)).toBeVisible();
  await expect(
    panel(page).getByRole('tab', { name: 'Activity', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(
    panel(page).getByRole('tab', { name: 'Settings', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(comparison).toBeVisible();
  await expect(comparison.locator('pre')).toContainText(remoteRules);
  await expect(panel(page).getByRole('alert')).toContainText(
    'This private draft changed in another session.',
  );
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeDisabled();
  expect(
    (await (await page.request.get(draftURL)).json()).draft.value.values.rules,
  ).toBe(remoteRules);
  await comparison
    .getByRole('button', { name: 'Save My Draft', exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await (await page.request.get(draftURL)).json()).draft.value.values
          .rules,
    )
    .toBe(localRules);
  expect(
    (await (await page.request.get(draftURL)).json()).draft.revision,
  ).toBeGreaterThan(remote.revision);
  expect(privateWrites).toBe(1);
  await expect(comparison).toHaveCount(0);
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeEnabled();
  await panel(page)
    .getByRole('button', { name: 'Back to Chat', exact: true })
    .click();
  await expect(panel(page)).toHaveCount(0);
  await openAutomation(page);
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    localRules,
  );
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/automation-policies`)
      ).json()
    ).policies,
  ).toEqual([]);
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/tasks?view=summary`)
      ).json()
    ).tasks,
  ).toEqual([]);
  expect(errors.errors).toEqual([]);
});

test('policy conflicts keep local edits and require an explicit new baseline', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Automation conflict ${randomUUID().slice(0, 8)}`,
  );
  await openAutomation(page);
  await panel(page)
    .getByLabel('Rules', { exact: true })
    .fill('My private rule.');
  const saved = await mutate(
    page,
    `/rooms/${room.id}/automation-policies/room`,
    {
      requestId: randomUUID(),
      revision: 0,
      enabled: false,
      mode: 'observe',
      rules: 'Rules saved in another session.',
      allowedActorIds: [],
      participantAgentIds: [],
      firstAgentId: '',
      maxTurns: 6,
      maxModelRequests: 24,
      dailyEvaluations: 20,
      dailyTasks: 5,
      allowAnswers: false,
      allowComments: false,
    },
    'PUT',
  );
  expect(saved.ok()).toBe(true);
  await panel(page).getByRole('button', { name: 'Refresh Policies' }).click();
  await expect(
    panel(page).getByText(
      'The saved policy changed. Your edits are preserved.',
    ),
  ).toBeVisible();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    'My private rule.',
  );
  await expect(
    panel(page).getByRole('button', { name: 'Save Policy', exact: true }),
  ).toBeDisabled();
  await panel(page).getByRole('button', { name: 'Keep My Edits' }).click();
  const before = (await (
    await page.request.get(`/api/rooms/${room.id}/automation-policies`)
  ).json()) as { policies: AutomationPolicy[] };
  expect(before.policies[0].rules).toBe('Rules saved in another session.');
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await expect
    .poll(async () => {
      const result = (await (
        await page.request.get(`/api/rooms/${room.id}/automation-policies`)
      ).json()) as { policies: AutomationPolicy[] };
      return result.policies[0].rules;
    })
    .toBe('My private rule.');
  expect(errors.errors).toEqual([]);
});

test('policy controls retain Off intent, first participant and folded allowance errors', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Policy controls ${randomUUID().slice(0, 8)}`,
  );
  const agents: { id: string; name: string }[] = [];
  for (const suffix of ['One', 'Two']) {
    const created = await mutate(page, `/rooms/${room.id}/agents`, {
      name: `Policy Writer ${suffix}`,
      handle: `policy-${randomUUID().slice(0, 8)}`,
      access: 'write',
      enabled: true,
    });
    expect(created.ok()).toBe(true);
    agents.push((await created.json()).agent);
  }
  const writes: Record<string, unknown>[] = [];
  await page.route(
    `**/api/rooms/${room.id}/automation-policies/room`,
    (route) => {
      if (route.request().method() === 'PUT')
        writes.push(route.request().postDataJSON() as Record<string, unknown>);
      return route.fallback();
    },
  );
  await openAutomation(page);
  await panel(page)
    .getByLabel('Rules', { exact: true })
    .fill('文'.repeat(3000));
  await expect(
    panel(page).getByText('Keep rules within 8192 UTF-8 bytes.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    panel(page).getByRole('button', { name: 'Save Policy', exact: true }),
  ).toBeDisabled();
  expect(writes).toEqual([]);
  await panel(page)
    .getByLabel('Rules', { exact: true })
    .fill('Prepare a concise proposal for an approved request.');
  const allowed = panel(page).getByRole('group', {
    name: 'Allowed members',
    exact: true,
  });
  await allowed.locator('summary').click();
  await allowed
    .getByRole('checkbox', { name: 'Browser Owner', exact: true })
    .check();
  const participants = panel(page).getByRole('group', {
    name: 'Participants (up to 8)',
    exact: true,
  });
  await participants.locator('summary').click();
  await participants
    .getByRole('checkbox', { name: agents[0].name, exact: true })
    .check();
  await expect(
    panel(page).getByLabel('First Agent', { exact: true }),
  ).toHaveCount(0);
  await participants
    .getByRole('checkbox', { name: agents[1].name, exact: true })
    .check();
  await expect(
    panel(page).getByLabel('First Agent', { exact: true }),
  ).toHaveValue(agents[0].id);
  await panel(page)
    .getByLabel('First Agent', { exact: true })
    .selectOption(agents[1].id);
  await participants
    .getByRole('checkbox', { name: agents[1].name, exact: true })
    .uncheck();
  await expect(
    panel(page).getByLabel('First Agent', { exact: true }),
  ).toHaveCount(0);
  await allowed.locator('summary').click();
  await participants.locator('summary').click();
  await panel(page)
    .getByRole('radio', { name: 'Observe', exact: true })
    .check();
  const captures = [];
  for (const [scene, viewport] of [
    ['desktop', { width: 1440, height: 900 }],
    ['narrow', { width: 390, height: 844 }],
  ] as const) {
    await page.setViewportSize(viewport);
    await panel(page).evaluate((element) => {
      element.scrollTop = 0;
    });
    const path = `test-results/automation-${test.info().project.name}-settings-configured-${scene}.png`;
    await workspace(page).screenshot({ path });
    expect(
      await panel(page).evaluate(
        (element) => element.scrollWidth <= element.clientWidth + 1,
      ),
    ).toBe(true);
    captures.push({
      scene,
      path,
      url: page.url(),
      viewport: page.viewportSize(),
      windowBounds: await workspace(page).boundingBox(),
    });
  }
  await writeFile(
    `test-results/automation-${test.info().project.name}-configured-captures.json`,
    JSON.stringify({ synthetic: true, unsaved: true, captures }, null, 2) +
      '\n',
    { mode: 0o600 },
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await panel(page).getByRole('radio', { name: 'Auto', exact: true }).check();
  await panel(page).getByRole('radio', { name: 'Off', exact: true }).check();
  await panel(page).getByText('Adjust allowance', { exact: true }).click();
  await panel(page).getByLabel('Daily assessments', { exact: true }).fill('0');
  await panel(page).getByLabel('Daily tasks', { exact: true }).fill('0');
  await panel(page).getByText('Adjust allowance', { exact: true }).click();
  expect(writes).toEqual([]);
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toMatchObject({
    enabled: false,
    mode: 'auto',
    firstAgentId: agents[0].id,
    participantAgentIds: [agents[0].id],
    dailyEvaluations: 0,
    dailyTasks: 0,
  });
  await expect(
    panel(page).getByText('Unsaved policy', { exact: true }),
  ).toHaveCount(0);
  await panel(page)
    .getByRole('radio', { name: 'Observe', exact: true })
    .check();
  await expect(
    panel(page).getByText('Use 1–1000 assessments per day.', { exact: true }),
  ).toBeVisible();
  await expect(
    panel(page).getByLabel('Daily assessments', { exact: true }),
  ).toBeHidden();
  await expect(
    panel(page).getByRole('button', { name: 'Save Policy', exact: true }),
  ).toBeDisabled();
  expect(writes).toHaveLength(1);
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/tasks?view=summary`)
      ).json()
    ).tasks,
  ).toEqual([]);
  expect(errors.errors).toEqual([]);
});

test('recent activity pins older pages, escapes original text and fits narrow windows', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Automation history ${randomUUID().slice(0, 8)}`,
  );
  const requests: URL[] = [];
  const untrusted =
    '<script>window.sourceExecuted = true</script>\nKeep all original context.';
  let newest = 40;
  let decisionReads = 0;
  await page.route(
    '**/api/automation-evaluations/fixture-evaluation',
    (route) => {
      decisionReads++;
      const evaluation: Evaluation = {
        id: 'fixture-evaluation',
        status: 'observed',
        policy: {
          id: 'fixture-policy',
          roomId: room.id,
          sourceId: 'room',
          revision: 3,
          ownerUserId: 'member-7',
          enabled: true,
          mode: 'observe',
          rules: 'Prepare a concise reviewable proposal for a clear request.',
          allowedActorIds: ['external-7'],
          participantAgentIds: ['agent-7'],
          firstAgentId: 'agent-7',
          maxTurns: 6,
          maxModelRequests: 24,
          dailyEvaluations: 20,
          dailyTasks: 5,
          allowAnswers: false,
          allowComments: false,
          credentialConfigured: false,
          startAfter: 0,
        },
        events: [],
        tasks: [],
        totalTasks: 0,
        tasksOmitted: false,
        modelRevision: 'fixture-model',
        assessment: {
          outcome: 'accept',
          goal: 'Prepare a reviewable proposal.',
          reason: 'The message asks for a proposal covered by the rule.',
        },
        createdAt: '2026-11-03T02:00:00Z',
      };
      return route.fulfill({ json: evaluation });
    },
  );
  const event = (seq: number, values: Partial<SourceEvent>): SourceEvent => ({
    seq,
    id: `fixture-event-${seq}`,
    roomId: room.id,
    sourceId: 'room',
    threadKey: `${room.id}:room`,
    kind: 'room.message',
    actorId: 'external-7',
    actorLogin: 'A teammate',
    actorKind: 'human',
    content: 'A synthetic discussion event.',
    occurredAt: '2026-11-03T02:00:00Z',
    status: 'ignored',
    ...values,
  });
  let olderRequests = 0;
  let olderAvailable = false;
  await page.route(`**/api/rooms/${room.id}/automation-events?*`, (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    expect(url.searchParams.get('direction')).toBe('desc');
    const older = url.searchParams.has('before');
    if (older) {
      olderRequests++;
      expect(url.searchParams.get('before')).toBe('38');
      expect(url.searchParams.get('through')).toBe('40');
    }
    if (older && !olderAvailable)
      return route.fulfill({
        status: 503,
        json: {
          error: 'Earlier events are temporarily unavailable.',
          code: 'unavailable',
        },
      });
    const through = Number(url.searchParams.get('through') ?? newest);
    const response: SourceEventPage = {
      items: older
        ? [event(20, { preview: 'An earlier discussion.', content: untrusted })]
        : [
            ...(through > 40
              ? [event(41, { preview: 'A newly arrived request.' })]
              : []),
            event(40, {
              sourceId: 'fixture-github',
              externalEventId: 'fixture-original-event',
              repository: 'camp-example/acceptance',
              number: 42,
              kind: 'issues.opened',
              actorLogin: 'github-teammate',
              preview: 'Prepare the onboarding proposal.',
              status: 'needs_attention',
              assessmentReason: 'Review the source rule before continuing.',
            }),
            event(39, {
              preview: 'Can you prepare a concise proposal?',
              content: untrusted,
              status: 'observed',
              assessmentOutcome: 'accept',
              assessmentReason: 'The request is covered by the saved rule.',
              evaluationId: 'fixture-evaluation',
            }),
            event(38, {
              sourceId: 'task',
              kind: 'task.cancel',
              preview: 'Cancelled the earlier draft.',
              status: 'stopped',
              content: '',
            }),
          ],
      through,
      nextAfter: null,
      nextBefore: older ? null : 38,
      hasMore: !older,
    };
    return route.fulfill({ json: response });
  });
  await openActivity(page);
  await expect(
    workspace(page).getByRole('button', { name: 'GitHub', exact: true }),
  ).toHaveCount(0);
  await expect(
    panel(page).getByRole('form', { name: 'Automation policy' }),
  ).toHaveCount(0);
  const events = panel(page).getByRole('region', { name: 'Automation events' });
  const titles = events.locator('.automation-event-title');
  await expect(titles).toHaveText([
    'Prepare the onboarding proposal.',
    'Can you prepare a concise proposal?',
    'Cancelled the earlier draft.',
  ]);
  await expect(
    events.getByText('Would start a collaboration', { exact: true }),
  ).toBeVisible();
  await expect(
    events.getByText('Collaboration stopped', { exact: true }),
  ).toBeVisible();
  expect(decisionReads).toBe(0);
  const proposal = events
    .locator('li')
    .filter({ hasText: 'Can you prepare a concise proposal?' });
  await proposal.getByText('Details', { exact: true }).click();
  await expect(proposal.locator('pre')).toHaveText(untrusted);
  expect(await page.evaluate(() => 'sourceExecuted' in window)).toBe(false);
  await proposal.getByText('Assessment decision', { exact: true }).click();
  await expect(
    proposal.getByText('Prepare a reviewable proposal.', { exact: true }),
  ).toBeVisible();
  expect(decisionReads).toBeGreaterThanOrEqual(1);
  await proposal.getByText('Details', { exact: true }).click();

  newest = 41;
  await events.getByRole('button', { name: 'Older Events' }).click();
  await expect(events.getByRole('alert')).toContainText(
    'Earlier events are temporarily unavailable.',
  );
  await expect(titles).toHaveCount(0);
  expect(requests.at(-1)?.searchParams.get('before')).toBe('38');
  expect(requests.at(-1)?.searchParams.get('through')).toBe('40');
  await events.getByRole('button', { name: 'Newer Events' }).click();
  await expect(titles).toHaveCount(3);
  expect(requests.at(-1)?.searchParams.get('through')).toBe('40');
  await expect(
    events.getByText('A newly arrived request.', { exact: true }),
  ).toHaveCount(0);
  await events.getByRole('button', { name: 'Older Events' }).click();
  await expect(events.getByRole('alert')).toContainText(
    'Earlier events are temporarily unavailable.',
  );
  olderAvailable = true;
  await events
    .getByRole('button', { name: 'Retry Events', exact: true })
    .click();
  await expect(titles).toHaveText('An earlier discussion.');
  await expect(events.getByRole('alert')).toHaveCount(0);
  expect(olderRequests).toBeGreaterThanOrEqual(2);
  expect(requests.at(-1)?.searchParams.get('before')).toBe('38');
  expect(requests.at(-1)?.searchParams.get('through')).toBe('40');
  await events.getByRole('button', { name: 'Newer Events' }).click();
  await expect(titles).toHaveCount(3);
  expect(requests.at(-1)?.searchParams.get('through')).toBe('40');
  await expect(
    events.getByText('A newly arrived request.', { exact: true }),
  ).toHaveCount(0);
  await events.getByRole('button', { name: 'Refresh Events' }).click();
  await expect(titles).toHaveCount(4);
  await expect(titles.first()).toHaveText('A newly arrived request.');
  await expect(
    events.getByRole('button', { name: 'Newer Events' }),
  ).toHaveCount(0);

  const captures: Record<string, unknown>[] = [];

  async function capture(scene: string) {
    await panel(page).evaluate((element) => {
      element.scrollTop = 0;
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    expect(
      await panel(page).evaluate(
        (element) => element.scrollWidth <= element.clientWidth + 1,
      ),
    ).toBe(true);
    const path = `test-results/automation-${test.info().project.name}-${scene}.png`;
    await workspace(page).screenshot({ path });
    captures.push({
      scene,
      path,
      url: page.url(),
      viewport: page.viewportSize(),
      windowBounds: await workspace(page).boundingBox(),
    });
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await capture('activity-desktop');
  await page.setViewportSize({ width: 390, height: 844 });
  await capture('activity-narrow');
  await panel(page).getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(
    panel(page).getByRole('radio', { name: 'Off', exact: true }),
  ).toBeChecked();
  await panel(page)
    .getByLabel('Rules', { exact: true })
    .fill('Prepare a concise proposal when an approved teammate requests one.');
  await capture('settings-narrow');
  await page.setViewportSize({ width: 1440, height: 900 });
  await capture('settings-desktop');
  await writeFile(
    `test-results/automation-${test.info().project.name}-captures.json`,
    JSON.stringify({ synthetic: true, captures }, null, 2) + '\n',
    { mode: 0o600 },
  );
  expect(errors.errors).toEqual([]);
});

test('activity source filters reach storage and new events do not shift older pages', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Activity filtering ${randomUUID().slice(0, 8)}`,
  );
  const secret = 'private-synthetic-activity-filter-secret';
  const created = await mutate(page, `/rooms/${room.id}/github-sources`, {
    requestId: randomUUID(),
    repository: 'camp-example/activity-filter',
    secret,
  });
  expect(created.ok()).toBe(true);
  const { source } = (await created.json()) as { source: { id: string } };
  for (let index = 1; index <= 21; index++) {
    const posted = await mutate(page, `/rooms/${room.id}/messages`, {
      requestId: randomUUID(),
      content: `Discussion ${String(index).padStart(2, '0')}`,
      postOnly: true,
    });
    expect(posted.ok()).toBe(true);
  }
  const original = JSON.stringify({
    action: 'opened',
    repository: { id: 124, full_name: 'camp-example/activity-filter' },
    sender: { id: 7, login: 'github-teammate', type: 'User' },
    issue: {
      id: 991,
      number: 42,
      title: 'Newest GitHub observation',
      body: 'Read without starting work.',
    },
  });
  const ingress = await fetch(
    new URL(`/api/hooks/github/${source.id}`, page.url()),
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-GitHub-Event': 'issues',
        'X-GitHub-Delivery': randomUUID(),
        'X-Hub-Signature-256': `sha256=${createHmac('sha256', secret).update(original).digest('hex')}`,
      },
      body: original,
    },
  );
  expect(ingress.ok).toBe(true);
  await openActivity(page);
  const events = panel(page).getByRole('region', { name: 'Automation events' });
  const titles = events.locator('.automation-event-title');
  await expect(titles).toHaveCount(20);
  await expect(titles.first()).toContainText('Newest GitHub observation');
  await panel(page)
    .getByLabel('Activity source', { exact: true })
    .selectOption('room');
  await expect(titles).toHaveCount(20);
  await expect(titles.first()).toHaveText('Discussion 21');
  await expect(titles.last()).toHaveText('Discussion 02');
  await events.getByRole('button', { name: 'Older Events' }).click();
  await expect(titles).toHaveText('Discussion 01');
  const later = await mutate(page, `/rooms/${room.id}/messages`, {
    requestId: randomUUID(),
    content: 'Discussion after snapshot',
    postOnly: true,
  });
  expect(later.ok()).toBe(true);
  await expect(titles).toHaveText('Discussion 01');
  await events.getByRole('button', { name: 'Newer Events' }).click();
  await expect(titles.first()).toHaveText('Discussion 21');
  await expect(titles).toHaveCount(20);
  await events.getByRole('button', { name: 'Refresh Events' }).click();
  await expect(titles.first()).toHaveText('Discussion after snapshot');
  await expect(
    events.getByRole('button', { name: 'Newer Events' }),
  ).toHaveCount(0);
  await panel(page)
    .getByLabel('Activity source', { exact: true })
    .selectOption('task');
  await expect(
    events.getByText('No activity yet.', { exact: true }),
  ).toBeVisible();
  await panel(page)
    .getByLabel('Activity source', { exact: true })
    .selectOption(source.id);
  await expect(titles).toHaveCount(1);
  await expect(titles.first()).toContainText('Newest GitHub observation');
  await events.getByText('Details', { exact: true }).click();
  await events
    .getByRole('button', { name: 'Review GitHub Event', exact: true })
    .click();
  const inbox = panel(page).getByRole('region', {
    name: 'GitHub events',
    exact: true,
  });
  await expect(
    inbox.getByRole('heading', {
      name: 'Newest GitHub observation',
      exact: true,
    }),
  ).toBeVisible();
  await inbox
    .getByRole('button', { name: 'Back to Events', exact: true })
    .click();
  await inbox
    .getByRole('button', { name: 'Back to Activity', exact: true })
    .click();
  await expect(
    panel(page).getByLabel('Activity source', { exact: true }),
  ).toHaveValue(source.id);
  await expect(titles).toHaveCount(1);
  await panel(page)
    .getByLabel('Activity source', { exact: true })
    .selectOption('');
  await expect(titles.first()).toHaveText('Discussion after snapshot');
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/tasks?view=summary`)
      ).json()
    ).tasks,
  ).toEqual([]);
  expect(errors.errors).toEqual([]);
});

test('comment credential recovery keeps the synthetic token out of browser storage', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Comment credential ${randomUUID().slice(0, 8)}`,
  );
  const created = await mutate(page, `/rooms/${room.id}/github-sources`, {
    requestId: randomUUID(),
    repository: 'camp-example/automation',
    secret: 'private-synthetic-browser-webhook-secret',
  });
  expect(created.ok()).toBe(true);
  const { source } = (await created.json()) as { source: { id: string } };
  const configured = await mutate(
    page,
    `/rooms/${room.id}/automation-policies/${source.id}`,
    {
      requestId: randomUUID(),
      revision: 0,
      enabled: false,
      mode: 'observe',
      rules: '',
      allowedActorIds: [],
      participantAgentIds: [],
      firstAgentId: '',
      maxTurns: 6,
      maxModelRequests: 24,
      dailyEvaluations: 20,
      dailyTasks: 5,
      allowAnswers: false,
      allowComments: false,
    },
    'PUT',
  );
  expect(configured.ok()).toBe(true);
  await openAutomation(page);
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption(source.id);
  await panel(page)
    .getByText('Comment credential · Not configured', { exact: true })
    .click();
  let submissions = 0;
  await page.route(
    `**/api/github-sources/${source.id}/comment-credential`,
    async (route) => {
      submissions++;
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      return route.abort('failed');
    },
  );
  // This keyless Server stores only a synthetic credential under a disabled
  // policy. No GitHub request or real credential is used by this scenario.
  const token = 'camp-synthetic-browser-comment-token-only';
  await panel(page).getByLabel('GitHub account ID', { exact: true }).fill('7');
  await panel(page)
    .getByLabel('GitHub comment token', { exact: true })
    .fill(token);
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption('room');
  await expect(
    panel(page).getByLabel('Event source', { exact: true }),
  ).toHaveValue(source.id);
  await expect(
    panel(page).getByText(
      'Save or clear these credential edits before leaving.',
    ),
  ).toBeVisible();
  await panel(page).getByRole('tab', { name: 'Activity', exact: true }).click();
  await panel(page).getByRole('button', { name: 'Back to Chat' }).click();
  await expect(
    panel(page).getByRole('tab', { name: 'Settings', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await expect(
    panel(page).getByLabel('GitHub comment token', { exact: true }),
  ).toHaveValue(token);
  expect(submissions).toBe(0);
  await panel(page)
    .getByRole('button', { name: 'Clear Credential Edits', exact: true })
    .click();
  await expect(
    panel(page).getByLabel('GitHub comment token', { exact: true }),
  ).toHaveValue('');
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption('room');
  await expect(
    panel(page).getByLabel('Event source', { exact: true }),
  ).toHaveValue('room');
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption(source.id);
  await panel(page)
    .getByText('Comment credential · Not configured', { exact: true })
    .click();
  await panel(page).getByLabel('GitHub account ID', { exact: true }).fill('7');
  await panel(page)
    .getByLabel('GitHub comment token', { exact: true })
    .fill(token);
  await panel(page)
    .getByRole('button', { name: 'Save Comment Credential' })
    .click();
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toBeVisible();
  expect(
    await page.evaluate(() =>
      JSON.stringify({
        local: { ...localStorage },
        session: { ...sessionStorage },
      }),
    ),
  ).not.toContain(token);
  await errors.reload();
  await openAutomationForRecovery(page);
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption(source.id);
  await panel(page)
    .getByText('Comment credential · Configured', { exact: true })
    .click();
  await expect(
    panel(page).getByLabel('GitHub comment token', { exact: true }),
  ).toHaveValue('');
  await panel(page)
    .getByRole('button', { name: 'Check Saved Operation' })
    .click();
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toHaveCount(0);
  expect(submissions).toBe(1);
  expect(errors.errors).toEqual([]);
});

test('following an event and recovering an unknown comment retain their explicit targets', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Follow and reconcile ${randomUUID().slice(0, 8)}`,
  );
  const { user } = (await (
    await page.request.get('/api/auth/status')
  ).json()) as { user: { id: string } };
  const task: Task = {
    id: `fixture-task-${randomUUID()}`,
    roomId: room.id,
    resourceId: room.resourceId,
    requestedByUserId: user.id,
    threadRootId: '',
    originMessageId: '',
    goal: 'Keep the proposal current.',
    status: 'running',
    revision: 1,
    participants: [],
    currentAgentId: '',
    activeRunId: '',
    baseSharedCommit: '',
    checkpointCommit: '',
    publishedCommit: '',
    error: '',
    summary: '',
    turnsUsed: 1,
    maxTurns: 6,
    modelRequestsUsed: 1,
    maxModelRequests: 24,
    inputRevision: 0,
    inputBytes: 0,
    coveredInputRevision: 0,
    createdAt: '2026-11-03T02:00:00Z',
    updatedAt: '2026-11-03T02:00:00Z',
  };
  const event = {
    seq: 1,
    id: 'fixture-room-event',
    roomId: room.id,
    sourceId: 'room',
    threadKey: `${room.id}:room`,
    kind: 'room.message',
    actorId: user.id,
    actorLogin: 'Browser Owner',
    actorKind: 'user',
    content: 'A separate discussion for this proposal.',
    occurredAt: '2026-11-03T02:00:00Z',
    status: 'context',
    taskId: task.id,
  };
  let subscriptions: Subscription[] = [];
  let subscriptionWrites = 0;
  let delivery: Delivery = {
    id: `fixture-delivery-${randomUUID()}`,
    taskId: task.id,
    sourceId: 'fixture-github',
    policyId: 'fixture-policy',
    revision: 1,
    repository: 'camp-example/automation',
    repositoryId: 7,
    number: 42,
    body: 'The proposal is ready for your review.',
    marker: 'fixture-marker',
    status: 'unknown',
    reason: 'The response was lost.',
    createdAt: '2026-11-03T02:00:00Z',
  };
  let reconciliations = 0;
  let resends = 0;
  let recoveryId = '';
  let commentsAvailable = false;
  const withdrawn: Delivery = {
    ...delivery,
    id: `${delivery.id}-withdrawn`,
    status: 'withdrawn',
    reason: 'No further comment was sent.',
  };
  let failed: Delivery = {
    ...delivery,
    id: `${delivery.id}-failed`,
    status: 'failed',
    reason: 'Repair comment permissions before retrying.',
  };
  let repairedRetries = 0;
  let withdrawnUnknown: Delivery = {
    ...withdrawn,
    id: `${delivery.id}-withdrawn-unknown`,
    number: 43,
    withdrawn: true,
    attempts: [
      {
        revision: 1,
        actorId: 7,
        startedAt: '2026-11-03T02:00:00Z',
        outcome: 'unknown',
      },
      {
        revision: 3,
        actorId: 8,
        startedAt: '2026-11-03T02:02:00Z',
        outcome: 'sent',
      },
      { actorId: 7, outcome: 'unknown' },
    ],
    receiptScanLimited: true,
    receipts: [
      {
        commentId: 101,
        actorId: 7,
        url: 'https://github.com/camp-example/automation/issues/43#issuecomment-101',
      },
      {
        commentId: 102,
        actorId: 8,
        url: 'https://github.com/camp-example/automation/issues/43#issuecomment-102',
      },
    ],
  };
  let withdrawnChecks = 0;
  const withdrawnLegacy: Delivery = {
    ...withdrawn,
    id: `${delivery.id}-withdrawn-legacy`,
    number: 44,
    status: 'unknown',
    withdrawn: true,
  };
  // Browser-only task/outbox fixtures. Storage and channel tests establish
  // actual admission, target validation and external delivery behavior.
  await page.route(`**/api/rooms/${room.id}/tasks?*`, (route) =>
    route.fulfill({ json: { tasks: [task] } }),
  );
  await page.route(`**/api/tasks/${task.id}?*`, (route) =>
    route.fulfill({ json: { task } }),
  );
  await page.route(`**/api/rooms/${room.id}/automation-events?*`, (route) =>
    route.fulfill({
      json: { items: [event], through: 1, nextAfter: null, hasMore: false },
    }),
  );
  await page.route(`**/api/tasks/${task.id}/subscriptions`, (route) => {
    if (route.request().method() === 'GET')
      return route.fulfill({ json: { subscriptions } });
    subscriptionWrites++;
    const input = route.request().postDataJSON() as Record<string, unknown>;
    expect(input).toMatchObject({
      sourceId: 'room',
      threadKey: event.threadKey,
      enabled: true,
      revision: 0,
    });
    const subscription: Subscription = {
      id: 'fixture-subscription',
      taskId: task.id,
      sourceId: 'room',
      threadKey: event.threadKey,
      enabled: true,
      revision: 1,
    };
    subscriptions = [subscription];
    return route.fulfill({ json: { subscription } });
  });
  await page.route(`**/api/tasks/${task.id}/deliveries?*`, (route) =>
    commentsAvailable
      ? route.fulfill({
          json: {
            deliveries: [
              delivery,
              withdrawn,
              failed,
              withdrawnUnknown,
              withdrawnLegacy,
            ],
            nextCursor: '',
          },
        })
      : route.fulfill({
          status: 503,
          json: {
            error: 'Comments are temporarily unavailable.',
            code: 'unavailable',
          },
        }),
  );
  await page.route(
    `**/api/automation-deliveries/${withdrawnUnknown.id}/*`,
    (route) => {
      expect(route.request().url()).toMatch(/\/reconcile$/);
      withdrawnChecks++;
      withdrawnUnknown = {
        ...withdrawnUnknown,
        revision: withdrawnUnknown.revision + 1,
        status: 'sending',
      };
      return route.fulfill({ json: { delivery: withdrawnUnknown } });
    },
  );
  await page.route(
    `**/api/automation-deliveries/${failed.id}/resend`,
    (route) => {
      repairedRetries++;
      failed = { ...failed, revision: 2, status: 'pending', reason: '' };
      return route.fulfill({ json: { delivery: failed } });
    },
  );
  await page.route(
    `**/api/automation-deliveries/${delivery.id}/*`,
    async (route) => {
      const input = route.request().postDataJSON() as {
        requestId: string;
        revision: number;
      };
      if (route.request().url().endsWith('/reconcile')) {
        reconciliations++;
        recoveryId = input.requestId;
        delivery = {
          ...delivery,
          revision: 2,
          reason: 'The comment was not found; delivery remains unknown.',
        };
        return route.abort('failed');
      }
      resends++;
      delivery = { ...delivery, revision: 3, status: 'pending', reason: '' };
      return route.fulfill({ json: { delivery } });
    },
  );
  await page.route('**/api/automation-requests/*', (route) => {
    expect(route.request().url()).toContain(recoveryId);
    return route.fulfill({ json: { kind: 'delivery', delivery } });
  });
  await openActivity(page);
  const events = panel(page).getByRole('region', { name: 'Automation events' });
  await events.getByText('Details', { exact: true }).click();
  await events
    .getByRole('button', { name: 'Follow in Collaboration…' })
    .click();
  await events
    .getByLabel('Collaboration', { exact: true })
    .selectOption(task.id);
  await events
    .getByRole('button', { name: 'Follow Conversation', exact: true })
    .click();
  await expect(events.getByRole('status')).toHaveText(
    `Conversation followed in ${task.goal}.`,
  );
  expect(subscriptionWrites).toBe(1);
  await events.getByRole('button', { name: 'Open Collaboration' }).click();
  await workspace(page).getByRole('tab', { name: 'Task setup' }).click();
  const comments = workspace(page).getByRole('region', {
    name: 'GitHub comments',
  });
  await expect(
    workspace(page).getByRole('checkbox', {
      name: 'Follow conversation',
      exact: true,
    }),
  ).toBeChecked();
  await expect(comments.getByRole('alert')).toContainText(
    'Comments are temporarily unavailable.',
  );
  commentsAvailable = true;
  await comments.getByRole('button', { name: 'Retry Comments' }).click();
  await expect(
    comments.getByText('Delivery not confirmed', { exact: true }),
  ).toBeVisible();
  const rows = comments.locator('.automation-deliveries > li');
  const pendingRow = rows.nth(0);
  const withdrawnRow = rows.nth(1);
  await expect(
    withdrawnRow.getByRole('button', {
      name: /Retry|Resend|Check GitHub Delivery/,
    }),
  ).toHaveCount(0);
  const legacyWithdrawal = rows.nth(4);
  await expect(
    legacyWithdrawal.getByText('An earlier send is still unconfirmed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    legacyWithdrawal.getByRole('button', { name: 'Check GitHub Delivery' }),
  ).toBeEnabled();
  await expect(
    legacyWithdrawal.getByRole('button', { name: /Retry|Resend/ }),
  ).toHaveCount(0);
  const unknownWithdrawal = rows.nth(3);
  await expect(
    unknownWithdrawal.getByText('An earlier send is still unconfirmed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    unknownWithdrawal.getByRole('button', { name: /Retry|Resend/ }),
  ).toHaveCount(0);
  await unknownWithdrawal
    .getByText('Delivery details', { exact: true })
    .click();
  await expect(
    unknownWithdrawal.getByText('GitHub account 7', { exact: false }).first(),
  ).toBeVisible();
  await expect(
    unknownWithdrawal.getByRole('link', { name: 'Comment 101', exact: true }),
  ).toHaveAttribute('href', withdrawnUnknown.receipts![0].url);
  await expect(
    unknownWithdrawal.getByRole('link', { name: 'Comment 102', exact: true }),
  ).toHaveAttribute('href', withdrawnUnknown.receipts![1].url);
  await expect(
    unknownWithdrawal.getByText('Delivery not confirmed', { exact: false }),
  ).toHaveCount(2);
  await expect(
    unknownWithdrawal.getByText('Time not recorded', { exact: false }),
  ).toBeVisible();
  await expect(
    unknownWithdrawal.getByText('Some matching comments may not be listed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(unknownWithdrawal).not.toContainText('Invalid Date');
  await unknownWithdrawal
    .getByRole('button', { name: 'Check GitHub Delivery' })
    .click();
  await expect(
    unknownWithdrawal.getByText('Checking GitHub delivery', { exact: true }),
  ).toBeVisible();
  await expect(
    unknownWithdrawal.getByText('An earlier send is still unconfirmed.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    unknownWithdrawal.getByRole('button', {
      name: /Retry|Resend|Check GitHub Delivery/,
    }),
  ).toHaveCount(0);
  expect(withdrawnChecks).toBe(1);
  await pendingRow
    .getByRole('button', { name: 'Check GitHub Delivery' })
    .click();
  await expect(
    pendingRow.getByRole('button', { name: 'Check Saved Operation' }),
  ).toBeVisible();
  await expect(
    pendingRow.getByRole('button', { name: 'Resend Comment…' }),
  ).toBeDisabled();
  await pendingRow
    .getByRole('button', { name: 'Check Saved Operation' })
    .click();
  await expect(
    pendingRow.getByRole('button', { name: 'Check Saved Operation' }),
  ).toHaveCount(0);
  expect(reconciliations).toBe(1);
  await pendingRow.getByRole('button', { name: 'Resend Comment…' }).click();
  await expect(page.getByRole('dialog')).toContainText(
    'this can create a duplicate',
  );
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect(resends).toBe(0);
  await pendingRow.getByRole('button', { name: 'Resend Comment…' }).click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Resend Comment', exact: true })
    .click();
  await expect(
    comments.getByText('Waiting to send', { exact: true }),
  ).toBeVisible();
  expect(resends).toBe(1);
  await comments
    .getByRole('button', { name: 'Retry Comment', exact: true })
    .click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(repairedRetries).toBe(1);
  expect(errors.errors).toEqual([]);
});

test('unavailable receipt storage sends nothing and rapid submission keeps one write', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Receipt storage ${randomUUID().slice(0, 8)}`,
  );
  await openAutomation(page);
  await panel(page)
    .getByLabel('Rules', { exact: true })
    .fill('Keep this draft when browser receipt storage fails.');
  let submissions = 0;
  await page.route(
    `**/api/rooms/${room.id}/automation-policies/room`,
    (route) => {
      submissions++;
      return route.fallback();
    },
  );
  await page.evaluate(() => {
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (key.includes(':automation-operation:'))
        throw new DOMException(
          'Synthetic receipt storage failure',
          'QuotaExceededError',
        );
      return original.call(this, key, value);
    };
    Object.assign(window, {
      restoreAutomationReceiptStorage: () => {
        Storage.prototype.setItem = original;
      },
    });
  });
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await expect(panel(page).getByRole('alert')).toContainText(
    'Camp could not retain this request',
  );
  await expect(
    panel(page).getByRole('button', { name: 'Check Saved Operation' }),
  ).toHaveCount(0);
  await expect(panel(page).getByLabel('Rules', { exact: true })).toBeEnabled();
  expect(submissions).toBe(0);
  await page.evaluate(() =>
    (
      window as unknown as { restoreAutomationReceiptStorage: () => void }
    ).restoreAutomationReceiptStorage(),
  );
  await panel(page)
    .getByRole('form', { name: 'Automation policy' })
    .evaluate((form) => {
      (form as HTMLFormElement).requestSubmit();
      (form as HTMLFormElement).requestSubmit();
    });
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(`/api/rooms/${room.id}/automation-policies`)
          ).json()
        ).policies.length,
    )
    .toBe(1);
  expect(submissions).toBe(1);
  expect(errors.errors).toEqual([]);
});

test('cancelling an unconfirmed operation preserves its draft and fences a late write', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Cancel unconfirmed ${randomUUID().slice(0, 8)}`,
  );
  await openAutomation(page);
  const rules = 'Preserve these edits after cancellation.';
  await panel(page).getByLabel('Rules', { exact: true }).fill(rules);
  const path = `/rooms/${room.id}/automation-policies/room`;
  let original: Record<string, unknown> = {};
  const pattern = `**/api${path}`;
  await page.route(pattern, (route) => {
    original = route.request().postDataJSON() as Record<string, unknown>;
    return route.abort('failed');
  });
  let cancellations = 0;
  await page.route('**/api/automation-requests/*/cancel', async (route) => {
    cancellations++;
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    return route.abort('failed');
  });
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await panel(page)
    .getByRole('button', { name: 'Check Saved Operation' })
    .click();
  await panel(page)
    .getByRole('button', { name: 'Cancel Unconfirmed Operation' })
    .click();
  await panel(page)
    .getByRole('button', { name: 'Check Saved Operation' })
    .click();
  await expect(
    panel(page).getByText('Operation cancelled.', { exact: true }),
  ).toBeVisible();
  await expect(panel(page).getByLabel('Rules', { exact: true })).toHaveValue(
    rules,
  );
  await expect(
    panel(page).getByRole('button', { name: 'Save Policy', exact: true }),
  ).toBeEnabled();
  expect(cancellations).toBe(1);
  const late = await mutate(page, path, original, 'PUT');
  expect(late.status()).toBe(409);
  expect((await late.json()).code).toBe('request_cancelled');
  expect(
    (
      await (
        await page.request.get(`/api/rooms/${room.id}/automation-policies`)
      ).json()
    ).policies,
  ).toEqual([]);
  await page.unroute(pattern);
  await panel(page)
    .getByRole('button', { name: 'Save Policy', exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(`/api/rooms/${room.id}/automation-policies`)
          ).json()
        ).policies[0]?.rules,
    )
    .toBe(rules);
  expect(errors.errors).toEqual([]);
});

test('credential edits retain their policy baseline until the newer policy is reviewed', async ({
  page,
  browserName,
}) => {
  const errors = watchPageErrors(page, browserName);
  await signIn(page);
  const room = await createRoom(
    page,
    `Credential baseline ${randomUUID().slice(0, 8)}`,
  );
  const created = await mutate(page, `/rooms/${room.id}/github-sources`, {
    requestId: randomUUID(),
    repository: 'camp-example/credential-review',
    secret: 'private-synthetic-credential-review-secret',
  });
  expect(created.ok()).toBe(true);
  const { source } = (await created.json()) as { source: { id: string } };
  const input = {
    requestId: randomUUID(),
    revision: 0,
    enabled: false,
    mode: 'observe',
    rules: 'Original saved rules.',
    allowedActorIds: [],
    participantAgentIds: [],
    firstAgentId: '',
    maxTurns: 6,
    maxModelRequests: 24,
    dailyEvaluations: 20,
    dailyTasks: 5,
    allowAnswers: false,
    allowComments: false,
  };
  expect(
    (
      await mutate(
        page,
        `/rooms/${room.id}/automation-policies/${source.id}`,
        input,
        'PUT',
      )
    ).ok(),
  ).toBe(true);
  await openAutomation(page);
  await panel(page)
    .getByLabel('Event source', { exact: true })
    .selectOption(source.id);
  await panel(page)
    .getByText('Comment credential · Not configured', { exact: true })
    .click();
  const token = 'camp-synthetic-reviewed-comment-token-only';
  await panel(page).getByLabel('GitHub account ID', { exact: true }).fill('7');
  await panel(page)
    .getByLabel('GitHub comment token', { exact: true })
    .fill(token);
  expect(
    (
      await mutate(
        page,
        `/rooms/${room.id}/automation-policies/${source.id}`,
        {
          ...input,
          requestId: randomUUID(),
          revision: 1,
          rules: 'A newer saved policy.',
        },
        'PUT',
      )
    ).ok(),
  ).toBe(true);
  await panel(page).getByRole('button', { name: 'Refresh Policies' }).click();
  await expect(
    panel(page).getByText(
      'The policy changed while you edited this credential. Review the saved policy before continuing.',
    ),
  ).toBeVisible();
  await expect(
    panel(page).getByLabel('GitHub comment token', { exact: true }),
  ).toHaveValue(token);
  await expect(
    panel(page).getByRole('button', { name: 'Save Comment Credential' }),
  ).toBeDisabled();
  let submittedRevision = 0;
  await page.route(
    `**/api/github-sources/${source.id}/comment-credential`,
    (route) => {
      submittedRevision = (
        route.request().postDataJSON() as { revision: number }
      ).revision;
      return route.fallback();
    },
  );
  await panel(page).getByRole('button', { name: 'Use Current Policy' }).click();
  expect(submittedRevision).toBe(0);
  await panel(page)
    .getByRole('button', { name: 'Save Comment Credential' })
    .click();
  await expect(
    panel(page).getByLabel('GitHub comment token', { exact: true }),
  ).toHaveValue('');
  expect(submittedRevision).toBe(2);
  expect(errors.errors).toEqual([]);
});
