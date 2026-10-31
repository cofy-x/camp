import { failure } from './state.mjs';

export const digestPattern = /^[a-f0-9]{64}$/;
// LayerFS public history IDs are tagged 33-byte identities, not SHA-256
// content digests. 0x12 is the pinned SDK's Commit tag.
export const checkpointPattern = /^12[a-f0-9]{64}$/;
export const uuidPattern =
  /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;

export function validateBinding(binding) {
  if (
    !binding ||
    !uuidPattern.test(binding.operationId) ||
    !uuidPattern.test(binding.storeId) ||
    !digestPattern.test(binding.workspaceId) ||
    !digestPattern.test(binding.digest) ||
    !Number.isSafeInteger(binding.generation) ||
    binding.generation < 1 ||
    !(
      binding.parentCheckpoint === null ||
      checkpointPattern.test(binding.parentCheckpoint)
    )
  )
    throw failure('InvalidInput', 'Invalid checkpoint identity or generation.');
  for (const field of ['runnerId', 'storageId', 'attemptId'])
    if (
      typeof binding[field] !== 'string' ||
      !/^[a-zA-Z0-9_-]{1,128}$/.test(binding[field])
    )
      throw failure('InvalidInput', 'Invalid checkpoint owner identity.');
  const normalized = {
    runnerId: binding.runnerId,
    storageId: binding.storageId,
    workspaceId: binding.workspaceId,
    attemptId: binding.attemptId,
    operationId: binding.operationId,
    generation: binding.generation,
    digest: binding.digest,
    parentCheckpoint: binding.parentCheckpoint,
    storeId: binding.storeId,
  };
  if (binding.recoveryFrom !== undefined) {
    const from = binding.recoveryFrom;
    if (
      !from ||
      !uuidPattern.test(from.operationId) ||
      !uuidPattern.test(from.storeId) ||
      from.storeId === binding.storeId ||
      !Number.isSafeInteger(from.generation) ||
      from.generation < 1 ||
      from.digest !== binding.digest ||
      !['confirmed', 'uncertain'].includes(from.outcome) ||
      !(from.checkpoint === null || checkpointPattern.test(from.checkpoint)) ||
      !(
        from.parentCheckpoint === null ||
        checkpointPattern.test(from.parentCheckpoint)
      ) ||
      binding.generation !== 1 ||
      binding.parentCheckpoint !== null
    )
      throw failure(
        'InvalidInput',
        'A reconstruction requires a new Store and operation with exact retained snapshot provenance.',
      );
    normalized.recoveryFrom = {
      operationId: from.operationId,
      storeId: from.storeId,
      generation: from.generation,
      digest: from.digest,
      checkpoint: from.checkpoint,
      parentCheckpoint: from.parentCheckpoint,
      outcome: from.outcome,
    };
  }
  return normalized;
}
