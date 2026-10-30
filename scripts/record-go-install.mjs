import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release } from 'node:os';
import { root } from './lib.mjs';

const platform = `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`;
const macRunner = platform === 'darwin-arm64';
let nativeRunner = null;
if (macRunner) {
  try {
    nativeRunner = JSON.parse(
      await readFile(join(root, 'dist/smoke-github.json'), 'utf8'),
    );
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}
const report = {
  schema: 1,
  version: process.env.VERSION,
  sourceCommit: process.env.GITHUB_SHA,
  platform,
  host: { os: release(), architecture: process.arch },
  status:
    process.env.CAMP_GO_INSTALL_OUTCOME === 'success' &&
    process.env.CAMP_GO_SMOKE_OUTCOME === 'success' &&
    (!macRunner ||
      (process.env.CAMP_GO_RUNNER_OUTCOME === 'success' &&
        nativeRunner?.status === 'passed' &&
        nativeRunner.version === process.env.VERSION &&
        nativeRunner.sourceCommit === process.env.GITHUB_SHA &&
        nativeRunner.platform === 'darwin' &&
        nativeRunner.architecture === 'arm64' &&
        nativeRunner.entrypoint === 'provided-binary'))
      ? 'passed'
      : 'failed',
  install: process.env.CAMP_GO_INSTALL_OUTCOME || 'not_completed',
  lifecycle: process.env.CAMP_GO_SMOKE_OUTCOME || 'not_completed',
  nativeRunner,
  completedAt: new Date().toISOString(),
  workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
  untested: [
    'paid model provider',
    'systemd service installation',
    ...(!macRunner ? ['native Runner through Go entrypoint'] : []),
  ],
};
await mkdir(join(root, 'dist'), { recursive: true });
await writeFile(
  join(root, `dist/go-install-verification-${platform}.json`),
  `${JSON.stringify(report, null, 2)}\n`,
);
if (report.status !== 'passed') process.exitCode = 1;
