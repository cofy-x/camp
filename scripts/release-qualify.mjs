import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { root } from './lib.mjs';
import { releaseIdentity } from './release.mjs';
import { collectReleaseEvidence } from './release-evidence.mjs';

assert.equal(process.env.CAMP_RELEASE_VALIDATE_ONLY, 'true');
const identity = await releaseIdentity();
const directory = join(root, 'dist/release');
const { acceptance } = await collectReleaseEvidence(directory, identity, true);
await writeFile(
  join(directory, 'candidate-acceptance.json'),
  `${JSON.stringify(acceptance, null, 2)}\n`,
);
console.log(
  `Qualified candidate ${identity.version} at ${identity.sourceCommit}; no tag, release or registry image published.`,
);
