import assert from 'node:assert/strict';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { dockerVolumeDirectory } from '../docker.mjs';

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
