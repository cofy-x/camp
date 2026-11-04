import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import process from 'node:process';
import test from 'node:test';
import { fileURLToPath, URL } from 'node:url';

test('cached Bake images have exactly the tags Compose consumes and PRs never export caches', () => {
  const cwd = fileURLToPath(new URL('../../', import.meta.url));
  for (const write of ['false', 'true']) {
    const env = {
      ...process.env,
      COMPOSE_PROJECT_NAME: 'camp-remote-cache-plan',
      CACHE_WRITE: write,
    };
    const plan = JSON.parse(
      execFileSync(
        'docker',
        [
          'buildx',
          'bake',
          '-f',
          'compose.remote.yml',
          '-f',
          '.github/fixture-cache.hcl',
          '--print',
        ],
        { cwd, env, encoding: 'utf8' },
      ),
    );
    const images = execFileSync(
      'docker',
      [
        'compose',
        '-f',
        'compose.remote.yml',
        '-p',
        env.COMPOSE_PROJECT_NAME,
        'config',
        '--images',
      ],
      { cwd, env, encoding: 'utf8' },
    )
      .trim()
      .split('\n');
    assert.deepEqual(Object.keys(plan.target).sort(), [
      'daemon-a',
      'daemon-b',
      'server',
      'tests',
    ]);
    assert.deepEqual(
      Object.values(plan.target)
        .flatMap((target) => target.tags)
        .sort(),
      images.sort(),
    );
    const exports = Object.values(plan.target).flatMap(
      (target) => target['cache-to'] ?? [],
    );
    assert.equal(exports.length, write === 'true' ? 2 : 0);
    for (const target of Object.values(plan.target)) {
      assert.equal(target['cache-from'][0].type, 'gha');
      assert.equal(target['cache-from'][0].version, '2');
      assert.ok(
        ['camp-fixture-server-v1', 'camp-fixture-tests-v1'].includes(
          target['cache-from'][0].scope,
        ),
      );
    }
  }
});
