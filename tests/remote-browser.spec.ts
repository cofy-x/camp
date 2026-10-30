import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test as base, type Page } from '@playwright/test';
import type {
  Runner,
  SourceImport,
} from '../apps/dashboard/src/features/runners/api';
import type { Agent, Run } from '../apps/dashboard/src/features/workspace/api';
import { remoteBrowserEnvironment } from './remote-browser-helpers';
import {
  createRoom,
  library,
  mutate,
  openApp,
  workspace,
} from './team-helpers';

type Remote = Awaited<ReturnType<typeof remoteBrowserEnvironment>>;
const test = base.extend<{ remote: Remote }>({
  remote: async ({ browserName }, use, info) => {
    const remote = await remoteBrowserEnvironment(info, browserName);
    try {
      await use(remote);
    } finally {
      await remote.close();
    }
  },
  baseURL: async ({ remote }, use) => {
    await use(remote.origin);
  },
});
test.use({ actionTimeout: 10_000 });

async function runnerPanel(page: Page) {
  await openApp(page, 'Runners');
  return page.locator('[data-window-id="runners:main"]');
}

async function register(page: Page, remote: Remote, name: string) {
  const app = await runnerPanel(page);
  const panel = app.getByRole('region', {
    name: 'Connect Runner',
    exact: true,
  });
  if (!(await panel.isVisible()))
    await app
      .getByRole('button', { name: 'Connect Runner…', exact: true })
      .click();
  await panel.getByLabel('Runner name', { exact: true }).fill(name);
  await panel
    .getByRole('button', { name: 'Create Connection Code', exact: true })
    .click();
  const credential = panel.getByLabel('Connection code', { exact: true });
  await expect(credential).not.toHaveValue('');
  await expect(panel).toContainText(`--name '${name}'`);
  const daemon = await remote.join(name, await credential.inputValue());
  let runner: Runner | undefined;
  await expect
    .poll(async () => {
      const result = await (await page.request.get('/api/runners')).json();
      runner = result.runners.find((value: Runner) => value.name === name);
      return runner?.online && runner?.capabilities.ready;
    })
    .toBe(true);
  await app.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(panel).toContainText(`${name} registered.`);
  await expect(credential).toHaveCount(0);
  await panel.getByRole('button', { name: 'Open Runner', exact: true }).click();
  await expect(
    app
      .getByRole('region', { name: 'Runner details', exact: true })
      .getByRole('heading', { name, exact: true }),
  ).toBeVisible();
  return { ...daemon, runner: runner! };
}

async function writer(page: Page, roomId: string, handle: string) {
  const response = await mutate(page, `/rooms/${roomId}/agents`, {
    name: 'Remote Writer',
    handle,
    enabled: true,
    access: 'write',
    instructions: 'Prepare the requested file.',
  });
  expect(response.ok()).toBe(true);
  return (await response.json()).agent as Agent;
}

async function routeAgent(page: Page, agentId: string, runnerId: string) {
  const { agents } = (await (await page.request.get('/api/agents')).json()) as {
    agents: Agent[];
  };
  const agent = agents.find((agent) => agent.id === agentId)!;
  const response = await mutate(
    page,
    `/agents/${agent.id}`,
    {
      name: agent.name,
      handle: agent.handle,
      icon: agent.icon,
      instructions: agent.instructions,
      access: agent.access,
      enabled: agent.enabled,
      revision: agent.revision,
      preferredRunnerId: runnerId,
    },
    'PATCH',
  );
  expect(response.ok(), await response.text()).toBeTruthy();
  await page.reload();
}

async function send(
  page: Page,
  room: { id: string; name: string },
  agent: Agent,
  content: string,
) {
  const composer = workspace(page).getByRole('form', {
    name: 'Room composer',
    exact: true,
  });
  await composer
    .getByRole('textbox', { name: `Message ${room.name}`, exact: true })
    .fill(`@${agent.handle} ${content}`);
  const response = page.waitForResponse(
    (value) =>
      value.url().endsWith(`/api/rooms/${room.id}/messages`) &&
      value.request().method() === 'POST',
  );
  await composer
    .getByRole('button', { name: 'Send to Agent', exact: true })
    .click();
  const received = await response;
  expect(received.ok(), await received.text()).toBe(true);
  return (await received.json()).runs[0] as Run;
}

async function runStatus(page: Page, id: string, status: string) {
  let run: Run | undefined;
  await expect
    .poll(
      async () => {
        run = (await (await page.request.get(`/api/runs/${id}`)).json()).run;
        return run?.status;
      },
      { timeout: 20_000 },
    )
    .toBe(status);
  return run!;
}

