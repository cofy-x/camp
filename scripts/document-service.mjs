import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { lstat, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { promisify } from 'node:util';

const execute = promisify(execFile);

function known(status) {
  assert.equal(status.supported, true);
  assert.ok(
    ['absent', 'stopped', 'running'].includes(status.state),
    'Service ownership is uncertain; preserve the fixture.',
  );
  assert.equal(
    status.loginEnabled,
    false,
    'Acceptance never enables login startup.',
  );
  return status;
}

// Only the document smoke's transport adapter. Production Camp commands own
// launchd state, authenticated shutdown and file ownership; never signal a PID.
export function documentService({
  binary,
  data,
  cwd,
  environment,
  address,
  signal,
  run = execute,
}) {
  let attempted = false;
  const observations = [];
  const call = async (args, cleanup = false) =>
    run(binary, ['service', ...args, '--data-dir', data], {
      cwd,
      env: environment,
      encoding: 'utf8',
      timeout: 45000,
      maxBuffer: 128 << 10,
      ...(signal && !cleanup ? { signal } : {}),
    });
  const status = async (cleanup = false) =>
    known(JSON.parse((await call(['status', '--json'], cleanup)).stdout));
  return {
    observations,
    async install() {
      attempted = true;
      await call(['install', '--addr', address]);
      const value = await status();
      assert.equal(value.installed, true);
      assert.equal(value.loaded, false);
      assert.equal(value.running, false);
      await assert.rejects(lstat(join(data, 'desktop.sqlite')), {
        code: 'ENOENT',
      });
      observations.push({ action: 'install', ...value });
    },
    async start(ready) {
      const log = join(data, 'logs/server.log');
      let offset = 0;
      try {
        offset = (await lstat(log)).size;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
      await call(['start']);
      const deadline = Date.now() + 30000;
      while (Date.now() < deadline) {
        signal?.throwIfAborted();
        const value = await status();
        if (value.running && value.loaded) {
          let announced = false;
          try {
            const info = await lstat(log);
            assert.ok(
              info.isFile() && info.size <= 1 << 20,
              'Unexpected service log inventory; preserve the fixture.',
            );
            announced = (await readFile(log, 'utf8'))
              .slice(offset)
              .includes(`Open Camp: http://${address}\n`);
          } catch (error) {
            if (error.code !== 'ENOENT') throw error;
          }
          if (announced && (await ready())) {
            observations.push({ action: 'start', ...value });
            return;
          }
        }
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      throw new Error(
        'The owned Server service did not announce its listener and become healthy.',
      );
    },
    async stop(cleanup = false) {
      await call(['stop'], cleanup);
      const value = await status(cleanup);
      assert.equal(value.loaded, false);
      assert.equal(value.running, false);
      assert.equal(value.installed, true);
      observations.push({ action: 'stop', ...value });
    },
    async remove() {
      if (!attempted) return;
      const before = await status(true);
      if (before.installed || before.loaded) await call(['remove'], true);
      const after = await status(true);
      assert.equal(after.state, 'absent');
      assert.equal(after.installed, false);
      assert.equal(after.loaded, false);
      assert.equal(after.running, false);
      observations.push({ action: 'remove', ...after });
    },
  };
}

export function assertDocumentServiceReceipt(
  report,
  { version, sourceCommit, platform } = {},
) {
  assert.equal(report?.status, 'passed');
  assert.equal(report.mode, 'service');
  assert.equal(report.cleanupConfirmed, true);
  assert.equal(report.retainedDataPreserved, true);
  assert.equal(report.readerJobsRemaining, 0);
  assert.equal(report.modelRequests, 0);
  assert.equal(report.sourceModified, false);
  for (const [key, value] of Object.entries({
    version,
    sourceCommit,
    platform,
  }))
    if (value !== undefined) assert.equal(report[key], value);
  assert.match(report.binarySHA256, /^[a-f0-9]{64}$/);
  assert.ok(
    report.service?.some(
      (item) => item.action === 'remove' && item.state === 'absent',
    ),
  );
}
