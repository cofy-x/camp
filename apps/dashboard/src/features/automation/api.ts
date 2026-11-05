import { request } from '../../shared/api/http';
import type { WorkBudget, WorkItemSummary } from '../workitems/api';
import type { Memory } from '../memory/api';

export interface AutomationPolicy {
  id: string;
  roomId: string;
  sourceId: string;
  revision: number;
  ownerUserId: string;
  enabled: boolean;
  mode: 'observe' | 'auto';
  rules: string;
  allowedActorIds: string[];
  participantAgentIds: string[];
  firstAgentId: string;
  maxTurns: number;
  maxModelRequests: number;
  itemBudget?: WorkBudget;
  dailyEvaluations: number;
  dailyTasks: number;
  allowAnswers: boolean;
  allowComments: boolean;
  credentialConfigured: boolean;
  commentActorId?: number;
  startAfter: number;
}

export type PolicyValues = Pick<
  AutomationPolicy,
  | 'enabled'
  | 'mode'
  | 'rules'
  | 'allowedActorIds'
  | 'participantAgentIds'
  | 'firstAgentId'
  | 'maxTurns'
  | 'maxModelRequests'
  | 'itemBudget'
  | 'dailyEvaluations'
  | 'dailyTasks'
  | 'allowAnswers'
  | 'allowComments'
>;

export interface SourceEvent {
  seq: number;
  id: string;
  roomId: string;
  sourceId: string;
  threadKey: string;
  kind: string;
  actorId: string;
  actorLogin: string;
  actorKind: string;
  messageId?: string;
  externalEventId?: string;
  repository?: string;
  repositoryId?: number;
  number?: number;
  content: string;
  occurredAt: string;
  status: string;
  reason?: string;
  evaluationId?: string;
  taskId?: string;
  workItemId?: string;
  feishu?: {
    eventId: string;
    appId: string;
    tenantKey: string;
    chatId: string;
    messageId: string;
    threadId: string;
    actorOpenId: string;
  };
  policyRevision?: number;
  preview?: string;
  assessmentOutcome?: AssessmentOutcome;
  assessmentReason?: string;
}

export type AssessmentOutcome =
  | 'accept'
  | 'execute'
  | 'wait'
  | 'request_judgment'
  | 'complete'
  | 'ignore'
  | 'unavailable';

export interface SourceEventPage {
  items: SourceEvent[];
  through: number;
  nextAfter: number | null;
  nextBefore?: number | null;
  hasMore: boolean;
}

export interface Evaluation {
  id: string;
  status: string;
  policy: AutomationPolicy;
  events: SourceEvent[];
  tasks: { id: string; goal: string; status: string; revision: number }[];
  workItems?: WorkItemSummary[];
  memories?: Memory[];
  workItemsOmitted?: boolean;
  memoriesOmitted?: boolean;
  totalWorkItems?: number;
  totalMemories?: number;
  contextLimited?: boolean;
  modelRevision: string;
  totalTasks: number;
  tasksOmitted: boolean;
  assessment?: {
    outcome: AssessmentOutcome;
    goal?: string;
    reason: string;
    workItemId?: string;
    completionCriteria?: string;
    waitCondition?: string;
    resumeAt?: string;
    question?: string;
  } | null;
  createdAt: string;
}

export interface Subscription {
  id: string;
  taskId: string;
  sourceId: string;
  threadKey: string;
  enabled: boolean;
  revision: number;
}

export interface Delivery {
  attempts?: {
    revision?: number;
    actorId: number;
    startedAt?: string;
    outcome?: 'sending' | 'sent' | 'failed' | 'unknown';
  }[];
  receipts?: { commentId: number; actorId: number; url: string }[];
  receiptScanLimited?: boolean;
  withdrawn?: boolean;
  id: string;
  taskId: string;
  sourceId: string;
  policyId: string;
  revision: number;
  repository: string;
  repositoryId: number;
  number: number;
  body: string;
  marker: string;
  questionId?: string;
  status: string;
  reason?: string;
  commentId?: number;
  url?: string;
  createdAt: string;
}

