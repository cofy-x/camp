import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release } from 'node:os';
import { root } from './lib.mjs';

const outcomes = {
  download: process.env.CAMP_MAC_DOWNLOAD_OUTCOME ?? 'not_completed',
  online: process.env.CAMP_MAC_ONLINE_OUTCOME ?? 'not_completed',
  offline: process.env.CAMP_MAC_OFFLINE_OUTCOME ?? 'not_completed',
};
const installations = [];
for (const mode of ['github', 'github-offline']) {
  try {
    installations.push(
      JSON.parse(await readFile(join(root, `dist/smoke-${mode}.json`), 'utf8')),
    );
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}
const report = {
  schema: 1,
  version: process.env.VERSION,
  sourceCommit: process.env.GITHUB_SHA,
  platform: `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`,
  host: { os: release(), architecture: process.arch },
  status:
    process.platform === 'darwin' &&
    process.arch === 'arm64' &&
    Object.values(outcomes).every((outcome) => outcome === 'success') &&
    installations.length === 2
      ? 'passed'
      : 'failed',
  outcomes,
  installations,
  completedAt: new Date().toISOString(),
  workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
  untested: ['paid model provider', 'operator-specific tool installations'],
};
await mkdir(join(root, 'dist'), { recursive: true });
await writeFile(
  join(root, 'dist/public-mac-runner-verification.json'),
  `${JSON.stringify(report, null, 2)}\n`,
);
