import { describe, expect, it } from 'vitest';
import { workItemOverviewFallback, workItemStatus } from './presentation';

type PresentedWork = Parameters<typeof workItemStatus>[0];

const ready: PresentedWork = {
  status: 'open',
  execution: {
    taskId: 'latest-task',
    status: 'completed',
    revision: 4,
    summary: 'The weekly plan is ready.',
    error: '',
    currentAgentName: 'Writer',
    activeRunId: '',
    updatedAt: '2026-11-05T09:00:00Z',
  },
  result: {
    taskId: 'latest-task',
    summary: 'The weekly plan is ready.',
    resourceId: 'room-files',
    commit: 'saved-version',
    published: false,
    files: [{ path: 'weekly-plan.md', status: 'added' }],
  },
};

describe('work result status', () => {
  it.each<{
    name: string;
    changes: Partial<PresentedWork>;
    expected: string;
  }>([
    {
      name: 'completed latest team draft',
      changes: {},
      expected: 'Result ready',
    },
    {
      name: 'published result is available without completing the matter',
      changes: { result: { ...ready.result!, published: true } },
      expected: 'Result ready',
    },
    {
      name: 'normal open matter with no work yet',
      changes: { execution: undefined, result: undefined },
      expected: 'Open',
    },
    {
      name: 'completed task without a captured result',
      changes: { result: undefined },
      expected: 'Open',
    },
    {
      name: 'result without the latest execution projection',
      changes: { execution: undefined },
      expected: 'Open',
    },
    {
      name: 'older result does not describe the latest task',
      changes: { result: { ...ready.result!, taskId: 'earlier-task' } },
      expected: 'Open',
    },
    {
      name: 'missing task identity cannot establish a matching result',
      changes: {
        execution: { ...ready.execution!, taskId: '' },
        result: { ...ready.result!, taskId: '' },
      },
      expected: 'Open',
    },
    {
      name: 'active task remains active even with a completed projection',
      changes: { activeTaskId: 'latest-task' },
      expected: 'Open',
    },
    {
      name: 'current work supersedes an older completed result',
      changes: {
        status: 'running',
        activeTaskId: 'current-task',
        execution: {
          ...ready.execution!,
          taskId: 'current-task',
          status: 'running',
        },
      },
      expected: 'Working',
    },
    {
      name: 'failed latest work cannot make retained files ready',
      changes: {
        execution: {
          ...ready.execution!,
          status: 'needs_attention',
          error: 'Runner disconnected.',
        },
      },
      expected: 'Open',
    },
    {
      name: 'an execution error remains unresolved',
      changes: {
        execution: { ...ready.execution!, error: 'Result delivery failed.' },
      },
      expected: 'Open',
    },
    {
      name: 'matter question takes precedence over a ready result',
      changes: { question: 'Should we change the release date?' },
      expected: 'Open',
    },
    {
      name: 'unanswered task question takes precedence over a ready result',
      changes: {
        execution: {
          ...ready.execution!,
          question: {
            id: 'question',
            text: 'Which date?',
            options: [],
            answer: '',
          },
        },
      },
      expected: 'Open',
    },
    {
      name: 'answered task question does not hide the completed result',
      changes: {
        execution: {
          ...ready.execution!,
          question: {
            id: 'question',
            text: 'Which date?',
            options: [],
            answer: 'Monday',
          },
        },
      },
      expected: 'Result ready',
    },
    {
      name: 'waiting matter',
      changes: { status: 'waiting' },
      expected: 'Waiting',
    },
    {
      name: 'matter awaiting a person',
      changes: { status: 'waiting_for_person' },
      expected: 'Needs your judgment',
    },
    {
      name: 'matter needing attention',
      changes: { status: 'needs_attention' },
      expected: 'Needs attention',
    },
    {
      name: 'stopping matter',
      changes: { status: 'stopping' },
      expected: 'Stopping',
    },
    {
      name: 'completed matter',
      changes: { status: 'completed' },
      expected: 'Completed',
    },
    {
      name: 'cancelled matter',
      changes: { status: 'cancelled' },
      expected: 'Cancelled',
    },
  ])('$name', ({ changes, expected }) => {
    expect(workItemStatus({ ...ready, ...changes })).toBe(expected);
  });
});

