import type { CurrentWorkDecision, WorkRecovery } from '../workitems/api';

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
  workRecovery?: WorkRecovery;
  currentDecision?: CurrentWorkDecision;
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
