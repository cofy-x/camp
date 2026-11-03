import { cp, mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { command, go, root } from './lib.mjs';
import { collectLicenses } from './licenses.mjs';
import {
  archiveTree,
  installationNotes,
  releaseIdentity,
  serverBuildFlags,
  serverPlatforms,
} from './release.mjs';

const selected = process.argv.slice(2);
if (
  selected.length &&
  (selected.length !== 2 ||
    selected[0] !== '--platform' ||
    !serverPlatforms.includes(selected[1]))
)
  throw new Error(
    'Usage: node scripts/package-server.mjs [--platform PLATFORM]',
  );
if (process.platform !== 'linux' && process.platform !== 'darwin')
  throw new Error('Build Server packages on Linux or Mac.');
const platforms = selected.length
  ? [selected[1]]
  : process.platform === 'darwin'
    ? [`darwin-${process.arch === 'x64' ? 'amd64' : process.arch}`]
    : serverPlatforms;
if (process.platform === 'darwin' && platforms.includes('linux-amd64'))
  throw new Error(
    'Build the Linux Server package on its native Linux builder.',
  );
const identity = await releaseIdentity();
const flags = await serverBuildFlags(identity);
const servers = {};
for (const platform of platforms) {
  const destination = join(root, `dist/camp-server-${platform}`);
  const staging = `${destination}.staging`;
  await rm(staging, { recursive: true, force: true });
  await mkdir(staging, { recursive: true });
  try {
    const licenses = await collectLicenses(staging);
    if (platform === 'linux-amd64')
      await cp(join(root, 'dist/camp'), join(staging, 'camp'));
    else {
      const [GOOS, GOARCH] = platform.split('-');
      await command(
        go,
        [
          'build',
          '-trimpath',
          '-tags',
          'production',
          '-ldflags',
          flags,
          '-o',
          join(staging, 'camp'),
          './apps/server/cmd/server',
        ],
        { env: { ...process.env, CGO_ENABLED: '0', GOOS, GOARCH } },
      );
    }
    await writeFile(
      join(staging, 'INSTALL.md'),
      installationNotes(identity, 'server'),
    );
    await writeFile(
      join(staging, 'licenses/modules.json'),
      `${JSON.stringify(licenses, null, 2)}\n`,
    );
    if (identity.published)
      await cp(
        join(root, 'dist/runner-release.json'),
        join(staging, 'runner-release.json'),
      );
    await rm(destination, { recursive: true, force: true });
    await cp(staging, destination, { recursive: true });
    const artifact = await archiveTree(destination);
    await writeFile(
      join(root, 'dist', `${artifact.filename}.sha256`),
      `${artifact.sha256}  ${artifact.filename}\n`,
    );
    servers[platform] = artifact;
    console.log(`Packaged ${artifact.filename}: ${artifact.bytes} bytes`);
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}
await writeFile(
  join(root, 'dist/server-release.json'),
  `${JSON.stringify({ schema: 1, version: identity.version, sourceCommit: identity.sourceCommit, server: servers['linux-amd64'], servers }, null, 2)}\n`,
);
