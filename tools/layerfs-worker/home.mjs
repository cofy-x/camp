import { mkdir, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import { WorkerClient } from './client.mjs';
import { MacCheckpoints, resolveCapture } from './checkpoint.mjs';
import { uuidPattern } from './contracts.mjs';
import { failure, readState } from './state.mjs';

export async function selectedEpoch(home) {
  const selected = await readState(path.join(home, 'current.json'));
  if (!selected) return { epoch: 'initial', root: home };
  if (
    selected.schema !== 1 ||
    !uuidPattern.test(selected.epoch) ||
    !uuidPattern.test(selected.storeId)
  )
    throw failure(
      'Unknown',
      'The selected worker epoch is incompatible; it is retained for explicit recovery.',
    );
  const root = path.join(home, 'epochs', selected.epoch);
  const resolved = await realpath(root);
  if (resolved !== root)
    throw failure(
      'Unauthorized',
      'Worker epoch storage cannot be a filesystem alias.',
    );
  return { ...selected, root };
}

export async function openHome(config) {
  const selected = await selectedEpoch(config.home);
  const worker = await WorkerClient.open({
    home: path.join(selected.root, 'control'),
    socket: config.docker,
    dockerBinary: config.dockerBinary,
    installation: config.installation,
    qualification: config.qualification === true,
  });
  if (selected.storeId && worker.status.storeId !== selected.storeId) {
    await worker.close();
    throw failure(
      'LostStore',
      'The selected epoch no longer has its recorded Store identity.',
    );
  }
  const checkpoints = new MacCheckpoints({
    root: path.join(selected.root, 'checkpoints'),
    captureBase: config.home,
    worker,
    runnerId: config.runnerId,
    storageId: config.storageId,
  });
  return { ...selected, worker, checkpoints };
}

export async function findOperation(home, operationId) {
  if (!uuidPattern.test(operationId))
    throw failure('InvalidInput', 'Invalid retained operation ID.');
  const roots = [home];
  await mkdir(home, { recursive: true, mode: 0o700 });
  const epochs = path.join(home, 'epochs');
  const names = await readdir(epochs).catch((error) => {
    if (error.code === 'ENOENT') return [];
    throw error;
  });
  if (names.length > 1000)
    throw failure(
      'Capacity',
      'Too many retained Store epochs to inspect in one command.',
    );
  for (const name of names)
    if (uuidPattern.test(name)) roots.push(path.join(epochs, name));
  for (const root of roots) {
    const record = await readState(
      path.join(root, 'checkpoints', 'operations', `${operationId}.json`),
    );
    if (record) return { root, record: resolveCapture(record, home) };
  }
  throw failure(
    'NotFound',
    'The retained operation is not present in this private Runner home.',
  );
}
