import assert from 'node:assert/strict';
import { constants } from 'node:fs';
import { createHash } from 'node:crypto';
import { lstat, open } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';

const names = ['journal.sqlite', 'journal.sqlite-wal', 'journal.sqlite-shm'];
const maximumBytes = 64 << 20;
const emptySHA256 = createHash('sha256').digest('hex');

async function inventory(directory) {
  const files = {};
  let bytes = 0;
  for (const name of names) {
    const info = await lstat(path.join(directory, name), {
      bigint: true,
    }).catch((error) => {
      if (error.code === 'ENOENT' && name !== names[0]) return null;
      throw error;
    });
    if (!info) {
      files[name] = null;
      continue;
    }
    assert.ok(
      info.isFile() &&
        info.uid === BigInt(process.getuid()) &&
        info.nlink === 1n &&
        !(info.mode & 0o077n),
      'Stopped journal evidence requires private owned regular files.',
    );
    bytes += Number(info.size);
    assert.ok(
      bytes <= maximumBytes,
      'Stopped journal evidence exceeds its bounded fixture size.',
    );
    files[name] = {
      bytes: Number(info.size),
      mode: Number(info.mode & 0o777n),
      device: info.dev,
      inode: info.ino,
      modified: info.mtimeNs,
      changed: info.ctimeNs,
    };
  }
  return files;
}

// Hash every durable sidecar without opening SQLite or checkpointing its WAL.
// Call only after the fixture's Runner and service have confirmed their stop.
export async function stoppedJournalEvidence(directory) {
  const before = await inventory(directory);
  const files = {};
  for (const [name, witness] of Object.entries(before)) {
    if (!witness) {
      files[name] = null;
      continue;
    }
    const file = await open(
      path.join(directory, name),
      constants.O_RDONLY | constants.O_NOFOLLOW,
    );
    try {
      const info = await file.stat({ bigint: true });
      assert.equal(info.dev, witness.device);
      assert.equal(info.ino, witness.inode);
      const hash = createHash('sha256');
      let bytes = 0;
      for await (const chunk of file.createReadStream({ autoClose: false })) {
        bytes += chunk.length;
        assert.ok(
          bytes <= maximumBytes,
          'Stopped journal changed while hashing.',
        );
        hash.update(chunk);
      }
      assert.equal(bytes, witness.bytes);
      files[name] = { bytes, mode: witness.mode, sha256: hash.digest('hex') };
    } finally {
      await file.close();
    }
  }
  assert.deepEqual(
    await inventory(directory),
    before,
    'Stopped journal changed during evidence capture.',
  );
  return {
    schema: 1,
    durableFiles: {
      'journal.sqlite': files['journal.sqlite'],
      'journal.sqlite-wal': files['journal.sqlite-wal'],
    },
    coordination: {
      name: 'journal.sqlite-shm',
      kind: 'volatile SQLite coordination index',
      file: files['journal.sqlite-shm'],
    },
  };
}

export function assertDurableJournalEqual(
  before,
  after,
  { allowEmptyWALLifecycle = false } = {},
) {
  assert.equal(before.schema, 1);
  assert.equal(after.schema, 1);
  const previous = before.durableFiles;
  let current = after.durableFiles;
  // Only a known read-only SQLite lifecycle may change empty WAL presence.
  // Preserve the raw observations; exact backup copies never opt in.
  if (allowEmptyWALLifecycle) {
    const empty = (file) =>
      file?.bytes === 0 &&
      file.sha256 === emptySHA256 &&
      Number.isInteger(file.mode) &&
      file.mode >= 0 &&
      file.mode <= 0o700 &&
      !(file.mode & 0o077);
    const priorWAL = previous['journal.sqlite-wal'];
    const currentWAL = current['journal.sqlite-wal'];
    if (
      (priorWAL === null && empty(currentWAL)) ||
      (currentWAL === null && empty(priorWAL))
    )
      current = { ...current, 'journal.sqlite-wal': priorWAL };
  }
  assert.deepEqual(
    current,
    previous,
    'Stopped operation changed durable journal main/WAL bytes.',
  );
}

export function assertJournalCopy(before, after) {
  assertDurableJournalEqual(before, after);
  assert.deepEqual(
    after.coordination,
    before.coordination,
    'Stopped backup omitted or changed the copied SQLite coordination file.',
  );
}
