import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { URL } from 'node:url';
import { CampLayerFS } from '../runtime.mjs';
import { identity } from '../validation.mjs';
import { saveState, readState } from '../state.mjs';

test('startup reset works with an empty private home when global SDK volume inventory is unavailable', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'camp-reset-'));
  const socket = path.join(home, 'docker.sock');
  let inventories = 0;
  const server = createServer((request, response) => {
    const target = new URL(request.url, 'http://docker');
    assert.equal(target.pathname, '/v1.54/volumes');
    assert.deepEqual(JSON.parse(target.searchParams.get('filters')), {
      label: ['layerfs.role=store', `layerfs.home=${home}`],
    });
    inventories++;
    response.setHeader('content-type', 'application/json');
    response.end(JSON.stringify({ Volumes: null }));
  });
  await new Promise((resolve) => server.listen(socket, resolve));
  const runtime = new CampLayerFS({
    home,
    options: { docker: socket },
    runtime: {
      sandbox: { list: async () => [], list_leftovers: async () => [] },
      project: { list: async () => [] },
      volume: {
        list: async () => {
          throw new Error(
            'Engine-wide inventory exceeds the SDK output buffer',
          );
        },
      },
    },
  });
  try {
    await runtime.beginReset();
    assert.deepEqual(await runtime.reset(), {
      reset: true,
      removedProjects: [],
    });
    assert.equal(runtime.resetState, 'ready');
    assert.ok(inventories > 0);
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(home, { recursive: true, force: true });
  }
});

test('project eviction removes only its closed receipts after public removal succeeds', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'camp-layerfs-evict-'));
  const projectId = identity('p', ['target']);
  const otherId = identity('p', ['other']);
  const calls = [];
  let fail = true;
  const runtime = new CampLayerFS({
    home,
    runtime: {
      project: {
        remove: async (id, options) => {
          calls.push({ id, options });
          if (fail) throw new Error('Store is busy');
          return { removed: true };
        },
      },
    },
  });
  const first = {
    workspaceId: identity('w', ['first']),
    projectId,
    status: 'closed',
  };
  const other = {
    workspaceId: identity('w', ['other']),
    projectId: otherId,
    status: 'ready',
  };
  try {
    await saveState(runtime.workspaceFile(first.workspaceId), first);
    await saveState(runtime.workspaceFile(other.workspaceId), other);
    await assert.rejects(runtime.removeProject({ projectId }), /Store is busy/);
    assert.deepEqual(
      await readState(runtime.workspaceFile(first.workspaceId)),
      first,
    );
    fail = false;
    assert.deepEqual(await runtime.removeProject({ projectId }), {
      removed: true,
    });
    assert.equal(
      await readState(runtime.workspaceFile(first.workspaceId)),
      null,
    );
    assert.deepEqual(
      await readState(runtime.workspaceFile(other.workspaceId)),
      other,
    );
    assert.deepEqual(calls.at(-1), {
      id: projectId,
      options: { confirm: projectId, stopSandboxes: false },
    });
    const count = calls.length;
    await assert.rejects(runtime.removeProject({ projectId: otherId }), {
      code: 'Busy',
    });
    assert.equal(calls.length, count);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});
