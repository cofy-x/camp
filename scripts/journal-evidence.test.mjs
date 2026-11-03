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

test('only an explicit read-only lifecycle accepts absent and empty WAL while preserving exact copy evidence', async (t) => {
  const directory = await fixture(t);
  const wal = path.join(directory, 'journal.sqlite-wal');
  await rm(wal);
  const missing = await stoppedJournalEvidence(directory);
  await writeFile(wal, '', { mode: 0o600 });
  const empty = await stoppedJournalEvidence(directory);
  const options = { allowEmptyWALLifecycle: true };
  assert.throws(() => assertDurableJournalEqual(missing, empty), /main\/WAL/);
  assert.throws(() => assertDurableJournalEqual(empty, missing), /main\/WAL/);
  assertDurableJournalEqual(missing, empty, options);
  assertDurableJournalEqual(empty, missing, options);
  assert.equal(missing.durableFiles['journal.sqlite-wal'], null);
  assert.deepEqual(empty.durableFiles['journal.sqlite-wal'], {
    bytes: 0,
    mode: 0o600,
    sha256: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
  });
  assert.throws(() => assertJournalCopy(missing, empty), /main\/WAL/);
  assert.throws(() => assertJournalCopy(empty, missing), /main\/WAL/);
});

test('empty WAL compatibility cannot hide changed main data, durable frames or invalid empty evidence', async (t) => {
  const directory = await fixture(t);
  const wal = path.join(directory, 'journal.sqlite-wal');
  const options = { allowEmptyWALLifecycle: true };
  const original = await stoppedJournalEvidence(directory);
  await writeFile(wal, 'changed durable WAL frames');
  const changed = await stoppedJournalEvidence(directory);
  assert.throws(
    () => assertDurableJournalEqual(original, changed, options),
    /main\/WAL/,
  );
  await rm(wal);
  const missing = await stoppedJournalEvidence(directory);
  assert.throws(
    () => assertDurableJournalEqual(original, missing, options),
    /main\/WAL/,
  );
  await writeFile(wal, '', { mode: 0o600 });
  const empty = await stoppedJournalEvidence(directory);
  assert.throws(
    () => assertDurableJournalEqual(original, empty, options),
    /main\/WAL/,
  );
  for (const change of [{ sha256: '0'.repeat(64) }, { mode: 0o644 }]) {
    const invalid = {
      ...empty,
      durableFiles: {
        ...empty.durableFiles,
        'journal.sqlite-wal': {
          ...empty.durableFiles['journal.sqlite-wal'],
          ...change,
        },
      },
    };
    assert.throws(
      () => assertDurableJournalEqual(missing, invalid, options),
      /main\/WAL/,
    );
  }
  await chmod(wal, 0o400);
  const changedMode = await stoppedJournalEvidence(directory);
  assert.throws(
    () => assertDurableJournalEqual(empty, changedMode, options),
    /main\/WAL/,
  );
  await writeFile(path.join(directory, 'journal.sqlite'), 'changed main');
  const changedMain = await stoppedJournalEvidence(directory);
  assert.throws(
    () => assertDurableJournalEqual(missing, changedMain, options),
    /main\/WAL/,
  );
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
