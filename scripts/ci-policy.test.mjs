import assert from 'node:assert/strict';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import {
  gateDecision,
  classifyChanges,
  validationStage,
} from './change-policy.mjs';

test('protected contexts always resolve, including documentation and failed classification', async () => {
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  assert(!/\n {4}paths(?:-ignore)?:/.test(text));
  for (const [id, name] of [
    ['source', 'Source'],
    ['browser', 'Browser'],
    ['fixture', 'Fixture'],
  ]) {
    const block = text.split(`\n  ${id}:\n`)[1].split(/\n {2}[a-z_]+:\n/)[0];
    assert(block.includes(`name: ${name}\n`));
    assert(block.includes('if: ${{ always() }}'));
    assert(
      block.includes('CLASSIFICATION_RESULT: ${{ needs.changes.result }}'),
    );
    assert(block.includes('test "$CLASSIFICATION_RESULT" = success'));
  }
  assert(text.includes('test "$RESULT" = skipped'));
  assert(text.includes("needs.changes.outputs.browser == 'true'"));
  assert(text.includes("needs.changes.outputs.server == 'true'"));
});
test('native/checkpoint workflows have one caller classification and retain explicit manual qualification', async () => {
  for (const name of ['native-mac.yml', 'checkpoint-proof.yml']) {
    const text = await readFile(
      new URL(`../.github/workflows/${name}`, import.meta.url),
      'utf8',
    );
    assert(text.includes('  workflow_call:'));
    assert(text.includes('  workflow_dispatch:'));
    assert(!text.includes('  pull_request:'));
  }
});

test('the actual required Browser shell gate matches the fail-closed policy', async (t) => {
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const script = text
    .split('  browser:\n')[1]
    .split('  fixture:\n')[0]
    .split('        run: |\n')[1]
    .split('\n')
    .filter((line) => line.startsWith('          '))
    .map((line) => line.slice(10))
    .join('\n');
  const tmp = await mkdtemp(path.join(os.tmpdir(), 'camp-ci-gate-'));
  t.after(() => rm(tmp, { recursive: true }));
  for (const required of ['true', 'false', ''])
    for (const result of ['success', 'failure', 'cancelled', 'skipped', ''])
      for (const classification of [
        'success',
        'failure',
        'cancelled',
        'skipped',
      ]) {
        const execution = spawnSync('bash', ['-e', '-c', script], {
          env: {
            ...process.env,
            REQUIRED: required,
            RESULT: result,
            CLASSIFICATION_RESULT: classification,
            GITHUB_STEP_SUMMARY: path.join(tmp, 'summary'),
          },
        });
        assert.equal(
          execution.status === 0,
          gateDecision(required, result, classification).passed,
          JSON.stringify({ required, result, classification }),
        );
      }
});
test('release runs complete core regression even after documentation-only main CI', async () => {
  const plan = validationStage(
    classifyChanges([{ status: 'M', paths: ['README.md'] }]),
    'release',
  );
  assert.equal(plan.documentationOnly, false);
  for (const key of ['source', 'browser', 'fixture'])
    assert.equal(plan.checks[key], true);
  const release = await readFile(
    new URL('../.github/workflows/release.yml', import.meta.url),
    'utf8',
  );
  assert.match(
    release,
    /core_validation:[\s\S]+uses: \.\/\.github\/workflows\/ci.yml[\s\S]+validation-stage: release/,
  );
  assert.match(
    release,
    /checkpoint_worker_build:[\s\S]+needs: \[preflight, core_validation\]/,
  );
});

test('portable Mac source policy drives the real command path and propagates failures', async () => {
  const { checkPortableGo } = await import('./check-portable-go.mjs');
  const calls = [];
  const packages = [
    'github.com/cofy-x/camp',
    'github.com/cofy-x/camp/apps/server/internal/layerfs',
    'github.com/cofy-x/camp/apps/server/internal/resource',
  ];
  const plan = checkPortableGo((file, args, options) => {
    calls.push({ file, args, options });
    return packages.join('\n');
  });
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1].args, [
    'test',
    '-race',
    '-p',
    '2',
    packages[0],
    packages[2],
  ]);
  assert.equal(calls[1].options.env.GOTOOLCHAIN, 'local');
  assert.equal(calls[1].options.env.DEEPSEEK_API_KEY, undefined);
  assert.equal(plan.exclusions.length, 1);
  assert.throws(
    () => checkPortableGo(() => packages[0]),
    /Stale platform exclusion/,
  );
  assert.throws(
    () =>
      checkPortableGo((_file, args) => {
        if (args[0] === 'test') throw Error('controlled Go failure');
        return packages.join('\n');
      }),
    /controlled Go failure/,
  );
  const text = await readFile(
    new URL('../.github/workflows/ci.yml', import.meta.url),
    'utf8',
  );
  const block = text.split('  mac_source:\n')[1].split(/\n {2}[a-z_]+:\n/)[0];
  assert(block.includes("inputs.validation-stage == 'release'"));
  assert(block.includes('node scripts/check-portable-go.mjs'));
  assert(block.includes('test-js'));
});
