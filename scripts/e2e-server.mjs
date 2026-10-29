import { spawn } from 'node:child_process';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { binary, command, dispose, root, scratch } from './lib.mjs';

const requestedPort = process.env.E2E_PORT || '13009';
const port = Number(requestedPort);
if (
  !/^[0-9]+$/.test(requestedPort) ||
  !Number.isInteger(port) ||
  port < 1 ||
  port > 65535
) {
  throw new Error('E2E_PORT must be a decimal integer between 1 and 65535.');
}
// Fail early on an occupied port. Final readiness still requires the Go process
// to announce its own successful bind after the build.
const probe = createServer();
await new Promise((resolve, reject) => {
  probe.once('error', reject);
  probe.listen(port, '127.0.0.1', resolve);
});
await new Promise((resolve, reject) =>
  probe.close((error) => (error ? reject(error) : resolve())),
);
const dir = await scratch();
const manifest = join(root, '.data', `e2e-${port}.json`);
const children = [];
let stopping = false;

async function shutdown(code = 0) {
  if (stopping) return;
  stopping = true;
  await dispose(dir, children);
  await rm(manifest, { force: true });
  process.exit(code);
}

process.on('SIGINT', () => void shutdown());
process.on('SIGTERM', () => void shutdown());
try {
  await command(process.execPath, [join(root, 'scripts/build.mjs')]);
  await mkdir(join(root, '.data'), { recursive: true });
  await writeFile(manifest, JSON.stringify({ dataDir: join(dir, 'data') }), {
    mode: 0o600,
  });
  if (stopping) throw new Error('Browser test startup was cancelled.');
  const child = spawn(binary, [], {
    cwd: dir,
    stdio: 'inherit',
    env: {
      PATH: process.env.PATH,
      HOME: process.env.HOME,
      LANG: 'C.UTF-8',
      ADDR: `127.0.0.1:${port}`,
      DATA_DIR: join(dir, 'data'),
      // A fresh, keyless installation: no inherited model or runtime settings.
    },
  });
  children.push(child);
  child.on('error', (error) => {
    console.error(error);
    void shutdown(1);
  });
  child.on('exit', (code) => {
    if (!stopping) void shutdown(code || 0);
  });
} catch (error) {
  if (!stopping) {
    console.error(error);
    await shutdown(1);
  }
}
