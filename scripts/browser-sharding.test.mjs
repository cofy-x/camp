import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { basename, join } from 'node:path';
import test from 'node:test';
import { browserProjects } from './check-browser-shards.mjs';
import { root } from './lib.mjs';

function list(project, shard) {
  const report = JSON.parse(
    execFileSync(
      process.execPath,
      [
        join(root, 'node_modules/@playwright/test/cli.js'),
        'test',
        `--project=${project}`,
        '--list',
        '--reporter=json',
        ...(shard ? [`--shard=${shard}`] : []),
      ],
      {
        cwd: root,
        env: { ...process.env, CI: 'true' },
        encoding: 'utf8',
        maxBuffer: 32 << 20,
      },
    ),
  );
  const cases = [];

  function walk(suites) {
    for (const suite of suites) {
      for (const spec of suite.specs) {
        assert.ok(spec.id);
        cases.push({ id: spec.id, file: basename(spec.file) });
      }
      walk(suite.suites ?? []);
    }
  }

  walk(report.suites);
  assert.ok(cases.length, project);
  return cases;
}

test('real Playwright shards preserve every project case and keep each evidence-producing file together', () => {
  for (const project of browserProjects) {
    const full = list(project);
    const shards = [list(project, '1/2'), list(project, '2/2')];
    assert.deepEqual(
      shards
        .flat()
        .map((item) => item.id)
        .sort(),
      full.map((item) => item.id).sort(),
      project,
    );
    for (const file of [
      'avatars.spec.ts',
      'workspace-results.spec.ts',
      'reviews.spec.ts',
    ])
      assert.equal(
        shards.filter((shard) => shard.some((item) => item.file === file))
          .length,
        1,
        `${project}: ${file}`,
      );
  }
});