async function importPanel(page: Page) {
  const drawer = library(page).getByRole('button', {
    name: 'Sources',
    exact: true,
  });
  if (await drawer.isVisible()) await drawer.click();
  await library(page)
    .getByRole('button', { name: 'Import from Runner…', exact: true })
    .click();
  return library(page).getByRole('region', {
    name: 'Import from Runner',
    exact: true,
  });
}

test('real fixture work keeps its queued Runner, cancels once, and delivers the resulting document', async ({
  page,
  remote,
}, info) => {
  test.setTimeout(100_000);
  await remote.setup(page);
  const a = await register(page, remote, 'browser-a');
  const b = await register(page, remote, 'browser-b');
  await openApp(page, 'Workspace');
  const room = await createRoom(page, 'Remote delivery');
  const agent = await writer(page, room.id, 'remote-writer');
  await page.reload();
  await routeAgent(page, agent.id, a.runner.id);
  const first = await send(page, room, agent, '[fixture:wait] Hold this work.');
  await expect
    .poll(async () =>
      (
        await (await page.request.get(`/api/runs/${first.id}/events`)).json()
      ).events.some((event: { text: string }) =>
        event.text.includes('cancellation barrier'),
      ),
    )
    .toBe(true);
  await runStatus(page, first.id, 'running');
  const activityURL = `**/api/runs/${first.id}/events?*`;
  await page.route(activityURL, (route) => route.abort());
  await expect(workspace(page)).toContainText(
    'Reconnecting to activity. The last reported state is shown.',
  );
  await page.unroute(activityURL);
  const queued = await send(
    page,
    room,
    agent,
    'Produce the first reviewable file.',
  );
  await runStatus(page, queued.id, 'queued');
  await routeAgent(page, agent.id, b.runner.id);
  const next = await send(
    page,
    room,
    agent,
    'Revise the file on the next Runner.',
  );
  expect(queued.runnerId).toBe(a.runner.id);
  expect(next.runnerId).toBe(b.runner.id);
  await runStatus(page, next.id, 'queued');
  await workspace(page)
    .getByTestId('agent-work')
    .filter({ has: page.getByText('Working', { exact: true }) })
    .getByRole('button', { name: 'Stop', exact: true })
    .click();
  await runStatus(page, first.id, 'cancelled');
  const firstResult = await runStatus(page, queued.id, 'succeeded');
  expect(firstResult.runnerId).toBe(a.runner.id);
  const result = await runStatus(page, next.id, 'succeeded');
  expect(result.runnerId).toBe(b.runner.id);
  expect(result.baselineCommit).toBe(firstResult.resultCommit);
  expect(result.published).toBe(true);
  const { messages } = await (
    await page.request.get(
      `/api/rooms/${room.id}/messages?threadRootId=&after=0`,
    )
  ).json();
  const reply = messages.find(
    (message: { runId?: string; authorKind: string }) =>
      message.runId === next.id && message.authorKind === 'agent',
  );
  expect(reply).toBeTruthy();
  const resultMessage = workspace(page).locator(
    `[data-message-id="${reply.id}"]`,
  );
  const results = resultMessage.getByRole('region', {
    name: 'Result files',
    exact: true,
  });
  const file = result.files.find((item) => item.status !== 'deleted')!;
  await results
    .getByRole('button', { name: `Open ${file.path}`, exact: true })
    .click();
  await resultMessage
    .getByRole('region', { name: 'Work result', exact: true })
    .getByRole('button', { name: /^Details/ })
    .click();
  const workDetails = workspace(page).getByRole('region', {
    name: 'Work details',
    exact: true,
  });
  await expect(
    workDetails.getByRole('tab', { name: 'Overview', exact: true }),
  ).toHaveAttribute('aria-selected', 'true');
  await workDetails.getByRole('tab', { name: /^Files \(/ }).click();
  await workDetails
    .locator('.workspace-file-change > summary')
    .filter({ hasText: file.path })
    .click();
  await workDetails
    .getByRole('button', { name: 'Open in Library', exact: true })
    .click();
  await expect(library(page)).toContainText(
    'Generated by Camp fixture Runner browser-b.',
  );
  await expect(library(page)).toContainText('File from selected version');
  await expect(library(page)).toContainText('Saved version · read only');
  await expect(
    library(page).getByRole('button', { name: 'Edit Text', exact: true }),
  ).toBeDisabled();
  await library(page)
    .getByRole('button', { name: 'Request Review…', exact: true })
    .click();
  const request = library(page).getByRole('form', {
    name: 'Request document review',
    exact: true,
  });
  await request
    .getByLabel('Title', { exact: true })
    .fill('Remote delivery review');
  const user = (await (await page.request.get('/api/auth/status')).json()).user;
  await request.getByLabel('Reviewer', { exact: true }).selectOption(user.id);
  await request
    .getByRole('button', { name: 'Request Review', exact: true })
    .click();
  const review = workspace(page).getByRole('region', {
    name: 'Document review',
    exact: true,
  });
  await expect(review).toBeVisible();
  await review.getByRole('button', { name: 'Approve', exact: true }).click();
  await review.getByRole('button', { name: 'Deliver', exact: true }).click();
  await expect(review).toContainText('Delivered');
  const reviews = (
    await (await page.request.get(`/api/reviews?roomId=${room.id}`)).json()
  ).reviews;
  expect(reviews[0].deliveredCommit).toBe(result.resultCommit);
  await page.screenshot({ path: info.outputPath('remote-delivery.png') });
});

test('an offline source import can be recovered and cancelled, then its snapshot runs on another Runner', async ({
  page,
  remote,
}, info) => {
  test.setTimeout(100_000);
  await remote.setup(page);
  const a = await register(page, remote, 'source-a');
  const b = await register(page, remote, 'source-b');
  await a.stop();
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/runners')).json()).runners.find(
          (runner: Runner) => runner.id === a.runner.id,
        ).online,
    )
    .toBe(false);
  await page.reload();
  await openApp(page, 'Library');
  let panel = await importPanel(page);
  await panel
    .getByLabel('Source Runner', { exact: true })
    .selectOption(a.runner.id);
  await panel
    .getByLabel('Registered directory', { exact: true })
    .selectOption('notes');
  await panel
    .getByLabel('New Room name', { exact: true })
    .fill('Cancelled notes');
  await expect(library(page).locator('.camp-library-toolbar')).toHaveCount(0);
  await expect(
    library(page).getByRole('complementary', { name: 'Library resources' }),
  ).toBeHidden();
  await page.screenshot({ path: info.outputPath('import-form.png') });
  await panel
    .getByRole('button', { name: 'Import Snapshot', exact: true })
    .click();
  await expect(panel).toContainText('Waiting for Runner');
  await panel
    .getByRole('button', { name: 'Back to Files', exact: true })
    .click();
  const sourcesToggle = library(page).getByRole('button', {
    name: 'Sources',
    exact: true,
  });
  await expect(
    (await sourcesToggle.isVisible())
      ? sourcesToggle
      : library(page).getByRole('button', {
          name: 'Import from Runner…',
          exact: true,
        }),
  ).toBeFocused();
  panel = await importPanel(page);
  await expect(panel).toContainText('Waiting for Runner');
  await panel
    .getByRole('button', { name: 'Cancel Import', exact: true })
    .click();
  await expect(panel).toContainText('Import cancelled');
  a.start();
  await expect
    .poll(
      async () =>
        (await (await page.request.get('/api/runners')).json()).runners.find(
          (runner: Runner) => runner.id === a.runner.id,
        ).online,
    )
    .toBe(true);
  await panel
    .getByRole('button', { name: 'Import Another Snapshot', exact: true })
    .click();
  const heading = panel.getByRole('heading', {
    name: 'Import from Runner',
    exact: true,
  });
  await expect(heading).toBeFocused();
  await expect(
    panel.getByLabel('Registered directory', { exact: true }),
  ).toBeEnabled();
  await panel
    .getByLabel('Source Runner', { exact: true })
    .selectOption(a.runner.id);
  await panel
    .getByLabel('Registered directory', { exact: true })
    .selectOption('notes');
  await panel
    .getByLabel('New Room name', { exact: true })
    .fill('Imported notes');
  await panel
    .getByRole('button', { name: 'Import Snapshot', exact: true })
    .click();
  await expect(panel).toContainText('Room created', { timeout: 20_000 });
  const items = (await (await page.request.get('/api/source-imports')).json())
    .sourceImports as SourceImport[];
  expect(items).toHaveLength(2);
  const imported = items.find((item) => item.status === 'succeeded')!;
  await expect(
    panel.getByRole('heading', { name: 'Imported notes', exact: true }),
  ).toBeVisible();
  await expect(panel).toContainText('choose an Agent');
  const openRoom = panel.getByRole('button', {
    name: 'Open Room',
    exact: true,
  });
  const openFiles = panel.getByRole('button', {
    name: 'Open Imported Files',
    exact: true,
  });
  await expect(openFiles).toBeVisible();
  const another = panel.getByRole('button', {
    name: 'Import Another Snapshot',
    exact: true,
  });
  const viewport = page.viewportSize()!;
  for (const width of [1440, 840, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await openRoom.focus();
    await expect(openRoom).toBeFocused();
    await expect
      .poll(() =>
        panel.evaluate((element) => {
          const bounds = element.getBoundingClientRect();
          const buttons = [...element.querySelectorAll('button')];
          const rectangle = (label: string) =>
            buttons
              .find((button) => button.textContent?.trim() === label)!
              .getBoundingClientRect();
          const roomButton = rectangle('Open Room');
          const fileButton = rectangle('Open Imported Files');
          const nextButton = rectangle('Import Another Snapshot');
          return {
            noOverflow: element.scrollWidth - element.clientWidth <= 1,
            primaryMargin: roomButton.left - bounds.left >= 8,
            secondaryMargin: bounds.right - fileButton.right >= 8,
            repeatSeparation:
              nextButton.top - Math.max(roomButton.bottom, fileButton.bottom) >=
              16,
          };
        }),
      )
      .toEqual({
        noOverflow: true,
        primaryMargin: true,
        secondaryMargin: true,
        repeatSeparation: true,
      });
    await page.screenshot({
      path: info.outputPath(`import-success-${width}.png`),
    });
  }
  await page.setViewportSize({ width: 390, height: 430 });
  const history = panel.getByRole('region', {
    name: 'Recent imports',
    exact: true,
  });
  await history.getByText('Recent imports', { exact: true }).click();
  await expect(history).toContainText('Import cancelled');
  await history
    .getByRole('button', { name: 'View Import', exact: true })
    .scrollIntoViewIfNeeded();
  await expect
    .poll(() =>
      panel
        .locator('.camp-import-content')
        .evaluate((element) => element.scrollTop),
    )
    .toBeGreaterThan(0);
  await history
    .getByRole('button', { name: 'View Import', exact: true })
    .click();
  await expect(panel.getByRole('status')).toHaveText('Import cancelled');
  await expect(heading).toBeFocused();
  await expect
    .poll(() =>
      panel
        .locator('.camp-import-content')
        .evaluate((element) => element.scrollTop),
    )
    .toBe(0);
  await another.click();
  await expect(heading).toBeFocused();
  await expect(panel.getByLabel('New Room name', { exact: true })).toHaveValue(
    'Cancelled notes',
  );
  await panel
    .getByLabel('Source Runner', { exact: true })
    .selectOption(b.runner.id);
  await panel
    .getByLabel('New Room name', { exact: true })
    .fill('Unsubmitted draft');
  await expect(history).toContainText('Room created');
  const completedImport = history
    .locator('li')
    .filter({ hasText: 'Imported notes' });
  await completedImport
    .getByRole('button', { name: 'View Import', exact: true })
    .click();
  await expect(panel.getByRole('status')).toHaveText('Room created');
  await expect(heading).toBeFocused();
  await another.click();
  await expect(panel.getByLabel('Source Runner', { exact: true })).toHaveValue(
    a.runner.id,
  );
  await expect(
    panel.getByLabel('Registered directory', { exact: true }),
  ).toHaveValue('notes');
  await expect(panel.getByLabel('New Room name', { exact: true })).toHaveValue(
    'Imported notes',
  );
  await completedImport
    .getByRole('button', { name: 'View Import', exact: true })
    .click();
  await expect(panel.getByRole('status')).toHaveText('Room created');
  await page.setViewportSize(viewport);
  await panel.getByRole('button', { name: 'Open Room', exact: true }).click();
  const agent = await writer(page, imported.roomId!, 'snapshot-writer');
  await page.reload();
  await routeAgent(page, agent.id, b.runner.id);
  const run = await send(
    page,
    { id: imported.roomId!, name: 'Imported notes' },
    agent,
    'Use the imported source to prepare a result.',
  );
  await runStatus(page, run.id, 'succeeded');
  expect(
    await readFile(join(remote.directory, 'source', 'notes.md'), 'utf8'),
  ).toBe('# Source notes\n\nKeep this original unchanged.\n');
  await page.screenshot({ path: info.outputPath('remote-import.png') });
});
