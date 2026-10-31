import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmod,
  link,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { WorkerClient } from './client.mjs';
import { MacCheckpoints } from './checkpoint.mjs';
import { scanTree } from './tree.mjs';
import { saveState } from './state.mjs';

assert.equal(
  process.platform,
  'darwin',
  'This qualification must run on the actual Mac.',
);
assert.equal(process.arch, 'arm64');
const base = path.resolve('.data');
await mkdir(base, { recursive: true, mode: 0o700 });
const root = await mkdtemp(path.join(base, 'mac-worker-qualification-'));
await chmod(root, 0o700);
const source = path.join(root, 'native');
await mkdir(source, { mode: 0o750 });
for (const directory of [
  '.git',
  'empty',
  'node_modules',
  'node_modules/pkg',
  'old',
])
  await mkdir(path.join(source, directory), { mode: 0o750 });
await writeFile(path.join(source, '.git/HEAD'), 'ref: refs/heads/main\n', {
  mode: 0o600,
});
await writeFile(path.join(source, '.gitignore'), 'node_modules\n');
await writeFile(
  path.join(source, 'binary'),
  Buffer.from([0, 255, 128, 254, 0, 1]),
);
await writeFile(
  path.join(source, 'node_modules/pkg/index.js'),
  'export const value = 44;\n',
);
await symlink('pkg', path.join(source, 'node_modules/alias'));
await symlink('/not-granted/opaque-target', path.join(source, 'outside'));
await writeFile(path.join(source, 'executable'), '#!/bin/sh\nexit 0\n', {
  mode: 0o755,
});
await link(
  path.join(source, 'executable'),
  path.join(source, 'executable-alias'),
);
await writeFile(path.join(source, 'old/deleted'), 'delete this');
await writeFile(path.join(source, '中文-é'), 'unicode names');
const endpoint = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
).trim();
assert.ok(endpoint.startsWith('unix://'));
const report = {
  schema: 1,
  scope: 'actual Mac full-tree transport to one native Linux ARM64 worker',
  passed: false,
  root,
  host: {
    platform: process.platform,
    architecture: process.arch,
    node: process.version,
  },
  checks: [],
  observations: [],
};
let worker;
try {
  worker = await WorkerClient.open({
    home: path.join(root, 'worker'),
    socket: endpoint.slice(7),
  });
  report.worker = worker.status;
  report.engine = worker.engine;
  report.startupMs = worker.startupMs;
  const checkpoints = new MacCheckpoints({
    root: path.join(root, 'checkpoints'),
    worker,
    runnerId: 'controlled-mac-runner',
    storageId: 'controlled-mac-storage',
  });
  const workspaceId = createHash('sha256')
    .update('private-full-tree-fixture')
    .digest('hex');
  const initial = await checkpoints.checkpoint(source, workspaceId, 'initial', {
    quiescent: true,
  });
  report.observations.push(initial);
  report.checks.push(
    'Actual Mac capture, raw pipe transfer, FUSE apply, Commit and independent remount match the complete manifest including .git, ignored dependencies, modes, directories, symlinks and hardlinks.',
  );
  await rm(path.join(source, 'old'), { recursive: true });
  await rename(
    path.join(source, 'binary'),
    path.join(source, 'renamed-binary'),
  );
  await writeFile(
    path.join(source, 'renamed-binary'),
    Buffer.from([255, 0, 2, 127]),
  );
  await chmod(path.join(source, 'executable'), 0o644);
  await mkdir(path.join(source, 'new-empty'), { mode: 0o700 });
  const changed = await checkpoints.checkpoint(source, workspaceId, 'delta', {
    quiescent: true,
  });
  report.observations.push(changed);
  const unchanged = await checkpoints.checkpoint(
    source,
    workspaceId,
    'unchanged',
    { quiescent: true },
  );
  report.observations.push(unchanged);
  assert.equal(
    unchanged.workerReceipt.checkpoint.head,
    changed.workerReceipt.checkpoint.head,
  );
  assert.equal(unchanged.wire.payloadBytes, 0);
  const restored = await checkpoints.restore(
    unchanged.binding.operationId,
    path.join(root, 'restored'),
  );
  report.restore = restored;
  assert.equal(
    (await scanTree(restored.root)).digest,
    unchanged.binding.digest,
  );
  assert.deepEqual(
    await readFile(path.join(restored.root, 'renamed-binary')),
    Buffer.from([255, 0, 2, 127]),
  );
  report.checks.push(
    'Edit, binary replacement, chmod, rename, deletion, new empty directories, unchanged generation and actual Linux-to-Mac restore preserve exact contents and link topology.',
  );
  report.passed = true;
} catch (error) {
  report.error = {
    code: error.code,
    message: error.message,
    operationId: error.operationId,
    capture: error.capture,
  };
  process.exitCode = 1;
} finally {
  try {
    await worker?.close();
    report.workerDrainConfirmed = true;
  } catch (error) {
    report.workerDrainConfirmed = false;
    report.cleanupError = error.message;
    report.passed = false;
    process.exitCode = 1;
  }
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Actual Mac qualification ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
}