describe('empty work overview', () => {
  const closed: Parameters<typeof workItemOverviewFallback>[0] = {
    status: 'completed',
    decisions: [
      {
        seq: 5,
        id: 'completion',
        kind: 'complete',
        reason: 'Confirmed the matter is complete.',
        reasonSource: 'system',
        actorKind: 'user',
        actorId: 'owner',
        evidenceIds: [],
        createdAt: '2026-11-05T08:51:40Z',
      },
    ],
  };

  it('uses the actual completion time without presenting a system note as a result', () => {
    expect(workItemOverviewFallback(closed, true)).toEqual({
      label: 'Marked complete',
      createdAt: closed.decisions[0].createdAt,
      note: undefined,
    });
  });

  it('retains the exact human comment from the latest matching record', () => {
    const latest = {
      ...closed.decisions[0],
      seq: 8,
      reasonSource: 'user' as const,
      reason: 'The owner checked the customer’s confirmation.\nReady to share.',
      createdAt: '2026-11-05T09:00:00Z',
    };
    expect(
      workItemOverviewFallback(
        { ...closed, decisions: [latest, ...closed.decisions] },
        true,
      ),
    ).toEqual({
      label: 'Marked complete',
      createdAt: latest.createdAt,
      note: latest.reason,
    });
  });

  it.each(['stop', 'cancel'])(
    'uses a recorded %s for an empty cancelled matter',
    (kind) => {
      expect(
        workItemOverviewFallback(
          {
            ...closed,
            status: 'cancelled',
            decisions: [{ ...closed.decisions[0], kind }],
          },
          true,
        ),
      ).toMatchObject({
        label: 'Stop recorded',
        createdAt: closed.decisions[0].createdAt,
      });
    },
  );

  it('does not invent a timestamp when the bounded records omit the closure', () => {
    expect(
      workItemOverviewFallback({ ...closed, decisions: [] }, true),
    ).toEqual({
      label: 'No result to show.',
    });
  });

  it('treats an empty result projection as empty without inferring no work occurred', () => {
    expect(
      workItemOverviewFallback(
        { ...closed, result: { ...ready.result!, commit: '', summary: '' } },
        true,
      )?.label,
    ).toBe('Marked complete');
  });

  it.each<Partial<typeof closed>>([
    { question: 'Which customer?' },
    {
      execution: {
        ...ready.execution!,
        summary: '',
        question: { id: 'q', text: 'Which customer?', options: [], answer: '' },
      },
    },
    {
      execution: {
        ...ready.execution!,
        summary: '',
        error: 'Runner disconnected.',
      },
    },
    { execution: ready.execution },
    { waitCondition: 'Waiting for confirmed delivery receipts.' },
    { result: { ...ready.result!, summary: '' } },
    { result: { ...ready.result!, commit: '' } },
    { activeTaskId: 'current-task', result: ready.result },
    { status: 'stopping' },
  ])(
    'does not add a fallback beside current content or unfinished work: %j',
    (change) => {
      expect(
        workItemOverviewFallback({ ...closed, ...change }, true),
      ).toBeNull();
    },
  );

  it('keeps editable open work clear while giving empty read-only work a quiet fact', () => {
    const open = { ...closed, status: 'open', decisions: [] };
    expect(workItemOverviewFallback(open, true)).toBeNull();
    expect(workItemOverviewFallback(open, false)).toEqual({
      label: 'No result yet.',
    });
  });

  it('does not count earlier files and duplicate hidden progress as visible read-only content', () => {
    expect(
      workItemOverviewFallback(
        {
          ...closed,
          status: 'running',
          activeTaskId: 'current-task',
          execution: ready.execution,
          result: ready.result,
        },
        false,
      ),
    ).toEqual({ label: 'No result yet.' });
  });
});
