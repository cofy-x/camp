import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, open, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';

export const digest = (value) =>
  createHash('sha256').update(value).digest('hex');

// Qualification input is quiescent and private. This accepts exactly the
// regular-file checkpoint surface, not dependency symlinks or a full filesystem.
export async function manifest(directory, excludeNames = ['node_modules']) {
  const root = await realpath(directory);
  const entries = [];
  const spellings = new Map();
  let bytes = 0;
  let visited = 0;
  const walk = async (relative = '') => {
    for (const name of (await readdir(path.join(root, relative))).sort()) {
      if (excludeNames.includes(name)) continue;
      if (++visited > 30000)
        throw new Error('Checkpoint exceeds its entry limit');
      const local = relative ? `${relative}/${name}` : name;
      if (local.length > 1024 || /[\\\x00-\x1f]/u.test(local))
        throw new Error('Unsupported checkpoint path');
      const folded = local.normalize('NFC').toLowerCase();
      if (spellings.has(folded) && spellings.get(folded) !== local)
        throw new Error('Checkpoint has case or Unicode aliases');
      spellings.set(folded, local);
      const filename = path.join(root, local);
      const info = await lstat(filename);
      if (info.isDirectory()) {
        await walk(local);
        continue;
      }
      if (
        !info.isFile() ||
        info.nlink !== 1 ||
        info.size > 8 << 20 ||
        entries.length >= 10000 ||
        bytes + info.size > 128 << 20
      )
        throw new Error(
          'Checkpoint contains unsupported files or exceeds its limits',
        );
      const handle = await open(
        filename,
        constants.O_RDONLY | constants.O_NOFOLLOW,
      );
      let data;
      try {
        const before = await handle.stat();
        if (before.ino !== info.ino || before.dev !== info.dev)
          throw new Error('Checkpoint input changed');
        data = await handle.readFile();
        const after = await handle.stat();
        if (
          after.size !== before.size ||
          after.mtimeMs !== before.mtimeMs ||
          data.length !== info.size
        )
          throw new Error('Checkpoint input changed');
      } finally {
        await handle.close();
      }
      entries.push({
        path: local,
        bytes: data.length,
        mode: info.mode & 0o111 ? 0o755 : 0o644,
        sha256: digest(data),
      });
      bytes += data.length;
    }
  };
  await walk();
  entries.sort((a, b) => a.path.localeCompare(b.path, 'en'));
  return {
    directory: root,
    entries,
    files: entries.length,
    bytes,
    sha256: digest(JSON.stringify(entries)),
  };
}

export async function fileBytes(snapshot, entry) {
  const filename = path.join(snapshot.directory, entry.path);
  const info = await lstat(filename);
  if (
    !info.isFile() ||
    info.nlink !== 1 ||
    (info.mode & 0o111 ? 0o755 : 0o644) !== entry.mode
  )
    throw new Error('Sealed checkpoint input changed');
  const handle = await open(
    filename,
    constants.O_RDONLY | constants.O_NOFOLLOW,
  );
  try {
    const bytes = await handle.readFile();
    if (bytes.length !== entry.bytes || digest(bytes) !== entry.sha256)
      throw new Error('Sealed checkpoint input changed');
    return bytes;
  } finally {
    await handle.close();
  }
}
