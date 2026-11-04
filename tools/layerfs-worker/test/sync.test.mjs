import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { test } from 'node:test';
import { treeSynchronizer } from '../sync.mjs';
import { captureTree, restoreTree, scanTree } from '../tree.mjs';

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'camp-staging-sync-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'source');
  await mkdir(source);
  await writeFile(path.join(source, 'file'), 'retained bytes', { mode: 0o640 });
  return {
    root,
    capture: await captureTree(source, path.join(root, 'captures')),
  };
}

test('restore admits one full-tree barrier after materialization and retains independent readback', async (t) => {
  const { root, capture } = await fixture(t);
  let barriers = 0;
  const result = await restoreTree(capture, path.join(root, 'results'), {
    syncTree: async (directory) => {
      barriers++;
      assert.equal((await scanTree(directory)).digest, capture.digest);
    },
  });
  assert.equal(barriers, 1);
  assert.equal(result.synchronization, 'staged-full-sync');
  assert.equal((await scanTree(result.root)).digest, capture.digest);
  assert.ok(result.stagingSyncMs >= 0 && result.readbackMs >= 0);
});

test('a failed staging barrier retains the capture and rejected tree without returning a result', async (t) => {
  const { root, capture } = await fixture(t);
  const failure = Object.assign(new Error('controlled staging flush failure'), {
    code: 'EIO',
  });
  await assert.rejects(
    restoreTree(capture, path.join(root, 'results'), {
      syncTree: async () => {
        throw failure;
      },
    }),
    (error) => error === failure && typeof error.restore === 'string',
  );
  assert.equal(
    await readFile(path.join(failure.restore, 'file'), 'utf8'),
    'retained bytes',
  );
  assert.equal(
    (await restoreTree(capture, path.join(root, 'recovered'))).synchronization,
    'per-entry-full-sync',
  );
});

test('independent readback rejects a changed tree even after a successful synchronization callback', async (t) => {
  const { root, capture } = await fixture(t);
  await assert.rejects(
    restoreTree(capture, path.join(root, 'results'), {
      syncTree: async (directory) => {
        await writeFile(path.join(directory, 'file'), 'wrong bytes');
      },
    }),
    { code: 'Mismatch' },
  );
});

test('a selected helper never silently falls back on missing, failed or incompatible completion', async (t) => {
  const { root } = await fixture(t);
  assert.equal(treeSynchronizer(undefined), undefined);
  assert.throws(() => treeSynchronizer('relative/helper'), {
    code: 'InvalidInput',
  });
  await assert.rejects(treeSynchronizer(path.join(root, 'missing'))(root), {
    code: 'ENOENT',
  });
  for (const [index, reply] of [
    'null',
    '{}',
    '{"schema":1,"synchronized":false}',
    '{"schema":1,"synchronized":true,"extra":1}',
    'not-json',
    '{"schema":1,"synchronized":true}',
  ].entries()) {
    const helper = path.join(root, `helper-${index}.mjs`);
    await writeFile(
      helper,
      `#!${process.execPath}\nimport process from 'node:process'; if(process.argv[2]!=='sync-staging-tree'||!process.argv[3]||process.env.HOME||process.env.DEEPSEEK_API_KEY)process.exit(2); process.stdout.write(${JSON.stringify(reply)});\n`,
      { mode: 0o700 },
    );
    const operation = treeSynchronizer(helper)(root);
    if (index === 5) await operation;
    else await assert.rejects(operation, { code: 'Unknown' });
  }
});
