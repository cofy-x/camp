// Private host administration state. No Runner credentials or worker client.
import { Buffer } from 'node:buffer';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { failure, saveState } from './state.mjs';

export async function ownedDirectory(directory, create = false) {
  if (create) {
    try {
      await mkdir(directory, { mode: 0o700 });
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
    }
  }
  const stat = await lstat(directory);
  if (
    !stat.isDirectory() ||
    stat.uid !== process.getuid() ||
    stat.mode & 0o077 ||
    (await realpath(directory)) !== directory
  )
    throw failure(
      'Unauthorized',
      'Storage administration requires private owned directories without aliases.',
    );
  return directory;
}

export async function storageContext(
  { dataRoot, runnerId, storageId },
  create = false,
) {
  if (
    ![runnerId, storageId].every(
      (v) => typeof v === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(v),
    )
  )
    throw failure(
      'InvalidIdentity',
      'Storage administration requires the registered Runner identity.',
    );
  const stat = await lstat(dataRoot);
  if (!stat.isDirectory() || stat.uid !== process.getuid() || stat.mode & 0o077)
    throw failure(
      'Unauthorized',
      'Storage administration requires private owned Runner storage.',
    );
  dataRoot = await realpath(dataRoot);
  const admin = path.join(dataRoot, 'checkpoint-storage');
  try {
    await ownedDirectory(admin);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    if (create) await ownedDirectory(admin, true);
  }
  return { dataRoot, admin, runnerId, storageId };
}

export async function readControl(file, maximum = 32 << 20) {
  let handle;
  try {
    handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
  try {
    const before = await handle.stat();
    if (
      !before.isFile() ||
      before.uid !== process.getuid() ||
      before.mode & 0o077 ||
      before.nlink !== 1 ||
      before.size > maximum
    )
      throw failure(
        'UnsafeMetadata',
        'Storage administration metadata is unsafe or exceeds its bound.',
      );
    const bytes = Buffer.alloc(before.size + 1);
    let length = 0;
    while (length < bytes.length) {
      const part = await handle.read(
        bytes,
        length,
        bytes.length - length,
        null,
      );
      if (!part.bytesRead) break;
      length += part.bytesRead;
    }
    const after = await handle.stat();
    if (
      length !== before.size ||
      before.ino !== after.ino ||
      before.size !== after.size ||
      before.mtimeMs !== after.mtimeMs ||
      before.ctimeMs !== after.ctimeMs
    )
      throw failure(
        'ChangedDuringInspection',
        'Storage metadata changed while being read.',
      );
    return JSON.parse(bytes.subarray(0, length).toString('utf8'));
  } finally {
    await handle.close();
  }
}

export async function writeControl(file, value) {
  if (Buffer.byteLength(JSON.stringify(value)) > 32 << 20)
    throw failure(
      'Capacity',
      'Storage administration metadata exceeds its bound.',
    );
  await ownedDirectory(path.dirname(file));
  await readControl(file);
  await saveState(file, value);
}

export function ownedRecord(context, value, kind) {
  if (
    !value ||
    value.schema !== 1 ||
    value.kind !== kind ||
    value.runnerId !== context.runnerId ||
    value.storageId !== context.storageId
  )
    throw failure(
      'InvalidIdentity',
      'Storage metadata has another owner or unsupported format.',
    );
  return value;
}
