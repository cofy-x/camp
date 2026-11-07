import { afterEach, describe, expect, it, vi } from 'vitest';
import { workspaceApi, type Message } from './api';
import { findRunReply } from './runMessages';

const run = { id: 'run-1', roomId: 'room-1', threadRootId: 'thread-1' };

function message(seq: number, overrides: Partial<Message> = {}): Message {
  return {
    id: `message-${seq}`,
    roomId: run.roomId,
    threadRootId: run.threadRootId,
    authorKind: 'user',
    authorUserId: 'user-1',
    authorName: 'Alex',
    agentId: '',
    runId: '',
    content: 'Recorded message',
    requestId: '',
    references: [],
    seq,
    createdAt: '2026-10-28T00:00:00Z',
    updatedAt: '2026-10-28T00:00:00Z',
    ...overrides,
  };
}

afterEach(() => vi.restoreAllMocks());

describe('completed run replies', () => {
  it('loads the exact reply without downloading the surrounding conversation', async () => {
    const reply = message(501, { authorKind: 'agent', runId: run.id });
    const fetch = vi
      .spyOn(workspaceApi, 'runReply')
      .mockResolvedValue({ message: reply });
    const conversation = vi.spyOn(workspaceApi, 'messages');
    const signal = new AbortController().signal;
    expect(await findRunReply(run, signal)).toBe(reply);
    expect(fetch.mock.calls).toEqual([[run.id, signal]]);
    expect(conversation).not.toHaveBeenCalled();
  });

  it('retains an existing execution with no Agent reply as an empty result', async () => {
    vi.spyOn(workspaceApi, 'runReply').mockResolvedValue({ message: null });
    expect(await findRunReply(run)).toBeNull();
  });

  it.each([
    { authorKind: 'system' as const },
    { runId: 'another-run' },
    { roomId: 'another-room' },
    { threadRootId: 'another-thread' },
  ])('rejects a reply whose source identity changed: %j', async (changed) => {
    vi.spyOn(workspaceApi, 'runReply').mockResolvedValue({
      message: message(1, { authorKind: 'agent', runId: run.id, ...changed }),
    });
    await expect(findRunReply(run)).rejects.toThrow(
      'Could not verify this Agent reply.',
    );
  });

  it('does not return a reply after navigation cancels the request', async () => {
    const controller = new AbortController();
    vi.spyOn(workspaceApi, 'runReply').mockImplementation(async () => {
      controller.abort();
      return { message: message(1, { authorKind: 'agent', runId: run.id }) };
    });
    await expect(findRunReply(run, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
  });

  it('preserves a failed lookup for the caller to report', async () => {
    const error = new Error('Could not load the reply.');
    vi.spyOn(workspaceApi, 'runReply').mockRejectedValue(error);
    await expect(findRunReply(run)).rejects.toBe(error);
  });
});
