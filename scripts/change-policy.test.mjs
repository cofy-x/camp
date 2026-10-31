import assert from 'node:assert/strict';
import test from 'node:test';
import { execFileSync } from 'node:child_process';
import { mkdtemp, writeFile, mkdir, rm, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {
  classifyChanges,
  parseNameStatus,
  gateDecision,
} from './change-policy.mjs';
import { classifyGit } from './classify-changes.mjs';

const classify = (paths, options) =>
  classifyChanges(
    paths.map((p) => ({ status: 'M', paths: [p] })),
    options,
  );
test('readmes in runtime/native/checkpoint directories remain documentation', () => {
  const result = classify([
    'tools/dsh-runtime/README.md',
    'tools/layerfs-runtime/hybrid/README.md',
    'apps/server/internal/sandboxexec/README.md',
  ]);
  assert.equal(result.documentationOnly, true);
  assert.deepEqual(
    Object.entries(result.checks)
      .filter(([, v]) => v)
      .map(([k]) => k),
    ['documentation'],
  );
});
test('instruction/process documents require behavior review without claiming runtime validation', () => {
  const result = classify(['AGENTS.md', 'CONTRIBUTING.md']);
  assert.equal(result.documentationOnly, true);
  assert.equal(result.governanceReview, true);
  assert.equal(result.checks.native, false);
});
test('renames, copies, additions and deletions inspect every original and destination path', () => {
  const changes = parseNameStatus(
    'R100\0tools/dsh-runtime/patch.mjs\0docs/retired.md\0A\0docs/new.md\0D\0README.md\0C100\0docs/new.md\0docs/copy.md\0',
  );
  const result = classifyChanges(changes);
  assert.equal(changes.length, 4);
  assert.equal(result.documentationOnly, false);
  assert.equal(result.checks.native, true);
  assert.equal(result.checks.checkpoint, true);
  assert.equal(
    classifyChanges(parseNameStatus('D\0docs/old.md\0')).documentationOnly,
    true,
  );
});
test('mixed, unknown, workflow, classifier and unavailable inputs choose complete checks', () => {
  for (const paths of [
    ['README.md', 'apps/dashboard/src/app.ts'],
    ['unrecognized.bin'],
    ['.github/workflows/ci.yml'],
    ['scripts/change-policy.mjs'],
    [],
  ]) {
    const result = classify(paths);
    assert.equal(result.classification, 'full');
    for (const key of [
      'source',
      'browser',
      'fixture',
      'server',
      'native',
      'checkpoint',
    ])
      assert.equal(result.checks[key], true);
  }
  for (const value of [
    'R100\0one\0',
    'Z\0file\0',
    'M\0../escape\0',
    'M\0README.md',
  ])
    assert.throws(() => parseNameStatus(value));
});
test('source and workspace owners select different checks', () => {
  assert.equal(
    classify(['apps/server/internal/cli/cli.go']).checks.native,
    true,
  );
  assert.equal(classify(['apps/dashboard/src/app.ts']).checks.native, false);
  assert.equal(
    classify(['tools/layerfs-runtime/bridge.mjs']).checks.checkpoint,
    true,
  );
  const workspace = classify(['scripts/services.mjs'], {
    repository: 'workspace',
  });
  assert.equal(workspace.checks.workspace, true);
  assert.equal(workspace.checks.browser, false);
});
test('required gates fail closed for classification failures, cancellations and unexpected skipped work', () => {
  for (const result of ['failure', 'cancelled', 'skipped', 'timed_out', ''])
    assert.equal(gateDecision('true', result, 'success').passed, false);
  assert.equal(gateDecision('true', 'success', 'success').passed, true);
  assert.equal(gateDecision('false', 'skipped', 'success').passed, true);
  for (const result of ['success', 'failure', 'cancelled', ''])
    assert.equal(gateDecision('false', result, 'success').passed, false);
  assert.equal(gateDecision('false', 'skipped', 'failure').passed, false);
  assert.equal(gateDecision('', 'success', 'success').passed, false);
});
test('classification uses real Git changes and policy from the baseline, not a modified target policy', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-classify-'));
  t.after(() => rm(root, { recursive: true }));
  const git = (...args) =>
    execFileSync('git', ['-C', root, ...args], { encoding: 'utf8' }).trim();
  git('init', '-b', 'main');
  git('config', 'user.name', 'Policy fixture');
  git('config', 'user.email', 'fixture@invalid.example');
  await mkdir(path.join(root, 'scripts'));
  await writeFile(
    path.join(root, 'scripts/change-policy.mjs'),
    await readFile(new URL('./change-policy.mjs', import.meta.url)),
  );
  await writeFile(path.join(root, 'README.md'), 'initial\n');
  git('add', '.');
  git('-c', 'commit.gpgsign=false', 'commit', '-m', 'baseline');
  const base = git('rev-parse', 'HEAD');
  await writeFile(path.join(root, 'README.md'), 'docs\n');
  git('add', '.');
  git('-c', 'commit.gpgsign=false', 'commit', '-m', 'docs');
  assert.equal(
    (await classifyGit({ root, base, head: git('rev-parse', 'HEAD') }))
      .documentationOnly,
    true,
  );
  await writeFile(
    path.join(root, 'scripts/change-policy.mjs'),
    'export function classifyChanges(){return {documentationOnly:true}}\n',
  );
  git('add', '.');
  git('-c', 'commit.gpgsign=false', 'commit', '-m', 'policy mutation');
  assert.equal(
    (await classifyGit({ root, base, head: git('rev-parse', 'HEAD') }))
      .classification,
    'full',
  );
  assert.equal(
    (
      await classifyGit({
        root,
        base: '0'.repeat(40),
        head: git('rev-parse', 'HEAD'),
      })
    ).classification,
    'full',
  );
});

test('local workflow checks avoid product regression while manifest dependency changes require source checks', async () => {
  const { localProfile } = await import('./change-policy.mjs');
  const result = classify([
    '.github/workflows/ci.yml',
    'scripts/change-policy.mjs',
    'package.json',
    'CONTRIBUTING.md',
  ]);
  assert.equal(result.classification, 'full');
  assert.equal(localProfile(result), 'source');
  assert.equal(localProfile(result, { manifestTestsOnly: true }), 'workflow');
  assert.equal(localProfile(classify(['apps/server/cli.go'])), 'source');
});
