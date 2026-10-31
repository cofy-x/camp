import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { openHome } from './home.mjs';
import { MAX_MESSAGE } from './protocol.mjs';
import { LIMITS, loadCapture } from './tree.mjs';
import { saveState } from './state.mjs';

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
const root = await mkdtemp(path.resolve('.data/worker-contracts-')),
  home = path.join(root, 'worker'),
  source = path.join(root, 'source');
await mkdir(home, { mode: 0o700 });
await mkdir(source, { mode: 0o700 });
await writeFile(path.join(source, 'note'), 'controlled contract baseline');
const socket = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
)
  .trim()
  .slice(7);
const config = {
  dataRoot: root,
  home,
  docker: socket,
  runnerId: 'controlled-contract-runner',
  storageId: 'controlled-contract-storage',
  qualification: true,
};
let active;
const report = { schema: 1, root, checks: [], passed: false, modelRequests: 0 };
try {
  active = await openHome(config);
  const workspaceId = createHash('sha256')
    .update('private-contract-workspace')
    .digest('hex');
  await assert.rejects(
    active.checkpoints.checkpoint(source, workspaceId, 'unsealed'),
    { code: 'Busy' },
  );
  report.checks.push(
    'Unsealed native admission is refused before capture or mutation.',
  );
  const initial = await active.checkpoints.checkpoint(
      source,
      workspaceId,
      'initial',
      { quiescent: true },
    ),
    capture = await loadCapture(initial.captureRoot);
  const next = {
    ...initial.binding,
    operationId: randomUUID(),
    generation: 2,
    parentCheckpoint: initial.workerReceipt.checkpoint.head,
  };
  for (const [label, binding, manifest] of [
    [
      'wrong-parent',
      { ...next, parentCheckpoint: `12${'0'.repeat(64)}` },
      capture.manifest,
    ],
    ['stale-generation', { ...next, generation: 1 }, capture.manifest],
    [
      'wrong-owner',
      { ...next, runnerId: 'different-controlled-runner' },
      capture.manifest,
    ],
    [
      'reused-operation-body',
      { ...initial.binding, attemptId: 'different-attempt' },
      capture.manifest,
    ],
    [
      'escaping-path',
      next,
      {
        ...capture.manifest,
        entries: [{ ...capture.manifest.entries[0], path: '../outside' }],
      },
    ],
  ]) {
    await assert.rejects(
      active.worker.request({ op: 'prepare', args: { binding, manifest } }),
      (error) =>
        ['Conflict', 'Unknown', 'InvalidSnapshot'].includes(error.code),
    );
    const actual = await active.worker.request({
      op: 'qualification.inspect',
      args: { operationId: initial.binding.operationId },
    });
    assert.equal(actual.namespace.generation, 1);
    assert.equal(actual.namespace.head, initial.workerReceipt.checkpoint.head);
    assert.equal(actual.namespace.pending, null);
    report.checks.push(
      `${label}: actual worker refusal retains the current generation/head without mutation.`,
    );
  }
  active.worker.upload = (operationId) =>
    active.worker.request({
      op: 'upload',
      args: { operationId },
      binaryBytes: LIMITS.bytes + MAX_MESSAGE + 1,
    });
  await writeFile(
    path.join(source, 'note'),
    'retained oversized-transfer snapshot',
  );
  let failure;
  try {
    await active.checkpoints.checkpoint(
      source,
      workspaceId,
      'oversized-transfer',
      { quiescent: true },
    );
  } catch (error) {
    failure = error;
  }
  assert.equal(failure?.code, 'Capacity');
  assert.ok(failure.operationId);
  try {
    await active.worker.close();
  } catch (error) {
    report.expectedDrainError = error.code;
  }
  active = await openHome(config);
  const observed = await active.checkpoints.observe(failure.operationId);
  assert.notEqual(observed.state, 'confirmed');
  await loadCapture(observed.captureRoot);
  const actual = await active.worker.request({
    op: 'qualification.inspect',
    args: { operationId: failure.operationId },
  });
  assert.equal(actual.commits.length, 0);
  assert.equal(actual.namespace.generation, 1);
  assert.equal(actual.namespace.head, initial.workerReceipt.checkpoint.head);
  report.checks.push(
    'An actually oversized pipe body is refused before apply/Commit; its native capture and uncertain intent survive reopening.',
  );
  report.passed = true;
} catch (error) {
  report.error = { code: error.code, message: error.message };
} finally {
  try {
    await active?.worker.close();
  } catch (error) {
    report.closeError = error.message;
    report.passed = false;
  }
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `Actual contract qualification ${report.passed ? 'passed' : 'failed'}: ${path.join(root, 'report.json')}\n`,
  );
  if (!report.passed) process.exitCode = 1;
}
