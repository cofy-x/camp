import { APIError } from '../../shared/api/http';
import type { Reference } from './api';
import { workspaceApi } from './api';
import {
  addDraftReferences,
  draftReferences,
  readStored,
  writeStored,
} from './conversationState';

export const uploadLimit = 8 * 1024 * 1024;

export interface DraftUpload {
  requestId: string;
  roomId: string;
  name: string;
  size: number;
  state: 'uploading' | 'checking' | 'error';
  error: string;
  needsFile: boolean;
}

interface UploadDependencies {
  read: (key: string) => string;
  write: (key: string, value: string) => void;
  references: (key: string) => Reference[];
  attach: (key: string, references: Reference[]) => void;
  createId: () => string;
  upload: typeof workspaceApi.upload;
  receipt: typeof workspaceApi.uploadReceipt;
}

// The conversation owns the upload, not the mounted composer. Only receipt metadata
// is persisted; browser File objects stay in memory until completion or removal.
export class UploadDraftStore {
  private entries = new Map<string, DraftUpload[]>();
  private files = new Map<string, File>();
  private active = new Map<string, AbortController>();
  private restored = new Set<string>();
  private listeners = new Map<string, Set<() => void>>();

  constructor(private dependencies: UploadDependencies) {}

  suspend() {
    for (const controller of this.active.values()) controller.abort();
    this.active.clear();
    this.files.clear();
    this.entries.clear();
    this.restored.clear();
    this.listeners.clear();
  }

  snapshot(key: string): DraftUpload[] {
    const existing = this.entries.get(key);
    if (existing) return existing;
    let entries: DraftUpload[] = [];
    try {
      const saved: unknown = JSON.parse(
        this.dependencies.read(`uploads:${key}`),
      );
      if (Array.isArray(saved))
        entries = saved
          .filter(
            (entry): entry is DraftUpload =>
              Boolean(entry) &&
              typeof entry.requestId === 'string' &&
              typeof entry.roomId === 'string' &&
              typeof entry.name === 'string' &&
              typeof entry.size === 'number',
          )
          .slice(0, 8)
          .map((entry) => ({
            ...entry,
            state: 'checking',
            error: '',
            needsFile: true,
          }));
    } catch {
      // A damaged upload record must not discard the message draft.
    }
    this.entries.set(key, entries);
    for (const entry of entries) this.restored.add(entry.requestId);
    return entries;
  }

  subscribe(key: string, listener: () => void) {
    const group = this.listeners.get(key) ?? new Set<() => void>();
    group.add(listener);
    this.listeners.set(key, group);
    return () => {
      group.delete(listener);
      if (!group.size) this.listeners.delete(key);
    };
  }

  private replace(key: string, entries: DraftUpload[]) {
    this.entries.set(key, entries);
    this.dependencies.write(
      `uploads:${key}`,
      entries.length ? JSON.stringify(entries) : '',
    );
    this.listeners.get(key)?.forEach((listener) => listener());
  }

  private update(key: string, requestId: string, update: Partial<DraftUpload>) {
    this.replace(
      key,
      this.snapshot(key).map((entry) =>
        entry.requestId === requestId ? { ...entry, ...update } : entry,
      ),
    );
  }

  add(key: string, roomId: string, files: File[]) {
    if (
      files.length +
        this.snapshot(key).length +
        this.dependencies.references(key).length >
      8
    )
      throw new Error(
        'A message can include up to 8 references. Remove a reference before attaching more files.',
      );
    for (const file of files) {
      if (file.size > uploadLimit)
        throw new Error(
          `${file.name} exceeds the 8 MiB attachment limit. Choose a smaller file.`,
        );
    }
    const entries = files.map((file) => {
      const requestId = this.dependencies.createId();
      this.files.set(requestId, file);
      return {
        requestId,
        roomId,
        name: file.name,
        size: file.size,
        state: 'uploading' as const,
        error: '',
        needsFile: false,
      };
    });
    this.replace(key, [...this.snapshot(key), ...entries]);
    for (const entry of entries) void this.perform(key, entry, false);
  }

