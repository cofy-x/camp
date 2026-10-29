import { describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import type { Reference } from './api';
import { UploadDraftStore, uploadLimit } from './uploadState';

const missing = () => new APIError('Upload not found.', 404, 'not_found');
const file = (name = 'brief.md') =>
  new File(['A launch brief'], name, { lastModified: 100 });
const reference = (name = 'brief.md'): Reference => ({
  kind: 'file',
  resourceId: `upload-${name}`,
  path: name,
  commit: 'immutable',
  title: name,
  origin: 'upload',
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

function fixture(
  saved = new Map<string, string>(),
  drafts = new Map<string, Reference[]>(),
) {
  let next = 0;
  const upload = vi.fn(
    async (
      _room: string,
      _request: string,
      selected: File,
      _signal?: AbortSignal,
    ) => ({ reference: reference(selected.name) }),
  );
  const receipt = vi.fn(
    async (
      _room: string,
      _request: string,
      _signal?: AbortSignal,
    ): Promise<{ reference: Reference }> => {
      throw missing();
    },
  );
  const dependencies = {
    read: (key: string) => saved.get(key) ?? '',
    write: (key: string, value: string) => {
      if (value) saved.set(key, value);
      else saved.delete(key);
    },
    references: (key: string) => drafts.get(key) ?? [],
    attach: (key: string, refs: Reference[]) => {
      const existing = drafts.get(key) ?? [];
      if (existing.length + refs.length > 8)
        throw new Error('Too many references.');
      drafts.set(key, [...existing, ...refs]);
    },
    createId: () => `request-${++next}`,
    upload,
    receipt,
  };
  return {
    store: new UploadDraftStore(dependencies),
    dependencies,
    upload,
    receipt,
    saved,
    drafts,
  };
}

describe('draft upload lifecycle', () => {
  it('completes into its original room and thread after navigation, preserving newer references', async () => {
    const f = fixture();
    const completion = deferred<{ reference: Reference }>();
    f.upload.mockReturnValueOnce(completion.promise);
    f.store.add('room-a:thread-a', 'room-a', [file()]);
    f.drafts.set('room-a:thread-a', [reference('newer.md')]);
    f.drafts.set('room-b:room', [reference('other.md')]);
    expect(f.store.snapshot('room-b:room')).toEqual([]);
    expect(f.saved.get('uploads:room-a:thread-a')).not.toContain(
      'A launch brief',
    );
    completion.resolve({ reference: reference() });
    await vi.waitFor(() =>
      expect(f.store.snapshot('room-a:thread-a')).toEqual([]),
    );
    expect(f.drafts.get('room-a:thread-a')?.map((ref) => ref.path)).toEqual([
      'newer.md',
      'brief.md',
    ]);
    expect(f.drafts.get('room-b:room')?.map((ref) => ref.path)).toEqual([
      'other.md',
    ]);
    expect(f.saved.has('uploads:room-a:thread-a')).toBe(false);
  });

  it('does not resurrect a removed upload when a non-cooperative request completes late', async () => {
    const f = fixture();
    const completion = deferred<{ reference: Reference }>();
    f.upload.mockReturnValueOnce(completion.promise);
    f.store.add('room:room', 'room', [file()]);
    const signal = f.upload.mock.calls[0][3];
    f.store.remove('room:room', 'request-1');
    expect(signal?.aborted).toBe(true);
    completion.resolve({ reference: reference() });
    await completion.promise;
    await Promise.resolve();
    expect(f.store.snapshot('room:room')).toEqual([]);
    expect(f.drafts.get('room:room')).toBeUndefined();
  });

  it('resolves a lost upload response through its receipt without another POST', async () => {
    const f = fixture();
    f.upload.mockRejectedValueOnce(
      new APIError('Disconnected', 0, 'network_error'),
    );
    f.receipt.mockResolvedValueOnce({ reference: reference() });
    f.store.add('room:room', 'room', [file()]);
    await vi.waitFor(() =>
      expect(f.drafts.get('room:room')).toEqual([reference()]),
    );
    expect(f.upload).toHaveBeenCalledTimes(1);
    expect(f.receipt.mock.calls[0].slice(0, 2)).toEqual(['room', 'request-1']);
    expect(f.store.snapshot('room:room')).toEqual([]);
  });

  it('keeps file bytes in memory and the same request ID for an explicit retry', async () => {
    const f = fixture();
    const selected = file();
    f.upload.mockRejectedValueOnce(new Error('Connection lost'));
    f.store.add('room:room', 'room', [selected]);
    await vi.waitFor(() =>
      expect(f.store.snapshot('room:room')[0].state).toBe('error'),
    );
    expect(f.store.snapshot('room:room')[0].needsFile).toBe(false);
    f.store.retry('room:room', 'request-1');
    await vi.waitFor(() => expect(f.store.snapshot('room:room')).toEqual([]));
    expect(f.upload).toHaveBeenCalledTimes(2);
    expect(f.upload.mock.calls.map((call) => call.slice(0, 3))).toEqual([
      ['room', 'request-1', selected],
      ['room', 'request-1', selected],
    ]);
  });

  it('recovers a saved receipt after reload without pretending File bytes persisted', async () => {
    const f = fixture();
    const completion = deferred<{ reference: Reference }>();
    f.upload.mockReturnValueOnce(completion.promise);
    f.store.add('room:thread', 'room', [file()]);
    const reloaded = fixture(f.saved, f.drafts);
    reloaded.receipt.mockResolvedValueOnce({ reference: reference() });
    expect(reloaded.store.snapshot('room:thread')[0].needsFile).toBe(true);
    reloaded.store.recover('room:thread');
    reloaded.store.recover('room:thread');
    await vi.waitFor(() =>
      expect(reloaded.store.snapshot('room:thread')).toEqual([]),
    );
    expect(reloaded.receipt).toHaveBeenCalledTimes(1);
    expect(reloaded.upload).not.toHaveBeenCalled();
    expect(reloaded.drafts.get('room:thread')).toEqual([reference()]);
  });

  it('asks for original bytes after interrupted upload and rejects a different replacement', async () => {
    const f = fixture();
    f.upload.mockReturnValueOnce(new Promise(() => {}));
    const selected = file();
    f.store.add('room:room', 'room', [selected]);
    const reloaded = fixture(f.saved, f.drafts);
    reloaded.store.recover('room:room');
    await vi.waitFor(() =>
      expect(reloaded.store.snapshot('room:room')[0].state).toBe('error'),
    );
    expect(reloaded.store.snapshot('room:room')[0].error).toContain(
      'Choose the file again',
    );
    expect(reloaded.upload).not.toHaveBeenCalled();
    expect(() =>
      reloaded.store.retry('room:room', 'request-1', file('different.md')),
    ).toThrow('Choose the original');
    const copied = new File([selected], selected.name, { lastModified: 200 });
    reloaded.store.retry('room:room', 'request-1', copied);
    await vi.waitFor(() =>
      expect(reloaded.store.snapshot('room:room')).toEqual([]),
    );
    expect(reloaded.upload.mock.calls[0].slice(0, 3)).toEqual([
      'room',
      'request-1',
      copied,
    ]);
  });

  it('does not resend on a failed receipt lookup after reload', async () => {
    const f = fixture();
    f.upload.mockReturnValueOnce(new Promise(() => {}));
    f.store.add('room:room', 'room', [file()]);
    const reloaded = fixture(f.saved);
    reloaded.receipt.mockRejectedValueOnce(
      new APIError('Offline', 0, 'network_error'),
    );
    reloaded.store.recover('room:room');
    await vi.waitFor(() =>
      expect(reloaded.store.snapshot('room:room')[0].error).toBe('Offline'),
    );
    expect(reloaded.upload).not.toHaveBeenCalled();
    reloaded.receipt.mockResolvedValueOnce({ reference: reference() });
    reloaded.store.retry('room:room', 'request-1');
    await vi.waitFor(() =>
      expect(reloaded.store.snapshot('room:room')).toEqual([]),
    );
    expect(reloaded.upload).not.toHaveBeenCalled();
  });

  it('reserves the eight-reference limit across pending uploads and existing evidence', () => {
    const f = fixture();
    f.drafts.set(
      'room:room',
      Array.from({ length: 6 }, (_, i) => reference(`${i}.md`)),
    );
    f.upload.mockReturnValue(new Promise(() => {}));
    f.store.add('room:room', 'room', [file('a.md')]);
    expect(() =>
      f.store.add('room:room', 'room', [file('b.md'), file('c.md')]),
    ).toThrow('up to 8');
    expect(f.upload).toHaveBeenCalledTimes(1);
    expect(f.store.snapshot('room:room')).toHaveLength(1);
  });

  it('retains a successful upload receipt if newer evidence fills the draft before completion', async () => {
    const f = fixture();
    const completion = deferred<{ reference: Reference }>();
    f.upload.mockReturnValueOnce(completion.promise);
    f.store.add('room:room', 'room', [file()]);
    f.drafts.set(
      'room:room',
      Array.from({ length: 8 }, (_, i) => reference(`${i}.md`)),
    );
    completion.resolve({ reference: reference() });
    await vi.waitFor(() =>
      expect(f.store.snapshot('room:room')[0].error).toBe(
        'Too many references.',
      ),
    );
    f.drafts.set('room:room', [reference('keep.md')]);
    f.receipt.mockResolvedValueOnce({ reference: reference() });
    f.store.retry('room:room', 'request-1');
    await vi.waitFor(() => expect(f.store.snapshot('room:room')).toEqual([]));
    expect(f.upload).toHaveBeenCalledTimes(1);
    expect(f.drafts.get('room:room')?.map((ref) => ref.path)).toEqual([
      'keep.md',
      'brief.md',
    ]);
  });

  it('rejects an oversized batch before starting any upload', () => {
    const f = fixture();
    const large = new File([new Uint8Array(uploadLimit + 1)], 'large.bin');
    expect(() => f.store.add('room:room', 'room', [file(), large])).toThrow(
      '8 MiB',
    );
    expect(f.upload).not.toHaveBeenCalled();
    expect(f.store.snapshot('room:room')).toEqual([]);
  });
});
