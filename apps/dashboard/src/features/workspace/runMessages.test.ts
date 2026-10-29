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
  it('reads beyond the first page and matches the agent reply for this run', async () => {
    const first = Array.from({ length: 500 }, (_, index) => message(index + 1));
    first[0] = message(1, { authorKind: 'system', runId: run.id });
    first[1] = message(2, { authorKind: 'agent', runId: 'another-run' });
    const reply = message(501, { authorKind: 'agent', runId: run.id });
    const fetch = vi
      .spyOn(workspaceApi, 'messages')
      .mockResolvedValueOnce({ messages: first })
      .mockResolvedValueOnce({ messages: [reply] });
    const signal = new AbortController().signal;
    expect(await findRunReply(run, signal)).toBe(reply);
    expect(fetch.mock.calls).toEqual([
      [run.roomId, run.threadRootId, 0, signal],
      [run.roomId, run.threadRootId, 500, signal],
    ]);
  });

  it('returns no reply when a page is exhausted or fails to advance', async () => {
    const fetch = vi.spyOn(workspaceApi, 'messages');
    fetch.mockResolvedValueOnce({ messages: [message(1)] });
    expect(await findRunReply(run)).toBeNull();
    const page = Array.from({ length: 500 }, (_, index) => message(index + 1));
    fetch.mockResolvedValue({ messages: page });
    expect(await findRunReply(run)).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('does not return a reply after navigation cancels the request', async () => {
    const controller = new AbortController();
    vi.spyOn(workspaceApi, 'messages').mockImplementation(async () => {
      controller.abort();
      return { messages: [message(1, { authorKind: 'agent', runId: run.id })] };
    });
    await expect(findRunReply(run, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
  });

  it('preserves a failed lookup for the caller to report', async () => {
    const error = new Error('Could not load messages.');
    vi.spyOn(workspaceApi, 'messages').mockRejectedValue(error);
    await expect(findRunReply(run)).rejects.toBe(error);
  });
});
