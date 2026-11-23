import { request } from '../shared/api/http';

export interface User {
  id: string;
  username: string;
  name: string;
  avatar: string;
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
    | 'task_finished'
    | 'work_item_attention'
    | 'work_item_finished';
  taskId?: string;
  workItemId?: string;
  roomId: string;
  messageId: string;
  runId: string;
  reviewId?: string;
  roomName: string;
  reviewTitle: string;
  title?: string;
  question?: string;
  summary?: string;
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
  avatars: (signal?: AbortSignal) =>
    request<{
      avatars: { id: string; name: string; kind: 'member' | 'agent' }[];
    }>('/avatars', { signal }),
  saveAvatar: (avatar: string, expectedAvatar: string) =>
    request<{ user: User }>(
      '/me/avatar',
      json('PATCH', { avatar, expectedAvatar }),
    ),
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
  renameTeam: (name: string) => request('/team', json('PATCH', { name })),
  logout: () => request('/auth/logout', json('POST', {})),
  updateMember: (
    id: string,
    value: { role?: User['role']; enabled?: boolean },
  ) =>
    request<{ member: User }>(
      `/team/members/${encodeURIComponent(id)}`,
      json('PATCH', value),
    ),
  invite: () =>
    request<{ invite: Invitation; token: string }>(
      '/team/invites',
      json('POST', { expiresInHours: 48 }),
    ),
  revokeInvite: (id: string) =>
    request(`/team/invites/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  readInbox: (id: string) =>
    request(`/me/inbox/${encodeURIComponent(id)}/read`, json('POST', {})),
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
    request<{ draft: PrivateDraft }>(
      `/me/drafts/${kind}`,
      json('DELETE', { key, revision }),
    ),
  drafts: (kind: string, signal?: AbortSignal) =>
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
    }>(`/me/drafts?kind=${kind}`, { signal }),
};
