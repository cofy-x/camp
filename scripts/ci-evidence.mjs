import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import {
  appendFile,
  lstat,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

const ciWorkflow = '.github/workflows/ci.yml';
const coreJobs = ['Source', 'Browser', 'Fixture'];
const platformJobs = {
  server: ['Server / darwin-arm64', 'Server / darwin-amd64'],
  native: [
    'native / Native boundary / macos-15',
    'native / Native boundary / macos-26',
  ],
  checkpoint: ['checkpoint / Linux checkpoint consistency and cost'],
};
const sha = /^[a-f0-9]{40}$/;
const digest = /^[a-f0-9]{64}$/;
const positiveId = (id) => Number.isSafeInteger(id) && id > 0;

export function githubCIAPI(repository, signal) {
  assert.match(repository, /^[\w.-]+\/[\w.-]+$/);
  return async (route, allowMissing = false) => {
    const response = await fetch(
      `https://api.github.com/repos/${repository}/${route}`,
      {
        headers: {
          Authorization: `Bearer ${process.env.GH_TOKEN}`,
          Accept: 'application/vnd.github+json',
          'X-GitHub-Api-Version': '2022-11-28',
        },
        signal: signal ?? AbortSignal.timeout(30000),
      },
    );
    if (allowMissing && response.status === 404) return null;
    assert.equal(
      response.status,
      200,
      `GitHub API failed: ${route} (${response.status})`,
    );
    return response.json();
  };
}

async function latestCI(api, repository, commit, event, branch) {
  const query = new URLSearchParams({
    head_sha: commit,
    event,
    per_page: '100',
  });
  const { workflow_runs: runs, total_count: total } = await api(
    `actions/workflows/ci.yml/runs?${query}`,
  );
  assert.ok(
    Array.isArray(runs) && runs.length === total,
    'Incomplete CI runs.',
  );
  return runs
    .filter(
      (run) =>
        positiveId(run.id) &&
        run.path === ciWorkflow &&
        run.repository?.full_name === repository &&
        run.head_repository?.full_name === repository &&
        run.head_branch === branch &&
        run.head_sha === commit &&
        run.event === event,
    )
    .sort(
      (a, b) =>
        Date.parse(b.created_at) - Date.parse(a.created_at) || b.id - a.id,
    )[0];
}

async function latestJobs(api, runId) {
  const { jobs, total_count: total } = await api(
    `actions/runs/${runId}/jobs?filter=latest&per_page=100`,
  );
  assert.ok(
    Array.isArray(jobs) && jobs.length === total,
    'Incomplete CI jobs.',
  );
  return jobs;
}

function successful(jobs, name) {
  const matches = jobs.filter((job) => job.name === name);
  return (
    matches.length === 1 &&
    matches[0].status === 'completed' &&
    matches[0].conclusion === 'success'
  );
}

export async function requireMainCI(api, repository, commit) {
  assert.match(commit, sha);
  const run = await latestCI(api, repository, commit, 'push', 'main');
  assert.ok(run, 'The exact main commit needs a push CI run.');
  const jobs = await latestJobs(api, run.id);
  for (const name of coreJobs)
    assert.ok(
      successful(jobs, name),
      `Expected one successful ${name} check for ${commit}.`,
    );
  return `https://github.com/${repository}/actions/runs/${run.id}`;
}

export async function priorPlatformChecks({ api, readPlan, repository, plan }) {
  const requested = Object.keys(platformJobs).filter(
    (key) => plan.checks?.[key] === true,
  );
  if (
    !requested.length ||
    plan.schema !== 1 ||
    !positiveId(plan.policyVersion) ||
    plan.stage !== 'merge' ||
    plan.classification !== 'code' ||
    plan.checkedCommit !== plan.commit ||
    plan.policySource !== plan.base ||
    ![plan.base, plan.commit, plan.checkedTree].every((value) =>
      sha.test(value ?? ''),
    ) ||
    ![plan.policyDigest, plan.driverDigest].every((value) =>
      digest.test(value ?? ''),
    )
  )
    return null;

  const pulls = await api(`commits/${plan.commit}/pulls?per_page=100`);
  const matching = pulls.filter(
    (pr) =>
      positiveId(pr.number) &&
      pr.state === 'closed' &&
      pr.merged_at &&
      pr.merge_commit_sha === plan.commit &&
      pr.base?.ref === 'main' &&
      pr.base.sha === plan.base &&
      pr.head?.repo?.full_name === repository &&
      sha.test(pr.head.sha ?? ''),
  );
  if (matching.length !== 1) return null;
  const [pr] = matching;
  const run = await latestCI(
    api,
    repository,
    pr.head.sha,
    'pull_request',
    pr.head.ref,
  );
  if (run?.status !== 'completed' || run.conclusion !== 'success') return null;

  // The PR workflow runs against GitHub's merge checkout, so use the tree that
  // Change scope recorded from the checkout rather than the PR head's tree.
  const earlier = await readPlan(run.id);
  const matchingFields = [
    'schema',
    'policyVersion',
    'stage',
    'base',
    'checkedTree',
    'policySource',
    'policyDigest',
    'driverDigest',
  ];
  if (
    earlier?.pullRequest !== pr.number ||
    earlier.commit !== pr.head.sha ||
    !matchingFields.every((key) => earlier[key] === plan[key])
  )
    return null;

  const jobs = await latestJobs(api, run.id);
  if (!['Change scope', ...coreJobs].every((name) => successful(jobs, name)))
    return null;
  const checks = requested.filter(
    (key) =>
      earlier.checks?.[key] === true &&
      platformJobs[key].every((name) => successful(jobs, name)),
  );
  return checks.length
    ? { pullRequest: pr.number, runId: run.id, checks }
    : null;
}

async function downloadPlan(repository, runId, signal) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'camp-ci-plan-'));
  try {
    await promisify(execFile)(
      'gh',
      [
        'run',
        'download',
        String(runId),
        '--repo',
        repository,
        '--name',
        'validation-plan',
        '--dir',
        directory,
      ],
      { signal, maxBuffer: 65536 },
    );
    const file = path.join(directory, 'validation-plan.json');
    const info = await lstat(file);
    assert.ok(
      info.isFile() && info.size <= 1 << 20,
      'Invalid validation plan artifact.',
    );
    return JSON.parse(await readFile(file, { encoding: 'utf8', signal }));
  } finally {
    await rm(directory, { recursive: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const repository = process.env.GITHUB_REPOSITORY;
  const file = 'validation-plan.json';
  const plan = JSON.parse(await readFile(file, 'utf8'));
  let prior;
  if (
    process.env.GITHUB_EVENT_NAME === 'push' &&
    process.env.GITHUB_REF === 'refs/heads/main' &&
    process.env.GITHUB_SHA === plan.commit
  ) {
    const signal = AbortSignal.timeout(25000);
    try {
      prior = await priorPlatformChecks({
        api: githubCIAPI(repository, signal),
        readPlan: (runId) => downloadPlan(repository, runId, signal),
        repository,
        plan,
      });
    } catch {
      console.log(
        'Run applicable platform checks on this commit because prior evidence could not be verified.',
      );
    }
  }
  if (prior) {
    await writeFile(
      file,
      `${JSON.stringify({ ...plan, priorPlatformChecks: prior }, null, 2)}\n`,
    );
    await appendFile(
      process.env.GITHUB_OUTPUT,
      prior.checks.map((key) => `${key}=true\n`).join(''),
    );
    const url = `https://github.com/${repository}/actions/runs/${prior.runId}`;
    await appendFile(
      process.env.GITHUB_STEP_SUMMARY,
      `\nReused ${prior.checks.join(', ')} checks from [PR #${prior.pullRequest} CI](${url}) for tree \`${plan.checkedTree}\`.\n`,
    );
  }
}
