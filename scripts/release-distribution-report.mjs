import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';

const read = async (name) => {
  try {
    return JSON.parse(await readFile(join(root, 'dist', name), 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return { status: 'not_completed' };
    throw error;
  }
};
const online = await read('smoke-github.json');
const offline = await read('smoke-github-offline.json');
const image = await read('smoke-server-image.json');
const report = {
  schema: 1,
  version: process.env.VERSION,
  sourceCommit: process.env.GITHUB_SHA,
  completedAt: new Date().toISOString(),
  host: {
    os: (await readFile('/etc/os-release', 'utf8')).match(
      /^PRETTY_NAME="(.*)"$/m,
    )?.[1],
    architecture: process.arch,
  },
  workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}/attempts/${process.env.GITHUB_RUN_ATTEMPT}`,
  status: [online, offline, image].every((check) => check.status === 'passed')
    ? 'passed'
    : 'failed',
  publicOnline: online,
  publicOffline: offline,
  anonymousServerImage: image,
  imagePull: process.env.CAMP_IMAGE_PULL_OUTCOME || 'not_completed',
  untested: [
    'paid model provider',
    'cross-version upgrade',
    'systemd service installation',
  ],
};
await writeFile(
  join(root, 'dist/distribution-verification.json'),
  `${JSON.stringify(report, null, 2)}\n`,
);
console.log(`Public distribution verification: ${report.status}`);
