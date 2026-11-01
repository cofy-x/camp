// Read-only resolution shared by live worker control and offline inspection.
import { realpath } from 'node:fs/promises';
import path from 'node:path';
import { uuidPattern } from './contracts.mjs';
import { failure, readState } from './state.mjs';

export async function selectedEpoch(home, read = readState) {
  const selected = await read(path.join(home, 'current.json'));
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

export function resolveCapture(record, base) {
  if (!record) return record;
  if (record.captureRelative !== undefined) {
    const name = record.captureRelative;
    if (
      typeof name !== 'string' ||
      !name ||
      path.isAbsolute(name) ||
      name.includes('\\') ||
      name.includes('\0') ||
      name.split('/').some((part) => !part || part === '.' || part === '..')
    )
      throw failure(
        'Unauthorized',
        'The retained capture reference escapes its owning home.',
      );
    return { ...record, captureRoot: path.join(base, name) };
  }
  const relative = path.relative(base, record.captureRoot);
  if (
    !relative ||
    relative === '..' ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  )
    throw failure(
      'RelocatedLegacyCapture',
      'An absolute-only capture receipt is retained for explicit inspection; its relocated path is not guessed.',
    );
  return record;
}
