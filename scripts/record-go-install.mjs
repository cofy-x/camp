import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release } from 'node:os';
import { root } from './lib.mjs';

const platform = `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`;
const report = {
  schema: 1,
  version: process.env.VERSION,
  sourceCommit: process.env.GITHUB_SHA,
  platform,
  host: { os: release(), architecture: process.arch },
  status:
    process.env.CAMP_GO_INSTALL_OUTCOME === 'success' &&
    process.env.CAMP_GO_SMOKE_OUTCOME === 'success'
      ? 'passed'
      : 'failed',
  install: process.env.CAMP_GO_INSTALL_OUTCOME || 'not_completed',
  lifecycle: process.env.CAMP_GO_SMOKE_OUTCOME || 'not_completed',
  completedAt: new Date().toISOString(),
  workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
  untested: ['paid model provider', 'systemd service installation'],
};
await mkdir(join(root, 'dist'), { recursive: true });
await writeFile(
  join(root, `dist/go-install-verification-${platform}.json`),
  `${JSON.stringify(report, null, 2)}\n`,
);
