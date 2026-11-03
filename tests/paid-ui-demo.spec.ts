import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test as base } from '@playwright/test';
import type { Runner } from '../apps/dashboard/src/features/runners/api';
import {
  importDemoRoom,
  createDemoAgent,
  installDemoDispatchGuard,
  prepareDemoComposer,
  type DemoLedger,
} from '../scripts/demo-remote-ui-driver.mjs';
import { watchPageErrors } from './page-errors';
import { remoteBrowserEnvironment } from './remote-browser-helpers';
import { mutate, workspace } from './team-helpers';

const brief = '# Demo brief\n\nPreserve this immutable source.\n';
type Remote = Awaited<ReturnType<typeof remoteBrowserEnvironment>>;
const test = base.extend<{ remote: Remote }>({
  remote: async ({ browserName }, use, info) => {
    const remote = await remoteBrowserEnvironment(info, browserName, {
      'brief.md': brief,
    });
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
test.setTimeout(90_000);

for (const uncertain of [false, true])
  test(`paid UI demo uses explicit owned Agent setup without a model (uncertain send: ${uncertain})`, async ({
    page,
    remote,
    browserName,
  }, info) => {
    const pageErrors = watchPageErrors(page, browserName);
    const { errors } = pageErrors;
    await remote.setup(page);
    const get = async (route: string) => {
      const response = await page.request.get(route);
      expect(response.ok()).toBe(true);
      return response.json();
    };
    const { user } = await get('/api/auth/status');
    if (!uncertain) {
      const assistant = (await get('/api/agents')).agents.find(
        (agent: { handle: string }) => agent.handle === 'assistant',
      );
      const disabled = await mutate(
        page,
        `/agents/${assistant.id}`,
        {
          name: assistant.name,
          handle: assistant.handle,
          icon: assistant.icon,
          avatar: assistant.avatar,
          instructions: assistant.instructions,
          access: assistant.access,
          enabled: false,
          revision: assistant.revision,
          preferredRunnerId: assistant.preferredRunnerId,
        },
        'PATCH',
      );
      expect(disabled.ok(), await disabled.text()).toBe(true);
    }

    async function register(name: string) {
      const response = await mutate(page, '/runner-enrollments', { name });
      expect(response.ok()).toBe(true);
      const { token } = await response.json();
      await remote.join(name, token);
      let result: Runner | undefined;
      await expect
        .poll(async () => {
          result = (await get('/api/runners')).runners.find(
            (value: Runner) => value.name === name,
          );
          return result?.online && result.capabilities.ready;
        })
        .toBe(true);
      return result!;
    }

    const runner = await register('Demo source Runner');
    const other = await register('Another default Runner');
    const defaults = await get('/api/execution-defaults');
    const changed = await mutate(
      page,
      '/execution-defaults',
      {
        scope: 'personal',
        runnerId: other.id,
        revision: defaults.personalRevision,
      },
      'PATCH',
    );
    expect(changed.ok()).toBe(true);
    const { sources } = await get(`/api/runners/${runner.id}/sources`);
    const ledger: DemoLedger = {
      origin: remote.origin,
      allowedRuns: 2,
      requests: [],
    };
    const saved: DemoLedger[] = [];
    const forwarded: unknown[] = [];
    if (uncertain) {
      // Only browser availability is a fixture. The Server stays keyless and
      // the message response is intercepted after the real dispatch guard.
      await page.route('**/api/workspace/status', (route) =>
        route.fulfill({ json: { available: true, reason: '' } }),
      );
      await page.route('**/api/rooms/*/messages', (route) => {
        if (route.request().method() !== 'POST') return route.fallback();
        forwarded.push(route.request().postDataJSON());
        return route.fulfill({
          status: 503,
          json: { code: 'unavailable', error: 'Uncertain submission fixture' },
        });
      });
    }
    const dispatch = await installDemoDispatchGuard({
      page,
      ledger,
      save: async () => {
        saved.push(structuredClone(ledger));
      },
    });
    await pageErrors.reload();
    const roomName = `UI demo ${randomUUID().slice(0, 8)}`;
    const snapshot = async () => {};
    const { room, imported } = await importDemoRoom({
      page,
      get,
      runnerId: runner.id,
      source: sources[0],
      roomName,
      snapshot,
    });
    if (uncertain) expect(room.defaultAgentId).toBeTruthy();
    else {
      expect(room.defaultAgentId).toBeFalsy();
      expect((await get(`/api/rooms/${room.id}/members`)).members).toEqual([]);
    }
    const agent = await createDemoAgent({
      page,
      get,
      room,
      user,
      runnerId: runner.id,
      name: `Demo Writer ${randomUUID().slice(0, 8)}`,
      snapshot,
    });
    expect(agent.ownerUserId).toBe(user.id);
    expect(agent.id).not.toBe(room.defaultAgentId);
    expect(agent.preferredRunnerId).toBe(runner.id);
    expect(agent.preferredRunnerId).not.toBe(other.id);
    expect((await get('/api/settings/models')).apiKeyConfigured).toBe(false);
    expect((await get(`/api/rooms/${room.id}/runs`)).runs).toEqual([]);
    expect((await get(`/api/rooms/${room.id}/messages`)).messages).toEqual([]);
    expect(
      (await get(`/api/rooms/${room.id}/tasks?view=summary`)).tasks,
    ).toEqual([]);
    expect(ledger.requests).toEqual([]);
    const original = await page.request.get(
      `/api/resources/${room.resourceId}/file?${new URLSearchParams({ path: 'brief.md', commit: imported.commit })}`,
    );
    expect(await original.text()).toBe(brief);
    expect(
      await readFile(join(remote.directory, 'source', 'brief.md'), 'utf8'),
    ).toBe(brief);
    if (uncertain) {
      const prompt = 'Write the onboarding guide from brief.md.';
      const composer = await prepareDemoComposer(page, room, agent, prompt);
      dispatch.reserve({
        route: `/api/rooms/${room.id}/messages`,
        prompt,
        runnerRevision: room.runnerRevision!,
        agentId: agent.id,
      });
      await composer
        .getByRole('button', { name: 'Send to Agent', exact: true })
        .click();
      await expect(composer.getByRole('alert')).toContainText(
        'Uncertain submission fixture',
      );
      dispatch.disarm();
      expect(forwarded).toHaveLength(1);
      expect(saved.at(-1)?.requests[0]).toMatchObject({
        state: 'dispatched-outcome-unknown',
        body: forwarded[0],
      });
      expect(forwarded[0]).toMatchObject({
        recipientAgentIds: [agent.id],
        runnerRevision: room.runnerRevision,
        references: [],
        content: prompt,
      });
      expect(() =>
        dispatch.reserve({
          route: `/api/rooms/${room.id}/messages`,
          prompt,
          runnerRevision: room.runnerRevision!,
          agentId: agent.id,
        }),
      ).toThrow('cannot be reused');
      await composer
        .getByRole('button', { name: 'Send to Agent', exact: true })
        .click();
      await expect.poll(() => dispatch.blocked()).toBe(true);
      expect(forwarded).toHaveLength(1);
      expect(ledger.requests).toHaveLength(1);
      expect((await get(`/api/rooms/${room.id}/runs`)).runs).toEqual([]);
    } else {
      const composer = await prepareDemoComposer(
        page,
        room,
        agent,
        'An unsent demo request.',
      );
      await expect(composer.getByRole('checkbox')).toHaveCount(0);
      expect(ledger.requests).toHaveLength(0);
      if (['chromium', 'touch'].includes(info.project.name))
        await workspace(page).screenshot({
          path: info.outputPath('explicit-demo-agent.png'),
        });
    }
    expect(errors).toEqual([]);
  });
