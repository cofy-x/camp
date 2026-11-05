import { request } from '../../shared/api/http';

export interface WorkBudget {
  maxEvaluations: number;
  maxTasks: number;
  maxModelRequests: number;
  maxActions: number;
  maxFollowUps: number;
}

export interface WorkItem {
  seq: number;
  id: string;
  roomId: string;
  ownerUserId: string;
  policyId?: string;
  goal: string;
  completionCriteria: string;
  completionMode?: 'human' | 'criteria';
  status: string;
  revision: number;
  activeTaskId?: string;
  question?: string;
  questionKind?: string;
  waitCondition?: string;
  deadline: string;
  expiresAt?: string;
  dueAt?: string;
  execution?: {
    taskId: string;
    status: string;
    revision: number;
    summary: string;
    error: string;
    question?: { id: string; text: string; options: string[]; answer: string };
    currentAgentName: string;
    activeRunId: string;
    updatedAt: string;
  };
  result?: {
    taskId: string;
    summary: string;
    resourceId: string;
    commit: string;
    published: boolean;
    files: { path: string; status: string }[];
    filesLimited?: boolean;
  };
  budget: WorkBudget;
  usage: {
    evaluations: number;
    tasks: number;
    modelRequests: number;
    reservedModelRequests: number;
    actions: number;
    followUps: number;
  };
  evidence: { eventId: string; kind: string; createdAt: string }[];
  decisions: {
    seq: number;
    id: string;
    kind: string;
    reason: string;
    reasonSource?: 'user' | 'system';
    actorKind: string;
    actorId: string;
    evaluationId?: string;
    policyRevision?: number;
    taskId?: string;
    evidenceIds: string[];
    createdAt: string;
  }[];
  tasks: {
    id: string;
    status: string;
    summary: string;
    createdAt: string;
  }[];
  timers: {
    id: string;
    dueAt: string;
    condition: string;
    status: string;
    eventId?: string;
  }[];
  actions: {
    id: string;
    kind: string;
    status: string;
    taskId?: string;
    deliveryId?: string;
    body: string;
    createdAt: string;
  }[];
  watches: { sourceId: string; threadKey: string; enabled: boolean }[];
  historyLimited?: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface WorkItemInput {
  requestId: string;
  goal: string;
  completionCriteria?: string;
  policyId?: string;
  policyRevision?: number;
  evidenceIds?: string[];
  deadline?: string;
  expiresAt?: string;
  dueAt?: string;
  budget?: WorkBudget;
  start?: boolean;
  follow?: boolean;
  sourceEventId?: string;
  messageId?: string;
  participantAgentIds?: string[];
  firstAgentId?: string;
}

export interface WorkItemDefaults {
  budget: WorkBudget;
  expiresAt: string;
  participantAgentIds: string[];
  firstAgentId: string;
  execution: { maxTurns: number; maxModelRequests: number };
}

export type WorkActionName =
  | 'start'
  | 'answer'
  | 'answer_continue'
  | 'wait'
  | 'complete'
  | 'stop'
  | 'delegate'
  | 'attach'
  | 'follow'
  | 'unfollow'
  | 'authorize';

export interface WorkActionInput {
  requestId: string;
  revision: number;
  comment?: string;
  content?: string;
  taskId?: string;
  taskRevision?: number;
  questionId?: string;
  participantAgentIds?: string[];
  firstAgentId?: string;
  evidenceIds?: string[];
  sourceEventId?: string;
  sourceId?: string;
  threadKey?: string;
  follow?: boolean;
  waitCondition?: string;
  resumeAt?: string;
  policyId?: string;
  policyRevision?: number;
  budget?: WorkBudget;
  expiresAt?: string;
  dueAt?: string;
}

export type WorkItemSummary = Pick<
  WorkItem,
  | 'id'
  | 'goal'
  | 'completionCriteria'
  | 'status'
  | 'revision'
  | 'activeTaskId'
  | 'waitCondition'
  | 'question'
  | 'budget'
  | 'usage'
  | 'deadline'
> & {
  recentTasks?: WorkItem['tasks'];
  recentDecisions?: WorkItem['decisions'];
  actions?: WorkItem['actions'];
  contextLimited?: boolean;
  actionsLimited?: boolean;
};

export interface WorkItemOperation {
  budget?: WorkBudget;
  deadline?: string;
  requestId: string;
  revision: number;
  kind: string;
  reason: string;
  evidenceIds?: string[];
  waitCondition?: string;
  resumeAt?: string;
  question?: string;
  sourceId?: string;
  threadKey?: string;
  participantAgentIds?: string[];
  firstAgentId?: string;
}

export interface WorkItemPage {
  items: WorkItem[];
  nextAfter: number;
  hasMore: boolean;
}

const id = encodeURIComponent;
const post = (value: unknown) => ({
  method: 'POST',
  body: JSON.stringify(value),
});

export const workItemsApi = {
  list: (roomId: string, after = 0, signal?: AbortSignal, state = 'all') =>
    request<WorkItemPage>(
      `/rooms/${id(roomId)}/work-items?after=${after}&limit=20&state=${id(state)}`,
      { signal },
    ),
  defaults: (roomId: string, signal?: AbortSignal, policyId = '') =>
    request<{ defaults: WorkItemDefaults }>(
      `/rooms/${id(roomId)}/work-item-defaults${policyId ? `?policyId=${id(policyId)}` : ''}`,
      { signal },
    ),
  item: (itemId: string, signal?: AbortSignal) =>
    request<{ item: WorkItem }>(`/work-items/${id(itemId)}`, { signal }),
  history: <
    K extends 'evidence' | 'decisions' | 'tasks' | 'timers' | 'actions',
  >(
    itemId: string,
    kind: K,
    cursor = '',
    signal?: AbortSignal,
  ) =>
    request<{ items: WorkItem[K]; nextCursor: string }>(
      `/work-items/${id(itemId)}/history/${kind}?${new URLSearchParams({ cursor, limit: '20' })}`,
      { signal },
    ),
  create: (roomId: string, value: WorkItemInput) =>
    request<{ item: WorkItem }>(`/rooms/${id(roomId)}/work-items`, post(value)),
  decide: (itemId: string, value: WorkItemOperation) =>
    request<{ item: WorkItem }>(
      `/work-items/${id(itemId)}/decisions`,
      post(value),
    ),
  act: (itemId: string, action: WorkActionName, value: WorkActionInput) =>
    request<{ item: WorkItem }>(
      `/work-items/${id(itemId)}/actions/${id(action)}`,
      post(value),
    ),
  receipt: (requestId: string) =>
    request<{ item: WorkItem }>(`/work-item-requests/${id(requestId)}`),
  cancelReceipt: (requestId: string) =>
    request<{ cancelled: boolean }>(
      `/work-item-requests/${id(requestId)}/cancel`,
      post({}),
    ),
};

export const workStatus: Record<string, string> = {
  open: 'Open',
  running: 'Working',
  waiting: 'Waiting',
  waiting_for_person: 'Needs your judgment',
  needs_attention: 'Needs attention',
  completed: 'Completed',
  cancelled: 'Cancelled',
  stopping: 'Stopping',
};

export const decisionLabel: Record<string, string> = {
  create: 'Work item created',
  execute: 'Start collaboration',
  wait: 'Wait for evidence',
  request_judgment: 'Request judgment',
  ignore: 'Ignore evidence',
  complete: 'Complete work item',
  cancel: 'Cancel work item',
  evidence: 'Add evidence',
  answer: 'Record judgment',
  follow: 'Follow conversation',
  unfollow: 'Stop following conversation',
  authorize: 'Change allowance',
};
