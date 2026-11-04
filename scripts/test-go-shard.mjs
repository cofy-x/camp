import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { command, go, root } from './lib.mjs';

export function parseShard(value) {
  if (!/^[1-9][0-9]*\/[1-9][0-9]*$/.test(value ?? ''))
    throw new Error('Expected --shard INDEX/TOTAL, with 1 <= INDEX <= TOTAL.');
  const [index, total] = value.split('/').map(Number);
  if (![index, total].every(Number.isSafeInteger) || index > total)
    throw new Error('Expected --shard INDEX/TOTAL, with 1 <= INDEX <= TOTAL.');
  return { index, total };
}

export function listedTests(output) {
  const names = new Set();
  for (const line of output.trim().split('\n')) {
    const event = JSON.parse(line);
    if (event.Action === 'fail')
      throw new Error(`Go test discovery failed for ${event.Package}.`);
    const name = event.Output?.trim();
    if (
      event.Action === 'output' &&
      /^(?:Test|Example|Fuzz)[\p{L}\p{N}_]*$/u.test(name ?? '')
    )
      names.add(name);
  }
  if (!names.size) throw new Error('Go test discovery returned no test names.');
  return [...names].sort();
}

export function shardTests(names, { index, total }) {
  // Identical names in different packages must select the same shard: -run
  // applies to every package. Top-level matching keeps every subtest/fuzz seed.
  return [...new Set(names)]
    .sort()
    .filter(
      (name) =>
        createHash('sha256').update(name).digest().readUInt32BE(0) % total ===
        index - 1,
    );
}

export async function testGoShard(
  shard,
  { cwd = root, goCommand = go, discover = spawnSync, execute = command } = {},
) {
  const selection = parseShard(shard);
  // Discover with the same race-enabled build as execution, so discovery does
  // not compile a second, uninstrumented copy of the suite.
  const listing = discover(
    goCommand,
    ['test', '-race', '-json', '-list', '^(Test|Example|Fuzz)', './...'],
    {
      cwd,
      encoding: 'utf8',
      maxBuffer: 64 << 20,
      stdio: ['ignore', 'pipe', 'inherit'],
    },
  );
  if (listing.error) throw listing.error;
  if (listing.status !== 0) throw new Error('Go test discovery failed.');
  const all = listedTests(listing.stdout);
  const selected = shardTests(all, selection);
  if (!selected.length) throw new Error(`Go shard ${shard} is empty.`);
  console.log(
    `Go shard ${shard}: ${selected.length}/${all.length} test names; all packages, race detection enabled.`,
  );
  await execute(
    goCommand,
    ['test', '-race', '-run', `^(${selected.join('|')})$`, './...'],
    { cwd },
  );
  return selected;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const { values } = parseArgs({ options: { shard: { type: 'string' } } });
  await testGoShard(values.shard);
}
