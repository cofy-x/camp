import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

export const browserProjects = ['chromium', 'firefox', 'webkit', 'touch'];
export const browserEvidenceSteps = [
  'Retain radio keyboard evidence',
  'Retain Markdown file preview evidence',
  'Retain review navigation evidence',
];

export function checkBrowserShards(
  jobs,
  { projects = browserProjects, shards = 2 } = {},
) {
  for (const project of projects) {
    const selected = [];
    for (let shard = 1; shard <= shards; shard++) {
      const name = `Browser / ${project} / ${shard}/${shards}`;
      // Reusable workflow calls prepend their caller's job name.
      const matches = jobs.filter(
        (job) => job.name === name || job.name.endsWith(` / ${name}`),
      );
      if (matches.length !== 1 || matches[0].conclusion !== 'success')
        throw new Error(`Expected one successful ${name}.`);
      selected.push(matches[0]);
    }
    for (const name of browserEvidenceSteps) {
      const uploads = selected
        .flatMap((job) => job.steps ?? [])
        .filter((step) => step.name === name && step.conclusion === 'success');
      if (uploads.length !== 1)
        throw new Error(`Expected exactly one ${project} upload: ${name}.`);
    }
  }
}

export function checkRunBrowserShards({
  repository,
  runId,
  fetch = execFileSync,
}) {
  if (
    !/^[\w.-]+\/[\w.-]+$/.test(repository ?? '') ||
    !/^[0-9]+$/.test(runId ?? '')
  )
    throw new Error('Expected GITHUB_REPOSITORY and GITHUB_RUN_ID.');
  const pages = JSON.parse(
    fetch(
      'gh',
      [
        'api',
        '--paginate',
        '--slurp',
        `repos/${repository}/actions/runs/${runId}/jobs?filter=latest&per_page=100`,
      ],
      { encoding: 'utf8', maxBuffer: 16 << 20 },
    ),
  );
  if (!Array.isArray(pages) || pages.some((page) => !Array.isArray(page.jobs)))
    throw new Error('Invalid workflow job response.');
  // Latest-attempt job records also contain the original step outcomes for
  // successful shards reused by "re-run failed jobs". Never require re-uploading
  // their evidence under a new attempt number, or accept a stale artifact alone.
  checkBrowserShards(pages.flatMap((page) => page.jobs));
  console.log('Every browser shard and every required evidence upload passed.');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href)
  checkRunBrowserShards({
    repository: process.env.GITHUB_REPOSITORY,
    runId: process.env.GITHUB_RUN_ID,
  });
