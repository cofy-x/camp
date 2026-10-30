import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { release } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { command, root, scratch } from './lib.mjs';
import { verifyPublishedFormula } from './homebrew.mjs';
import { hashFile, validVersion } from './release.mjs';

assert.equal(process.platform, 'darwin');
const execute = promisify(execFile);
const formula = join(root, 'Formula/camp.rb');
const text = await readFile(formula, 'utf8');
const version = `v${text.match(/^ {2}version "([^"]+)"$/m)?.[1]}`;
assert.ok(validVersion(version));
const response = await fetch(
  `https://github.com/cofy-x/camp/releases/download/${version}/server-release.json`,
  { signal: AbortSignal.timeout(30000) },
);
assert.equal(response.status, 200);
const descriptor = await response.json();
assert.equal(descriptor.version, version);
const [publishedFormula, publishedChecksums] = await Promise.all(
  ['camp.rb', 'SHA256SUMS'].map(async (name) => {
    const response = await fetch(
      `https://github.com/cofy-x/camp/releases/download/${version}/${name}`,
      { signal: AbortSignal.timeout(30000) },
    );
    assert.equal(response.status, 200);
    const content = await response.text();
    assert.ok(Buffer.byteLength(content) < 64 << 10);
    return content;
  }),
);
verifyPublishedFormula(text, publishedFormula, publishedChecksums);
const publicTap =
  process.env.GITHUB_EVENT_NAME === 'push' &&
  process.env.GITHUB_REF === 'refs/heads/main';
const environment = {
  ...process.env,
  HOMEBREW_NO_AUTO_UPDATE: '1',
  HOMEBREW_NO_ANALYTICS: '1',
};
const brew = (args) => command('brew', args, { env: environment });
const platform = `darwin-${process.arch === 'x64' ? 'amd64' : process.arch}`;
const report = {
  schema: 1,
  version,
  sourceCommit: descriptor.sourceCommit,
  lifecycleSourceCommit: descriptor.sourceCommit,
  formulaSourceCommit: process.env.GITHUB_SHA,
  formulaSHA256: await hashFile(formula),
  archiveSHA256: descriptor.servers[platform].sha256,
  platform,
  host: { os: release(), architecture: process.arch },
  workflow: `https://github.com/cofy-x/camp/actions/runs/${process.env.GITHUB_RUN_ID}`,
  tap: publicTap ? 'public repository' : 'reviewed checkout',
  status: 'failed',
  checks: [],
  untested: ['paid model provider', 'macOS native Runner'],
};
try {
  await brew([
    'tap',
    'cofy-x/camp',
    publicTap ? 'https://github.com/cofy-x/camp' : root,
  ]);
  const { stdout: repository } = await execute('brew', [
    '--repository',
    'cofy-x/camp',
  ]);
  assert.equal(
    await readFile(join(repository.trim(), 'Formula/camp.rb'), 'utf8'),
    text,
  );
  await brew(['install', 'cofy-x/camp/camp']);
  await brew(['test', 'cofy-x/camp/camp']);
  report.checks.push('verified-public-archive', 'formula-version-test');
  const { stdout: prefix } = await execute('brew', [
    '--prefix',
    'cofy-x/camp/camp',
  ]);
  // Verify the installed release with its own lifecycle contract. Future API
  // changes must not make CI require an older tap release to implement them.
  const source = await scratch();
  try {
    await command('git', [
      '-c',
      'advice.detachedHead=false',
      'clone',
      '--quiet',
      '--depth=1',
      '--branch',
      version,
      'https://github.com/cofy-x/camp',
      source,
    ]);
    const { stdout: commit } = await execute('git', [
      '-C',
      source,
      'rev-parse',
      'HEAD',
    ]);
    assert.equal(commit.trim(), descriptor.sourceCommit);
    await command(
      process.execPath,
      [join(source, 'scripts/smoke-production.mjs')],
      {
        cwd: source,
        env: {
          ...environment,
          VERSION: version,
          CAMP_SMOKE_BINARY: join(prefix.trim(), 'bin/camp'),
        },
      },
    );
  } finally {
    await rm(source, { recursive: true, force: true });
  }
  report.checks.push(
    'complete-embedded-ui',
    'authentication-and-discussion',
    'exact-versioned-files',
    'private-drafts',
    'restart-persistence',
    'doctor-and-open-print',
  );
  report.status = 'passed';
} finally {
  report.completedAt = new Date().toISOString();
  await mkdir(join(root, 'dist'), { recursive: true });
  await writeFile(
    join(root, `dist/homebrew-verification-${platform}.json`),
    `${JSON.stringify(report, null, 2)}\n`,
  );
}
