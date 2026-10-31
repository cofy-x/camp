import assert from 'node:assert/strict';
import { mkdtemp, mkdir, realpath, rm, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  identity,
  inside,
  ownedDirectory,
  ownedOutput,
  shellQuote,
} from '../validation.mjs';

test('owned paths reject prefix confusion and symlink escape before creating a directory', async () => {
  const root = await realpath(
    await mkdtemp(path.join(os.tmpdir(), 'camp-layerfs-paths-')),
  );
  try {
    const data = path.join(root, 'data');
    const outside = path.join(root, 'outside');
    await mkdir(data);
    await mkdir(outside);
    await symlink(outside, path.join(data, 'link'));
    assert.equal(inside(data, `${data}-outside`), false);
    await assert.rejects(
      ownedDirectory(data, path.join(data, 'link/created'), true),
      { code: 'InvalidInput' },
    );
    assert.equal(
      await ownedDirectory(data, path.join(data, 'new'), true),
      path.join(data, 'new'),
    );
    await assert.rejects(ownedOutput(data, path.join(outside, 'archive.tar')), {
      code: 'InvalidInput',
    });
    assert.equal(
      await ownedOutput(data, path.join(data, 'new/out.tar')),
      path.join(data, 'new/out.tar'),
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('identities are domain separated and shell quoting preserves apostrophes', () => {
  assert.notEqual(identity('p', ['a', 'b']), identity('p', ['a,b']));
  assert.match(identity('w', ['project', 'run']), /^w-[0-9a-f]{40}$/);
  assert.equal(shellQuote("a'b"), "'a'\\''b'");
});