export interface AutomationReceipt {
  kind: string;
  policy?: AutomationPolicy;
  delivery?: Delivery;
  subscription?: Subscription;
  credentialConfigured?: boolean;
}

const id = encodeURIComponent;
const json = (method: string, value: unknown) => ({
  method,
  body: JSON.stringify(value),
});

export const automationApi = {
  event: (eventId: string, signal?: AbortSignal) =>
    request<{ event: SourceEvent }>(`/automation-events/${id(eventId)}`, {
      signal,
    }),
  policies: (room: string, signal?: AbortSignal) =>
    request<{ policies: AutomationPolicy[] }>(
      `/rooms/${id(room)}/automation-policies`,
      { signal },
    ),
  savePolicy: (
    room: string,
    source: string,
    value: PolicyValues & { requestId: string; revision: number },
  ) =>
    request<{ policy: AutomationPolicy }>(
      `/rooms/${id(room)}/automation-policies/${id(source)}`,
      json('PUT', value),
    ),
  events: (
    room: string,
    after: number,
    through?: number,
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({ after: String(after), limit: '20' });
    if (through !== undefined) query.set('through', String(through));
    return request<SourceEventPage>(
      `/rooms/${id(room)}/automation-events?${query}`,
      { signal },
    );
  },
  activity: (
    room: string,
    options: { before?: number; through?: number; sourceId?: string },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({ direction: 'desc', limit: '20' });
    if (options.before !== undefined)
      query.set('before', String(options.before));
    if (options.through !== undefined)
      query.set('through', String(options.through));
    if (options.sourceId) query.set('sourceId', options.sourceId);
    return request<SourceEventPage>(
      `/rooms/${id(room)}/automation-events?${query}`,
      { signal },
    );
  },
  receipt: (requestId: string) =>
    request<AutomationReceipt>(`/automation-requests/${id(requestId)}`),
  cancelReceipt: (requestId: string) =>
    request<AutomationReceipt>(
      `/automation-requests/${id(requestId)}/cancel`,
      json('POST', {}),
    ),
  evaluation: (evaluationId: string, signal?: AbortSignal) =>
    request<Evaluation>(`/automation-evaluations/${id(evaluationId)}`, {
      signal,
    }),
  evaluationContext: <K extends 'work-items' | 'memories'>(
    evaluationId: string,
    kind: K,
    after = 0,
    signal?: AbortSignal,
  ) =>
    request<{
      items: (K extends 'work-items' ? WorkItemSummary : Memory)[];
      nextAfter: number;
      hasMore: boolean;
      total: number;
      contextLimited?: boolean;
    }>(
      `/automation-evaluations/${id(evaluationId)}/context/${kind}?after=${after}&limit=10`,
      { signal },
    ),
  subscriptions: (task: string, signal?: AbortSignal) =>
    request<{ subscriptions: Subscription[] }>(
      `/tasks/${id(task)}/subscriptions`,
      { signal },
    ),
  saveSubscription: (
    task: string,
    value: Pick<
      Subscription,
      'sourceId' | 'threadKey' | 'revision' | 'enabled'
    > & {
      requestId: string;
    },
  ) =>
    request<AutomationReceipt>(
      `/tasks/${id(task)}/subscriptions`,
      json('PUT', value),
    ),
  deliveries: (task: string, cursor = '', signal?: AbortSignal) =>
    request<{ deliveries: Delivery[]; nextCursor: string }>(
      `/tasks/${id(task)}/deliveries?${new URLSearchParams({ cursor, limit: '20' })}`,
      { signal },
    ),
  deliveryAction: (
    delivery: string,
    action: 'reconcile' | 'resend',
    value: { requestId: string; revision: number },
  ) =>
    request<AutomationReceipt>(
      `/automation-deliveries/${id(delivery)}/${action}`,
      json('POST', value),
    ),
  credential: (
    source: string,
    value: {
      requestId: string;
      revision: number;
      token: string;
      actorId: number;
    },
  ) =>
    request<AutomationReceipt>(
      `/github-sources/${id(source)}/comment-credential`,
      json('PUT', value),
    ),
};
