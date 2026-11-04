import assert from 'node:assert/strict';
import test from 'node:test';
import {
  browserProjects,
  browserEvidenceSteps,
  checkBrowserShards,
  checkRunBrowserShards,
} from './check-browser-shards.mjs';

function jobs() {
  return browserProjects.flatMap((project) =>
    [1, 2].map((shard) => ({
      name: `Browser / ${project} / ${shard}/2`,
      conclusion: 'success',
      steps: browserEvidenceSteps.map((name, index) => ({
        name,
        conclusion: shard === (index % 2) + 1 ? 'success' : 'skipped',
      })),
    })),
  );
}

test('every project requires both successful shards and one owner of every evidence type', () => {
  checkBrowserShards(jobs());
  for (const conclusion of [
    'failure',
    'cancelled',
    'skipped',
    null,
    undefined,
  ]) {
    const input = jobs();
    input[0].conclusion = conclusion;
    assert.throws(() => checkBrowserShards(input), /successful/);
  }
  assert.throws(() => checkBrowserShards(jobs().slice(1)), /successful/);
  assert.throws(() => checkBrowserShards([...jobs(), jobs()[0]]), /successful/);
  for (let i = 0; i < browserEvidenceSteps.length; i++) {
    const input = jobs();
    for (const job of input.slice(0, 2)) job.steps[i].conclusion = 'skipped';
    // A stale artifact from an earlier attempt cannot replace a current upload.
    input[0].artifacts = ['stale-evidence'];
    assert.throws(() => checkBrowserShards(input), /exactly one/);
    for (const job of input.slice(0, 2)) job.steps[i].conclusion = 'success';
    assert.throws(() => checkBrowserShards(input), /exactly one/);
  }
});

test('failed-job re-runs can reuse successful shard steps and release callers can prefix names', () => {
  const input = jobs().map((job) => ({
    ...job,
    name: `Complete release regression / ${job.name}`,
    run_attempt: 2,
    steps: job.steps.map((step) => ({
      ...step,
      started_at: '2026-11-01T00:00:00Z',
    })),
  }));
  checkBrowserShards(input);
});

test('the verifier paginates latest jobs and rejects unavailable or malformed responses', () => {
  checkRunBrowserShards({
    repository: 'cofy-x/camp',
    runId: '123',
    fetch: (binary, args) => {
      assert.equal(binary, 'gh');
      assert.ok(args.includes('--paginate'));
      assert.ok(args.at(-1).includes('filter=latest'));
      return JSON.stringify([
        { jobs: jobs().slice(0, 4) },
        { jobs: jobs().slice(4) },
      ]);
    },
  });
  for (const response of ['{}', '[{}]', 'not-json'])
    assert.throws(() =>
      checkRunBrowserShards({
        repository: 'cofy-x/camp',
        runId: '123',
        fetch: () => response,
      }),
    );
  assert.throws(
    () =>
      checkRunBrowserShards({
        repository: 'cofy-x/camp',
        runId: '123',
        fetch: () => {
          throw new Error('API unavailable');
        },
      }),
    /API unavailable/,
  );
});
