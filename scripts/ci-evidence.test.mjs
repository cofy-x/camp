import assert from 'node:assert/strict';
import test from 'node:test';
import { priorPlatformChecks, requireMainCI } from './ci-evidence.mjs';

const repository = 'cofy-x/camp';
const base = 'a'.repeat(40);
const head = 'b'.repeat(40);
const merged = 'c'.repeat(40);
const tree = 'd'.repeat(40);
const coreJobs = ['Source', 'Browser', 'Fixture'];
const serverJobs = ['Server / darwin-arm64', 'Server / darwin-amd64'];
const nativeJobs = [
  'native / Native boundary / macos-15',
  'native / Native boundary / macos-26',
];
const checkpointJob = 'checkpoint / Linux checkpoint consistency and cost';

function run(overrides = {}) {
  return {
    id: 101,
    path: '.github/workflows/ci.yml',
    repository: { full_name: repository },
    head_repository: { full_name: repository },
    head_branch: 'main',
    head_sha: merged,
    event: 'push',
    created_at: '2026-11-25T18:36:00Z',
    status: 'completed',
    conclusion: 'success',
    ...overrides,
  };
}

function job(name, conclusion = 'success') {
  return { name, status: 'completed', conclusion };
}

function github({
  event = 'push',
  commit = merged,
  runs = [run()],
  jobs,
  pulls = [],
  jobRunId = runs.at(-1)?.id,
}) {
  return async (route) => {
    const url = new URL(route, 'https://example.invalid/');
    if (url.pathname === '/actions/workflows/ci.yml/runs') {
      assert.equal(url.searchParams.get('event'), event);
      assert.equal(url.searchParams.get('head_sha'), commit);
      return { workflow_runs: runs, total_count: runs.length };
    }
    if (
      url.pathname.startsWith('/actions/runs/') &&
      url.pathname.endsWith('/jobs')
    ) {
      assert.equal(url.searchParams.get('filter'), 'latest');
      assert.equal(url.pathname, `/actions/runs/${jobRunId}/jobs`);
      return { jobs, total_count: jobs.length };
    }
    assert.equal(url.pathname, `/commits/${merged}/pulls`);
    return pulls;
  };
}

test('release preflight uses exact-main gates when platform jobs are skipped, queued or unassigned', async () => {
  for (const [status, conclusion, platform] of [
    ['completed', 'failure', job(nativeJobs[1], 'cancelled')],
    ['completed', 'success', job('Server / ${{ matrix.platform }}', 'skipped')],
    [
      'in_progress',
      null,
      { name: nativeJobs[1], status: 'queued', conclusion: null },
    ],
  ]) {
    const api = github({
      runs: [run({ status, conclusion })],
      jobs: [...coreJobs.map((name) => job(name)), platform],
    });
    assert.equal(
      await requireMainCI(api, repository, merged),
      `https://github.com/${repository}/actions/runs/101`,
    );
  }
});

test('release preflight does not accept a different source or an older successful run', async () => {
  for (const overrides of [
    { head_sha: head },
    { head_branch: 'feature' },
    { event: 'workflow_dispatch' },
    { event: 'pull_request' },
    { path: '.github/workflows/other.yml' },
    { repository: { full_name: 'other/camp' } },
  ]) {
    const api = github({
      runs: [run(overrides)],
      jobs: coreJobs.map((name) => job(name)),
    });
    await assert.rejects(
      requireMainCI(api, repository, merged),
      /needs a push CI run/,
    );
  }
  const pending = run({
    id: 102,
    created_at: '2026-11-25T18:37:00Z',
    status: 'in_progress',
    conclusion: null,
  });
  const api = github({
    runs: [run(), pending],
    jobs: [{ name: 'Source', status: 'in_progress', conclusion: null }],
  });
  await assert.rejects(
    requireMainCI(api, repository, merged),
    /one successful Source check/,
  );
});

