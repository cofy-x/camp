// Qualification only: the unmodified, pinned upstream worker and Mac SDK.
// Never used by the production Runner or its command environment.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawn } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { performance } from 'node:perf_hooks';
import { pathToFileURL } from 'node:url';
import { ByteReader } from './transfer.mjs';
import { writeBytes } from './protocol.mjs';
import { saveState } from './state.mjs';

// Observe the actual builtin codec calls without changing SDK source or frames.
// Object tagging/iteration remains in the containing SDK call's wall/CPU time.
export async function measureCodec(action) {
  const metrics = {
    base64EncodeMs: 0,
    jsonStringifyMs: 0,
    jsonFrames: 0,
    note: 'Actual Mac builtin calls during the upstream request; excludes object-tag construction and frame buffer copies. Observer overhead stays in measured wall time.',
  };
  const stringify = JSON.stringify,
    toString = Buffer.prototype.toString;
  JSON.stringify = function (...args) {
    const started = performance.now();
    try {
      return stringify.apply(this, args);
    } finally {
      if (Array.isArray(args[0]) && args[0][0] === 'layerfs-worker') {
        metrics.jsonStringifyMs += performance.now() - started;
        metrics.jsonFrames++;
      }
    }
  };
  Buffer.prototype.toString = function (...args) {
    const started = performance.now();
    try {
      return toString.apply(this, args);
    } finally {
      if (args[0] === 'base64')
        metrics.base64EncodeMs += performance.now() - started;
    }
  };
  try {
    return { value: await action(), metrics };
  } finally {
    JSON.stringify = stringify;
    Buffer.prototype.toString = toString;
  }
}

export async function openUpstream(worker, directory) {
  const started = performance.now();
  const sdkRoot = path.join(
    worker.installed.buildContext,
    'source/api/agent/src',
  );
  const { frame, decode } = await import(
    pathToFileURL(path.join(sdkRoot, 'transport/frames.js'))
  );
  const environment = {
    PATH: process.env.PATH,
    HOME: worker.home,
    DOCKER_HOST: `unix://${worker.socket}`,
    DOCKER_CONFIG: path.join(worker.home, 'docker-client'),
  };
  const server = spawn(
    'docker',
    [
      'exec',
      '-i',
      worker.name,
      'node',
      '/opt/camp/layerfs/api/runtime/src/worker/server.js',
      '--stdin',
    ],
    { env: environment, stdio: ['pipe', 'pipe', 'pipe'] },
  );
  const ended = new Promise((resolve) => server.once('close', resolve));
  let diagnostic = '';
  server.stderr.on('data', (chunk) => {
    diagnostic = `${diagnostic}${chunk}`.slice(-8192);
  });
  const secret = randomBytes(32).toString('hex');
  await writeBytes(
    server.stdin,
    frame({
      secret,
      home: worker.config.home,
      bundle: '/opt/camp/layerfs',
    }),
  );
  let ready;
  try {
    const reader = new ByteReader(server.stdout);
    const length = (await reader.take(4)).readUInt32BE(0);
    assert.ok(length < 4 << 20);
    ready = decode(await reader.take(length));
  } catch (error) {
    throw new Error(`Upstream startup failed: ${diagnostic}`, { cause: error });
  }
  assert.equal(ready.build, worker.status.buildId);
  // A private loopback relay carries the upstream frames unchanged through the
  // selected Docker pipe. No VM listener is published to the LAN or Agent.
  const pipes = new Set();
  const wire = { sent: 0, received: 0 };
  const relay = createServer((socket) => {
    const child = spawn(
      'docker',
      [
        'exec',
        '-i',
        worker.name,
        'node',
        '-e',
        'const n=require("net");const s=n.connect({host:"127.0.0.1",port:Number(process.argv[1])});process.stdin.pipe(s);s.pipe(process.stdout);s.on("end",()=>process.stdin.destroy());s.on("error",()=>process.exit(1));process.stdin.on("end",()=>s.end());',
        String(ready.port),
      ],
      { env: environment, stdio: ['pipe', 'pipe', 'pipe'] },
    );
    pipes.add(child);
    child.stderr.resume();
    socket.on('data', (chunk) => {
      wire.sent += chunk.length;
    });
    child.stdout.on('data', (chunk) => {
      wire.received += chunk.length;
    });
    socket.pipe(child.stdin);
    child.stdout.pipe(socket);
    socket.on('error', () => {});
    child.stdin.on('error', () => {});
    socket.on('close', () => child.stdin.end());
    child.once('close', () => {
      pipes.delete(child);
      socket.destroy();
    });
  });
  await new Promise((resolve, reject) => {
    relay.once('error', reject);
    relay.listen(0, '127.0.0.1', resolve);
  });
  const endpoint = path.join(directory, 'upstream-endpoint.json');
  await saveState(endpoint, {
    home: worker.config.home,
    port: relay.address().port,
    secret,
    build: ready.build,
    docker: '/var/run/docker.sock',
    daemonHost: '127.0.0.1',
  });
  const agent = path.join(
    worker.installed.buildContext,
    'source/api/agent/src',
  );
  const { LayerFS } = await import(pathToFileURL(path.join(agent, 'index.js')));
  const { useWorker, connection, endpointOf } = await import(
    pathToFileURL(path.join(agent, 'internal.js'))
  );
  for (const [file, expected] of Object.entries(ready.sdk)) {
    const { createHash } = await import('node:crypto');
    const local = path.join(worker.installed.buildContext, 'source', file);
    assert.equal(
      createHash('sha256')
        .update(await readFile(local))
        .digest('hex'),
      expected,
    );
  }
  useWorker(endpoint);
  Object.assign(process.env, {
    LAYERFS_HOME: worker.config.home,
    LAYERFS_STORE_PATH: `/var/lib/docker/volumes/${worker.status.volume}/_data/global/store.sqlite`,
    LAYERFS_IMAGE: worker.installed.image,
    LAYERFS_DAEMON: '/usr/local/bin/layerfs-daemon',
    LAYERFS_VOLUME: worker.status.volume,
    LAYERFS_CONTAINER_PREFIX: worker.config.prefix,
  });
  const client = await LayerFS.open(worker.status.project);
  const rejected = await connection({
    ...endpointOf(endpoint),
    secret: '0'.repeat(64),
  }).call({ fn: 'ping' });
  assert.ok(
    rejected.refused,
    'The actual upstream listener must refuse the wrong secret.',
  );
  return {
    client,
    wire,
    startupMs: performance.now() - started,
    transport:
      'unmodified upstream authenticated base64/whole-frame SDK over private Mac loopback and Docker pipe relay; Linux listener remains unpublished',
    async fork(parent = 'main') {
      const branch = `u-${randomUUID().replaceAll('-', '')}`;
      await client.branch.fork(branch, parent);
      return branch;
    },
    async close() {
      await client.close();
      for (const child of pipes) child.stdin.end();
      relay.close();
      server.stdin.end();
      const exit = await ended;
      assert.equal(exit, 0, diagnostic);
      for (const name of Object.keys(process.env))
        if (name.startsWith('LAYERFS_')) delete process.env[name];
    },
    dockerEnvironment: environment,
  };
}