  recover(key: string) {
    for (const entry of this.snapshot(key)) {
      if (!this.restored.delete(entry.requestId)) continue;
      void this.perform(key, entry, true);
    }
  }

  retry(key: string, requestId: string, file?: File) {
    const entry = this.snapshot(key).find(
      (entry) => entry.requestId === requestId,
    );
    if (!entry || this.active.has(requestId)) return;
    if (file) {
      if (file.name !== entry.name || file.size !== entry.size)
        throw new Error(
          `Choose the original ${entry.name} file. Remove this attachment to upload a different version.`,
        );
      this.files.set(requestId, file);
    }
    void this.perform(key, entry, true);
  }

  remove(key: string, requestId: string) {
    this.active.get(requestId)?.abort();
    this.active.delete(requestId);
    this.files.delete(requestId);
    this.restored.delete(requestId);
    this.replace(
      key,
      this.snapshot(key).filter((entry) => entry.requestId !== requestId),
    );
  }

  private async perform(key: string, entry: DraftUpload, checkFirst: boolean) {
    if (this.active.has(entry.requestId)) return;
    const controller = new AbortController();
    this.active.set(entry.requestId, controller);
    const file = this.files.get(entry.requestId);
    this.update(key, entry.requestId, {
      state: checkFirst ? 'checking' : 'uploading',
      error: '',
      needsFile: !file,
    });
    const finish = (reference: Reference) => {
      if (
        controller.signal.aborted ||
        !this.snapshot(key).some((value) => value.requestId === entry.requestId)
      )
        return;
      this.dependencies.attach(key, [reference]);
      this.files.delete(entry.requestId);
      this.replace(
        key,
        this.snapshot(key).filter(
          (value) => value.requestId !== entry.requestId,
        ),
      );
    };
    try {
      if (checkFirst) {
        try {
          const { reference } = await this.dependencies.receipt(
            entry.roomId,
            entry.requestId,
            controller.signal,
          );
          finish(reference);
          return;
        } catch (error) {
          if (!(error instanceof APIError && error.status === 404)) throw error;
        }
      }
      if (controller.signal.aborted) return;
      if (!file)
        throw new Error(
          'Choose the file again to retry. Your message draft is preserved.',
        );
      this.update(key, entry.requestId, {
        state: 'uploading',
        needsFile: false,
      });
      let reference: Reference;
      try {
        ({ reference } = await this.dependencies.upload(
          entry.roomId,
          entry.requestId,
          file,
          controller.signal,
        ));
      } catch (uploadError) {
        if (controller.signal.aborted) return;
        // A lost response can follow a successful save. Resolve its durable receipt
        // before presenting a retry, without starting a second upload automatically.
        try {
          ({ reference } = await this.dependencies.receipt(
            entry.roomId,
            entry.requestId,
            controller.signal,
          ));
        } catch {
          throw uploadError;
        }
      }
      finish(reference);
    } catch (error) {
      if (!controller.signal.aborted)
        this.update(key, entry.requestId, {
          state: 'error',
          needsFile: !this.files.has(entry.requestId),
          error:
            error instanceof Error
              ? error.message
              : 'The upload could not be confirmed. Retry or remove this attachment.',
        });
    } finally {
      if (this.active.get(entry.requestId) === controller)
        this.active.delete(entry.requestId);
    }
  }
}

export const uploadDrafts = new UploadDraftStore({
  read: readStored,
  write: writeStored,
  references: draftReferences,
  attach: addDraftReferences,
  createId: () => crypto.randomUUID(),
  upload: (...args) => workspaceApi.upload(...args),
  receipt: (...args) => workspaceApi.uploadReceipt(...args),
});

export function attachmentSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${Math.ceil(size / 1024)} KiB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MiB`;
}
