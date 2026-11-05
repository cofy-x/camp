import { request } from '../../shared/api/http';

export interface MemoryProvenance {
  kind: 'human_note' | 'message' | 'source_event' | 'file';
  messageId?: string;
  sourceEventId?: string;
  resourceId?: string;
  commit?: string;
  path?: string;
}

export const memorySourceLabel: Record<MemoryProvenance['kind'], string> = {
  human_note: 'Human note',
  message: 'Room message',
  source_event: 'Source event',
  file: 'Exact file version',
};

export interface Memory {
  id: string;
  roomId: string;
  revision: number;
  supersedesRevision?: number;
  state: string;
  content: string;
  provenance: MemoryProvenance;
  authorUserId: string;
  recordedByUserId: string;
  createdAt: string;
  current: boolean;
}

export interface MemoryInput {
  requestId: string;
  revision: number;
  content: string;
  provenance: MemoryProvenance;
}

interface MemoryReceipt {
  kind: string;
  memory?: Memory;
}

const id = encodeURIComponent;
const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const memoryApi = {
  list: (room: string, query: string, signal?: AbortSignal) =>
    request<{ memories: Memory[] }>(
      `/rooms/${id(room)}/memories?${new URLSearchParams({ q: query, limit: '20' })}`,
      { signal },
    ),
  get: (memory: string, revision?: number, signal?: AbortSignal) =>
    request<{ memory: Memory }>(
      `/memories/${id(memory)}${revision ? `?revision=${revision}` : ''}`,
      { signal },
    ),
  save: (room: string, memory: string, input: MemoryInput) =>
    request<{ memory: Memory }>(
      memory ? `/memories/${id(memory)}` : `/rooms/${id(room)}/memories`,
      json(memory ? 'PUT' : 'POST', input),
    ).then(({ memory }) => ({ kind: 'memory', memory })),
  retract: (memory: string, revision: number, requestId: string) =>
    request<{ memory: Memory }>(
      `/memories/${id(memory)}/retract`,
      json('POST', { revision, requestId }),
    ).then(({ memory }) => ({ kind: 'retract', memory })),
  receipt: (requestId: string) =>
    request<MemoryReceipt>(`/memory-requests/${id(requestId)}`),
  cancelReceipt: (requestId: string) =>
    request<MemoryReceipt>(
      `/memory-requests/${id(requestId)}/cancel`,
      json('POST', {}),
    ),
};
