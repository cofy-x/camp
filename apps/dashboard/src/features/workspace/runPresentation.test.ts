import { describe, expect, it } from 'vitest';
import type { CheckResult, Run } from './api';
import {
  checkResultLabel,
  runChecksLabel,
  runFilesLabel,
} from './runPresentation';

function result(name: string, changes: Partial<CheckResult> = {}): CheckResult {
  return {
    name,
    command: name,
    status: 'passed',
    exitCode: 0,
    output: '',
    startedAt: '2026-10-28T01:00:00Z',
    finishedAt: '2026-10-28T01:00:01Z',
    ...changes,
  };
}

function run(changes: Partial<Run> = {}): Run {
  return {
    id: 'run',
    roomId: 'room',
    threadRootId: '',
    originMessageId: 'request',
    agentId: 'agent',
    resourceId: 'resource',
    prompt: 'Revise the document.',
    references: [],
    status: 'succeeded',
    createdAt: '2026-10-28T01:00:00Z',
    updatedAt: '2026-10-28T01:00:01Z',
    finishedAt: '2026-10-28T01:00:01Z',
    error: '',
    output: '',
    sessionId: '',
    sessionMode: 'fresh',
    baselineCommit: 'baseline',
    resultCommit: '',
    agentSnapshot: {
      id: 'agent',
      name: 'Writer',
      handle: 'writer',
      icon: 'document',
      instructions: '',
      access: 'write',
      enabled: true,
      revision: 1,
      createdAt: '',
      updatedAt: '',
    },
    environment: {
      kind: 'command',
      checks: [
        { name: 'content', arguments: ['check-content'] },
        { name: 'links', arguments: ['check-links'] },
      ],
    },
    checks: [],
    files: [],
    exitCode: 0,
    testExitCode: null,
    maxTokens: 4096,
    maxSteps: 8,
    timeoutSeconds: 180,
    sessionGeneration: 1,
    published: false,
    ...changes,
  };
}

describe('recorded execution outcomes', () => {
  it('keeps a successful collaboration turn separate from shared publication and final checks', () => {
    const draft = run({
      taskId: 'task',
      resultCommit: 'checkpoint',
      checksDeferred: true,
    });
    expect(runFilesLabel(draft)).toBe('Saved to task draft');
    expect(runChecksLabel(draft)).toBe('Deferred until finish');
    expect(runFilesLabel({ ...draft, published: true })).toBe(
      'Saved to room files',
    );
  });
  it('requires a captured result to claim changes were retained or files were unchanged', () => {
    const files: Run['files'] = [
      { path: 'old.md', status: 'deleted', before: 'Old text', after: '' },
    ];
    expect(runFilesLabel(run({ status: 'failed', files }))).toBe(
      'No result files captured',
    );
    expect(runFilesLabel(run({ status: 'interrupted' }))).toBe(
      'No result files captured',
    );
    expect(runFilesLabel(run({ resultCommit: 'result', files }))).toBe(
      'Changes retained separately',
    );
    expect(runFilesLabel(run({ resultCommit: 'result' }))).toBe(
      'No files changed',
    );
  });

  it('distinguishes shared publication from read-only work without a captured result', () => {
    expect(
      runFilesLabel(run({ published: true, resultCommit: 'result' })),
    ).toBe('Saved to room files');
    expect(
      runFilesLabel(
        run({ agentSnapshot: { ...run().agentSnapshot, access: 'read' } }),
      ),
    ).toBe('Read-only work');
  });

  it('distinguishes missing check configuration from checks that were never run', () => {
    expect(runChecksLabel(run({ environment: null }))).toBe('Not configured');
    expect(runChecksLabel(run({ status: 'cancelled' }))).toBe('Not run');
    expect(
      runChecksLabel(
        run({ agentSnapshot: { ...run().agentSnapshot, access: 'read' } }),
      ),
    ).toBe('Not run · read-only work');
  });

  it('requires complete matching check records and aggregate success before claiming a pass', () => {
    const checks = [result('content'), result('links')];
    expect(runChecksLabel(run({ checks, testExitCode: 0 }))).toBe('Passed');
    for (const incomplete of [
      { checks, testExitCode: null },
      { checks: [], testExitCode: 0 },
      { checks: [checks[0]], testExitCode: 0 },
      { checks: [checks[0], checks[0]], testExitCode: 0 },
      { checks: [checks[0], result('unrelated')], testExitCode: 0 },
      {
        checks: [checks[0], result('links', { exitCode: null })],
        testExitCode: 0,
      },
    ])
      expect(runChecksLabel(run(incomplete))).toBe('Incomplete');
  });

  it('preserves failed evidence even when the aggregate exit code claims success', () => {
    expect(
      runChecksLabel(
        run({
          checks: [
            result('content'),
            result('links', { status: 'failed', exitCode: 1 }),
          ],
          testExitCode: 0,
        }),
      ),
    ).toBe('Failed');
    expect(
      runChecksLabel(
        run({ checks: [result('content'), result('links')], testExitCode: 1 }),
      ),
    ).toBe('Failed');
  });

  it('presents interrupted checks separately from a command failure', () => {
    const interrupted = result('links', { status: 'interrupted', exitCode: 1 });
    const cancelled = run({
      status: 'cancelled',
      checks: [result('content'), interrupted],
      testExitCode: 1,
    });
    expect(checkResultLabel(interrupted, cancelled)).toBe('Interrupted');
    expect(runChecksLabel(cancelled)).toBe('Interrupted');
  });

  it('never leaves a recovered terminal run displaying a running check', () => {
    const active = result('content', {
      status: 'running',
      exitCode: null,
      finishedAt: '',
    });
    expect(runChecksLabel(run({ status: 'checking', checks: [active] }))).toBe(
      'Running',
    );
    const recovered = run({ status: 'interrupted', checks: [active] });
    expect(runChecksLabel(recovered)).toBe('Interrupted');
    expect(checkResultLabel(active, recovered)).toBe('Interrupted');
  });

  it('distinguishes checks in progress from a partial record left after termination', () => {
    expect(runChecksLabel(run({ status: 'checking' }))).toBe('Running');
    const partial = [result('content')];
    expect(runChecksLabel(run({ status: 'checking', checks: partial }))).toBe(
      'Running',
    );
    expect(
      runChecksLabel(run({ status: 'interrupted', checks: partial })),
    ).toBe('Incomplete');
  });
});
