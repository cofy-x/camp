import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { chmod, mkdir, readFile, statfs, writeFile } from 'node:fs/promises';
import os from 'node:os';
import process from 'node:process';
import { setTimeout as pause } from 'node:timers/promises';
import { LayerFS } from '/opt/camp/layerfs/api/agent/src/index.js';
import { LayerFSRuntime } from '/opt/camp/layerfs/api/runtime/src/index.js';

assert.equal(process.platform, 'linux');
assert.equal(process.arch, 'arm64');
const input = [];
for await (const chunk of process.stdin) {
  input.push(chunk);
  assert.ok(input.reduce((bytes, part) => bytes + part.length, 0) < 8192);
}
const { home, image, project, prefix } = JSON.parse(
  Buffer.concat(input).toString('utf8'),
);
assert.match(project, /^camp44-[a-f0-9]+$/);
assert.match(prefix, /^camp44-[a-f0-9]+-$/);
await chmod(home, 0o700);
const report = {
  platform: `${process.platform}/${process.arch}`,
  kernel: os.release(),
  node: process.version,
  image,
  checks: [],
  passed: false,
};
const artifacts = {};
for (const file of [
  '/opt/camp/layerfs/layerfs.node',
  '/usr/local/bin/layerfs-daemon',
]) {
  const bytes = await readFile(file);
  assert.equal(bytes.subarray(0, 4).toString('hex'), '7f454c46');
  assert.equal(bytes.readUInt16LE(18), 183, 'The binary must be AArch64 ELF');
  artifacts[file.split('/').pop()] = createHash('sha256')
    .update(bytes)
    .digest('hex');
}
report.artifacts = artifacts;
const runtime = await LayerFSRuntime.open({
  home,
  image,
  daemon: '/usr/local/bin/layerfs-daemon',
  docker: '/var/run/docker.sock',
  volumes: '/var/lib/docker/volumes',
  containerPrefix: prefix,
  sweep: false,
});
let client, first, second, store;
const sandboxes = new Set();
try {
  const source = '/tmp/camp-source';
  await mkdir(source, { mode: 0o700 });
  await writeFile(`${source}/binary`, Buffer.from([0, 255, 1, 254]));
  await writeFile(`${source}/note`, 'initial\n');
  let job = await runtime.project.start_init(project, source, {
    timeout: 180_000,
  });
  while (!job.ended) {
    await pause(100);
    job = await runtime.project.get_init(job.id);
  }
  if (job.error) throw new Error(JSON.stringify(job.error));
  store = (await runtime.project.list()).find((item) => item.name === project);
  assert.ok(store?.initialized && store.volume);
  const storePath = `/var/lib/docker/volumes/${store.volume}/_data/global/store.sqlite`;
  const fs = await statfs(storePath);
  assert.ok(
    [0x9123683e, 0xef53, 0x58465342].includes(fs.type),
    `Store filesystem ${fs.type} must be Linux-local btrfs/ext4/xfs`,
  );
  report.store = {
    volume: store.volume,
    filesystemMagic: fs.type,
    profile: 'Disposable',
    crashDurable: false,
  };
  Object.assign(process.env, {
    LAYERFS_HOME: home,
    LAYERFS_STORE_PATH: storePath,
    LAYERFS_IMAGE: image,
    LAYERFS_DAEMON: '/usr/local/bin/layerfs-daemon',
    LAYERFS_DOCKER: '/var/run/docker.sock',
    LAYERFS_VOLUME: store.volume,
    LAYERFS_CONTAINER_PREFIX: prefix,
  });
  client = await LayerFS.open(project);
  report.fork = await client.branch.fork('qualification', 'main');
  const sandbox = (await client.sandbox.create()).sandbox;
  sandboxes.add(sandbox);
  first = {
    sandbox,
    ...(await client.workspace.mount(sandbox, 'qualification')),
  };
  const changed = await client.workspace.exec(
    first,
    "printf 'changed\\n' > note; printf 'new\\n' > extra; cat binary | od -An -t x1",
    'Qualify the ARM64 checkpoint worker',
  );
  assert.equal(changed.exitCode, 0, changed.stderr);
  assert.match(changed.stdout, /00 ff 01 fe/);
  report.checks.push(
    'Native ARM64 addon initializes and opens a Disposable Store on a Linux-local filesystem.',
  );
  const committed = await client.workspace.commit(
    first,
    'Camp ARM64 qualification',
  );
  report.commit = committed;
  assert.equal(committed.kind, 'Committed');
  await client.workspace.unmount(first);
  first = null;
  await client.sandbox.close(sandbox);
  sandboxes.delete(sandbox);
  const reader = (await client.sandbox.create()).sandbox;
  sandboxes.add(reader);
  second = {
    sandbox: reader,
    ...(await client.workspace.mount(reader, 'qualification')),
  };
  const readback = await client.workspace.exec(
    second,
    'test "$(cat note)" = changed && test "$(cat extra)" = new && test "$(od -An -t x1 binary | tr -d \' \\n\')" = 00ff01fe',
    'Read back an independently mounted checkpoint',
  );
  assert.equal(readback.exitCode, 0, readback.stderr);
  report.checks.push(
    'Actual FUSE mutation, Commit and a fresh Sandbox remount preserve text and binary content.',
  );
  report.passed = true;
} catch (error) {
  report.error = {
    code: error.code,
    message: error.message,
    detail: error.detail,
  };
  process.exitCode = 1;
} finally {
  const cleanup = [];
  for (const mounted of [first, second]) {
    if (!mounted) continue;
    try {
      await client.workspace.unmount(mounted, true);
    } catch (error) {
      cleanup.push(error.message);
    }
  }
  for (const sandbox of sandboxes)
    try {
      await client.sandbox.close(sandbox);
    } catch (error) {
      cleanup.push(error.message);
    }
  await client?.close().catch((error) => cleanup.push(error.message));
  if (store)
    await runtime.project
      .remove(project, { confirm: project, stopSandboxes: false })
      .catch((error) => cleanup.push(error.message));
  await runtime.close().catch((error) => cleanup.push(error.message));
  report.cleanupErrors = cleanup;
  if (cleanup.length) {
    report.passed = false;
    process.exitCode = 1;
  }
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
}
