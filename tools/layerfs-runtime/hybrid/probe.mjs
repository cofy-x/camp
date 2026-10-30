import assert from 'node:assert/strict';
import { createCipheriv } from 'node:crypto';
import { spawn } from 'node:child_process';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { performance } from 'node:perf_hooks';
import { CampLayerFS } from '../runtime.mjs';
import { CheckpointMirror } from './checkpoint.mjs';
import { manifest } from './manifest.mjs';

if (process.platform !== 'linux')
  throw new Error(
    'This probe qualifies the Linux Store path; Mac transport is a separate acceptance',
  );
const output = path.resolve(process.argv[2] ?? 'dist/hybrid-proof.json');
const base = path.resolve('.data');
await mkdir(base, { recursive: true });
const root = await mkdtemp(path.join(base, 'hybrid-proof-'));
const source = path.join(root, 'empty');
await mkdir(source);
const report = {
  schema: 1,
  platform: `${process.platform}/${process.arch}`,
  node: process.version,
  actualMacTransport: false,
  actualWorkerTransport: false,
  completeVMMeasured: false,
  root,
  memoryScope:
    'Controller Node process including the in-process Linux Store; excludes Docker containers and any VM. Peak RSS may include inherited process history; do not interpret it as incremental worker cost.',
  baselineRSSBytes: process.memoryUsage().rss,
  baselinePeakRSSKiB: process.resourceUsage().maxRSS,
  observations: [],
  faults: {},
};
const timer = async (name, fn) => {
  const start = performance.now();
  const value = await fn();
  return { value, [name]: performance.now() - start };
};
const run = async (file, args) =>
  new Promise((resolve, reject) => {
    const child = spawn(file, args, { stdio: ['ignore', 'ignore', 'pipe'] });
    let diagnostic = '';
    child.stderr.on('data', (data) => {
      if (diagnostic.length < 8192) diagnostic += data;
    });
    child.on('error', reject);
    child.on('close', (code) =>
      code === 0
        ? resolve()
        : reject(new Error(`${file} exited ${code}: ${diagnostic}`)),
    );
  });
