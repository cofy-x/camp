import { spawn } from 'node:child_process';
import { join, resolve } from 'node:path';
import { readFile } from 'node:fs/promises';
import { command, dispose, go, root, scratch, waitFor } from './lib.mjs';

const dir = await scratch();
const children = [];
let stopping = false;

async function shutdown(code = 0) {
  if (stopping) return;
  stopping = true;
  await dispose(dir, children);
  process.exit(code);
}

process.on('SIGINT', () => void shutdown());
process.on('SIGTERM', () => void shutdown());
try {
  const executable = join(
    dir,
    process.platform === 'win32' ? 'server.exe' : 'server',
  );
  await command(go, ['build', '-o', executable, './apps/server/cmd/server']);
  if (stopping) throw new Error('Development startup was cancelled.');
  const port = process.env.PORT || '3001';
  const server = spawn(executable, [], {
    cwd: root,
    stdio: ['inherit', 'inherit', 'pipe'],
    env: { ...process.env, ADDR: `127.0.0.1:${port}` },
  });
  children.push(server);
  await waitFor(`http://127.0.0.1:${port}/api/health`, server);
  if (stopping) throw new Error('Development startup was cancelled.');
  if (process.env.CAMP_DAEMON_DATA_DIR) {
    const runnerDirectory = resolve(root, process.env.CAMP_DAEMON_DATA_DIR);
    // Registration is an explicit operator step. Development only runs an
    // already enrolled private Runner, and owns its foreground process lifetime.
    const runner = JSON.parse(
      await readFile(join(runnerDirectory, 'daemon.json'), 'utf8'),
    );
    if (!runner.runnerId || runner.serverUrl !== `http://127.0.0.1:${port}`)
      throw new Error(
        'CAMP_DAEMON_DATA_DIR must contain a Runner enrolled with this development server origin.',
      );
    children.push(
      spawn(executable, ['runner', 'run', '--data-dir', runnerDirectory], {
        cwd: root,
        stdio: 'inherit',
        env: process.env,
      }),
    );
  }
  const vite = spawn(
    process.execPath,
    [join(root, 'node_modules/vite/bin/vite.js')],
    {
      cwd: join(root, 'apps/dashboard'),
      stdio: 'inherit',
      env: { ...process.env, PORT: port },
    },
  );
  children.push(vite);
  for (const child of children) {
    child.on('error', (error) => {
      console.error(error);
      void shutdown(1);
    });
    child.on('exit', (code) => {
      if (!stopping) void shutdown(code || 0);
    });
  }
} catch (error) {
  if (!stopping) {
    console.error(error);
    await shutdown(1);
  }
}
