import { describe, expect, it } from 'vitest';
import { APIError } from '../api/http';
import { ReadStore } from './store';
import { affectedRead } from './invalidation';
import type { WorkspaceEvent } from '../../features/workspace/api';

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const event = (
  type: string,
  roomId = 'one',
  entityId = 'run-one',
): WorkspaceEvent => ({ id: 1, type, roomId, entityId, createdAt: '' });

function pending() {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<unknown>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe('shared read ownership', () => {
  it('does not allocate snapshots or start reads for a large set of disabled historical rows', () => {
    let calls = 0;
    const store = new ReadStore(async () => {
      calls++;
      return {};
    });
    const absent = store.peek('/runs/absent/events?after=0');
    for (let i = 0; i < 1000; i++)
      expect(store.peek(`/runs/historical-${i}/events?after=0`)).toBe(absent);
    expect(calls).toBe(0);
    expect(store.snapshot('/rooms')).toBe(store.snapshot('/rooms'));
    store.dispose();
  });

  it('evicts settled inactive cache entries without evicting new render snapshots', async () => {
    const store = new ReadStore(async (path) => ({ path }));
    const awaitingSubscription = store.snapshot('/not-yet-subscribed');
    for (let i = 0; i < 140; i++) await store.refresh(`/retained/${i}`);
    expect(store.peek('/retained/0')).toBeUndefined();
    expect(store.peek('/retained/139')?.data).toEqual({
      path: '/retained/139',
    });
    expect(store.snapshot('/not-yet-subscribed')).toBe(awaitingSubscription);
    store.dispose();
  });

  it('shares one in-flight read and accepted snapshot across consumers', async () => {
    const read = pending();
    let calls = 0;
    const store = new ReadStore(() => {
      calls++;
      return read.promise;
    });
    const first = store.subscribe('/rooms', () => {});
    const second = store.subscribe('/rooms', () => {});
    expect(calls).toBe(1);
    read.resolve({ rooms: ['one'] });
    await tick();
    expect(store.snapshot('/rooms')).toMatchObject({
      data: { rooms: ['one'] },
      loading: false,
      refreshing: false,
    });
    first();
    second();
    store.dispose();
  });

  it('cancels abandoned reads but keeps a pending read adopted during immediate resubscription', async () => {
    const signals: AbortSignal[] = [];
    const store = new ReadStore((_path, signal) => {
      signals.push(signal);
      return pending().promise;
    });
    const first = store.subscribe('/runs/one', () => {});
    first();
    const adopted = store.subscribe('/runs/one', () => {});
    await tick();
    expect(signals).toHaveLength(1);
    expect(signals[0].aborted).toBe(false);
    adopted();
    await tick();
    expect(signals[0].aborted).toBe(true);
    store.subscribe('/runs/one', () => {});
    expect(signals).toHaveLength(2);
    store.dispose();
  });

  it('finishes a slow read then performs one follow-up for a burst of changes', async () => {
    const reads: ReturnType<typeof pending>[] = [];
    const signals: AbortSignal[] = [];
    const store = new ReadStore((_path, signal) => {
      const read = pending();
      reads.push(read);
      signals.push(signal);
      return read.promise;
    });
    store.subscribe('/rooms/one/messages?after=0', () => {});
    for (let i = 0; i < 12; i++) store.invalidate([event('message.created')]);
    expect(reads).toHaveLength(1);
    expect(signals[0].aborted).toBe(false);
    reads[0].resolve({ messages: ['accepted'] });
    await tick();
    expect(reads).toHaveLength(2);
    expect(store.snapshot('/rooms/one/messages?after=0').data).toEqual({
      messages: ['accepted'],
    });
    reads[1].resolve({ messages: ['accepted', 'new'] });
    await tick();
    expect(reads).toHaveLength(2);
    expect(store.snapshot('/rooms/one/messages?after=0').data).toEqual({
      messages: ['accepted', 'new'],
    });
    store.dispose();
  });

  it('synchronously pauses page reads, rejects buffered completions and catches up once after restoration', async () => {
    const reads: ReturnType<typeof pending>[] = [];
    const signals: AbortSignal[] = [];
    const store = new ReadStore((_path, signal) => {
      const read = pending();
      reads.push(read);
      signals.push(signal);
      return read.promise;
    });
    store.subscribe('/rooms', () => {});
    reads[0].resolve({ rooms: ['accepted'] });
    await tick();
    void store.refresh('/rooms');
    store.setActive(false);
    expect(signals[1].aborted).toBe(true);
    for (let i = 0; i < 4; i++) store.invalidateAll();
    void store.refresh('/rooms');
    reads[1].resolve({ rooms: ['buffered while away'] });
    await tick();
    expect(reads).toHaveLength(2);
    expect(store.snapshot('/rooms').data).toEqual({ rooms: ['accepted'] });
    store.setActive(true);
    expect(reads).toHaveLength(3);
    reads[2].resolve({ rooms: ['current'] });
    await tick();
    expect(store.snapshot('/rooms').data).toEqual({ rooms: ['current'] });
    store.dispose();
  });

  it('applies every Room in a notification batch without rereading unrelated views', async () => {
    const calls: string[] = [];
    const store = new ReadStore(async (path) => {
      calls.push(path);
      return {};
    });
    store.subscribe('/rooms/one/messages?after=8', () => {});
    store.subscribe('/rooms/two/messages?after=4', () => {});
    store.subscribe('/team/members', () => {});
    await tick();
    calls.length = 0;
    store.invalidate([
      event('message.created', 'one'),
      event('message.created', 'two'),
    ]);
    await tick();
    expect(calls.sort()).toEqual([
      '/rooms/one/messages?after=8',
      '/rooms/two/messages?after=4',
    ]);
    store.dispose();
  });

  it('does not refresh hidden views and revalidates accepted content on reopen', async () => {
    let value = 1,
      calls = 0;
    const store = new ReadStore(async () => {
      calls++;
      return { value };
    });
    const close = store.subscribe('/rooms', () => {});
    await tick();
    close();
    value = 2;
    store.invalidateAll();
    await tick();
    expect(calls).toBe(1);
    expect(store.snapshot('/rooms').data).toEqual({ value: 1 });
    store.subscribe('/rooms', () => {});
    expect(store.snapshot('/rooms').data).toEqual({ value: 1 });
    await tick();
    expect(calls).toBe(2);
    expect(store.snapshot('/rooms').data).toEqual({ value: 2 });
    store.dispose();
  });

  it('retains accepted data and its error until retry succeeds, including reopening', async () => {
    let fail = false;
    const store = new ReadStore(async () => {
      if (fail) throw new Error('Unavailable');
      return { value: 1 };
    });
    let close = store.subscribe('/library', () => {});
    await tick();
    fail = true;
    await store.refresh('/library');
    expect(store.snapshot('/library')).toMatchObject({
      data: { value: 1 },
      error: 'Unavailable',
      loading: false,
    });
    close();
    fail = false;
    close = store.subscribe('/library', () => {});
    expect(store.snapshot('/library').error).toBe('Unavailable');
    await tick();
    expect(store.snapshot('/library').error).toBe('');
    close();
    store.dispose();
  });

  it('clears accepted content after permission revocation', async () => {
    let revoked = false;
    const store = new ReadStore(async () => {
      if (revoked) throw new APIError('Forbidden', 403, 'forbidden');
      return { private: 'accepted' };
    });
    store.subscribe('/resources/one', () => {});
    await tick();
    revoked = true;
    await store.refresh('/resources/one');
    expect(store.snapshot('/resources/one')).toMatchObject({
      data: undefined,
      error: 'Forbidden',
      loading: false,
    });
    store.dispose();
  });

  it('aborts account disposal and cannot install an old account response', async () => {
    const read = pending();
    let signal!: AbortSignal;
    const store = new ReadStore((_path, current) => {
      signal = current;
      return read.promise;
    });
    store.subscribe('/me/inbox', () => {});
    store.dispose();
    expect(signal.aborted).toBe(true);
    read.resolve({ items: ['old account'] });
    await tick();
    expect(store.snapshot('/me/inbox').data).toBeUndefined();
    store.dispose();
  });
});

describe('notification routing', () => {
  it('refreshes joined Room names and catalogs without refreshing unrelated Room filters', () => {
    const change = event('room.updated', 'one', 'one');
    for (const path of [
      '/library',
      '/resources',
      '/me/inbox',
      '/attention',
      '/work-items?roomId=one',
      '/work-items?roomId=',
    ])
      expect(affectedRead(path, change), path).toBe(true);
    expect(affectedRead('/work-items?roomId=two', change)).toBe(false);
    expect(affectedRead('/resources/unrelated/files', change)).toBe(false);
    expect(
      affectedRead(
        '/library',
        event('upload.created', 'one', 'upload-resource'),
      ),
    ).toBe(true);
  });

  it('keeps Resource updates local and retained exact versions immutable', () => {
    const change = event('resource.updated', '', 'resource-one');
    for (const path of [
      '/resources/resource-one',
      '/resources/resource-one/files',
      '/resources/resource-one/history',
    ])
      expect(affectedRead(path, change), path).toBe(true);
    for (const path of [
      '/resources/other',
      '/resources/other/files',
      '/resources/resource-one/files?commit=exact',
      '/resources/resource-one/file?path=notes.md&commit=exact',
    ])
      expect(affectedRead(path, change), path).toBe(false);
    expect(
      affectedRead(
        '/resources/resource-one/files?commit=exact',
        event('management.updated'),
      ),
    ).toBe(true);
  });

  it('reconciles derived review freshness and responsibility when shared files change', () => {
    const change = event('resource.updated', '', 'resource-one');
    expect(affectedRead('/reviews/review-one', change)).toBe(true);
    expect(
      affectedRead('/reviews?roomId=one&resourceId=resource-one', change),
    ).toBe(true);
    expect(affectedRead('/reviews?roomId=one&resourceId=other', change)).toBe(
      false,
    );
    expect(affectedRead('/attention', change)).toBe(true);
  });

  it('updates personal read badges without treating reading as processing responsibility', () => {
    const change = event('personal_read.updated', '', 'member');
    expect(affectedRead('/me/inbox', change)).toBe(true);
    expect(affectedRead('/me/room-reads', change)).toBe(true);
    for (const path of [
      '/attention',
      '/work-items?view=needs_you',
      '/rooms',
      '/team/members',
    ])
      expect(affectedRead(path, change), path).toBe(false);
  });

  it('refreshes normalized source pages through their own canonical notification', () => {
    const change = event('automation.event.updated', 'one', 'room:original');
    expect(affectedRead('/rooms/one/automation-events?limit=20', change)).toBe(
      true,
    );
    expect(affectedRead('/rooms/two/automation-events?limit=20', change)).toBe(
      false,
    );
    expect(affectedRead('/rooms/one/automation-policies', change)).toBe(false);
    for (const type of ['message.created', 'task.updated', 'work_item.updated'])
      expect(
        affectedRead(
          '/rooms/one/automation-events?limit=20',
          event(type, 'one'),
        ),
      ).toBe(false);
  });
  it('refreshes only the current activity window without replaying pinned history', () => {
    const change = event('run.activity');
    expect(affectedRead('/runs/run-one/activity?limit=6', change)).toBe(true);
    for (const path of [
      '/runs/other/activity?limit=50',
      '/runs/run-one/activity?before=20&through=25',
      '/runs/run-one/events?after=0',
      '/runs/run-one?view=summary',
      '/rooms',
      '/workspace/status',
      '/work-items?view=all',
      '/me/inbox',
    ])
      expect(affectedRead(path, change), path).toBe(false);
    expect(
      affectedRead(
        '/runs/run-one/activity?before=20&through=25',
        event('run.updated'),
      ),
    ).toBe(false);
  });

  it('reconciles derived queue reasons without loading full executions or logs', () => {
    for (const change of [
      event('runner.updated', '', 'runner-one'),
      event('run.finished', 'other-room', 'other-run'),
    ]) {
      expect(affectedRead('/runs/queued?view=summary', change)).toBe(true);
      expect(affectedRead('/runs/queued', change)).toBe(false);
      expect(affectedRead('/runs/queued/events?after=3', change)).toBe(false);
    }
  });

  it.each([
    ['/automation-events/item', 'automation.event.updated'],
    [
      '/automation-evaluations/item/context/tasks',
      'automation.evaluation.updated',
    ],
    ['/github-events/item', 'github.event.decided'],
    ['/github-sources/item', 'github.source.updated'],
    ['/memories/item', 'memory.updated'],
    ['/source-imports/item', 'source_import.updated'],
  ])('targets %s by entity identity', (path, type) => {
    expect(affectedRead(path, event(type, 'one', 'item'))).toBe(true);
    expect(affectedRead(path, event(type, 'one', 'other'))).toBe(false);
  });

  it('does not invalidate exact historical memory revisions', () => {
    expect(
      affectedRead(
        '/memories/item?revision=2',
        event('memory.updated', 'one', 'item'),
      ),
    ).toBe(false);
  });

  it('matches URL-encoded canonical source identities', () => {
    expect(
      affectedRead(
        '/automation-events/room%3Aoriginal',
        event('automation.event.updated', 'one', 'room:original'),
      ),
    ).toBe(true);
    expect(
      affectedRead(
        '/automation-events/room%3Aother',
        event('automation.event.updated', 'one', 'room:original'),
      ),
    ).toBe(false);
  });

  it('updates execution defaults and enrollment lists from their business events', () => {
    expect(
      affectedRead('/execution-defaults', event('execution_defaults.updated')),
    ).toBe(true);
    expect(
      affectedRead('/runner-enrollments', event('runner_enrollment.updated')),
    ).toBe(true);
  });
});