const shell = (value) => `'${value.replaceAll("'", "'\\''")}'`;
// A remounted FUSE view independently hashes actual bytes and executable bits.
const witness = `const fs=require('fs'),p=require('path'),c=require('crypto');let rows=[];function walk(d,r=''){for(const n of fs.readdirSync(d).sort()){const q=p.join(d,n),s=fs.lstatSync(q),name=r?r+'/'+n:n;if(s.isDirectory())walk(q,name);else{if(!s.isFile())throw Error('special file');const b=fs.readFileSync(q);rows.push({path:name,bytes:b.length,mode:s.mode&73?493:420,sha256:c.createHash('sha256').update(b).digest('hex')});}}}walk(process.cwd());rows.sort((a,b)=>a.path.localeCompare(b.path,'en'));console.log(c.createHash('sha256').update(JSON.stringify(rows)).digest('hex'));`;
const verify = async (client, branch, expected) => {
  const sandbox = (await client.sandbox.create()).sandbox;
  let workspace;
  try {
    workspace = await client.workspace.mount(sandbox, branch);
    const result = await client.workspace.exec(
      workspace,
      `/usr/local/bin/node -e ${shell(witness)}`,
      'Verify sealed checkpoint',
    );
    assert.equal(result.exitCode, 0, result.stderr);
    assert.equal(result.stdout.trim(), expected.sha256);
  } finally {
    if (workspace) await client.workspace.unmount(workspace, true);
    await client.sandbox.close(sandbox);
  }
};
let runtime, prepared;
try {
  const cold = await timer('coldSetupMs', async () => {
    runtime = await CampLayerFS.open({
      dataRoot: root,
      home: path.join(root, 'layerfs'),
      docker:
        process.env.CAMP_TEST_DOCKER_SOCKET ?? process.env.CAMP_DOCKER_SOCKET,
    });
    await runtime.beginReset();
    await runtime.reset();
    prepared = await runtime.prepare({
      sourceDir: source,
      baselineKey: 'hybrid-proof-empty',
    });
    return runtime.client(prepared.projectId);
  });
  report.coldSetupMs = cold.coldSetupMs;
  report.layerfsCommit = runtime.installed.commit;
  report.image = runtime.installed.image;
  const client = cold.value;
  const specs = [
    { name: 'text', files: 6, bytes: 128 },
    { name: '10000-files', files: 10000, bytes: 128 },
    { name: '128-MiB-binary', files: 16, bytes: 8 << 20 },
  ];
  for (const spec of specs) {
    for (let iteration = 0; iteration < 3; iteration++) {
      const directory = path.join(root, `${spec.name}-${iteration}`);
      await mkdir(directory);
      for (let index = 0; index < spec.files; index++) {
        const iv = Buffer.alloc(16);
        iv.writeUInt32BE(index, 12);
        const bytes =
          spec.name === 'text'
            ? Buffer.alloc(spec.bytes, 65 + index)
            : createCipheriv('aes-256-ctr', Buffer.alloc(32, 0x43), iv).update(
                Buffer.alloc(spec.bytes),
              );
        if (bytes.length > 8) bytes.writeUInt32LE(index, 0);
        await writeFile(
          path.join(directory, `file-${String(index).padStart(5, '0')}.bin`),
          bytes,
          { mode: index === 3 ? 0o755 : 0o644 },
        );
      }
      const initial = await timer('initialScanMs', () => manifest(directory));
      const incrementalBranch = `incremental-${spec.name.toLowerCase()}-${iteration}`;
      const fullBranch = `complete-${spec.name.toLowerCase()}-${iteration}`;
      const warm = await timer('warmStartMs', async () => {
        await client.branch.fork(incrementalBranch, 'main');
        await client.branch.fork(fullBranch, 'main');
      });
      const incremental = new CheckpointMirror(
        client,
        incrementalBranch,
        path.join(root, `${incrementalBranch}.json`),
      );
      const complete = new CheckpointMirror(
        client,
        fullBranch,
        path.join(root, `${fullBranch}.json`),
      );
      const seed = await timer('initialTransferCommitMs', () =>
        incremental.synchronize(initial.value, 1),
      );
      await complete.synchronize(initial.value, 1);
      await writeFile(
        path.join(directory, 'file-00000.bin'),
        Buffer.from('changed binary\0\xff', 'latin1'),
      );
      await rm(path.join(directory, 'file-00001.bin'));
      await rename(
        path.join(directory, 'file-00002.bin'),
        path.join(directory, 'renamed.bin'),
      );
      await chmod(path.join(directory, 'file-00003.bin'), 0o644);
      const sealed = await timer('scanMs', () => manifest(directory));
      const archive = path.join(root, `${spec.name}-${iteration}.tar`);
      const local = await timer('localArchiveMs', () =>
        run('/bin/tar', ['--format=pax', '-cf', archive, '-C', directory, '.']),
      );
      const delta = await timer('incrementalTransferCommitMs', () =>
        incremental.synchronize(sealed.value, 2),
      );
      const full = await timer('completeTransferCommitMs', () =>
        complete.synchronize(sealed.value, 2, { full: true }),
      );
      await verify(client, incrementalBranch, sealed.value);
      await verify(client, fullBranch, sealed.value);
      const unchanged = await incremental.synchronize(sealed.value, 3);
      assert.equal(unchanged.commit, delta.value.commit);
      assert.equal(unchanged.operations, 0);
      assert.equal(unchanged.transferredBytes, 0);
      report.observations.push({
        workload: spec.name,
        iteration,
        files: sealed.value.files,
        acceptedBytes: sealed.value.bytes,
        initialScanMs: initial.initialScanMs,
        warmStartMs: warm.warmStartMs,
        initialTransferCommitMs: seed.initialTransferCommitMs,
        scanMs: sealed.scanMs,
        localArchiveMs: local.localArchiveMs,
        incrementalTransferCommitMs: delta.incrementalTransferCommitMs,
        completeTransferCommitMs: full.completeTransferCommitMs,
        incrementalTransferredBytes: delta.value.transferredBytes,
        completeTransferredBytes: full.value.transferredBytes,
        digest: sealed.value.sha256,
        workerCommit: delta.value.commit,
        rssBytes: process.memoryUsage().rss,
        peakRSSKiB: process.resourceUsage().maxRSS,
        remountedBytesAndModesMatch: true,
        unchangedGenerationPreservesHead: true,
      });
      process.stdout.write(
        `Verified ${spec.name}, iteration ${iteration + 1}: scan ${sealed.scanMs.toFixed(1)} ms, incremental ${delta.incrementalTransferCommitMs.toFixed(1)} ms, complete ${full.completeTransferCommitMs.toFixed(1)} ms\n`,
      );
      await rm(directory, { recursive: true });
      await rm(archive);
    }
  }
  const faultDirectory = path.join(root, 'fault-input');
  await mkdir(faultDirectory);
  await writeFile(
    path.join(faultDirectory, 'retained.bin'),
    Buffer.from([0, 255, 1]),
  );
  const frozen = await manifest(faultDirectory);
  for (const lost of [false, true]) {
    const branch = lost ? 'lost-reply' : 'interrupted-transfer';
    await client.branch.fork(branch, 'main');
    const receipt = path.join(root, `${branch}.json`);
    let attempts = 0;
    const apply = async (...args) => {
      attempts++;
      if (lost) await client.fs.apply(...args);
      throw new Error('Injected transport outcome uncertainty');
    };
    const mirror = new CheckpointMirror(client, branch, receipt);
    await assert.rejects(
      mirror.synchronize(frozen, 1, { apply }),
      /uncertainty/,
    );
    assert.equal(JSON.parse(await readFile(receipt)).state, 'uncertain');
    await assert.rejects(
      new CheckpointMirror(client, branch, receipt).synchronize(frozen, 1, {
        apply,
      }),
      /will not be replayed/,
    );
    assert.equal(attempts, 1);
    assert.equal((await manifest(faultDirectory)).sha256, frozen.sha256);
    report.faults[branch] = {
      injectedAtSDKBoundary: true,
      actualCommitBeforeLostReply: lost,
      nativeInputRetained: true,
      automaticReplayRefused: true,
      attempts,
    };
  }
  const link = path.join(root, 'symlink-input');
  await mkdir(link);
  await run('/bin/ln', ['-s', source, path.join(link, 'outside')]);
  await assert.rejects(manifest(link), /unsupported files/);
  report.symlinkCheckpointRefused = true;
  report.ok = true;
} catch (error) {
  report.ok = false;
  report.error = String(error.stack ?? error);
  throw error;
} finally {
  if (runtime) {
    if (prepared)
      await runtime.removeProject({ projectId: prepared.projectId });
    await runtime.close();
  }
  await mkdir(path.dirname(output), { recursive: true });
  await writeFile(output, JSON.stringify(report, null, 2) + '\n', {
    mode: 0o600,
  });
}
