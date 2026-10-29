import { request } from '../../shared/api/http';

export type ReviewStatus =
  'in_review' | 'changes_requested' | 'approved' | 'delivered' | 'cancelled';

export type ReviewAction =
  | 'approve'
  | 'request_changes'
  | 'resubmit'
  | 'deliver'
  | 'cancel'
  | 'reassign';

export interface Review {
  id: string;
  roomId: string;
  resourceId: string;
  path: string;
  title: string;
  requesterUserId: string;
  requesterName: string;
  reviewerUserId: string;
  reviewerName: string;
  reviewerEnabled: boolean;
  submittedCommit: string;
  deliveredCommit: string;
  currentCommit: string;
  status: ReviewStatus;
  revision: number;
  threadRootId: string;
  outdated: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ReviewEvent {
  id: string;
  seq: number;
  reviewId: string;
  actorUserId: string;
  actorName: string;
  action: string;
  comment: string;
  commit: string;
  revision: number;
  messageId: string;
  createdAt: string;
}

export const reviewLabels: Record<ReviewStatus, string> = {
  in_review: 'In Review',
  changes_requested: 'Changes Requested',
  approved: 'Approved',
  delivered: 'Delivered',
  cancelled: 'Cancelled',
};

export interface ReviewResult {
  review: Review;
  events: ReviewEvent[];
  replayed?: boolean;
}

export interface ReviewActionInput {
  requestId: string;
  revision: number;
  action: ReviewAction;
  comment?: string;
  commit?: string;
  reviewerUserId?: string;
}

const json = (value: unknown) => ({
  method: 'POST',
  body: JSON.stringify(value),
});
export const reviewsApi = {
  list: (
    scope: { roomId?: string; resourceId?: string },
    signal?: AbortSignal,
  ) =>
    request<{ reviews: Review[] }>(`/reviews?${new URLSearchParams(scope)}`, {
      signal,
    }),
  get: (id: string, signal?: AbortSignal) =>
    request<ReviewResult>(`/reviews/${encodeURIComponent(id)}`, { signal }),
  create: (
    roomId: string,
    input: {
      requestId: string;
      title: string;
      resourceId: string;
      path: string;
      commit: string;
      reviewerUserId: string;
    },
  ) =>
    request<ReviewResult>(
      `/rooms/${encodeURIComponent(roomId)}/reviews`,
      json(input),
    ),
  act: (id: string, input: ReviewActionInput) =>
    request<ReviewResult>(
      `/reviews/${encodeURIComponent(id)}/actions`,
      json(input),
    ),
};
