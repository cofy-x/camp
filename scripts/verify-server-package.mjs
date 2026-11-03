import assert from 'node:assert/strict';
import { readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { release as osRelease } from 'node:os';
import { command, root, scratch } from './lib.mjs';
import { hashFile, serverPlatforms } from './release.mjs';
import { assertDocumentServiceReceipt } from './document-service.mjs';
import {
  baselineLauncher,
  upgradeBaseline,
  previousRelease,
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
  for (const baseline of [upgradeBaseline, previousRelease]) {
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
  let documents;
  if (process.platform === 'darwin') {
    const documentReport = join(
      directory,
      `server-document-service-${platform}.json`,
    );
    await command(
      process.execPath,
      [join(root, 'scripts/smoke-documents.mjs'), '--service'],
      {
        env: {
          ...process.env,
          CAMP_SMOKE_BINARY: join(temporary, `camp-server-${platform}/camp`),
          CAMP_DOCUMENT_REPORT: documentReport,
          CAMP_SERVER_ARCHIVE: filename,
        },
      },
    );
    documents = JSON.parse(await readFile(documentReport, 'utf8'));
    assertDocumentServiceReceipt(documents, {
      version: descriptor.version,
      sourceCommit: descriptor.sourceCommit,
      platform: `darwin/${process.arch}`,
    });
    assert.equal(documents.archiveSHA256, artifact.sha256);
  }
  const report = {
    schema: 1,
    status: 'passed',
    version: descriptor.version,
    sourceCommit: descriptor.sourceCommit,
    platform,
    host: { os: osRelease(), architecture: process.arch },
    archiveSHA256: artifact.sha256,
    documents,
    upgrade: { fromVersion: upgradeBaseline },
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
            'bundled isolated PDF/DOCX readers without host Node or Runner',
            'allowlisted Mac directory import and immutable document restart',
            'Server launchd lifecycle with bundled readers and confirmed cleanup',
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
