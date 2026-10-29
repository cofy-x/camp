import { cp, mkdir, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { collectLicenses } from './licenses.mjs';
import { archiveTree, installationNotes, releaseIdentity } from './release.mjs';

if (process.platform !== 'linux' || process.arch !== 'x64')
  throw new Error('Server release packaging currently targets Linux amd64.');
const identity = await releaseIdentity();
const destination = join(root, 'dist/camp-server-linux-amd64');
const staging = `${destination}.staging`;
await rm(staging, { recursive: true, force: true });
await mkdir(staging, { recursive: true });
try {
  const licenses = await collectLicenses(staging);
  await cp(join(root, 'dist/camp'), join(staging, 'camp'));
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
  await writeFile(
    join(root, 'dist/server-release.json'),
    `${JSON.stringify({ schema: 1, version: identity.version, sourceCommit: identity.sourceCommit, server: artifact }, null, 2)}\n`,
  );
  console.log(`Packaged ${artifact.filename}: ${artifact.bytes} bytes`);
} finally {
  await rm(staging, { recursive: true, force: true });
}
