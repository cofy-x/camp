import assert from 'node:assert/strict';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release as osRelease } from 'node:os';
import { command, root, scratch } from './lib.mjs';
import { hashFile, serverPlatforms } from './release.mjs';
import { baselineLauncher, upgradeBaseline } from './upgrade.mjs';

const platform = `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`;
assert.ok(serverPlatforms.includes(platform));
const directory = join(root, 'dist/release');
const descriptor = JSON.parse(
  await readFile(join(directory, 'server-release.json'), 'utf8'),
);
assert.equal(descriptor.schema, 1);
assert.equal(descriptor.version, process.env.VERSION);
assert.equal(descriptor.sourceCommit, process.env.GITHUB_SHA);
const artifact = descriptor.servers[platform];
assert.equal(artifact.filename, `camp-server-${platform}.tar.gz`);
const filename = join(directory, artifact.filename);
assert.equal(await hashFile(filename), artifact.sha256);
assert.equal(
  await readFile(`${filename}.sha256`, 'utf8'),
  `${artifact.sha256}  ${artifact.filename}\n`,
);
const temporary = await scratch();
try {
  await command('tar', ['-xzf', filename, '-C', temporary]);
  const previous = await baselineLauncher(temporary, 'server');
  await command(
    process.execPath,
    [join(root, 'scripts/smoke-production.mjs')],
    {
      env: {
        ...process.env,
        CAMP_SMOKE_BINARY: join(temporary, `camp-server-${platform}/camp`),
        CAMP_SMOKE_PREVIOUS_BINARY: previous.binary,
        XDG_CACHE_HOME: previous.cache,
      },
    },
  );
  const report = {
    schema: 1,
    status: 'passed',
    version: descriptor.version,
    sourceCommit: descriptor.sourceCommit,
    platform,
    host: { os: osRelease(), architecture: process.arch },
    archiveSHA256: artifact.sha256,
    upgrade: { fromVersion: upgradeBaseline },
    completedAt: new Date().toISOString(),
    workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
    checks: [
      'relocated complete UI',
      'protected setup',
      'authentication',
      'discussion',
      'versioned text files',
      'idempotent writes',
      'drafts',
      'restart persistence',
      'upgrade retains authentication, exact files, private drafts and history',
    ],
    untested: [
      'native Runner (separately qualified by Runner installation acceptance)',
      'PDF/DOCX preparation on macOS (unsupported)',
      'paid model provider',
    ],
  };
  await writeFile(
    join(directory, `server-verification-${platform}.json`),
    `${JSON.stringify(report, null, 2)}\n`,
  );
} finally {
  await rm(temporary, { recursive: true, force: true });
}
