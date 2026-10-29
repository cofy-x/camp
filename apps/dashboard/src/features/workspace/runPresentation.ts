import { isActiveRun } from './api';
import type { CheckResult, Run } from './api';

export function runFilesLabel(run: Run): string {
  if (run.published) return 'Saved to room files';
  if (run.taskId && run.resultCommit) return 'Saved to task draft';
  if (run.agentSnapshot.access === 'read') return 'Read-only work';
  if (!run.resultCommit) return 'No result files captured';
  return run.files?.length ? 'Changes retained separately' : 'No files changed';
}

export function checkResultLabel(check: CheckResult, run: Run): string {
  // Restart recovery may retain the last persisted running check.
  if (check.status === 'interrupted') return 'Interrupted';
  if (check.status === 'running')
    return isActiveRun(run) ? 'Running' : 'Interrupted';
  if (
    check.status === 'failed' ||
    (check.exitCode !== null && check.exitCode !== 0)
  )
    return 'Failed';
  if (check.status === 'passed' && check.exitCode === 0) return 'Passed';
  return 'Incomplete';
}

export function runChecksLabel(run: Run): string {
  if (run.checksDeferred) return 'Deferred until finish';
  const expected = run.environment?.checks ?? [];
  if (!expected.length) return 'Not configured';

  const checks = run.checks ?? [];
  const labels = checks.map((check) => checkResultLabel(check, run));
  if (labels.includes('Failed')) return 'Failed';
  if (labels.includes('Interrupted')) return 'Interrupted';
  if (run.testExitCode !== null && run.testExitCode !== 0) return 'Failed';

  const complete =
    checks.length === expected.length &&
    expected.every(
      (check) =>
        checks.filter((result) => result.name === check.name).length === 1,
    );
  if (
    complete &&
    labels.every((label) => label === 'Passed') &&
    run.testExitCode === 0
  )
    return 'Passed';

  if (
    labels.includes('Running') ||
    (run.status === 'checking' && run.testExitCode === null)
  )
    return 'Running';

  if (!checks.length && run.testExitCode === null)
    return run.agentSnapshot.access === 'read'
      ? 'Not run · read-only work'
      : 'Not run';
  return 'Incomplete';
}