test('release preflight rejects failed, skipped, missing and ambiguous named gates', async () => {
  for (const source of [
    [job('Source', 'failure')],
    [job('Source', 'skipped')],
    [job('Source', 'cancelled')],
    [],
    [job('Source'), job('Source')],
  ]) {
    const api = github({ jobs: [...source, job('Browser'), job('Fixture')] });
    await assert.rejects(
      requireMainCI(api, repository, merged),
      /one successful Source check/,
    );
  }
});

const mergedPlan = {
  schema: 1,
  policyVersion: 2,
  classification: 'code',
  stage: 'merge',
  base,
  commit: merged,
  checkedCommit: merged,
  checkedTree: tree,
  policySource: base,
  policyDigest: '1'.repeat(64),
  driverDigest: '2'.repeat(64),
  checks: { server: true, native: true, checkpoint: true },
};
const pull = {
  number: 172,
  state: 'closed',
  merged_at: '2026-11-25T18:35:00Z',
  merge_commit_sha: merged,
  base: { ref: 'main', sha: base },
  head: { sha: head, ref: 'feature', repo: { full_name: repository } },
};
const priorRun = run({
  head_branch: 'feature',
  head_sha: head,
  event: 'pull_request',
});
const priorPlan = {
  ...mergedPlan,
  pullRequest: pull.number,
  commit: head,
  checkedCommit: 'e'.repeat(40),
};
const passing = [
  'Change scope',
  ...coreJobs,
  ...serverJobs,
  ...nativeJobs,
  checkpointJob,
].map((name) => job(name));

function review({
  plan = mergedPlan,
  runs = [priorRun],
  jobs = passing,
  pulls = [pull],
  evidence = priorPlan,
} = {}) {
  return priorPlatformChecks({
    api: github({ event: 'pull_request', commit: head, runs, jobs, pulls }),
    readPlan: async (id) => {
      assert.equal(id, priorRun.id);
      return evidence;
    },
    repository,
    plan,
  });
}

test('an exact merge checkout and successful prior jobs allow platform reuse only', async () => {
  assert.deepEqual(await review(), {
    pullRequest: pull.number,
    runId: priorRun.id,
    checks: ['server', 'native', 'checkpoint'],
  });
  assert.deepEqual(
    (
      await review({
        jobs: passing.filter((row) => row.name !== serverJobs[1]),
      })
    ).checks,
    ['native', 'checkpoint'],
  );
  assert.deepEqual(
    (
      await review({
        jobs: passing.map((row) =>
          row.name === nativeJobs[1] ? job(row.name, 'cancelled') : row,
        ),
      })
    ).checks,
    ['server', 'checkpoint'],
  );
});

test('direct pushes, changed policy and inexact or incomplete PR evidence run fresh checks', async () => {
  for (const plan of [
    { ...mergedPlan, classification: 'full' },
    { ...mergedPlan, stage: 'release' },
    { ...mergedPlan, checkedCommit: head },
    { ...mergedPlan, checks: {} },
  ]) {
    const unexpected = () =>
      assert.fail('Unexpected lookup for an ineligible plan');
    assert.equal(
      await priorPlatformChecks({
        api: unexpected,
        readPlan: unexpected,
        repository,
        plan,
      }),
      null,
    );
  }
  for (const options of [
    { pulls: [] },
    { pulls: [{ ...pull, merge_commit_sha: head }] },
    { pulls: [{ ...pull, base: { ref: 'main', sha: head } }] },
    {
      pulls: [
        { ...pull, head: { ...pull.head, repo: { full_name: 'fork/camp' } } },
      ],
    },
    { runs: [{ ...priorRun, conclusion: 'failure' }] },
    { runs: [{ ...priorRun, status: 'in_progress' }] },
    { evidence: null },
    { evidence: { ...priorPlan, pullRequest: 999 } },
    { evidence: { ...priorPlan, base: head } },
    { evidence: { ...priorPlan, checkedTree: head } },
    { evidence: { ...priorPlan, driverDigest: '3'.repeat(64) } },
    { evidence: { ...priorPlan, policyDigest: '3'.repeat(64) } },
    { jobs: passing.filter((row) => row.name !== 'Change scope') },
    { jobs: passing.filter((row) => row.name !== 'Source') },
  ])
    assert.equal(await review(options), null);
});
