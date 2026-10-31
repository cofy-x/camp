import { createHash, randomUUID } from 'node:crypto';
import { lstat, mkdir, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { WorkerClient } from './client.mjs';
import { MacCheckpoints } from './checkpoint.mjs';
import { findOperation, selectedEpoch } from './home.mjs';
import { uuidPattern } from './contracts.mjs';
import { failure, readState, saveState } from './state.mjs';
import { encodeCapture } from './transfer.mjs';
import { loadCapture, restoreTree } from './tree.mjs';

const within = (root, file) => {
  const relative = path.relative(root, file);
  return (
    relative !== '..' &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
  );
};

function describe(record) {
  return {
    operationId: record.binding.operationId,
    workspaceId: record.binding.workspaceId,
    generation: record.binding.generation,
    digest: record.binding.digest,
    storeId: record.binding.storeId,
    parentCheckpoint: record.binding.parentCheckpoint,
    checkpoint: record.workerReceipt?.checkpoint.head ?? null,
    state: record.state,
    recoveryFrom: record.binding.recoveryFrom,
    captureRoot: record.captureRoot,
    durableDelivery: false,
  };
}

export async function administer(config) {
  if (process.platform !== 'darwin' || process.arch !== 'arm64')
    throw failure(
      'Unsupported',
      'Checkpoint administration requires the native ARM64 Mac.',
    );
  const dataRoot = await realpath(config.dataRoot),
    home = await realpath(config.home);
  const owner = await lstat(home);
  if (
    !within(dataRoot, home) ||
    !owner.isDirectory() ||
    owner.uid !== process.getuid() ||
    owner.mode & 0o077
  )
    throw failure(
      'Unauthorized',
      'Checkpoint administration requires private owned Runner storage.',
    );
  const selected = await selectedEpoch(home);
  if (config.action === 'status') {
    const roots = [home],
      epochs = await readdir(path.join(home, 'epochs')).catch((error) => {
        if (error.code === 'ENOENT') return [];
        throw error;
      });
    for (const epoch of epochs)
      if (uuidPattern.test(epoch)) roots.push(path.join(home, 'epochs', epoch));
    const operations = [];
    for (const root of roots) {
      const names = await readdir(
        path.join(root, 'checkpoints', 'operations'),
      ).catch((error) => {
        if (error.code === 'ENOENT') return [];
        throw error;
      });
      for (const name of names)
        if (/^[a-f0-9-]{36}\.json$/.test(name)) {
          const record = await readState(
            path.join(root, 'checkpoints', 'operations', name),
          );
          operations.push({
            ...describe(record),
            selectedEpoch: root === selected.root,
          });
        }
    }
    const recoveries = [];
    const recoveryNames = await readdir(path.join(home, 'recoveries')).catch(
      (error) => {
        if (error.code === 'ENOENT') return [];
        throw error;
      },
    );
    for (const name of recoveryNames)
      if (/^[a-f0-9-]{36}\.json$/.test(name))
        recoveries.push(await readState(path.join(home, 'recoveries', name)));
    return {
      selectedEpoch: selected.epoch,
      workerObserved: false,
      lastStart: await readState(
        path.join(selected.root, 'control', 'last-start.json'),
      ),
      operations,
      recoveries,
      published: false,
    };
  }
  if (config.action === 'observe-reconstruction')
    return observeReconstruction(config, home);
  const { root, record } = await findOperation(home, config.operationId);
  if (
    record.binding.runnerId !== config.runnerId ||
    record.binding.storageId !== config.storageId
  )
    throw failure(
      'Unauthorized',
      'The retained operation belongs to a different Runner storage identity.',
    );
  const sourceRoot = await realpath(record.captureRoot);
  if (!within(home, sourceRoot))
    throw failure(
      'Unauthorized',
      'The retained native capture escaped its owned worker home.',
    );
  const capture = await loadCapture(sourceRoot);
  if (capture.digest !== record.binding.digest)
    throw failure(
      'Corrupt',
      'The retained native capture differs from its frozen operation.',
    );
  if (config.action === 'restore' && config.fromNative) {
    const parent = await privateDestination(config.parent);
    return {
      kind: 'retained-native-restoration',
      source: describe(record),
      ...(await restoreTree(capture, parent)),
      currentWorkerVerified: false,
      published: false,
      modelRequests: 0,
    };
  }
  if (config.action === 'reconstruct') {
    // Validate every immutable blob before making a new Store. This is a new
    // reconstruction operation; neither a mutation ID nor a model is replayed.
    for await (const chunk of encodeCapture(capture)) void chunk;
    const epoch = randomUUID(),
      recoveryId = randomUUID();
    const epochRoot = path.join(home, 'epochs', epoch);
    await mkdir(path.join(home, 'epochs'), { recursive: true, mode: 0o700 });
    await mkdir(epochRoot, { mode: 0o700 });
    const intentFile = path.join(home, 'recoveries', `${recoveryId}.json`);
    const intent = {
      schema: 1,
      recoveryId,
      sourceOperation: config.operationId,
      source: describe(record),
      epoch,
      previousEpoch: selected.epoch,
      state: 'store-create-dispatched',
    };
    await saveState(intentFile, intent);
    let worker;
    try {
      worker = await WorkerClient.open({
        home: path.join(epochRoot, 'control'),
        socket: config.docker,
        dockerBinary: config.dockerBinary,
        installation: config.installation,
      });
      const checkpoints = new MacCheckpoints({
        root: path.join(epochRoot, 'checkpoints'),
        worker,
        runnerId: config.runnerId,
        storageId: config.storageId,
      });
      const recoveryFrom = {
        operationId: record.binding.operationId,
        storeId: record.binding.storeId,
        generation: record.binding.generation,
        digest: record.binding.digest,
        checkpoint: record.workerReceipt?.checkpoint.head ?? null,
        parentCheckpoint: record.binding.parentCheckpoint,
        outcome: record.state === 'confirmed' ? 'confirmed' : 'uncertain',
      };
      const workspaceId = createHash('sha256')
        .update(`${record.binding.workspaceId}:${epoch}`)
        .digest('hex');
      intent.state = 'reconstruction-dispatched';
      intent.storeId = worker.status.storeId;
      await saveState(intentFile, intent);
      const reconstructed = await checkpoints.submitCapture(
        capture,
        workspaceId,
        `reconstruct-${recoveryId}`,
        { recoveryFrom },
      );
      intent.state = 'verified';
      intent.result = describe(reconstructed);
      await saveState(intentFile, intent);
      await worker.close();
      worker = null;
      if (config.qualification?.fault === 'before-select')
        throw failure(
          'InjectedRecoveryPause',
          'Controlled interruption before selecting a verified reconstructed epoch.',
        );
      const current = await selectedEpoch(home);
      if (current.epoch !== selected.epoch)
        throw failure(
          'Conflict',
          'Another Store epoch was selected; this completed reconstruction remains retained.',
        );
      await saveState(path.join(home, 'current.json'), {
        schema: 1,
        epoch,
        storeId: reconstructed.binding.storeId,
        recoveryId,
        operationId: reconstructed.binding.operationId,
        previousEpoch: selected.epoch,
      });
      intent.state = 'selected';
      await saveState(intentFile, intent);
      return {
        kind: 'new-store-reconstruction',
        recoveryId,
        source: describe(record),
        result: describe(reconstructed),
        selectedEpoch: epoch,
        previousEpoch: selected.epoch,
        published: false,
        modelRequests: 0,
      };
    } catch (error) {
      intent.error = {
        code: error.code ?? 'Unknown',
        message: error.message,
        operationId: error.operationId,
      };
      await saveState(intentFile, intent);
      error.recoveryId = recoveryId;
      throw error;
    } finally {
      await worker?.close().catch(() => {});
    }
  }
  if (!['observe', 'restore'].includes(config.action))
    throw failure('InvalidInput', 'Unknown checkpoint administration action.');
  const worker = await WorkerClient.open({
    home: path.join(root, 'control'),
    socket: config.docker,
    dockerBinary: config.dockerBinary,
    installation: config.installation,
  });
  try {
    const checkpoints = new MacCheckpoints({
      root: path.join(root, 'checkpoints'),
      worker,
      runnerId: config.runnerId,
      storageId: config.storageId,
    });
    if (config.action === 'observe') {
      const result = await checkpoints.observe(config.operationId);
      return {
        ...describe(result),
        observedWorkerState: result.observedWorkerState,
        published: false,
        modelRequests: 0,
      };
    }
    const parent = await privateDestination(config.parent);
    return {
      kind: 'verified-layerfs-restoration',
      source: describe(record),
      ...(await checkpoints.restore(config.operationId, parent)),
      published: false,
      modelRequests: 0,
    };
  } finally {
    await worker.close();
  }
}

async function observeReconstruction(config, home) {
  if (!uuidPattern.test(config.recoveryId))
    throw failure(
      'InvalidInput',
      'Choose a retained reconstruction intent ID.',
    );
  const intentFile = path.join(home, 'recoveries', `${config.recoveryId}.json`);
  const intent = await readState(intentFile);
  if (!intent || !uuidPattern.test(intent.epoch))
    throw failure('NotFound', 'The reconstruction intent is not available.');
  const current = await selectedEpoch(home);
  if (current.recoveryId === config.recoveryId && intent.state === 'selected')
    return {
      kind: 'selected-reconstruction',
      intent,
      published: false,
      modelRequests: 0,
    };
  if (current.epoch !== intent.previousEpoch)
    throw failure(
      'StaleGeneration',
      'The current Store epoch changed; an old reconstruction cannot become current.',
    );
  const root = path.join(home, 'epochs', intent.epoch);
  const names = await readdir(
    path.join(root, 'checkpoints', 'operations'),
  ).catch((error) => {
    if (error.code === 'ENOENT') return [];
    throw error;
  });
  const records = [];
  for (const name of names)
    if (/^[a-f0-9-]{36}\.json$/.test(name)) {
      const record = await readState(
        path.join(root, 'checkpoints', 'operations', name),
      );
      if (
        record.kind === 'reconstruction' &&
        record.binding.recoveryFrom?.operationId === intent.sourceOperation
      )
        records.push(record);
    }
  if (records.length !== 1)
    throw failure(
      'Unknown',
      'No unique reconstruction operation is known; nothing will be replayed.',
    );
  const worker = await WorkerClient.open({
    home: path.join(root, 'control'),
    socket: config.docker,
    dockerBinary: config.dockerBinary,
    installation: config.installation,
  });
  let result;
  try {
    const manager = new MacCheckpoints({
      root: path.join(root, 'checkpoints'),
      worker,
      runnerId: config.runnerId,
      storageId: config.storageId,
    });
    result = await manager.observe(records[0].binding.operationId);
  } finally {
    await worker.close();
  }
  if (result.state !== 'confirmed')
    return {
      kind: 'uncertain-reconstruction',
      intent,
      observedWorkerState: result.observedWorkerState,
      published: false,
      modelRequests: 0,
    };
  intent.state = 'verified';
  intent.result = describe(result);
  await saveState(intentFile, intent);
  await saveState(path.join(home, 'current.json'), {
    schema: 1,
    epoch: intent.epoch,
    storeId: result.binding.storeId,
    recoveryId: intent.recoveryId,
    operationId: result.binding.operationId,
    previousEpoch: intent.previousEpoch,
  });
  intent.state = 'selected';
  await saveState(intentFile, intent);
  return {
    kind: 'selected-reconstruction',
    intent,
    published: false,
    modelRequests: 0,
  };
}

async function privateDestination(parent) {
  if (typeof parent !== 'string' || !path.isAbsolute(parent))
    throw failure(
      'InvalidInput',
      'Choose an existing private absolute restoration parent.',
    );
  parent = await realpath(parent);
  const info = await lstat(parent);
  if (!info.isDirectory() || info.uid !== process.getuid() || info.mode & 0o077)
    throw failure(
      'Unauthorized',
      'Restore into an existing private owner-only directory. A fresh child is created without overwriting existing files.',
    );
  return parent;
}
