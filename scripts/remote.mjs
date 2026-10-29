import { execFile } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { parseArgs, promisify } from 'node:util';
import { command, go, root } from './lib.mjs';

const execute = promisify(execFile);
const { positionals, values } = parseArgs({
  allowPositionals: true,
  options: {
    project: { type: 'string' },
    port: { type: 'string' },
    'skip-build': { type: 'boolean' },
  },
});
const mode = positionals[0] || 'up';
if (
  !['up', 'test', 'down', 'logs', 'native'].includes(mode) ||
  positionals.length > 1
)
  throw new Error(
    'Usage: node scripts/remote.mjs up|test|down|logs|native [--project camp-remote-name] [--port number] [--skip-build]',
  );
if (mode === 'native') {
  if (process.platform !== 'linux')
    throw new Error(
      'Real DSH/LayerFS acceptance requires Linux; use test for the portable protocol suite.',
    );
  await command(
    go,
    [
      'test',
      '-race',
      './apps/server/internal/nativeexecutor',
      './apps/server/internal/daemon',
      './apps/server/internal/remoteruns',
      '-count=1',
      '-v',
    ],
    {
      env: {
        ...process.env,
        CAMP_TEST_LAYERFS_RUNTIME: join(root, 'tools/layerfs-runtime'),
        CAMP_TEST_DSH_RUNTIME: join(root, 'tools/dsh-runtime'),
      },
    },
  );
  process.exit(0);
}
const currentPath = join(root, '.data/remote/current.json');
let saved;
if (mode === 'logs' || mode === 'down') {
  try {
    saved = JSON.parse(await readFile(currentPath, 'utf8'));
  } catch {
    /* An explicit project also works without a local manifest. */
  }
}
const project =
  values.project ||
  process.env.CAMP_REMOTE_PROJECT ||
  saved?.project ||
  `camp-remote-${randomUUID().slice(0, 8)}`;
if (!/^camp-remote-[a-z0-9][a-z0-9_-]{0,50}$/.test(project))
  throw new Error(
    'Project names must start with camp-remote- and use lowercase letters, numbers, hyphens or underscores.',
  );
if (
  (mode === 'logs' || mode === 'down') &&
  !values.project &&
  !process.env.CAMP_REMOTE_PROJECT &&
  !saved
)
  throw new Error(
    'Choose --project explicitly; no previous remote environment is recorded.',
  );

async function availablePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close((error) => (error ? reject(error) : resolve(port)));
    });
  });
}

const port =
  values.port ||
  process.env.CAMP_REMOTE_PORT ||
  saved?.port ||
  (mode === 'test' ? await availablePort() : '13443');
if (!/^\d+$/.test(String(port)) || Number(port) < 1 || Number(port) > 65535)
  throw new Error('Choose a valid localhost port.');
const environment = { ...process.env, CAMP_REMOTE_PORT: String(port) };
const composeArgs = [
  'compose',
  '-f',
  join(root, 'compose.remote.yml'),
  '-p',
  project,
];
const compose = (args) =>
  command('docker', [...composeArgs, ...args], { env: environment });
const capture = async (args) =>
  (
    await execute('docker', args, {
      cwd: root,
      env: environment,
      maxBuffer: 16 << 20,
    })
  ).stdout.trim();
const harness = (...args) =>
  compose([
    'exec',
    '-T',
    'tests',
    'node',
    '/opt/camp-remote/harness.mjs',
    ...args,
  ]);
const logsDirectory = join(root, '.data/remote', project);
await mkdir(logsDirectory, { recursive: true, mode: 0o700 });

async function retainLogs() {
  const text = await capture([...composeArgs, 'logs', '--no-color']);
  await writeFile(join(logsDirectory, 'compose.log'), text, { mode: 0o600 });
}

if (mode === 'down') await compose(['down', '--volumes', '--remove-orphans']);
else if (mode === 'logs') await compose(['logs', '--no-color']);
else {
  // An isolated acceptance run must not replace the active developer project's
  // discovery pointer, especially since its own volumes are removed on success.
  if (mode === 'up')
    await writeFile(currentPath, JSON.stringify({ project, port }), {
      mode: 0o600,
    });
  try {
    if (!values['skip-build']) await compose(['build']);
    await compose(['run', '--rm', '--no-deps', 'tests', 'init']);
    await compose(['up', '-d', 'server', 'tests']);
    await harness('bootstrap');
    await compose(['up', '-d', 'daemon-a', 'daemon-b']);
    await harness('ready');
    if (mode === 'up') {
      console.log(
        `Remote development is running at https://localhost:${port}.`,
      );
      console.log(
        `Project: ${project}. View the private account with docker compose -f compose.remote.yml -p ${project} exec tests cat /control/account.json.`,
      );
      console.log(
        'The local CA is in the project trust volume; certificate validation is enabled throughout.',
      );
      console.log(
        `Stop and remove only these development volumes: node scripts/remote.mjs down --project ${project}`,
      );
    } else {
      await harness('check');
      await harness('task-begin');
      await compose(['restart', 'server', 'daemon-a', 'daemon-b']);
      await harness('task-finish');
      await harness('begin');
      await compose(['restart', 'server']);
      await harness('finish', 'cancelled');
      await harness('begin');
      await compose(['kill', '-s', 'SIGKILL', 'daemon-a']);
      await compose(['up', '-d', 'daemon-a']);
      await harness('finish', 'interrupted');
      await harness('begin');
      const container = await capture([...composeArgs, 'ps', '-q', 'daemon-a']);
      if (!/^[a-f0-9]{12,64}$/.test(container))
        throw new Error('Cannot identify this project Runner container.');
      await command('docker', [
        'network',
        'disconnect',
        `${project}_camp`,
        container,
      ]);
      try {
        await harness('offline');
      } finally {
        await command('docker', [
          'network',
          'connect',
          '--alias',
          'daemon-a',
          `${project}_camp`,
          container,
        ]);
      }
      await harness('finish', 'cancelled');
      await retainLogs();
      await writeFile(
        join(logsDirectory, 'result.json'),
        JSON.stringify(
          {
            project,
            platform: process.platform,
            architecture: process.arch,
            passed: true,
            completedAt: new Date().toISOString(),
            scope:
              'Compose fixture protocol/business/collaboration/handoff/question/restart/reconnect; no native runtime or paid model calls',
          },
          null,
          2,
        ),
      );
      await compose(['down', '--volumes', '--remove-orphans']);
      console.log(
        `Remote protocol acceptance passed. Report: ${join(logsDirectory, 'result.json')}`,
      );
    }
  } catch (error) {
    try {
      await retainLogs();
    } catch {
      /* A build failure can precede container creation. */
    }
    console.error(
      `Remote validation failed. Project and private logs retained: ${project}, ${logsDirectory}`,
    );
    console.error(`Cleanup: node scripts/remote.mjs down --project ${project}`);
    throw error;
  }
}
