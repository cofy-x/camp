import { request } from '../../shared/api/http';
import type { AgentSnapshot, ReferenceInput, Run } from '../workspace/api';

export interface Task {
  id: string;
  roomId: string;
  threadRootId: string;
  originMessageId: string;
  sourceEventId?: string;
  automationPolicyId?: string;
  automationPolicyRevision?: number;
  automationScope?: string;
  resourceId: string;
  requestedByUserId: string;
  goal: string;
  status:
    | 'queued'
    | 'running'
    | 'waiting_for_person'
    | 'needs_attention'
    | 'publishing'
    | 'completed'
    | 'cancelling'
    | 'cancelled'
    | 'interrupted';
  revision: number;
  participants: {
    agent: AgentSnapshot;
    runnerId: string;
    runnerName: string;
  }[];
  currentAgentId: string;
  activeRunId: string;
  baseSharedCommit: string;
  checkpointCommit: string;
  previousDraftCommit?: string;
  publishedCommit: string;
  error: string;
  summary: string;
  turnsUsed: number;
  maxTurns: number;
  modelRequestsUsed: number;
  maxModelRequests: number;
  inputRevision: number;
  inputBytes: number;
  coveredInputRevision: number;
  inputs?: TaskInput[];
  question?: {
    id: string;
    text: string;
    options: string[];
    answer: string;
    answeredByUserId: string;
    answerSourceEventId?: string;
    answeredByExternalActorId?: string;
  };
  createdAt: string;
  updatedAt: string;
}

export interface TaskInput {
  id: string;
  messageId?: string;
  seq: number;
  authorUserId: string;
  authorName: string;
  content: string;
  createdAt: string;
  sourceEventId?: string;
  authorKind?: string;
  externalActorId?: string;
  partIndex?: number;
}

export interface TaskInputPage {
  items: TaskInput[];
  through: number;
  nextAfter: number | null;
  hasMore: boolean;
}

export interface TaskRunSummary {
  id: string;
  agentId: string;
  agentName: string;
  runnerName: string;
  checksDeferred: boolean;
  status: Run['status'];
  createdAt: string;
  updatedAt: string;
}

export interface TaskRunPage {
  items: TaskRunSummary[];
  nextCursor: string;
}

const id = encodeURIComponent;
const post = (body: unknown) => ({
  method: 'POST',
  body: JSON.stringify(body),
});

export const tasksApi = {
  list: (roomId: string, signal?: AbortSignal) =>
    request<{ tasks: Task[] }>(`/rooms/${id(roomId)}/tasks?view=summary`, {
      signal,
    }),
  get: (taskId: string, signal?: AbortSignal) =>
    request<{ task: Task }>(`/tasks/${id(taskId)}?view=summary`, { signal }),
  inputs: (
    taskId: string,
    after: number,
    through: number | undefined,
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({ after: String(after) });
    if (through !== undefined) query.set('through', String(through));
    return request<TaskInputPage>(`/tasks/${id(taskId)}/inputs?${query}`, {
      signal,
    });
  },
  runs: (taskId: string, cursor: string, signal?: AbortSignal) =>
    request<TaskRunPage>(
      `/tasks/${id(taskId)}/runs?${new URLSearchParams({ cursor })}`,
      { signal },
    ),
  create: (
    roomId: string,
    body: {
      inputProtocol?: 1;
      requestId: string;
      sourceEventId?: string;
      goal: string;
      participantAgentIds: string[];
      firstAgentId: string;
      threadRootId?: string;
      references?: ReferenceInput[];
    },
  ) =>
    request<{ task: Task }>(
      `/rooms/${id(roomId)}/tasks?view=summary`,
      post(body),
    ),
  input: (
    taskId: string,
    body: { inputProtocol?: 1; requestId: string; content: string },
  ) =>
    request<{ task: Task }>(
      `/tasks/${id(taskId)}/inputs?view=summary`,
      post(body),
    ),
  answer: (
    taskId: string,
    body: {
      inputProtocol?: 1;
      requestId: string;
      revision: number;
      questionId: string;
      content: string;
    },
  ) =>
    request<{ task: Task }>(
      `/tasks/${id(taskId)}/answers?view=summary`,
      post(body),
    ),
  continue: (
    taskId: string,
    body: {
      inputProtocol?: 1;
      requestId: string;
      revision: number;
      agentId: string;
      content: string;
      addTurns?: number;
      addModelRequests?: number;
      fromLatest?: boolean;
      participantAgentIds?: string[];
    },
  ) =>
    request<{ task: Task }>(
      `/tasks/${id(taskId)}/continue?view=summary`,
      post(body),
    ),
  cancel: (taskId: string, body: { requestId: string; revision: number }) =>
    request<{ task: Task }>(
      `/tasks/${id(taskId)}/cancel?view=summary`,
      post(body),
    ),
};
export const taskStatus: Record<Task['status'], string> = {
  queued: 'Waiting for Runner',
  running: 'Working',
  waiting_for_person: 'Needs your answer',
  needs_attention: 'Needs attention',
  publishing: 'Saving to Room files',
  completed: 'Work completed',
  cancelling: 'Stopping',
  cancelled: 'Cancelled',
  interrupted: 'Interrupted',
};
