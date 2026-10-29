import { spawn } from 'node:child_process';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const project = JSON.parse(
  await readFile(join(root, 'package.json'), 'utf8'),
)
  .name.split('/')
  .at(-1);
export const binary = join(
  root,
  'dist',
  `${project}${process.platform === 'win32' ? '.exe' : ''}`,
);
export const go = process.env.GO || 'go';
const activeCommands = new Set();
const commandGroups = new WeakSet();
const cancelledCommands = new WeakSet();
const stoppingChildren = new WeakMap();
const serverReadiness = new WeakMap();

export function command(executable, args, options = {}) {
  return new Promise((resolve, reject) => {
    // A build owns one process group, including nested build commands. Signals
    // must not leave a compiler running after its task has removed scratch data.
    const ownGroup =
      process.platform !== 'win32' && process.env.DESKTOP_COMMAND_GROUP !== '1';
    const child = spawn(executable, args, {
      cwd: root,
      stdio: 'inherit',
      ...options,
      detached: ownGroup,
      env: {
        ...(options.env ?? process.env),
        ...(process.platform !== 'win32' ? { DESKTOP_COMMAND_GROUP: '1' } : {}),
      },
    });
    activeCommands.add(child);
    if (ownGroup) commandGroups.add(child);
    let spawnError;
    const interrupt = () => {
      void stop(child);
    };
    process.on('SIGINT', interrupt);
    process.on('SIGTERM', interrupt);
    child.once('error', (error) => {
      spawnError = error;
    });
    child.once('close', (code, signal) => {
      activeCommands.delete(child);
      commandGroups.delete(child);
      process.removeListener('SIGINT', interrupt);
      process.removeListener('SIGTERM', interrupt);
      if (spawnError) reject(spawnError);
      else if (cancelledCommands.has(child))
        reject(new Error(`${executable} was cancelled`));
      else if (code === 0) resolve();
      else reject(new Error(`${executable} exited with ${signal || code}`));
    });
  });
}

export async function scratch() {
  return mkdtemp(join(tmpdir(), `${project}-`));
}

export async function stop(child) {
  if (stoppingChildren.has(child)) return stoppingChildren.get(child);
  const group = commandGroups.has(child);
  if (
    !child.pid ||
    (!group && (child.exitCode !== null || child.signalCode !== null))
  )
    return;
  if (activeCommands.has(child)) cancelledCommands.add(child);
  const signal = (value) => {
    try {
      if (group) process.kill(-child.pid, value);
      else child.kill(value);
    } catch (error) {
      if (error.code !== 'ESRCH') throw error;
    }
  };
  const pending = new Promise((resolve) => {
    const timer = setTimeout(() => signal('SIGKILL'), 5000);
    child.once('close', () => {
      clearTimeout(timer);
      // The group leader may exit before descendants that ignore SIGTERM.
      // Its inherited stdio closing does not prove the whole build has ended.
      if (group) signal('SIGKILL');
      resolve();
    });
    signal('SIGTERM');
  });
  stoppingChildren.set(child, pending);
  return pending;
}

// Only the child that owns a listener can make its health endpoint eligible.
// Keep forwarding its stderr after readiness so runtime errors remain visible.
function observeServer(child) {
  const existing = serverReadiness.get(child);
  if (existing) return existing;
  if (!child.stderr)
    throw new Error(
      'Server stderr must be piped to confirm listener ownership.',
    );
  const state = { origins: new Set(), error: null };
  let buffered = '';
  const output = (chunk) => {
    process.stderr.write(chunk);
    buffered += chunk;
    let newline;
    while ((newline = buffered.indexOf('\n')) >= 0) {
      const line = buffered.slice(0, newline);
      buffered = buffered.slice(newline + 1);
      const origin = line.match(
        /\bmsg="desktop listening" url=(http:\/\/\S+) data=/,
      )?.[1];
      if (origin) {
        try {
          state.origins.add(new URL(origin).origin);
        } catch {
          /* Ignore diagnostic output that does not contain a valid origin. */
        }
      }
    }
    buffered = buffered.slice(-8192);
  };
  child.stderr.on('data', output);
  child.once('error', (error) => {
    state.error = error;
  });
  child.once('close', () => child.stderr.removeListener('data', output));
  serverReadiness.set(child, state);
  return state;
}

export async function waitFor(url, child, timeout = 30000) {
  const deadline = Date.now() + timeout;
  const origin = new URL(url).origin;
  const readiness = observeServer(child);
  const assertRunning = () => {
    if (readiness.error) throw readiness.error;
    if (child.exitCode !== null || child.signalCode !== null)
      throw new Error('Server exited before it was ready.');
  };
  while (Date.now() < deadline) {
    assertRunning();
    if (readiness.origins.has(origin)) {
      let healthy = false;
      try {
        healthy = (await fetch(url, { signal: AbortSignal.timeout(1000) })).ok;
      } catch {
        /* The bound server may still be starting its HTTP handler. */
      }
      assertRunning();
      if (healthy) return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for ${url}`);
}

export async function dispose(dir, children) {
  await Promise.all([...new Set([...children, ...activeCommands])].map(stop));
  await rm(dir, { recursive: true, force: true });
}
