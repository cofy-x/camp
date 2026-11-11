import { readdir, readFile, writeFile } from 'node:fs/promises';
import { extname, join } from 'node:path';
import { promisify } from 'node:util';
import { brotliCompress, gzip } from 'node:zlib';

const compressBrotli = promisify(brotliCompress);
const compressGzip = promisify(gzip);

export async function compressDashboardAssets(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await compressDashboardAssets(path);
    } else if (['.js', '.css'].includes(extname(entry.name))) {
      const body = await readFile(path);
      const [br, gz] = await Promise.all([
        compressBrotli(body),
        compressGzip(body, { level: 9 }),
      ]);
      await Promise.all([
        writeFile(path + '.br', br),
        writeFile(path + '.gz', gz),
      ]);
    }
  }
}
