import { spawn, type ChildProcess } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, type Page, type TestInfo } from '@playwright/test';

const binary = join(
  process.cwd(),
  'dist',
  process.platform === 'win32' ? 'camp.exe' : 'camp',
);

export async function remoteBrowserEnvironment(
  info: TestInfo,
  browser: string,
) {
  const directory = await mkdtemp(join(tmpdir(), `camp-remote-${browser}-`));
  const listener = createServer();
  await new Promise<void>((resolve, reject) => {
    listener.once('error', reject);
    listener.listen(0, '127.0.0.1', resolve);
  });
  const address = listener.address();
  if (!address || typeof address === 'string') throw new Error('No test port');
  const origin = `http://127.0.0.1:${address.port}`;
  await new Promise<void>((resolve, reject) =>
    listener.close((error) => (error ? reject(error) : resolve())),
  );
  const children = new Set<ChildProcess>();
  let output = '';
  const environment = Object.fromEntries(
    ['PATH', 'HOME', 'SystemRoot', 'TEMP', 'TMP', 'TMPDIR'].flatMap((key) =>
      process.env[key] ? [[key, process.env[key]!]] : [],
    ),
  );

  function launch(args: string[]) {
    const child = spawn(binary, args, {
      cwd: directory,
      env: environment,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    children.add(child);
    child.stdout?.on('data', (chunk: Buffer) => {
      output += chunk.toString();
    });
    child.stderr?.on('data', (chunk: Buffer) => {
      output += chunk.toString();
    });
    child.once('exit', () => children.delete(child));
    return child;
  }

  const server = launch([
    'serve',
    '--addr',
    `127.0.0.1:${address.port}`,
    '--data-dir',
    join(directory, 'server'),
    '--allow-fixture-runners',
  ]);
  try {
    await expect
      .poll(() => output.includes('desktop listening'), { timeout: 30_000 })
      .toBe(true);
    await mkdir(join(directory, 'source'));
    await writeFile(
      join(directory, 'source', 'notes.md'),
      '# Source notes\n\nKeep this original unchanged.\n',
    );
  } catch (error) {
    server.kill('SIGTERM');
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  return {
    origin,
    directory,
    async setup(page: Page) {
      await page.goto(origin);
      await page
        .getByLabel('Team name', { exact: true })
        .fill('Remote browser team');
      await page
        .getByLabel(/^Setup token/)
        .fill(
          (
            await readFile(
              join(directory, 'server', 'team-setup.token'),
              'utf8',
            )
          ).trim(),
        );
      await page.getByLabel('Your name', { exact: true }).fill('Remote Owner');
      await page.getByLabel('Username', { exact: true }).fill('remote-owner');
      await page
        .getByLabel(/^Password/)
        .fill(`Private browser ${randomUUID()}`);
      await page
        .getByRole('button', { name: 'Create Team', exact: true })
        .click();
      await expect(
        page.getByRole('region', { name: 'general — Workspace', exact: true }),
      ).toBeVisible();
    },
    async join(name: string, token: string) {
      const data = join(directory, name);
      const child = launch([
        'runner',
        'setup',
        '--server',
        origin,
        '--name',
        name,
        '--data-dir',
        data,
        '--code-file',
        '-',
        '--executor',
        'fixture',
        '--source',
        `notes=${join(directory, 'source')}`,
      ]);
      const exited = once(child, 'exit');
      child.stdin?.end(token);
      const [code] = await exited;
      expect(code, output).toBe(0);
      let running = launch(['runner', 'run', '--data-dir', data]);
      return {
        async stop() {
          const exited = once(running, 'exit');
          running.kill('SIGTERM');
          await exited;
        },
        start() {
          running = launch(['runner', 'run', '--data-dir', data]);
        },
      };
    },
    async close() {
      for (const child of [...children]) {
        const exited = once(child, 'exit');
        child.kill('SIGTERM');
        await exited;
      }
      if (info.status !== info.expectedStatus) {
        await info.attach('remote-processes.log', {
          body: output,
          contentType: 'text/plain',
        });
      }
      await rm(directory, { recursive: true, force: true });
    },
  };
}
