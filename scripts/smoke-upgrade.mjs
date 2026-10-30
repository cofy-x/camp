import assert from 'node:assert/strict';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { binary, command, root, scratch } from './lib.mjs';
import { hashFile, releaseIdentity, serverPlatforms } from './release.mjs';
import { baselineLauncher, upgradeBaseline } from './upgrade.mjs';

const platform = `${process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`;
assert.ok(serverPlatforms.includes(platform));
const identity = await releaseIdentity();
const directory = await scratch();
try {
  const previous = await baselineLauncher(directory, 'server');
  await command(
    process.execPath,
    [join(root, 'scripts/smoke-production.mjs')],
    {
      env: {
        ...process.env,
        CAMP_SMOKE_PREVIOUS_BINARY: previous.binary,
        XDG_CACHE_HOME: previous.cache,
      },
    },
  );
  const report = {
    schema: 1,
    status: 'passed',
    version: identity.version,
    sourceCommit: identity.sourceCommit,
    sourceModified: identity.modified,
    fromVersion: upgradeBaseline,
    binarySHA256: await hashFile(binary),
    completedAt: new Date().toISOString(),
    platform,
    checks: [
      'public-baseline-data',
      'authentication-session',
      'discussion',
      'versioned-file-bytes',
      'private-drafts',
      'history',
      'append-only-upgrade',
    ],
    untested: [
      'upgrades from releases other than the named baseline',
      'paid model provider',
    ],
  };
  await mkdir(join(root, 'dist'), { recursive: true });
  await writeFile(
    join(root, 'dist/server-upgrade-verification.json'),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  console.log(
    `Server upgrade from ${upgradeBaseline} passed without resetting data.`,
  );
} finally {
  await rm(directory, { recursive: true, force: true });
}
