import type { SourceImportInput } from '../runners/api';

export type ImportReceipt =
  | { input: SourceImportInput; importId?: string }
  | { input?: undefined; importId: string };

export function parseImportReceipt(value: string | null): ImportReceipt | null {
  if (!value) return null;
  try {
    const record = JSON.parse(value) as Partial<ImportReceipt>;
    const input = record.input;
    if (!input && typeof record.importId === 'string' && record.importId)
      return { importId: record.importId };
    if (
      !input ||
      typeof input.requestId !== 'string' ||
      !input.requestId ||
      typeof input.runnerId !== 'string' ||
      !input.runnerId ||
      typeof input.sourceId !== 'string' ||
      !input.sourceId ||
      typeof input.expectedSourceRevision !== 'string' ||
      !input.expectedSourceRevision ||
      typeof input.roomName !== 'string' ||
      !input.roomName.trim() ||
      (record.importId !== undefined && typeof record.importId !== 'string')
    )
      return null;
    return { input, importId: record.importId };
  } catch {
    return null;
  }
}

const memory = new Map<string, ImportReceipt | null>();

export function readImportReceipt(userId: string) {
  if (memory.has(userId)) return memory.get(userId) ?? null;
  try {
    return parseImportReceipt(
      sessionStorage.getItem(`camp:source-import:${userId}`),
    );
  } catch {
    return null;
  }
}

export function saveImportReceipt(userId: string, value: ImportReceipt | null) {
  memory.set(userId, value);
  try {
    if (value)
      sessionStorage.setItem(
        `camp:source-import:${userId}`,
        JSON.stringify(value),
      );
    else sessionStorage.removeItem(`camp:source-import:${userId}`);
  } catch {
    // Preserve receipt identity across window closes when storage is unavailable.
  }
}
