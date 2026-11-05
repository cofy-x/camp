import { expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';

export class DemoError extends Error {}

export function check(value, message) {
  if (!value) throw new DemoError(message);
}

export async function openDemoApp(page, name) {
  await page
    .getByRole('menubar')
    .getByRole('menuitem', { name: 'Desktop', exact: true })
    .click();
  await page
    .getByRole('menu')
    .getByRole('menuitem', { name, exact: true })
    .click();
}

// Read the value's position, then use the visible shared pop-up. Names need
// not be unique, and choosing an option must not bypass the product control.
export async function selectDemoValue(page, scope, name, value) {
  const picker = scope.getByRole('combobox', { name, exact: true });
  await expect(picker).toBeEnabled();
  await expect
    .poll(() =>
      picker.evaluate(
        (select, desired) =>
          [...select.options].filter(
            (option) => option.value === desired && !option.disabled,
          ).length,
        value,
      ),
    )
    .toBe(1);
  const index = await picker.evaluate(
    (select, desired) =>
      [...select.options].findIndex((option) => option.value === desired),
    value,
  );
  await picker.click();
  await page
    .getByRole('listbox', { name, exact: true })
    .getByRole('option')
    .nth(index)
    .click();
  await expect(picker).toHaveValue(value);
}

export async function importDemoRoom({
  page,
  get,
  runnerId,
  source,
  roomName,
  snapshot,
}) {
  const library = page.locator('[data-window-id="library:main"]');
  await openDemoApp(page, 'Library');
  const drawer = library.getByRole('button', { name: 'Sources', exact: true });
  if (await drawer.isVisible()) await drawer.click();
  await library
    .getByRole('button', { name: 'Import from Runner…', exact: true })
    .click();
  const panel = library.getByRole('region', {
    name: 'Import from Runner',
    exact: true,
  });
  await selectDemoValue(page, panel, 'Source Runner', runnerId);
  await selectDemoValue(page, panel, 'Registered directory', source.id);
  await panel.getByLabel('New Room name', { exact: true }).fill(roomName);
  await snapshot('1. 从 Runner 已授权来源导入独立资料快照');
  await panel
    .getByRole('button', { name: 'Import Snapshot', exact: true })
    .click();
  await expect(panel).toContainText('Room created', { timeout: 360_000 });
  const matches = (await get('/api/source-imports')).sourceImports.filter(
    (value) => value.roomName === roomName && value.runnerId === runnerId,
  );
  check(
    matches.length === 1 &&
      matches[0].status === 'succeeded' &&
      matches[0].roomId,
    'The source import did not create one confirmed Room.',
  );
  const imported = matches[0];
  check(
    imported.sourceId === source.id &&
      imported.sourceRevision === source.revision &&
      /^[0-9a-f]{40}$/.test(imported.commit),
    'The import did not retain the selected source and an immutable commit.',
  );
  const room = (await get('/api/rooms')).rooms.find(
    (value) => value.id === imported.roomId,
  );
  check(
    room && room.runnerId === runnerId,
    'The imported Room must target the source Runner.',
  );
  await snapshot('2. 快照导入完成，创建新的演示 Room');
  await panel.getByRole('button', { name: 'Open Room', exact: true }).click();
  await expect(
    page
      .locator('[data-window-id="workspace:main"]')
      .getByLabel(`Message ${roomName}`, { exact: true }),
  ).toBeVisible();
  return { room, imported };
}

export async function createDemoAgent({
  page,
  get,
  room,
  user,
  runnerId,
  name,
  snapshot,
}) {
  const workspace = page.locator('[data-window-id="workspace:main"]');
  await workspace
    .getByRole('button', { name: 'Room actions', exact: true })
    .click();
  await page.getByRole('menuitem', { name: 'Agents…', exact: true }).click();
  const panel = workspace.getByRole('region', {
    name: 'Room agents',
    exact: true,
  });
  await panel.getByRole('button', { name: 'New Agent…', exact: true }).click();
  const form = panel.getByRole('form', { name: 'Agent settings', exact: true });
  await form.getByLabel('Name', { exact: true }).fill(name);
  await form
    .getByLabel('Responsibilities', { exact: true })
    .fill(
      'Prepare the requested onboarding document from brief.md. Preserve source originals and follow human review feedback.',
    );
  await selectDemoValue(page, form, 'File access', 'write');
  await selectDemoValue(page, form, 'Preferred Runner', runnerId);
  await snapshot('3. 创建自己的写作 Agent，明确选择执行 Runner 并加入 Room');
  await form
    .getByRole('button', { name: 'Create and Add', exact: true })
    .click();
  await expect(form).toHaveCount(0);
  const matches = (await get(`/api/rooms/${room.id}/members`)).members.filter(
    (value) => value.name === name,
  );
  check(
    matches.length === 1,
    'Agent creation and Room membership were not confirmed.',
  );
  const agent = matches[0];
  check(
    agent.ownerUserId === user.id &&
      agent.enabled &&
      agent.access === 'write' &&
      agent.preferredRunnerId === runnerId,
    'The demo needs its own enabled writing Agent with the selected explicit Runner.',
  );
  await panel
    .getByRole('button', { name: 'Back to Chat', exact: true })
    .click();
  return agent;
}

export async function prepareDemoComposer(page, room, agent, prompt) {
  const composer = page
    .locator('[data-window-id="workspace:main"]')
    .getByRole('form', { name: 'Room composer', exact: true });
  await expect(composer).toBeVisible();
  await composer.getByRole('button', { name: /^To:/ }).click();
  const choices = composer.getByRole('checkbox');
  for (let index = 0; index < (await choices.count()); index++)
    await choices.nth(index).uncheck();
  await composer
    .getByRole('checkbox', { name: agent.name, exact: true })
    .check();
  await composer.getByRole('button', { name: /^To:/ }).click();
  await composer
    .getByLabel(`Message ${room.name}`, { exact: true })
    .fill(prompt);
  return composer;
}

// One owner for the two one-shot browser dispatches. Persist before network
// dispatch; an unknown outcome consumes its slot and can never be re-armed.
export async function installDemoDispatchGuard({ page, ledger, save }) {
  check(
    ledger.allowedRuns === 2 && ledger.requests.length === 0,
    'A new two-send ledger is required.',
  );
  let armed = null;
  let blocked = false;
  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() !== 'POST') return route.fallback();
    const message = /^\/api\/rooms\/[^/]+\/messages$/.test(url.pathname);
    const otherWork =
      /^\/api\/rooms\/[^/]+\/tasks$/.test(url.pathname) ||
      /^\/api\/tasks\/[^/]+\/operations$/.test(url.pathname);
    if (!message && !otherWork) return route.fallback();
    try {
      check(message && !blocked, 'Unexpected work admission was blocked.');
      const body = request.postDataJSON();
      check(
        armed &&
          armed.state === 'reserved' &&
          url.origin === ledger.origin &&
          url.pathname === armed.route &&
          body.content === armed.prompt &&
          body.threadRootId === '' &&
          body.runnerRevision === armed.runnerRevision &&
          body.recipientAgentIds?.length === 1 &&
          body.recipientAgentIds[0] === armed.agentId &&
          typeof body.requestId === 'string' &&
          body.requestId.length > 0 &&
          !body.postOnly &&
          ledger.requests.every(
            (entry) =>
              entry === armed || entry.body?.requestId !== body.requestId,
          ),
        'Unexpected message submission was blocked.',
      );
      check(
        Array.isArray(body.references) &&
          body.references.length === (armed.feedbackMessageId ? 1 : 0) &&
          (!armed.feedbackMessageId ||
            (body.references[0].kind === 'message' &&
              body.references[0].messageId === armed.feedbackMessageId)),
        'Unexpected source references were blocked.',
      );
      armed.body = body;
      armed.state = 'dispatched-outcome-unknown';
      await save();
      await route.fallback();
    } catch {
      blocked = true;
      await route.abort('blockedbyclient');
    }
  });
  return {
    reserve(intent) {
      check(
        !blocked &&
          !armed &&
          ledger.requests.length < 2 &&
          ledger.requests.every(
            (entry) =>
              entry.state === 'terminal' &&
              entry.run?.status === 'succeeded' &&
              entry.run?.published,
          ),
        'The two-send allowance cannot be reused after an unfinished or unsuccessful request.',
      );
      const entry = {
        ...intent,
        intentId: randomUUID(),
        reservedAt: new Date().toISOString(),
        state: 'reserved',
      };
      ledger.requests.push(entry);
      armed = entry;
      return entry;
    },
    disarm() {
      armed = null;
    },
    blocked() {
      return blocked;
    },
  };
}
