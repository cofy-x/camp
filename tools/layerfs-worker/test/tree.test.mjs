import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmod,
  link,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readlink,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import {
  captureTree,
  manifestDigest,
  restoreTree,
  scanTree,
  validateManifest,
} from '../tree.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-tree-'));
  await chmod(root, 0o700);
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source');
  await mkdir(source, { mode: 0o750 });
  return { root, source };
}

test('bounded concurrent sealing retains the sequential manifest and drains failed captures', async (t) => {
  const { root, source } = await fixture(t);
  for (let index = 0; index < 40; index++)
    await writeFile(
      path.join(source, `member-${index}`),
      Buffer.alloc(1024 + index, index),
    );
  const sequential = await captureTree(source, path.join(root, 'captures'), {
    concurrency: 1,
  });
  const concurrent = await captureTree(source, path.join(root, 'captures'), {
    concurrency: 8,
  });
  assert.deepEqual(concurrent.manifest, sequential.manifest);
  assert.equal(concurrent.digest, sequential.digest);
  const restored = await restoreTree(concurrent, path.join(root, 'restores'));
  assert.equal((await scanTree(restored.root)).digest, sequential.digest);
  await assert.rejects(scanTree(source, { concurrency: 1000 }), {
    code: 'InvalidInput',
  });
  // A concurrent writer must fail without leaving admitted descriptors or
  // writes racing with the caller's subsequent recovery path.
  const changed = path.join(source, 'member-0');
  let stopped = false,
    revision = 0,
    failure;
  const writer = (async () => {
    while (!stopped)
      await writeFile(changed, Buffer.alloc(8192, revision++ % 256));
  })();
  try {
    await captureTree(source, path.join(root, 'failed'));
  } catch (error) {
    failure = error;
  } finally {
    stopped = true;
    await writer;
  }
  assert.ok(failure);
  await writeFile(changed, 'retained subsequent work');
  const after = await scanTree(source);
  assert.notEqual(after.digest, concurrent.digest);
});

test('complete captures retain binary, modes, empty directories, dotfiles, ignored dependencies and opaque links', async (t) => {
  const { root, source } = await fixture(t);
  for (const dir of ['.git', 'empty', 'node_modules', 'a'])
    await mkdir(path.join(source, dir), { mode: 0o750 });
  const bytes = Buffer.from([0, 1, 255, 254, 0, 128]);
  await writeFile(path.join(source, 'a/member'), bytes, { mode: 0o755 });
  // This path precedes a/member in canonical byte order but is traversed later.
  await link(path.join(source, 'a/member'), path.join(source, 'a.alias'));
  await writeFile(path.join(source, '.git/HEAD'), 'ref: refs/heads/main\n');
  await writeFile(path.join(source, '.gitignore'), 'node_modules\n');
  await writeFile(
    path.join(source, 'node_modules/dependency'),
    'fixed fixture dependency',
  );
  await symlink('dependency', path.join(source, 'node_modules/link'));
  await symlink('/unavailable/opaque-target', path.join(source, 'outside'));
  await writeFile(path.join(source, '中文-é'), 'unicode');
  const captured = await captureTree(source, path.join(root, 'captures'));
  assert.equal(
    captured.manifest.entries.find((row) => row.path === 'a.alias').type,
    'file',
  );
  assert.deepEqual(
    captured.manifest.entries.find((row) => row.path === 'a/member'),
    { path: 'a/member', type: 'hardlink', mode: 0o755, target: 'a.alias' },
  );
  const restored = await restoreTree(captured, path.join(root, 'restores'));
  assert.deepEqual(await readFile(path.join(restored.root, 'a/member')), bytes);
  assert.equal(
    (await lstat(path.join(restored.root, 'a/member'))).ino,
    (await lstat(path.join(restored.root, 'a.alias'))).ino,
  );
  assert.equal(
    (await lstat(path.join(restored.root, 'empty'))).mode & 0o777,
    0o750,
  );
  assert.equal(
    await readlink(path.join(restored.root, 'outside')),
    '/unavailable/opaque-target',
  );
  assert.equal((await scanTree(restored.root)).digest, captured.digest);
  assert.equal(
    (await captureTree(source, path.join(root, 'captures'))).digest,
    captured.digest,
  );
  await rm(path.join(source, 'a/member'));
  await writeFile(path.join(source, '.git/HEAD'), 'changed');
  assert.notEqual((await scanTree(source)).digest, captured.digest);
  assert.equal((await scanTree(restored.root)).digest, captured.digest);
});

