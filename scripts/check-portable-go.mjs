import { execFileSync } from 'node:child_process';
import { readFileSync, realpathSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { portableGoPlan } from './change-policy.mjs';

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
  execute('go', plan.args, { ...options, stdio: 'inherit' });
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
