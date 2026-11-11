import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';
import { compressDashboardAssets } from './webui-assets.mjs';

test('embedded assets retain originals and complete lossless compressed representations', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'camp-webui-'));
  try {
    await mkdir(join(dir, 'assets'));
    const body = Buffer.from('console.log("Camp desktop");\n'.repeat(100));
    const script = join(dir, 'assets/app-12345678.js');
    const style = join(dir, 'assets/app-12345678.css');
    await writeFile(script, body);
    await writeFile(style, 'body { color: black; }');
    await writeFile(join(dir, 'index.html'), '<html>Camp</html>');
    await writeFile(join(dir, 'assets/font.woff2'), Buffer.from([0, 1, 2]));
    await compressDashboardAssets(dir);
    assert.deepEqual(await readFile(script), body);
    const gz = await readFile(script + '.gz');
    const br = await readFile(script + '.br');
    assert.deepEqual(gunzipSync(gz), body);
    assert.deepEqual(brotliDecompressSync(br), body);
    assert.ok(gz.length < body.length);
    assert.ok(br.length < body.length);
    assert.equal(
      gunzipSync(await readFile(style + '.gz')).toString(),
      'body { color: black; }',
    );
    await assert.rejects(readFile(join(dir, 'index.html.gz')), {
      code: 'ENOENT',
    });
    await assert.rejects(readFile(join(dir, 'assets/font.woff2.br')), {
      code: 'ENOENT',
    });
    await writeFile(script, 'console.log("updated");');
    await compressDashboardAssets(dir);
    assert.equal(
      gunzipSync(await readFile(script + '.gz')).toString(),
      'console.log("updated");',
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
