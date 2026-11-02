import { request } from '../../shared/api/http';

export interface GitHubSource {
  id: string;
  roomId: string;
  repository: string;
  repositoryId: number;
  enabled: boolean;
  revision: number;
  mode: 'observe';
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubEvent {
  id: string;
  seq: number;
  sourceId: string;
  sourceRevision: number;
  roomId: string;
  fingerprint: string;
  kind: string;
  action: string;
  repository: string;
  repositoryId: number;
  objectId?: number;
  number?: number;
  actorLogin?: string;
  actorId?: number;
  actorKind?: string;
  title?: string;
  body?: string;
  url?: string;
  occurredAt?: string;
  receivedAt: string;
  status: 'pending' | 'ignored' | 'dismissed' | 'accepted';
  ignoreReason?: string;
  decisionReason?: string;
  decidedBy?: string;
  decidedAt?: string;
  taskId?: string;
}

export interface GitHubEventPage {
  items: GitHubEvent[];
  through: number;
  nextAfter: number | null;
  hasMore: boolean;
}

export interface GitHubReceipt {
  kind: 'source.create' | 'source.update' | 'event.dismiss';
  source?: GitHubSource;
  event?: GitHubEvent;
}

const id = encodeURIComponent;
const body = (value: unknown) => JSON.stringify(value);

export const githubApi = {
  sources: (room: string, signal?: AbortSignal) =>
    request<{ sources: GitHubSource[] }>(`/rooms/${id(room)}/github-sources`, {
      signal,
    }),
  createSource: (
    room: string,
    value: { requestId: string; repository: string; secret: string },
  ) =>
    request<{ source: GitHubSource }>(`/rooms/${id(room)}/github-sources`, {
      method: 'POST',
      body: body(value),
    }),
  updateSource: (
    source: string,
    value: {
      requestId: string;
      revision: number;
      enabled?: boolean;
      secret?: string;
    },
  ) =>
    request<{ source: GitHubSource }>(`/github-sources/${id(source)}`, {
      method: 'PATCH',
      body: body(value),
    }),
  receipt: (requestId: string) =>
    request<GitHubReceipt>(`/github-requests/${id(requestId)}`),
  events: (
    room: string,
    state: string,
    after = 0,
    through?: number,
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({ state, after: String(after) });
    if (through !== undefined) query.set('through', String(through));
    return request<GitHubEventPage>(
      `/rooms/${id(room)}/github-events?${query}`,
      { signal },
    );
  },
  event: (event: string, signal?: AbortSignal) =>
    request<{ event: GitHubEvent }>(`/github-events/${id(event)}`, { signal }),
  dismiss: (event: string, value: { requestId: string; reason: string }) =>
    request<{ event: GitHubEvent }>(`/github-events/${id(event)}/dismiss`, {
      method: 'POST',
      body: body(value),
    }),
  payloadURL: (event: string) => `/api/github-events/${id(event)}/payload`,
};
