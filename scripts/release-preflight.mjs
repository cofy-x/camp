import assert from 'node:assert/strict';
import { appendFile } from 'node:fs/promises';
import { releaseIdentity, repository } from './release.mjs';

const identity = await releaseIdentity();
assert.ok(identity.published, 'Choose a fixed release version.');
assert.equal(process.env.GITHUB_REPOSITORY, repository);
assert.equal(
  process.env.GITHUB_REF,
  'refs/heads/main',
  'Publish only reviewed main source.',
);
assert.equal(process.env.GITHUB_SHA, identity.sourceCommit);
const api = async (route, allowMissing = false) => {
  const response = await fetch(
    `https://api.github.com/repos/${repository}/${route}`,
    {
      headers: {
        Authorization: `Bearer ${process.env.GH_TOKEN}`,
        Accept: 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28',
      },
      signal: AbortSignal.timeout(30000),
    },
  );
  if (allowMissing && response.status === 404) return null;
  assert.equal(
    response.status,
    200,
    `Release preflight failed: ${route} (${response.status})`,
  );
  return response.json();
};
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
const runs = await api(
  `actions/workflows/ci.yml/runs?head_sha=${identity.sourceCommit}&event=push&status=completed&per_page=100`,
);
const ci = runs.workflow_runs.find(
  (run) => run.head_branch === 'main' && run.conclusion === 'success',
);
assert.ok(
  ci,
  'The exact main commit must pass Source, Browser and Fixture CI before release.',
);
const jobs = await api(`actions/runs/${ci.id}/jobs?per_page=100`);
for (const name of ['Source', 'Browser', 'Fixture'])
  assert.ok(
    jobs.jobs.some((job) => job.name === name && job.conclusion === 'success'),
    `Missing successful ${name} check.`,
  );
await appendFile(
  process.env.GITHUB_OUTPUT,
  `source=${identity.sourceCommit}\nci-url=${ci.html_url}\n`,
);
console.log(
  `Release ${identity.version} is bound to ${identity.sourceCommit}; checks: ${ci.html_url}`,
);
