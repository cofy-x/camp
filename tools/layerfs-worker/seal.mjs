import { createHash } from 'node:crypto';
import {
  copyFile,
  lstat,
  readFile,
  readdir,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';

const root = '/opt/camp/layerfs';
await copyFile(
  '/usr/local/bin/layerfs-daemon',
  path.join(root, 'layerfs-daemon'),
);
await copyFile('/usr/local/bin/docker', path.join(root, 'docker'));
const files = {};

async function walk(relative) {
  const absolute = path.join(root, relative);
  const info = await lstat(absolute);
  if (info.isDirectory()) {
    for (const name of (await readdir(absolute)).sort()) {
      if (name !== 'node_modules') await walk(path.posix.join(relative, name));
    }
  } else if (info.isFile()) {
    files[relative] = createHash('sha256')
      .update(await readFile(absolute))
      .digest('hex');
  }
}

await walk('api');
await walk('worker');
await walk('layerfs.node');
await walk('layerfs-daemon');
await walk('docker');
await walk('licenses');
const build = createHash('sha256').update(JSON.stringify(files)).digest('hex');
await writeFile(
  path.join(root, 'manifest.json'),
  JSON.stringify({
    status: 'sealed',
    platform: 'linux',
    architecture: 'arm64',
    build_id: build,
    files,
  }),
);
