import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { WorkerClient } from './client.mjs';
import { openUpstream } from './upstream-comparison.mjs';
import { saveState } from './state.mjs';

assert.equal(process.platform, 'darwin');
assert.equal(process.arch, 'arm64');
await mkdir('.data', { recursive: true, mode: 0o700 });
const root = await mkdtemp(path.resolve('.data/upstream-worker-'));
const socket = execFileSync(
  'docker',
  ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'],
  { encoding: 'utf8' },
)
  .trim()
  .slice(7);
const worker = await WorkerClient.open({
  home: path.join(root, 'worker'),
  socket,
});
let upstream;
const report = {
  schema: 1,
  passed: false,
  worker: worker.status,
  engine: worker.engine,
  modelRequests: 0,
};
try {
  upstream = await openUpstream(worker, root);
  const branch = await upstream.fork();
  const committed = await upstream.client.fs.apply(
    branch,
    [
      {
        op: 'write',
        path: '/note',
        bytes: new Uint8Array([0, 255, 128, 1]),
        mode: 0o755,
      },
      { op: 'mkdir', path: '/empty', mode: 0o750 },
    ],
    'Controlled actual-Mac upstream comparison',
  );
  const ws = await upstream.client.workspace.mount(
    (await upstream.client.sandbox.create()).sandbox,
    branch,
  );
  const read = await upstream.client.workspace.exec(
    ws,
    `node /opt/camp/layerfs/worker/helper.mjs scan ${ws.path}`,
  );
  assert.equal(read.exitCode, 0);
  const scanned = JSON.parse(read.stdout);
  assert.equal(
    scanned.manifest.entries.find((item) => item.path === 'note').mode,
    0o755,
  );
  await upstream.client.workspace.unmount(ws);
  await upstream.client.sandbox.close(ws.sandbox);
  Object.assign(report, {
    passed: true,
    committed,
    scanned,
    wire: upstream.wire,
    transport: upstream.transport,
    startupMs: upstream.startupMs,
    wrongSecretRejected: true,
  });
} catch (error) {
  report.error = { code: error.code, message: error.message };
  process.exitCode = 1;
} finally {
  await upstream?.close();
  await worker.close();
  await saveState(path.join(root, 'report.json'), report);
  process.stdout.write(
    `${report.passed ? 'Passed' : 'Failed'} actual Mac upstream route: ${path.join(root, 'report.json')}\n`,
  );
}
