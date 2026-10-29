import { request } from '../../shared/api/http';
import type { Agent, ReferenceInput, Run } from '../workspace/api';

export interface Task {
  id: string;
  roomId: string;
  threadRootId: string;
  originMessageId: string;
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
  participants: { agent: Agent; runnerId: string; runnerName: string }[];
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
  inputs: {
    id: string;
    authorUserId: string;
    authorName: string;
    content: string;
    createdAt: string;
  }[];
  question?: {
    id: string;
    text: string;
    options: string[];
    answer: string;
    answeredByUserId: string;
  };
  createdAt: string;
  updatedAt: string;
}

const id = encodeURIComponent;
const post = (body: unknown) => ({
  method: 'POST',
  body: JSON.stringify(body),
});

export const tasksApi = {
  list: (roomId: string, signal?: AbortSignal) =>
    request<{ tasks: Task[] }>(`/rooms/${id(roomId)}/tasks`, { signal }),
  get: (taskId: string, signal?: AbortSignal) =>
    request<{ task: Task; runs: Run[] }>(`/tasks/${id(taskId)}`, { signal }),
  create: (
    roomId: string,
    body: {
      requestId: string;
      goal: string;
      participantAgentIds: string[];
      firstAgentId: string;
      threadRootId?: string;
      references?: ReferenceInput[];
    },
  ) => request<{ task: Task }>(`/rooms/${id(roomId)}/tasks`, post(body)),
  input: (taskId: string, body: { requestId: string; content: string }) =>
    request<{ task: Task }>(`/tasks/${id(taskId)}/inputs`, post(body)),
  answer: (
    taskId: string,
    body: {
      requestId: string;
      revision: number;
      questionId: string;
      content: string;
    },
  ) => request<{ task: Task }>(`/tasks/${id(taskId)}/answers`, post(body)),
  continue: (
    taskId: string,
    body: {
      requestId: string;
      revision: number;
      agentId: string;
      content: string;
      addTurns?: number;
      addModelRequests?: number;
      fromLatest?: boolean;
      participantAgentIds?: string[];
    },
  ) => request<{ task: Task }>(`/tasks/${id(taskId)}/continue`, post(body)),
  cancel: (taskId: string, body: { requestId: string; revision: number }) =>
    request<{ task: Task }>(`/tasks/${id(taskId)}/cancel`, post(body)),
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
