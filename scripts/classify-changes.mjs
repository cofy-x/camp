import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, writeFile, rm, appendFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import {
  classifyChanges,
  parseNameStatus,
  validationStage,
} from './change-policy.mjs';

export async function classifyGit({ root, base, head, loadBasePolicy = true }) {
  const git = (...args) =>
    execFileSync('git', ['-C', root, ...args], { encoding: 'utf8' });
  const sha = /^[a-f0-9]{40}$/;
  let policy = { classifyChanges, parseNameStatus },
    temporary,
    uncertain,
    policyDigest;
  try {
    if (!sha.test(base ?? '') || !sha.test(head ?? '') || /^0+$/.test(base))
      throw new Error('No trustworthy base/head identity.');
    for (const id of [base, head]) git('cat-file', '-e', `${id}^{commit}`);
    git('merge-base', '--is-ancestor', base, head);
    if (loadBasePolicy) {
      let source;
      try {
        source = git('show', `${base}:scripts/change-policy.mjs`);
      } catch {
        throw new Error(
          'Baseline predates the classifier; complete validation is required.',
        );
      }
      temporary = await mkdtemp(path.join(os.tmpdir(), 'camp-policy-'));
      policyDigest = createHash('sha256').update(source).digest('hex');
      const file = path.join(temporary, 'policy.mjs');
      await writeFile(file, source, { mode: 0o600 });
      policy = await import(pathToFileURL(file));
    }
    const changes = policy.parseNameStatus(
      git('diff', '--name-status', '-z', '--find-renames', base, head),
    );
    return {
      ...policy.classifyChanges(changes),
      base,
      commit: head,
      policySource: loadBasePolicy ? base : head,
      policyDigest,
    };
  } catch (error) {
    uncertain = error.message;
    return {
      ...classifyChanges([], { uncertain }),
      base: base ?? null,
      commit: head ?? null,
      policySource: null,
    };
  } finally {
    if (temporary) await rm(temporary, { recursive: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  const root = process.cwd();
  const event = process.env.GITHUB_EVENT_PATH
    ? JSON.parse(await readFile(process.env.GITHUB_EVENT_PATH, 'utf8'))
    : {};
  const base = process.argv[2] ?? event.pull_request?.base?.sha ?? event.before;
  const head =
    process.argv[3] ?? event.pull_request?.head?.sha ?? process.env.GITHUB_SHA;
  const result = validationStage(
    await classifyGit({ root, base, head }),
    process.env.CAMP_VALIDATION_STAGE || 'merge',
  );
  result.checkedCommit = execFileSync('git', ['rev-parse', 'HEAD'], {
    encoding: 'utf8',
  }).trim();
  result.checkedTree = execFileSync('git', ['rev-parse', 'HEAD^{tree}'], {
    encoding: 'utf8',
  }).trim();
  result.toolchain = {
    node: process.version,
    git: execFileSync('git', ['--version'], { encoding: 'utf8' }).trim(),
  };
  result.driverDigest = createHash('sha256')
    .update(await readFile(new URL('./classify-changes.mjs', import.meta.url)))
    .digest('hex');
  console.log(JSON.stringify(result, null, 2));
  if (process.env.GITHUB_OUTPUT) {
    const values = {
      docs: result.documentationOnly,
      governance: result.governanceReview,
      ...result.checks,
    };
    await appendFile(
      process.env.GITHUB_OUTPUT,
      Object.entries(values)
        .map(([key, value]) => `${key}=${value}`)
        .join('\n') + '\n',
    );
  }
  if (process.env.GITHUB_STEP_SUMMARY)
    await appendFile(
      process.env.GITHUB_STEP_SUMMARY,
      `## Change validation\n\nBase: \`${result.base}\`\n\nCommit: \`${result.commit}\`\n\n${result.reasons.join('\n\n')}\n\nGovernance behavior review required: **${result.governanceReview}**.\n\nChecks: \`${JSON.stringify(result.checks)}\`\n`,
    );
}
