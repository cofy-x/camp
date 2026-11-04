import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import {
  drainResources,
  releaseResource,
  requireReleased,
} from '../release.mjs';

async function fixture(t, failedPhase) {
  const home = await mkdtemp(path.join(os.tmpdir(), 'camp-worker-release-'));
  t.after(() => rm(home, { recursive: true, force: true }));
  const events = [];
  const action = (name) => async () => {
    events.push(name);
    if (name === failedPhase)
      throw Object.assign(new Error('fixture Docker control timeout'), {
        code: 'Docker',
      });
  };
  const owner = {
    bootId: 'controlled-worker',
    config: { home },
    state: { storeId: 'controlled-store' },
    takeFault: () => false,
    client: {
      workspace: { unmount: action('unmount') },
      sandbox: { close: action('close') },
      close: action('client.close'),
    },
    runtime: { close: action('runtime.close') },
    mounts: new Set(),
  };
  owner.release = (resource) => releaseResource(owner, resource);
  const resource = {
    sandbox: 'owned-sandbox',
    container: 'owned-container',
    workspace: { path: '/owned/mount' },
  };
  owner.mounts.add(resource);
  const records = async () =>
    Promise.all(
      (await readdir(path.join(home, 'releases'))).map(async (name) =>
        JSON.parse(await readFile(path.join(home, 'releases', name), 'utf8')),
      ),
    );
  return { owner, resource, events, records };
}

test('administrative release records completion before dropping owned resources', async (t) => {
  const f = await fixture(t);
  await f.owner.release(f.resource);
  assert.deepEqual(f.events, ['unmount', 'close']);
  assert.equal(f.owner.mounts.size, 0);
  assert.equal((await f.records())[0].state, 'released');
  const calls = (await f.records())[0].calls;
  for (const name of ['unmountMs', 'closeMs']) {
    assert.ok(Number.isFinite(calls[name]) && calls[name] >= 0);
    assert.equal(f.resource.releaseMetrics[name], calls[name]);
  }
  assert.ok(f.resource.releaseMetrics.journalMs >= 0);
  assert.ok(
    f.resource.releaseMetrics.totalMs >= calls.unmountMs + calls.closeMs,
  );
  await requireReleased(f.owner.config.home);
  await drainResources(f.owner);
  assert.deepEqual(f.events, [
    'unmount',
    'close',
    'client.close',
    'runtime.close',
  ]);
});

for (const phase of ['unmount', 'close'])
  test(`failed ${phase} keeps ownership and diagnostics without replay during drain`, async (t) => {
    const f = await fixture(t, phase);
    await assert.rejects(f.owner.release(f.resource), { code: 'Unknown' });
    assert.equal(f.owner.mounts.has(f.resource), true);
    const [record] = await f.records();
    assert.equal(record.state, 'unknown');
    assert.equal(record.failedPhase, `${phase}-dispatched`);
    assert.equal(record.error.code, 'Docker');
    assert.ok(Number.isFinite(record.calls[`${phase}Ms`]));
    assert.equal(f.resource.releaseMetrics, undefined);
    assert.equal(record.sandbox, f.resource.sandbox);
    await assert.rejects(requireReleased(f.owner.config.home), {
      code: 'Unknown',
    });
    await assert.rejects(drainResources(f.owner), { code: 'Unknown' });
    assert.equal(f.events.filter((name) => name === phase).length, 1);
    assert.equal(
      f.events.filter((name) => name === 'close').length,
      phase === 'unmount' ? 0 : 1,
    );
    assert.deepEqual(f.events.slice(-2), ['client.close', 'runtime.close']);
    assert.deepEqual(await f.records(), [record]);
  });

test('SDK owners both close when one close fails', async (t) => {
  const f = await fixture(t, 'client.close');
  await assert.rejects(drainResources(f.owner), { code: 'Unknown' });
  assert.equal(f.events.at(-1), 'runtime.close');
});

test('later resource cleanup cannot clear an earlier uncertain release', async (t) => {
  const f = await fixture(t, 'close');
  await assert.rejects(f.owner.release(f.resource), { code: 'Unknown' });
  f.owner.client.sandbox.close = async () => {};
  const other = { sandbox: 'second-owned-sandbox' };
  f.owner.mounts.add(other);
  await f.owner.release(other);
  assert.equal(f.owner.mounts.has(other), false);
  await assert.rejects(requireReleased(f.owner.config.home), {
    code: 'Unknown',
  });
  assert.deepEqual((await f.records()).map((row) => row.state).sort(), [
    'released',
    'unknown',
  ]);
});

test('a live worker cannot resume from an uncertain release even with a clean marker', async (t) => {
  const f = await fixture(t);
  await f.owner.release(f.resource);
  await assert.rejects(requireReleased(f.owner.config.home, true), {
    code: 'Unknown',
  });
});
