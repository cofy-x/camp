import { Buffer } from 'node:buffer';
import { createHash, randomUUID } from 'node:crypto';
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { createInterface } from 'node:readline';
import path from 'node:path';
import process from 'node:process';
import { openHome } from './home.mjs';
import { failure, readState } from './state.mjs';
import { scanTree } from './tree.mjs';

if (
  process.argv.length !== 3 ||
  process.platform !== 'darwin' ||
  process.arch !== 'arm64'
)
  throw new Error(
    'The checkpoint bridge requires an actual ARM64 Mac and a private configuration file.',
  );
const config = JSON.parse(await readFile(process.argv[2], 'utf8'));
if (config.mode !== 'checkpoint-worker')
  throw new Error('Select the checkpoint worker explicitly.');
const dataRoot = await realpath(config.dataRoot);

async function ownedDirectory(directory) {
  const resolved = await realpath(directory);
  const relative = path.relative(dataRoot, resolved);
  if (
    relative === '..' ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  )
    throw failure(
      'Unauthorized',
      'Checkpoint paths must remain inside private Runner storage.',
    );
  return resolved;
}

const home = await ownedDirectory(config.home);
const { worker, checkpoints } = await openHome({ ...config, home });

function summary(record) {
  return {
    operationId: record.binding.operationId,
    workspaceId: record.binding.workspaceId,
    attemptId: record.binding.attemptId,
    generation: record.binding.generation,
    digest: record.binding.digest,
    parentCheckpoint: record.binding.parentCheckpoint,
    checkpoint: record.workerReceipt?.checkpoint.head ?? null,
    storeId: record.binding.storeId,
    captureRoot: record.captureRoot,
    state: record.state,
    disposable: true,
    durableDelivery: false,
  };
}

async function status() {
  const current = await worker.request({ op: 'status' });
  const directory = path.join(checkpoints.root, 'workspaces');
  let names;
  try {
    names = await readdir(directory);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    names = [];
  }
  const pending = [];
  for (const name of names) {
    if (!/^[a-f0-9]{64}\.json$/.test(name))
      throw failure(
        'Unknown',
        'Unrecognized checkpoint receipt storage is retained.',
      );
    const namespace = await readState(path.join(directory, name));
    if (!namespace.pending) continue;
    const observed = await checkpoints.observe(namespace.pending);
    if (observed.state !== 'confirmed') pending.push(namespace.pending);
  }
  return { ...current, pending, ready: pending.length === 0 };
}

async function selftest() {
  const source = await mkdtemp(path.join(home, 'selftest-'));
  const workspaceId = createHash('sha256').update(randomUUID()).digest('hex');
  await mkdir(path.join(source, 'empty'), { mode: 0o750 });
  await mkdir(path.join(source, '.git'), { mode: 0o700 });
  await writeFile(
    path.join(source, '.git/HEAD'),
    'controlled worker selftest\n',
  );
  await writeFile(path.join(source, 'binary'), Buffer.from([0, 255, 128, 1]), {
    mode: 0o755,
  });
  await symlink('binary', path.join(source, 'link'));
  const record = await checkpoints.checkpoint(
    source,
    workspaceId,
    'worker-selftest',
    { quiescent: true },
  );
  const restored = await checkpoints.restore(
    record.binding.operationId,
    path.join(home, 'selftest-restores'),
  );
  if ((await scanTree(restored.root)).digest !== record.binding.digest)
    throw failure('Mismatch', 'The worker self-test restore did not match.');
  return summary(record);
}

const methods = {
  'runtime.status': status,
  'worker.selftest': selftest,
  'worker.check': async (params) => {
    const namespace = await readState(
      checkpoints.namespaceFile(params.workspaceId),
    );
    if (namespace?.pending)
      throw failure(
        'Unknown',
        'This native workspace retains an uncertain checkpoint; tools and models are not admitted.',
      );
    if (namespace?.operationId) {
      const receipt = await worker.request({
        op: 'observe',
        args: { operationId: namespace.operationId },
      });
      if (
        receipt.state !== 'verified' ||
        receipt.binding.generation !== namespace.generation ||
        receipt.binding.storeId !== namespace.storeId
      )
        throw failure(
          'LostStore',
          'The native workspace no longer has its matching verified worker checkpoint.',
        );
    } else await worker.request({ op: 'status' });
    return { ready: true };
  },
  'worker.capture': async (params) => {
    const current = await status();
    if (!current.ready)
      throw failure(
        'Unknown',
        'Checkpoint recovery is pending; no new tool result will be committed.',
      );
    const directory = await ownedDirectory(params.directory);
    return summary(
      await checkpoints.checkpoint(
        directory,
        params.workspaceId,
        params.attemptId,
        { quiescent: params.quiescent },
      ),
    );
  },
  'worker.observe': async (params) =>
    summary(await checkpoints.observe(params.operationId)),
  'worker.publication': async (params) =>
    checkpoints.publication(params.operationId, params.excluded),
  'worker.fork': async (params) =>
    summary(
      await checkpoints.fork(
        params.sourceOperation,
        params.workspaceId,
        params.attemptId,
      ),
    ),
  'worker.restore': async (params) => {
    const parent = await ownedDirectory(params.parent);
    return checkpoints.restore(params.operationId, parent);
  },
};
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
try {
  for await (const line of lines) {
    let request;
    try {
      if (Buffer.byteLength(line) > 64 << 10)
        throw failure('Capacity', 'Checkpoint bridge request exceeds 64 KiB.');
      request = JSON.parse(line);
      if (
        typeof request.id !== 'string' ||
        !request.id ||
        request.id.length > 128
      )
        throw failure(
          'InvalidInput',
          'A checkpoint request needs a stable request ID.',
        );
      if (request.method === 'shutdown') {
        await worker.close();
        process.stdout.write(
          `${JSON.stringify({ id: request.id, result: { closed: true } })}\n`,
        );
        break;
      }
      if (!Object.hasOwn(methods, request.method))
        throw failure(
          'UnknownMethod',
          'Unsupported checkpoint bridge operation.',
        );
      const result = await methods[request.method](request.params ?? {});
      process.stdout.write(`${JSON.stringify({ id: request.id, result })}\n`);
    } catch (error) {
      process.stdout.write(
        `${JSON.stringify({ id: request?.id ?? null, error: { code: error.code ?? 'Unknown', message: error.message } })}\n`,
      );
    }
  }
} finally {
  lines.close();
  await worker.close();
}