test('external hardlinks, special files, capture recursion and special permissions fail explicitly', async (t) => {
  const { root, source } = await fixture(t);
  const outside = path.join(root, 'outside');
  await writeFile(outside, 'private');
  await link(outside, path.join(source, 'alias'));
  await assert.rejects(captureTree(source, path.join(root, 'captures')), {
    code: 'ExternalHardlink',
  });
  await rm(path.join(source, 'alias'));
  execFileSync('/usr/bin/mkfifo', [path.join(source, 'fifo')]);
  await assert.rejects(captureTree(source, path.join(root, 'captures')), {
    code: 'UnsupportedEntry',
  });
  await rm(path.join(source, 'fifo'));
  await assert.rejects(captureTree(source, path.join(source, 'captures')), {
    code: 'InvalidInput',
  });
  await writeFile(path.join(source, 'setuid'), 'x');
  await chmod(path.join(source, 'setuid'), 0o4755);
  await assert.rejects(scanTree(source), { code: 'UnsupportedEntry' });
});

test('restore detects corrupt bytes and retains its original capture', async (t) => {
  const { root, source } = await fixture(t);
  await writeFile(path.join(source, 'file'), 'correct');
  const captured = await captureTree(source, path.join(root, 'captures'));
  const row = captured.manifest.entries[0];
  await writeFile(path.join(captured.root, 'blobs', row.sha256), 'corrupt');
  await assert.rejects(restoreTree(captured, path.join(root, 'restores')), {
    code: 'Corrupt',
  });
  assert.equal(await readFile(path.join(source, 'file'), 'utf8'), 'correct');
  assert.equal(
    JSON.parse(await readFile(path.join(captured.root, 'receipt.json'), 'utf8'))
      .state,
    'sealed',
  );
});

test('destination case and Unicode collisions are checked using the actual filesystem', async (t) => {
  const { root } = await fixture(t);
  const digest = createHash('sha256').update('').digest('hex');
  const captureRoot = path.join(root, 'capture');
  await mkdir(path.join(captureRoot, 'blobs'), { recursive: true });
  await writeFile(path.join(captureRoot, 'blobs', digest), '');
  for (const names of [
    ['A', 'a'],
    ['é', 'e\u0301'],
  ]) {
    names.sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
    const manifest = {
      schema: 1,
      rootMode: 0o700,
      entries: names.map((name) => ({
        path: name,
        type: 'file',
        mode: 0o600,
        bytes: 0,
        sha256: digest,
      })),
    };
    const probe = path.join(root, `probe-${names[0]}`);
    await writeFile(probe, 'x');
    const aliased = await lstat(path.join(root, `probe-${names[1]}`)).then(
      () => true,
      () => false,
    );
    const operation = restoreTree(
      { root: captureRoot, manifest, digest: manifestDigest(manifest) },
      path.join(root, 'restores'),
    );
    if (aliased) await assert.rejects(operation, { code: 'NameConflict' });
    else assert.ok((await operation).root);
  }
});

test('untrusted manifests reject escapes, alias parents, malformed links and unbounded inputs', () => {
  const manifest = (entries) => ({ schema: 1, rootMode: 0o700, entries });
  for (const name of [
    '../escape',
    '/absolute',
    'a//b',
    'a/./b',
    'a\\b',
    '\ud800',
  ])
    assert.throws(
      () =>
        validateManifest(
          manifest([{ path: name, type: 'directory', mode: 0o700 }]),
        ),
      { code: 'InvalidSnapshot' },
    );
  assert.throws(
    () =>
      validateManifest(
        manifest([
          {
            path: 'link',
            type: 'symlink',
            mode: 0o777,
            target: Buffer.from('/tmp').toString('base64'),
          },
          { path: 'link/child', type: 'directory', mode: 0o700 },
        ]),
      ),
    { code: 'InvalidSnapshot' },
  );
  assert.throws(
    () =>
      validateManifest(
        manifest([
          { path: 'alias', type: 'hardlink', mode: 0o600, target: '../secret' },
        ]),
      ),
    { code: 'InvalidSnapshot' },
  );
  assert.throws(
    () =>
      validateManifest(
        manifest([{ path: 'a', type: 'directory', mode: 0o700 }]),
        { entries: 0 },
      ),
    { code: 'InvalidSnapshot' },
  );
});
