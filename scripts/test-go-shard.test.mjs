import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  listedTests,
  parseShard,
  shardTests,
  testGoShard,
} from './test-go-shard.mjs';

test('all Go names have exactly one shard, including examples, fuzz targets and duplicate package names', () => {
  const names = [
    'Example',
    'FuzzBytes',
    'TestShared',
    ...Array.from({ length: 500 }, (_, i) => `TestCase${i}`),
  ];
  const output = ['one', 'two']
    .flatMap((Package) =>
      names.map((name) =>
        JSON.stringify({ Action: 'output', Package, Output: `${name}\n` }),
      ),
    )
    .join('\n');
  const catalog = listedTests(output);
  assert.equal(catalog.length, names.length);
  for (const total of [1, 2, 3, 8]) {
    const all = Array.from({ length: total }, (_, i) =>
      shardTests(catalog, parseShard(`${i + 1}/${total}`)),
    ).flat();
    assert.deepEqual(all.sort(), [...names].sort());
    assert.equal(new Set(all).size, names.length);
  }
  assert.deepEqual(
    shardTests([...catalog].reverse(), parseShard('1/2')),
    shardTests(catalog, parseShard('1/2')),
  );
});

test('invalid partitions and failed or empty Go discovery fail closed', async () => {
  for (const value of [
    undefined,
    '',
    '0/2',
    '3/2',
    '1/0',
    '1',
    '1/2/3',
    '-1/2',
    '1/9007199254740992',
  ])
    assert.throws(() => parseShard(value));
  for (const output of [
    '',
    '{}',
    'not-json',
    '{"Action":"fail","Package":"broken"}',
  ])
    assert.throws(() => listedTests(output));
  let executed = false;
  await assert.rejects(
    testGoShard('1/2', {
      discover: () => ({ status: 1, stdout: '' }),
      execute: () => {
        executed = true;
      },
    }),
    /discovery failed/,
  );
  assert.equal(executed, false);
});

test('real Go execution preserves nested tests, examples and every fuzz seed, and propagates failures', async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), 'camp-go-shards-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  await writeFile(join(cwd, 'go.mod'), 'module shardfixture\n\ngo 1.27.3\n');
  await writeFile(
    join(cwd, 'shard_test.go'),
    `package shardfixture
import ("fmt"; "testing")
func TestAlpha(t *testing.T) { t.Run("child", func(t *testing.T) {}) }
func TestBeta(t *testing.T) {}
func TestGamma(t *testing.T) {}
func FuzzSeed(f *testing.F) { f.Add("one"); f.Add("two"); f.Fuzz(func(t *testing.T, s string) {}) }
func Example() { fmt.Println("proof")
// Output: proof
}
`,
  );
  const events = [];
  const execute = (binary, args, options) => {
    assert.ok(args.includes('-race'));
    const stdout = execFileSync(binary, [args[0], '-json', ...args.slice(1)], {
      ...options,
      encoding: 'utf8',
    });
    events.push(
      ...stdout
        .trim()
        .split('\n')
        .map((line) => JSON.parse(line)),
    );
  };
  for (const shard of ['1/2', '2/2'])
    await testGoShard(shard, { cwd, execute });
  const ran = events
    .filter((event) => event.Action === 'run')
    .map((event) => event.Test);
  for (const name of [
    'TestAlpha',
    'TestAlpha/child',
    'TestBeta',
    'TestGamma',
    'Example',
    'FuzzSeed',
    'FuzzSeed/seed#0',
    'FuzzSeed/seed#1',
  ])
    assert.equal(ran.filter((value) => value === name).length, 1, name);
  const failed = new Error('controlled Go failure');
  await assert.rejects(
    testGoShard('1/2', {
      cwd,
      execute: () => {
        throw failed;
      },
    }),
    (error) => error === failed,
  );
});
