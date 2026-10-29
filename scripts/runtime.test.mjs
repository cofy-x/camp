import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import { createServer as createHTTPServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { access, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { command, root, stop, waitFor } from './lib.mjs';

test('browser tests refuse an occupied listener without terminating its owner', async () => {
  const listener = createServer();
  await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
  const { port } = listener.address();
  let child;
  try {
    child = spawn(process.execPath, ['scripts/e2e-server.mjs'], {
      cwd: root,
      env: { ...process.env, E2E_PORT: String(port) },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    child.stdout.on('data', (chunk) => {
      output += chunk;
    });
    child.stderr.on('data', (chunk) => {
      output += chunk;
    });
    const [code] = await once(child, 'exit');
    assert.equal(code, 1);
    assert.match(output, /EADDRINUSE/);
    assert.equal(listener.listening, true);
  } finally {
    if (child && child.exitCode === null && child.signalCode === null)
      child.kill('SIGKILL');
    await new Promise((resolve) => listener.close(resolve));
  }
});

test(
  'cancelling during a nested build stops its processes before removing scratch data',
  { skip: process.platform === 'win32', timeout: 10000 },
  async () => {
    const dir = await mkdtemp(join(tmpdir(), 'desktop-build-cancel-'));
    const library = new URL('./lib.mjs', import.meta.url).href;
    const leaf = `
      process.on('SIGTERM', () => {});
      console.log(JSON.stringify({ leaf: process.pid }));
      setInterval(() => {}, 1000);
    `;
    const middle = `
      import { command } from ${JSON.stringify(library)};
      process.on('SIGTERM', () => process.exit(0));
      console.log(JSON.stringify({ middle: process.pid }));
      await command(process.execPath, ['-e', ${JSON.stringify(leaf)}]);
    `;
    const program = `
      import { command, dispose } from ${JSON.stringify(library)};
      let stopping = false;
      process.on('SIGTERM', async () => {
        stopping = true;
        await dispose(${JSON.stringify(dir)}, []);
        process.exit(0);
      });
      try {
        await command(process.execPath, ['--input-type=module', '-e', ${JSON.stringify(middle)}]);
        console.log(JSON.stringify({ continued: true }));
      } catch (error) {
        if (!stopping) throw error;
      }
    `;
    const runner = spawn(
      process.execPath,
      ['--input-type=module', '-e', program],
      { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] },
    );
    const exited = once(runner, 'exit');
    const pids = [];
    const livePids = new Set();
    let continued = false;
    let stderr = '';
    runner.stderr.on('data', (chunk) => {
      stderr += chunk;
    });
    try {
      await new Promise((resolve, reject) => {
        let buffered = '';
        const timer = setTimeout(
          () => reject(new Error(`Build fixture did not start: ${stderr}`)),
          4000,
        );
        runner.once('error', reject);
        runner.stdout.on('data', (chunk) => {
          buffered += chunk;
          let newline;
          while ((newline = buffered.indexOf('\n')) >= 0) {
            const record = JSON.parse(buffered.slice(0, newline));
            buffered = buffered.slice(newline + 1);
            if (record.continued) {
              continued = true;
              continue;
            }
            const pid = record.middle ?? record.leaf;
            pids.push(pid);
            livePids.add(pid);
            if (record.leaf) {
              clearTimeout(timer);
              resolve();
            }
          }
        });
      });
      runner.kill('SIGTERM');
      const [code] = await exited;
      assert.equal(code, 0, stderr);
      assert.equal(continued, false, 'Cancelled build continued startup');
      await assert.rejects(access(dir), { code: 'ENOENT' });
      for (const pid of pids) {
        // Allow the OS to reap descendants after the group terminates.
        for (let attempt = 0; attempt < 40; attempt++) {
          try {
            process.kill(pid, 0);
          } catch (error) {
            if (error.code === 'ESRCH') {
              livePids.delete(pid);
              break;
            }
            throw error;
          }
          await new Promise((resolve) => setTimeout(resolve, 25));
        }
        assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
      }
    } finally {
      if (runner.exitCode === null && runner.signalCode === null)
        runner.kill('SIGKILL');
      livePids.forEach((pid) => {
        try {
          process.kill(pid, 'SIGKILL');
        } catch (error) {
          if (error.code !== 'ESRCH') throw error;
        }
      });
      await rm(dir, { recursive: true, force: true });
    }
  },
);

test('failed spawns reject and cleanup does not wait for an exit event', async () => {
  const missing = join(tmpdir(), 'missing-desktop-command', 'executable');
  await assert.rejects(command(missing), { code: 'ENOENT' });
  const child = spawn(missing);
  await once(child, 'error');
  await stop(child);
});

test('health checks never reuse a healthy listener before the child announces its bind', async () => {
  let requests = 0;
  const listener = createHTTPServer((_request, response) => {
    requests++;
    response.end('healthy');
  });
  await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
  const { port } = listener.address();
  const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  try {
    await assert.rejects(
      waitFor(`http://127.0.0.1:${port}/api/health`, child, 250),
      /Timed out waiting/,
    );
    assert.equal(
      requests,
      0,
      'An unrelated listener received a readiness request.',
    );
    assert.equal(listener.listening, true);
  } finally {
    await stop(child);
    await new Promise((resolve) => listener.close(resolve));
  }
});

test('readiness compares canonical origins for the default HTTP port', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => ({ ok: true }));
  const child = spawn(
    process.execPath,
    [
      '-e',
      'console.error(\'level=INFO msg="desktop listening" url=http://127.0.0.1:80 data=fixture\'); setInterval(() => {}, 1000);',
    ],
    { stdio: ['ignore', 'ignore', 'pipe'] },
  );
  try {
    await waitFor('http://127.0.0.1:80/api/health', child, 1000);
    assert.equal(fetch.mock.callCount(), 1);
    assert.equal(
      fetch.mock.calls[0].arguments[0],
      'http://127.0.0.1:80/api/health',
    );
  } finally {
    await stop(child);
  }
});
