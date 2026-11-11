import { APIError, request } from '../api/http';
import type { WorkspaceEvent } from '../../features/workspace/api';
import { affectedRead } from './invalidation';

export interface ReadSnapshot<T> {
  data: T | undefined;
  error: string;
  loading: boolean;
  refreshing: boolean;
}

type Entry = {
  path: string;
  snapshot: ReadSnapshot<unknown>;
  listeners: Set<() => void>;
  stale: boolean;
  pending?: Promise<void>;
  controller?: AbortController;
  dirty: boolean;
};

export class ReadStore {
  private entries = new Map<string, Entry>();
  private active = true;
  private activityListeners = new Set<() => void>();

  constructor(
    private load = (path: string, signal: AbortSignal): Promise<unknown> =>
      request(path, { signal }),
  ) {}

  isActive = () => this.active;

  subscribeActivity = (listener: () => void) => {
    this.activityListeners.add(listener);
    return () => this.activityListeners.delete(listener);
  };

  setActive(active: boolean) {
    if (this.active === active) return;
    this.active = active;
    for (const entry of this.entries.values()) {
      if (!active) {
        entry.controller?.abort();
        entry.controller = undefined;
        entry.pending = undefined;
        entry.stale = true;
        entry.dirty = false;
        this.publish(entry, { ...entry.snapshot, refreshing: false });
      } else if (entry.listeners.size && entry.stale) void this.fetch(entry);
    }
    for (const listener of this.activityListeners) listener();
  }

  private entry(path: string) {
    let entry = this.entries.get(path);
    if (!entry) {
      entry = {
        path,
        snapshot: {
          data: undefined,
          error: '',
          loading: true,
          refreshing: false,
        },
        listeners: new Set(),
        stale: true,
        dirty: false,
      };
      this.entries.set(path, entry);
      // Metadata views retain a small account-local working set, not an
      // unbounded cache of every previously visited Room and history page.
      if (this.entries.size > 128)
        for (const [key, value] of this.entries) {
          if (
            value !== entry &&
            !value.listeners.size &&
            !value.pending &&
            (value.snapshot.data !== undefined || value.snapshot.error)
          ) {
            this.entries.delete(key);
            break;
          }
        }
    }
    return entry;
  }

  snapshot<T>(path: string): ReadSnapshot<T> {
    return this.entry(path).snapshot as ReadSnapshot<T>;
  }

  peek<T>(path: string): ReadSnapshot<T> | undefined {
    return this.entries.get(path)?.snapshot as ReadSnapshot<T> | undefined;
  }

  subscribe(path: string, listener: () => void) {
    const entry = this.entry(path);
    const resumed = entry.listeners.size === 0;
    entry.listeners.add(listener);
    if (
      entry.stale ||
      (resumed &&
        !entry.pending &&
        (entry.snapshot.data !== undefined || entry.snapshot.error))
    )
      void this.fetch(entry);
    return () => {
      entry.listeners.delete(listener);
      queueMicrotask(() => {
        if (entry.listeners.size || !entry.controller) return;
        entry.controller.abort();
        entry.controller = undefined;
        entry.pending = undefined;
        entry.stale = true;
        entry.snapshot = { ...entry.snapshot, refreshing: false };
      });
    };
  }

  private publish(entry: Entry, snapshot: ReadSnapshot<unknown>) {
    entry.snapshot = snapshot;
    for (const listener of entry.listeners) listener();
  }

  private fetch(entry: Entry): Promise<void> {
    if (!this.active) return Promise.resolve();
    if (entry.pending) return entry.pending;
    const controller = new AbortController();
    entry.controller = controller;
    entry.stale = false;
    entry.dirty = false;
    this.publish(entry, {
      ...entry.snapshot,
      loading: entry.snapshot.data === undefined,
      refreshing: true,
    });
    entry.pending = this.load(entry.path, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted)
          this.publish(entry, {
            data,
            error: '',
            loading: false,
            refreshing: false,
          });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        const revoked =
          error instanceof APIError &&
          (error.status === 401 || error.status === 403);
        this.publish(entry, {
          data: revoked ? undefined : entry.snapshot.data,
          error:
            error instanceof Error
              ? error.message
              : 'Could not load this view.',
          loading: false,
          refreshing: false,
        });
      })
      .finally(() => {
        if (entry.controller !== controller) return;
        entry.pending = undefined;
        entry.controller = undefined;
        if (entry.dirty && entry.listeners.size) void this.fetch(entry);
      });
    return entry.pending;
  }

  refresh(path: string): Promise<void> {
    const entry = this.entry(path);
    entry.stale = true;
    if (entry.pending) {
      entry.dirty = true;
      return entry.pending;
    }
    return this.fetch(entry);
  }

  invalidate(events: WorkspaceEvent[]) {
    this.invalidateWhere((path) =>
      events.some((event) => affectedRead(path, event)),
    );
  }

  invalidateAll() {
    this.invalidateWhere(() => true);
  }

  private invalidateWhere(matches: (path: string) => boolean) {
    for (const entry of this.entries.values()) {
      if (!matches(entry.path)) continue;
      entry.stale = true;
      if (entry.pending) entry.dirty = true;
      else if (entry.listeners.size) void this.fetch(entry);
    }
  }

  dispose() {
    this.activityListeners.clear();
    for (const entry of this.entries.values()) {
      entry.listeners.clear();
      entry.controller?.abort();
    }
    this.entries.clear();
  }
}
