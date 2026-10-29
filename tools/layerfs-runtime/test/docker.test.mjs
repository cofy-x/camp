import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { URL } from 'node:url';
import {
  dockerVolumeDirectory,
  ownedStoreVolumes,
  removeOwnedStoreVolumes,
} from '../docker.mjs';

test('volume discovery uses the selected engine and requires its local directory', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-docker-'));
  const socket = path.join(root, 'engine.sock');
  const data = path.join(root, "custom engine's data");
  const volumes = path.join(data, 'volumes');
  await mkdir(volumes, { recursive: true });
  let info = { DockerRootDir: data };
  let status = 200;
  const server = createServer((req, res) => {
    assert.equal(req.url, '/v1.54/info');
    assert.equal(req.method, 'GET');
    assert.equal(req.headers.authorization, undefined);
    res.writeHead(status, { 'content-type': 'application/json' });
    res.end(JSON.stringify(info));
  });
  await new Promise((resolve) => server.listen(socket, resolve));
  try {
    assert.equal(await dockerVolumeDirectory(socket), volumes);
    for (const DockerRootDir of [undefined, 'relative', 'bad\0path']) {
      info = { DockerRootDir };
      await assert.rejects(dockerVolumeDirectory(socket), {
        code: 'Unsupported',
      });
    }
    info = null;
    await assert.rejects(dockerVolumeDirectory(socket), {
      code: 'Unsupported',
    });
    info = { padding: 'x'.repeat(1024 * 1024) };
    await assert.rejects(dockerVolumeDirectory(socket), {
      code: 'InvalidInput',
    });
    info = { DockerRootDir: path.join(root, 'missing') };
    await assert.rejects(dockerVolumeDirectory(socket), { code: 'ENOENT' });
    await rm(volumes, { recursive: true });
    await writeFile(volumes, 'not a directory');
    info = { DockerRootDir: data };
    await assert.rejects(dockerVolumeDirectory(socket), {
      code: 'Unsupported',
    });
    status = 403;
    await assert.rejects(dockerVolumeDirectory(socket), {
      code: 'Unsupported',
    });
    for (const invalid of [
      'tcp://remote:2375',
      'relative.sock',
      '/bad\0socket',
    ])
      await assert.rejects(dockerVolumeDirectory(invalid), {
        code: 'InvalidInput',
      });
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  }
});

test('Store cleanup filters one home and rechecks ownership without force or engine inventory', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-stores-'));
  const socket = path.join(root, 'engine.sock');
  const home = path.join(root, 'runner');
  const own = {
    Name: 'store-one',
    Labels: {
      'layerfs.role': 'store',
      'layerfs.home': home,
      'layerfs.project': 'project-one',
    },
  };
  let inventory = [own];
  let inspected = own;
  let busy = false;
  let absent = false;
  let deletes = 0;
  const server = createServer((req, res) => {
    const url = new URL(req.url, 'http://docker');
    res.setHeader('content-type', 'application/json');
    if (url.pathname === '/v1.54/volumes') {
      assert.equal(req.method, 'GET');
      assert.deepEqual(JSON.parse(url.searchParams.get('filters')), {
        label: ['layerfs.role=store', `layerfs.home=${home}`],
      });
      res.end(JSON.stringify({ Volumes: inventory }));
    } else if (url.pathname === '/v1.54/volumes/store-one') {
      if (absent) {
        res.writeHead(404);
        res.end('{}');
        return;
      }
      if (req.method === 'GET') res.end(JSON.stringify(inspected));
      else {
        assert.equal(req.method, 'DELETE');
        assert.equal(url.searchParams.get('force'), 'false');
        deletes++;
        res.writeHead(busy ? 409 : 204);
        res.end();
      }
    } else {
      assert.fail('Cleanup requested unrelated Docker inventory');
    }
  });
  await new Promise((resolve) => server.listen(socket, resolve));
  try {
    assert.deepEqual(await ownedStoreVolumes(socket, home), [
      { name: 'store-one', project: 'project-one' },
    ]);
    await removeOwnedStoreVolumes(socket, home, ['store-one']);
    assert.equal(deletes, 1);
    busy = true;
    await assert.rejects(
      removeOwnedStoreVolumes(socket, home, ['store-one']),
      /HTTP 409/,
    );
    inspected = {
      ...own,
      Labels: { ...own.Labels, 'layerfs.home': '/another-runner' },
    };
    await assert.rejects(
      removeOwnedStoreVolumes(socket, home, ['store-one']),
      /ownership changed/,
    );
    assert.equal(deletes, 2);
    inventory = [inspected];
    await assert.rejects(
      ownedStoreVolumes(socket, home),
      /outside this private/,
    );
    inventory = null;
    assert.deepEqual(await ownedStoreVolumes(socket, home), []);
    absent = true;
    await removeOwnedStoreVolumes(socket, home, ['store-one']);
    assert.equal(deletes, 2);
    await assert.rejects(
      removeOwnedStoreVolumes(socket, home, ['../store-one']),
      { code: 'InvalidInput' },
    );
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  }
});
