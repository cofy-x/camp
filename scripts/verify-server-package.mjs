import assert from 'node:assert/strict';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release as osRelease } from 'node:os';
import { command, root, scratch } from './lib.mjs';
import { hashFile, serverPlatforms } from './release.mjs';
import { assertServerServiceReceipt } from './server-service.mjs';
import {
  baselineLauncher,
  upgradeBaseline,
  protocolTransitionBaseline,
  previousRelease,
  serverUpgradeBaselines,
} from './upgrade.mjs';

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
let passed = false;
try {
  await command('tar', ['-xzf', filename, '-C', temporary]);
  for (const baseline of serverUpgradeBaselines) {
    const previous = await baselineLauncher(temporary, 'server', baseline);
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
  }
  let service;
  if (process.platform === 'darwin') {
    const serviceReport = join(
      directory,
      `server-service-verification-${platform}.json`,
    );
    await command(
      process.execPath,
      [join(root, 'scripts/smoke-server-service.mjs'), '--service'],
      {
        env: {
          ...process.env,
          CAMP_SMOKE_BINARY: join(temporary, `camp-server-${platform}/camp`),
          CAMP_SERVER_SERVICE_REPORT: serviceReport,
          CAMP_SERVER_ARCHIVE: filename,
        },
      },
    );
    service = JSON.parse(await readFile(serviceReport, 'utf8'));
    assertServerServiceReceipt(service, {
      version: descriptor.version,
      sourceCommit: descriptor.sourceCommit,
      platform: `darwin/${process.arch}`,
    });
    assert.equal(service.archiveSHA256, artifact.sha256);
  }
  const report = {
    schema: 1,
    status: 'passed',
    version: descriptor.version,
    sourceCommit: descriptor.sourceCommit,
    platform,
    host: { os: osRelease(), architecture: process.arch },
    archiveSHA256: artifact.sha256,
    service,
    upgrade: { fromVersion: upgradeBaseline },
    transitionUpgrade: { fromVersion: protocolTransitionBaseline },
    previousUpgrade: { fromVersion: previousRelease },
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
      ...(process.platform === 'darwin'
        ? [
            'Markdown, plain text and preserved original-only attachments',
            'allowlisted Mac directory import and immutable file restart',
            'Server launchd lifecycle without parser runtime and with confirmed cleanup',
          ]
        : []),
    ],
    untested: [
      'native Runner (separately qualified by Runner installation acceptance)',
      'paid model provider',
    ],
  };
  await writeFile(
    join(directory, `server-verification-${platform}.json`),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  passed = true;
} finally {
  if (passed) await rm(temporary, { recursive: true, force: true });
  else console.error(`Retained failed Server package fixture: ${temporary}`);
}
