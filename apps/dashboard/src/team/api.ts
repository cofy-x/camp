import { request } from '../shared/api/http';

export interface User {
  id: string;
  username: string;
  name: string;
  role: 'admin' | 'member';
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface InboxItem {
  id: string;
  kind:
    | 'mention'
    | 'run_finished'
    | 'review_requested'
    | 'review_updated'
    | 'task_question'
    | 'task_attention'
    | 'task_finished';
  taskId?: string;
  roomId: string;
  messageId: string;
  runId: string;
  reviewId?: string;
  roomName: string;
  reviewTitle: string;
  createdAt: string;
  readAt: string;
}

export interface PrivateDraft<T = unknown> {
  kind: string;
  key: string;
  value: T | null;
  revision: number;
  updatedAt: string;
}

export interface Invitation {
  id: string;
  createdAt: string;
  expiresAt: string;
  consumedAt: string;
  revokedAt: string;
}

const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const teamApi = {
  status: () =>
    request<{
      setupRequired: boolean;
      user: User | null;
      team: { id: string; name: string; updatedAt: string };
    }>('/auth/status'),
  authenticate: (
    mode: 'setup' | 'login' | 'accept-invite',
    value: {
      username: string;
      password: string;
      name?: string;
      token?: string;
      teamName?: string;
    },
  ) => request<{ user: User }>(`/auth/${mode}`, json('POST', value)),
  team: () =>
    request<{ team: { id: string; name: string; updatedAt: string } }>('/team'),
  renameTeam: (name: string) => request('/team', json('PATCH', { name })),
  logout: () => request('/auth/logout', json('POST', {})),
  members: (signal?: AbortSignal) =>
    request<{ members: User[] }>('/team/members', { signal }),
  updateMember: (
    id: string,
    value: { role?: User['role']; enabled?: boolean },
  ) =>
    request<{ member: User }>(
      `/team/members/${encodeURIComponent(id)}`,
      json('PATCH', value),
    ),
  invitations: () => request<{ invites: Invitation[] }>('/team/invites'),
  invite: () =>
    request<{ invite: Invitation; token: string }>(
      '/team/invites',
      json('POST', { expiresInHours: 48 }),
    ),
  revokeInvite: (id: string) =>
    request(`/team/invites/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  inbox: (signal?: AbortSignal) =>
    request<{ items: InboxItem[] }>('/me/inbox', { signal }),
  readInbox: (id: string) =>
    request(`/me/inbox/${encodeURIComponent(id)}/read`, json('POST', {})),
  reads: (signal?: AbortSignal) =>
    request<{ reads: { roomId: string; seq: number }[] }>('/me/room-reads', {
      signal,
    }),
  readRoom: (roomId: string, seq: number) =>
    request(
      `/me/room-reads/${encodeURIComponent(roomId)}`,
      json('PUT', { seq }),
    ),
  draft: <T>(kind: string, key: string, signal?: AbortSignal) =>
    request<{ draft: PrivateDraft<T> }>(
      `/me/drafts/${kind}?key=${encodeURIComponent(key)}`,
      { signal },
    ),
  saveDraft: <T>(kind: string, key: string, value: T, revision: number) =>
    request<{ draft: PrivateDraft<T> }>(
      `/me/drafts/${kind}`,
      json('PUT', { key, value, revision }),
    ),
  deleteDraft: (kind: string, key: string, revision: number) =>
    request(`/me/drafts/${kind}`, json('DELETE', { key, revision })),
  drafts: (kind: string) =>
    request<{
      drafts: {
        kind: string;
        key: string;
        revision: number;
        updatedAt: string;
        title: string;
        resourceId: string;
        path: string;
      }[];
    }>(`/me/drafts?kind=${kind}`),
};
