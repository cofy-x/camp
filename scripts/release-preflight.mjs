import assert from 'node:assert/strict';
import { appendFile } from 'node:fs/promises';
import { releaseIdentity, repository } from './release.mjs';
import { githubCIAPI, requireMainCI } from './ci-evidence.mjs';

const identity = await releaseIdentity();
assert.ok(identity.published, 'Choose a fixed release version.');
assert.equal(process.env.GITHUB_REPOSITORY, repository);
assert.equal(process.env.GITHUB_SHA, identity.sourceCommit);
if (process.env.CAMP_RELEASE_VALIDATE_ONLY === 'true') {
  await appendFile(
    process.env.GITHUB_OUTPUT,
    `source=${identity.sourceCommit}\nci-url=https://github.com/${repository}/actions/runs/${process.env.GITHUB_RUN_ID}\n`,
  );
  console.log(
    `Validation only: Native package checks for ${identity.sourceCommit}; the required complete regression and native packages qualify this candidate; publication is not granted.`,
  );
  process.exit(0);
}
assert.equal(
  process.env.GITHUB_REF,
  'refs/heads/main',
  'Publish only reviewed main source.',
);
const api = githubCIAPI(repository);
assert.equal(
  (await api('commits/main')).sha,
  identity.sourceCommit,
  'Main moved; dispatch again at its current commit.',
);
assert.equal(
  await api(`git/ref/tags/${identity.version}`, true),
  null,
  'Release tags are never moved or reused.',
);
assert.equal(
  await api(`releases/tags/${identity.version}`, true),
  null,
  'Release assets are never replaced.',
);
const ciURL = await requireMainCI(api, repository, identity.sourceCommit);
await appendFile(
  process.env.GITHUB_OUTPUT,
  `source=${identity.sourceCommit}\nci-url=${ciURL}\n`,
);
console.log(
  `Release ${identity.version} is bound to ${identity.sourceCommit}. CI: ${ciURL}`,
);
