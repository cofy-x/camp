import { afterEach, describe, expect, it, vi } from 'vitest';
import { request } from './http';

afterEach(() => vi.unstubAllGlobals());

describe('HTTP errors', () => {
  it('turns a browser network failure into a readable connection error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new TypeError('Failed to fetch')),
    );
    await expect(request('/entries')).rejects.toMatchObject({
      message:
        'Cannot reach the desktop server. Check your connection and try again.',
      status: 0,
      code: 'network_error',
    });
  });

  it('preserves an aborted request so cancellation is not shown as a connection failure', async () => {
    const controller = new AbortController();
    controller.abort();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(controller.signal.reason));
    await expect(
      request('/entries', { signal: controller.signal }),
    ).rejects.toBe(controller.signal.reason);
  });

  it('preserves server business errors and their revision-conflict status', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: 'The document changed in another window.',
            code: 'revision_conflict',
          }),
          { status: 409 },
        ),
      ),
    );
    await expect(request('/entries/note')).rejects.toMatchObject({
      message: 'The document changed in another window.',
      status: 409,
      code: 'revision_conflict',
    });
  });
});
