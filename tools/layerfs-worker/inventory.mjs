// The one bounded filesystem inventory used by live usage and stopped administration.
import { Buffer } from 'node:buffer';
import { constants } from 'node:fs';
import { lstat, open, opendir, realpath } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { failure } from './state.mjs';

const empty = () => ({
  logicalBytes: 0,
  files: 0,
  directories: 0,
  symlinks: 0,
});
const add = (target, value) => {
  for (const key of Object.keys(target)) target[key] += value[key];
};
const witness = (s) => `${s.dev}:${s.ino}:${s.size}:${s.mtimeMs}:${s.ctimeMs}`;

export async function storageInventory({
  dataRoot,
  limits = {},
  quarantine = true,
  deadlineMs = 30000,
}) {
  const deadline = Date.now() + Math.min(deadlineMs, 30000);
  const owner = await lstat(dataRoot);
  if (
    !owner.isDirectory() ||
    owner.uid !== process.getuid() ||
    owner.mode & 0o077
  )
    throw failure(
      'Unauthorized',
      'Storage inspection requires private owned Runner storage.',
    );
  dataRoot = await realpath(dataRoot);
  const home = path.join(dataRoot, 'layerfs-worker');
  const recovery = path.join(dataRoot, 'native-recovery');
  const trash = path.join(dataRoot, 'checkpoint-storage', 'quarantine');
  const maxEntries = Math.min(limits.maxEntries ?? 100000, 100000);
  const maxMetadataBytes = Math.min(
    limits.maxMetadataBytes ?? 32 << 20,
    32 << 20,
  );
  if (
    !Number.isSafeInteger(maxEntries) ||
    maxEntries < 1 ||
    !Number.isSafeInteger(maxMetadataBytes) ||
    maxMetadataBytes < 1
  )
    throw failure('InvalidInput', 'Choose positive bounded inspection limits.');
  let entries = 0,
    metadataBytes = 0,
    allocatedBytes = 0,
    allocationObserved = true;
  const nodes = new Map(),
    seen = new Set(),
    problems = [];
  const issue = (file, code) => {
    if (problems.length < 32)
      problems.push({ path: path.relative(dataRoot, file), code });
  };

  async function visit(file, depth = 0) {
    if (++entries > maxEntries || depth > 64 || Date.now() > deadline)
      throw failure('Capacity', 'The bounded storage inventory is incomplete.');
    const stat = await lstat(file);
    const total = empty();
    const node = { stat, total, children: [] };
    nodes.set(file, node);
    const id = `${stat.dev}:${stat.ino}`;
    if (!seen.has(id)) {
      seen.add(id);
      if (Number.isSafeInteger(stat.blocks))
        allocatedBytes += stat.blocks * 512;
      else allocationObserved = false;
    }
    if (stat.isDirectory()) {
      total.directories = 1;
      if ((await realpath(file)) !== file)
        throw failure(
          'Unauthorized',
          'A storage directory is a filesystem alias.',
        );
      const directory = await opendir(file);
      for await (const entry of directory) {
        const child = path.join(file, entry.name);
        node.children.push(child);
        try {
          add(total, await visit(child, depth + 1));
        } catch (error) {
          issue(child, error.code ?? 'Unreadable');
          if (error.code === 'Capacity') break;
        }
      }
      if (witness(stat) !== witness(await lstat(file)))
        issue(file, 'ChangedDuringInspection');
    } else if (stat.isFile() || stat.isSymbolicLink()) {
      total.logicalBytes = stat.size;
      total.files = stat.isFile() ? 1 : 0;
      total.symlinks = stat.isSymbolicLink() ? 1 : 0;
    } else issue(file, 'UnsupportedEntry');
    return total;
  }

  for (const root of [home, recovery, ...(quarantine ? [trash] : [])]) {
    try {
      const stat = await lstat(root);
      if (
        !stat.isDirectory() ||
        stat.uid !== process.getuid() ||
        stat.mode & 0o077
      ) {
        issue(root, 'UnsafeRoot');
        continue;
      }
      await visit(root);
    } catch (error) {
      if (error.code !== 'ENOENT') issue(root, error.code ?? 'Unreadable');
    }
  }

  const controls = new Map();

  async function readMetadata(file) {
    const node = nodes.get(file);
    if (!node) {
      try {
        await lstat(file);
      } catch (error) {
        if (error.code === 'ENOENT') return null;
        throw error;
      }
      throw failure(
        'ChangedDuringInspection',
        'Metadata was not included in the bounded inventory.',
      );
    }
    if (
      !node.stat.isFile() ||
      node.stat.uid !== process.getuid() ||
      node.stat.mode & 0o077 ||
      node.stat.size > 1 << 20
    )
      throw failure(
        'UnsafeMetadata',
        'A retained metadata file is unsafe or too large.',
      );
    metadataBytes += node.stat.size;
    if (metadataBytes > maxMetadataBytes)
      throw failure(
        'Capacity',
        'Retained metadata exceeds the inspection budget.',
      );
    const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
    try {
      if (witness(await handle.stat()) !== witness(node.stat))
        throw failure('ChangedDuringInspection', 'Retained metadata changed.');
      // A concurrent external writer must not turn a small metadata read into
      // an unbounded allocation, even though the owning Runner is stopped.
      const bytes = Buffer.alloc(node.stat.size + 1);
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
      if (length !== node.stat.size)
        throw failure('ChangedDuringInspection', 'Retained metadata changed.');
      const value = JSON.parse(bytes.subarray(0, length).toString('utf8'));
      controls.set(path.relative(dataRoot, file), value);
      if (witness(await handle.stat()) !== witness(node.stat))
        throw failure('ChangedDuringInspection', 'Retained metadata changed.');
      controls.set(path.relative(dataRoot, file), value);
      return value;
    } finally {
      await handle.close();
    }
  }

  async function records(directory, pattern) {
    const result = [];
    const node = nodes.get(directory);
    if (!node) return result;
    if (!node.stat.isDirectory()) {
      issue(directory, 'UnsafeMetadataDirectory');
      return result;
    }
    for (const file of node.children) {
      if (!pattern.test(path.basename(file))) {
        issue(file, 'UnrecognizedMetadata');
        continue;
      }
      try {
        const value = await readMetadata(file);
        if (!value)
          throw failure('InvalidMetadata', 'Retained metadata is unavailable.');
        result.push({ file, value });
      } catch (error) {
        issue(file, error.code ?? 'InvalidMetadata');
      }
    }
    return result;
  }

  return {
    dataRoot,
    home,
    recovery,
    trash,
    nodes,
    problems,
    issue,
    readMetadata,
    records,
    controls,
    allocatedBytes: allocationObserved ? allocatedBytes : null,
  };
}
