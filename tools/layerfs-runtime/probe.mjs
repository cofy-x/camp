import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { CampLayerFS } from './runtime.mjs';
import { errorRecord, shellQuote } from './validation.mjs';

const data = path.resolve(import.meta.dirname, '../../.data');
await mkdir(data, { recursive: true });
const dataRoot = await mkdtemp(path.join(data, 'layerfs-probe-'));
const sourceDir = path.join(dataRoot, 'source');
await mkdir(sourceDir);
const original = Buffer.from(
  Array.from({ length: 65536 }, (_, index) => index % 256),
);
await writeFile(path.join(sourceDir, 'binary.dat'), original);
await writeFile(
  path.join(sourceDir, 'brief.md'),
  '# Brief\nPrepare a short memo.\n',
);
const hostSecret = path.join(dataRoot, 'host-only-secret');
await writeFile(hostSecret, 'not a model credential', { mode: 0o600 });
const config = { dataRoot, home: path.join(dataRoot, 'home') };
await writeFile(
  path.join(dataRoot, 'config.json'),
  `${JSON.stringify(config, null, 2)}\n`,
  { mode: 0o600 },
);
const report = { sourceCommit: null, dataRoot, checks: [], passed: false };
let runtime;
let first;
let second;
try {
  runtime = await CampLayerFS.open(config);
  report.sourceCommit = runtime.installed.commit;
  report.image = runtime.installed.image;
  const prepared = await runtime.prepare({
    sourceDir,
    baselineKey: 'keyless-probe-v1',
  });
  report.projectId = prepared.projectId;
  const reused = await runtime.prepare({
    sourceDir,
    baselineKey: 'keyless-probe-v1',
  });
  assert.equal(reused.reused, true);
  report.checks.push(
    'Prepare imports the source once and reuses the exact baseline.',
  );
  first = await runtime.create({
    projectId: prepared.projectId,
    runId: 'writer',
  });
  second = await runtime.create({
    projectId: prepared.projectId,
    runId: 'reviewer',
  });
  report.workspaces = [first, second];
  assert.notEqual(first.containerName, second.containerName);
  assert.deepEqual(
    await runtime.create({ projectId: prepared.projectId, runId: 'writer' }),
    first,
  );
  report.checks.push(
    'Two Runs receive distinct Sandbox containers; the same Run receipt is idempotent.',
  );
  const client = await runtime.client(prepared.projectId);
  const ws = (record) => ({
    sandbox: record.sandboxId,
    path: record.mountPath,
  });
  const privacy = await client.workspace.exec(
    ws(first),
    `set -eu; test "$(id -u)" != 0; for file in /layerfs-store/global/store.sqlite /layerfs-local/config/daemon.setup /proc/1/environ /proc/1/root/layerfs-store/global/store.sqlite; do test ! -r "$file"; done; test ! -e ${shellQuote(hostSecret)}; test ! -w /dev/fuse; printf 'private backing denied\\n'`,
    'Check the unprivileged command boundary',
  );
  assert.equal(privacy.exitCode, 0, privacy.stderr);
  report.checks.push(
    'Non-root commands cannot read Store, daemon setup, PID 1 environment, proc aliases or host-only data, and cannot write /dev/fuse.',
  );
  const daemon = (await runtime.runtime.sandbox.list()).find(
    (sandbox) => sandbox.name === first.containerName,
  );
  const controlPort = Number(daemon?.ports[0]?.container.split('/')[0]);
  assert.ok(Number.isSafeInteger(controlPort) && controlPort > 0);
  const unauthenticated = `const net = require('node:net');
    const payloads = [Buffer.from('{"method":"workspace.commit"}\\n'), Buffer.concat([Buffer.from([0,32]), Buffer.alloc(32)])];
    (async () => {
      for (const payload of payloads) await new Promise((resolve, reject) => {
        let connected = false, bytes = 0;
        const socket = net.connect({host:'127.0.0.1',port:${controlPort}});
        socket.setTimeout(1500, () => { socket.destroy(); reject(new Error('Control endpoint did not reject the invalid handshake')); });
        socket.on('connect', () => { connected=true; socket.write(payload); });
        socket.on('data', chunk => { bytes += chunk.length; });
        socket.on('error', error => { if(error.code !== 'ECONNRESET') reject(error); });
        socket.on('close', () => connected && bytes === 0 ? resolve() : reject(new Error('Unexpected control response')));
      });
      console.log('Unauthenticated TCP connected but plaintext and invalid Noise records were rejected');
    })().catch(error => {console.error(error.message);process.exitCode=1;});`;
  const rejected = await client.workspace.exec(
    ws(first),
    `node -e ${shellQuote(unauthenticated)}`,
    'Verify daemon control requires an authenticated peer',
  );
  assert.equal(rejected.exitCode, 0, rejected.stderr);
  report.checks.push(
    'The daemon TCP socket is reachable before Camp command restrictions, but rejects plaintext RPC and an invalid Noise handshake without returning control data.',
  );
  const unixSockets = await client.workspace.exec(
    ws(first),
    `node -e ${shellQuote("const fs=require('node:fs'); const paths=fs.readFileSync('/proc/net/unix','utf8').trim().split('\\n').slice(1).map(line=>line.trim().split(/\\s+/).slice(7).join(' ')).filter(Boolean); if(paths.length){console.error(JSON.stringify(paths));process.exitCode=1;}else console.log('No named or abstract Unix sockets are exposed in the Sandbox network namespace');")}`,
    'Audit reachable Unix socket endpoints before command admission',
  );
  assert.equal(unixSockets.exitCode, 0, unixSockets.stderr);
  report.checks.push(
    'The Sandbox network namespace exposes no named or abstract Unix socket endpoints, including daemon or Docker control sockets.',
  );
  const write = await client.workspace.exec(
    ws(first),
    "printf '# Memo\\nUse a two-week pilot.\\n' > memo.md; cat brief.md",
    'Create a deterministic memo',
  );
  assert.equal(write.exitCode, 0, write.stderr);
  const isolated = await client.workspace.exec(
    ws(second),
    'test ! -e memo.md',
    'Check independent writable views',
  );
  assert.equal(isolated.exitCode, 0);
  const checkpoint = await runtime.checkpoint({
    workspaceId: first.workspaceId,
    message: 'Prepare memo',
    quiescent: true,
  });
  assert.equal(checkpoint.kind, 'Committed');
  report.checkpoint = checkpoint;
  report.checks.push(
    'A file created in one Workspace is absent in the other; explicit checkpoint saves it.',
  );
  const archive = await runtime.export({
    workspaceId: first.workspaceId,
    targetArchive: path.join(dataRoot, 'result.tar'),
    quiescent: true,
  });
  assert.deepEqual(
    execFileSync('tar', ['-xOf', archive.archivePath, './binary.dat']),
    original,
  );
  assert.equal(
    execFileSync('tar', ['-xOf', archive.archivePath, './memo.md'], {
      encoding: 'utf8',
    }),
    '# Memo\nUse a two-week pilot.\n',
  );
  assert.deepEqual(
    await readFile(path.join(sourceDir, 'binary.dat')),
    original,
  );
  await assert.rejects(readFile(path.join(sourceDir, 'memo.md')), {
    code: 'ENOENT',
  });
  report.archive = archive;
  report.checks.push(
    'Export preserves binary bytes and generated text; the imported original directory is unchanged.',
  );
  const history = await runtime.history({ projectId: prepared.projectId });
  assert.ok(JSON.stringify(history).includes('Prepare memo'));
  report.checks.push('The public history API exposes the saved checkpoint.');
  await runtime.closeWorkspace({ workspaceId: first.workspaceId });
  await runtime.closeWorkspace({
    workspaceId: second.workspaceId,
    discard: true,
  });
  const sandboxes = await client.project.list_sandboxes(1000, 0);
  assert.equal(sandboxes.items.length, 0);
  report.checks.push('Explicit unmount and close leave no running Sandbox.');
  await runtime.close();
  runtime = await CampLayerFS.open(config);
  assert.ok(
    JSON.stringify(
      await runtime.history({ projectId: prepared.projectId }),
    ).includes('Prepare memo'),
  );
  report.checks.push(
    'Orderly bridge restart reopens existing committed history without replaying work.',
  );
  await runtime.removeProject({ projectId: prepared.projectId });
  assert.equal(
    (await runtime.workspaces()).filter(
      (record) => record.projectId === prepared.projectId,
    ).length,
    0,
  );
  first = null;
  second = null;
  report.checks.push(
    'Removing the idle probe project deletes its disposable Store volume and closed workspace receipts.',
  );
  report.passed = true;
} catch (error) {
  report.error = errorRecord(error);
  process.exitCode = 1;
} finally {
  if (runtime) {
    for (const record of [first, second].filter(Boolean)) {
      try {
        await runtime.closeWorkspace({
          workspaceId: record.workspaceId,
          discard: true,
        });
      } catch (error) {
        (report.cleanupErrors ??= []).push(errorRecord(error));
      }
    }
    await runtime.close().catch((error) => {
      (report.cleanupErrors ??= []).push(errorRecord(error));
    });
  }
  report.originalSha256 = createHash('sha256').update(original).digest('hex');
  await writeFile(
    path.join(dataRoot, 'report.json'),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
}
