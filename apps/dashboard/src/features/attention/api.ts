import { request } from '../../shared/api/http';

export type AttentionKind = 'judgment' | 'review' | 'exception';

export interface AttentionItem {
  id: string;
  kind: AttentionKind;
  source: 'work_item' | 'review' | 'task' | 'delivery';
  title: string;
  summary: string;
  roomId: string;
  roomName: string;
  status: string;
  reason: string;
  priority: number;
  createdAt: string;
  updatedAt: string;
  textLimited?: true;
  target: {
    app: 'work' | 'workspace';
    roomId: string;
    workItemId?: string;
    reviewId?: string;
    taskId?: string;
  };
}

export interface AttentionPage {
  items: AttentionItem[];
  nextCursor: string;
  hasMore: boolean;
  total: number;
  counts: Record<AttentionKind, number>;
}

export const attentionApi = {
  list(query: { roomId?: string; cursor?: string } = {}, signal?: AbortSignal) {
    const params = new URLSearchParams({ limit: '20' });
    if (query.roomId) params.set('roomId', query.roomId);
    if (query.cursor) params.set('cursor', query.cursor);
    return request<AttentionPage>(`/attention?${params}`, { signal });
  },
};
