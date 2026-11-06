import { execFileSync } from 'node:child_process';
import { readFileSync, realpathSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { portableGoPlan } from './change-policy.mjs';
import { listedTests, parseShard, shardTests } from './test-go-shard.mjs';

export function checkPortableGo(execute = execFileSync) {
  const root = fileURLToPath(new URL('../', import.meta.url));
  const allowed = [
    'PATH',
    'HOME',
    'USER',
    'LOGNAME',
    'TMPDIR',
    'LANG',
    'LC_ALL',
    'SDKROOT',
    'DEVELOPER_DIR',
  ];
  const env = {
    ...Object.fromEntries(
      allowed
        .filter((name) => process.env[name] !== undefined)
        .map((name) => [name, process.env[name]]),
    ),
    GOTOOLCHAIN: 'local',
    TMPDIR: realpathSync(
      process.platform === 'darwin' ? '/private/tmp' : '/tmp',
    ),
  };
  const options = { cwd: root, env, encoding: 'utf8' };
  const plan = portableGoPlan(
    execute('go', ['list', './...'], options)
      .trim()
      .split('\n')
      .filter(Boolean),
  );
  console.log(JSON.stringify({ profile: 'mac-portable-go-race', ...plan }));
  const names = listedTests(
    execute(
      'go',
      ['test', '-json', '-list', '^(Test|Example|Fuzz)', ...plan.args.slice(1)],
      { ...options, maxBuffer: 64 << 20 },
    ),
  );
  const shards = ['1/2', '2/2'].map((shard) => ({
    shard,
    selected: shardTests(names, parseShard(shard)),
  }));
  if (shards.some(({ selected }) => !selected.length))
    throw new Error('Portable Go discovery returned an empty shard.');
  for (const { shard, selected } of shards) {
    console.log(
      `Portable Go shard ${shard}: ${selected.length}/${names.length} test names; race detection enabled, cached results disabled.`,
    );
    execute(
      'go',
      [
        'test',
        '-count=1',
        '-run',
        `^(${selected.join('|')})$`,
        ...plan.args.slice(1),
      ],
      { ...options, stdio: 'inherit' },
    );
  }
  return plan;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  const expected = readFileSync(
    new URL('../go.mod', import.meta.url),
    'utf8',
  ).match(/^go ([0-9.]+)$/m)?.[1];
  if (
    !execFileSync('go', ['version'], {
      encoding: 'utf8',
      env: {
        PATH: process.env.PATH,
        HOME: process.env.HOME,
        GOTOOLCHAIN: 'local',
      },
    }).includes(`go${expected} `)
  )
    throw new Error(`Use pinned Go ${expected}.`);
  checkPortableGo();
}
