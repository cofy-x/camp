import { constants } from 'node:fs';
import { createHash } from 'node:crypto';
import { open } from 'node:fs/promises';
import path from 'node:path';
import { blobIndex } from './transfer.mjs';
import { eachConcurrent } from './tree.mjs';

// Cache is an optimization input, never authority. Rehash every requested blob
// for this operation; retain manifest ordering independently of completion order.
export async function missingBlobs(directory, manifest) {
  const entries = [...blobIndex(manifest)],
    valid = new Set();
  await eachConcurrent(entries, async ([digest, length]) => {
    let file;
    try {
      file = await open(
        path.join(directory, digest),
        constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
      );
      const before = await file.stat({ bigint: true });
      if (!before.isFile() || before.size !== BigInt(length)) return;
      const hash = createHash('sha256');
      for await (const chunk of file.createReadStream({ autoClose: false }))
        hash.update(chunk);
      const after = await file.stat({ bigint: true });
      if (
        hash.digest('hex') === digest &&
        ['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs'].every(
          (key) => before[key] === after[key],
        )
      )
        valid.add(digest);
    } catch {
      // Missing, replaced, linked or corrupt bytes must be transferred again.
    } finally {
      await file?.close();
    }
  });
  return entries
    .filter(([digest]) => !valid.has(digest))
    .map(([digest]) => digest);
}
