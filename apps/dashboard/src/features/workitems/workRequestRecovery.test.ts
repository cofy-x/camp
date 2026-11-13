import { afterEach, describe, expect, it, vi } from 'vitest';
import { workItemsApi } from './api';
import {
  confirmedWorkAction,
  isPending,
  submitRetainedWorkRequest,
} from './workRequestRecovery';
import {
  restoreWorkActionDraft,
  workManagementForIntent,
} from './workActionDraft';

afterEach(() => vi.restoreAllMocks());

describe('retained Work requests', () => {
  it('replays an older creation without silently adding automatic completion', async () => {
    const request = {
      kind: 'create' as const,
      roomId: 'room',
      body: { requestId: 'original', goal: 'Save the original plan' },
    };
    const create = vi
      .spyOn(workItemsApi, 'create')
      .mockResolvedValue({ item: {} as never });
    expect(isPending(request)).toBe(true);
    await submitRetainedWorkRequest(request);
    expect(create).toHaveBeenCalledWith('room', request.body);
    expect(create.mock.calls[0][1]).not.toHaveProperty('completionMode');
  });

  it('keeps the original decision route, request ID, revision and authored reason', async () => {
    const request = {
      kind: 'decision' as const,
      itemId: 'matter',
      body: {
        requestId: 'lost-reply',
        revision: 7,
        kind: 'complete',
        reason: 'The customer confirmed it.',
      },
    };
    const decide = vi
      .spyOn(workItemsApi, 'decide')
      .mockResolvedValue({ item: {} as never });
    expect(isPending(request)).toBe(true);
    await submitRetainedWorkRequest(request);
    expect(decide).toHaveBeenCalledWith('matter', request.body);
    expect(confirmedWorkAction(request)).toBe('complete');
  });

  it('retains authored input in older private drafts without promoting execution', () => {
    const saved = { revision: 3, kind: 'answer', reason: 'Use Thursday.' };
    const draft = restoreWorkActionDraft(saved);
    expect(draft).toMatchObject({
      ...saved,
      intent: 'answer',
      content: 'Use Thursday.',
    });
    expect(saved).not.toHaveProperty('intent');
    expect(workManagementForIntent('completion')).toBe('completion');
    expect(workManagementForIntent('wait')).toBe('reminder');
  });
});
