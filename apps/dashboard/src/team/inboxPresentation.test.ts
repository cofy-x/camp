import { describe, expect, it } from 'vitest';
import { inboxItemDetail, inboxItemTitle } from './inboxPresentation';

describe('Inbox presentation', () => {
  it('identifies the actual matter and puts an unanswered question before a result summary', () => {
    expect(
      inboxItemTitle({
        kind: 'task_question',
        title: 'Ship the Room summary. Use the full pilot notes for evidence.',
        reviewTitle: '',
        workItemId: 'work-1',
      }),
    ).toBe('Ship the Room summary.');
    expect(
      inboxItemDetail({
        question: 'Which audience should receive it?',
        summary: 'Draft prepared.',
      }),
    ).toBe('Which audience should receive it?');
    expect(
      inboxItemDetail({
        question: '',
        summary: 'Published the approved version.',
      }),
    ).toBe('Published the approved version.');
  });

  it('retains a useful fallback for older notifications without metadata', () => {
    expect(inboxItemTitle({ kind: 'task_finished', reviewTitle: '' })).toBe(
      'Work is ready for review',
    );
    expect(
      inboxItemTitle({ kind: 'review_requested', reviewTitle: 'Launch plan' }),
    ).toBe('Launch plan');
    expect(
      inboxItemTitle({
        kind: 'review_requested',
        title: 'Launch plan. Wider rollout',
        reviewTitle: 'Launch plan. Wider rollout',
      }),
    ).toBe('Launch plan. Wider rollout');
  });
});
