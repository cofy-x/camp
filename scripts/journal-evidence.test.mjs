import assert from 'node:assert/strict';
import {
  chmod,
  mkdir,
  mkdtemp,
  open,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import {
  assertDurableJournalEqual,
  assertJournalCopy,
  stoppedJournalEvidence,
} from './journal-evidence.mjs';

async function fixture(t) {
  const directory = await mkdtemp(
    path.join(tmpdir(), 'camp-journal-evidence-'),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(
    path.join(directory, 'journal.sqlite'),
    'unchanged main database',
    { mode: 0o600 },
  );
  await writeFile(
    path.join(directory, 'journal.sqlite-wal'),
    'durable receipt only present in WAL',
    { mode: 0o600 },
  );
  await writeFile(
    path.join(directory, 'journal.sqlite-shm'),
    'volatile index',
    { mode: 0o600 },
  );
  return directory;
}

test('stopped journal proof detects changed or missing WAL even when the main file is unchanged', async (t) => {
  const directory = await fixture(t);
  const before = await stoppedJournalEvidence(directory);
  const main = await readFile(path.join(directory, 'journal.sqlite'));
  await writeFile(
    path.join(directory, 'journal.sqlite-wal'),
    'different durable receipt',
  );
  assert.deepEqual(
    await readFile(path.join(directory, 'journal.sqlite')),
    main,
  );
  const changed = await stoppedJournalEvidence(directory);
  assert.throws(() => assertDurableJournalEqual(before, changed), /main\/WAL/);
  await rm(path.join(directory, 'journal.sqlite-wal'));
  const missing = await stoppedJournalEvidence(directory);
  assert.throws(() => assertDurableJournalEqual(before, missing), /main\/WAL/);
});

test('coordination-only changes are observed separately and exact stopped copies preserve every present file', async (t) => {
  const directory = await fixture(t);
  const before = await stoppedJournalEvidence(directory);
  const copy = path.join(directory, 'backup');
  await mkdir(copy, { mode: 0o700 });
  for (const name of [
    'journal.sqlite',
    'journal.sqlite-wal',
    'journal.sqlite-shm',
  ])
    await writeFile(
      path.join(copy, name),
      await readFile(path.join(directory, name)),
      { mode: 0o600 },
    );
  assertJournalCopy(before, await stoppedJournalEvidence(copy));
  await writeFile(
    path.join(copy, 'journal.sqlite-shm'),
    'rebuilt volatile index',
  );
  const after = await stoppedJournalEvidence(copy);
  assertDurableJournalEqual(before, after);
  assert.throws(() => assertJournalCopy(before, after), /coordination/);
  assert.notDeepEqual(before.coordination, after.coordination);
});

test('journal evidence rejects linked, public or oversized fixture inputs without opening SQLite', async (t) => {
  const directory = await fixture(t);
  const wal = path.join(directory, 'journal.sqlite-wal');
  await chmod(wal, 0o644);
  await assert.rejects(stoppedJournalEvidence(directory), /private owned/);
  await rm(wal);
  await symlink(path.join(directory, 'journal.sqlite'), wal);
  await assert.rejects(stoppedJournalEvidence(directory), /private owned/);
  await rm(wal);
  const file = await open(wal, 'w', 0o600);
  try {
    await file.truncate(65 << 20);
  } finally {
    await file.close();
  }
  await assert.rejects(stoppedJournalEvidence(directory), /bounded/);
});
