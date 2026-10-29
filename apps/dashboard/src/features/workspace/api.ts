import { request } from '../../shared/api/http';

export type RunStatus =
  | 'queued'
  | 'preparing'
  | 'running'
  | 'checking'
  | 'cancelling'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'interrupted';

export interface Agent {
  ownerUserId?: string;
  preferredRunnerId?: string;
  id: string;
  name: string;
  handle: string;
  icon: string;
  avatar: string;
  instructions: string;
  access: 'read' | 'write';
  enabled: boolean;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

// Existing frozen execution records predate the avatar catalog.
export type AgentSnapshot = Omit<Agent, 'avatar'> & { avatar?: string };

export interface Room {
  id: string;
  name: string;
  description: string;
  resourceId: string;
  defaultAgentId: string;
  runnerId?: string;
  runnerRevision?: number;
  activitySeq: number;
  lastMessageSeq: number;
  createdAt: string;
  updatedAt: string;
}

export interface EnvironmentCheck {
  name: string;
  script?: string;
  arguments?: string[];
  directory?: string;
}

export interface Environment {
  kind?: 'node' | 'command';
  nodeVersion?: string;
  packageManager?: string;
  checks?: EnvironmentCheck[] | null;
}

export interface Resource {
  id: string;
  name: string;
  sourcePath: string;
  currentCommit: string;
  environment: Environment | null;
  createdAt: string;
  updatedAt: string;
}

export interface ReferenceInput {
  kind: 'message' | 'file';
  messageId?: string;
  resourceId?: string;
  commit?: string;
  path?: string;
  documentId?: string;
}

export type DocumentStatus =
  'queued' | 'extracting' | 'ready' | 'unreadable' | 'failed' | 'unavailable';

export interface DocumentError {
  code: string;
  message: string;
  retryable: boolean;
}

export interface DocumentSnapshot {
  id: string;
  format: 'pdf' | 'docx';
  status: DocumentStatus;
  sectionCount: number;
  warnings: string[];
  error?: DocumentError;
}

export interface ExtractedDocument {
  id: string;
  resourceId: string;
  commit: string;
  path: string;
  format: 'pdf' | 'docx';
  status: DocumentStatus;
  extractorVersion: string;
  sections: { id: string; label: string; text: string }[];
  warnings: string[];
  error?: DocumentError;
  createdAt: string;
  updatedAt: string;
}

export interface Reference extends ReferenceInput {
  title: string;
  origin?: 'upload' | 'runner';
  author?: string;
  content?: string;
  createdAt?: string;
  threadRootId?: string;
  truncated?: boolean;
  size?: number;
  sourceRunId?: string;
  sourceRunnerId?: string;
  sourceImportId?: string;
  sourceStatus?: string;
  published?: boolean;
  resultCommit?: string;
  checks?: string;
  document?: DocumentSnapshot;
}

export interface Message {
  taskId?: string;
  reviewId?: string;
  id: string;
  roomId: string;
  threadRootId: string;
  authorKind: 'user' | 'agent' | 'system';
  authorUserId: string;
  authorName: string;
  agentId: string;
  runId: string;
  content: string;
  requestId: string;
  references: Reference[];
  seq: number;
  createdAt: string;
  updatedAt: string;
}

export interface CheckResult {
  name: string;
  command: string;
  status: string;
  exitCode: number | null;
  output: string;
  startedAt: string;
  finishedAt: string;
}

export interface Run {
  taskId?: string;
  inputCommit?: string;
  checksDeferred?: boolean;
  id: string;
  roomId: string;
  threadRootId: string;
  originMessageId: string;
  agentId: string;
  resourceId: string;
  runnerId?: string;
  runnerName?: string;
  attemptId?: string;
  waitingReason?: string;
  prompt: string;
  references: Reference[];
  status: RunStatus;
  createdAt: string;
  updatedAt: string;
  finishedAt: string;
  error: string;
  output: string;
  sessionId: string;
  sessionMode: string;
  baselineCommit: string;
  resultCommit: string;
  agentSnapshot: AgentSnapshot;
  environment: Environment | null;
  checks: CheckResult[] | null;
  files: {
    path: string;
    status: 'added' | 'modified' | 'deleted';
    before: string;
    after: string;
  }[];
  exitCode: number | null;
  testExitCode: number | null;
  maxTokens: number;
  maxSteps: number;
  timeoutSeconds: number;
  sessionGeneration: number;
  published: boolean;
}

export interface RunEvent {
  runId: string;
  seq: number;
  type: string;
  text: string;
  createdAt: string;
}

export interface WorkspaceEvent {
  id: number;
  type: string;
  roomId: string;
  entityId: string;
  createdAt: string;
}

export interface WorkspaceStatus {
  agentStates?: Record<string, RunStatus>;
  available: boolean;
  reason: string;
  budget: { maxTokens: number; maxSteps: number; timeoutSeconds: number };
  maxConcurrent: number;
}

export interface SendInput {
  requestId: string;
  runnerRevision?: number;
  content: string;
  threadRootId?: string;
  recipientAgentIds?: string[];
  postOnly?: boolean;
  references?: ReferenceInput[];
}

export const statusLabel: Record<RunStatus, string> = {
  queued: 'Queued',
  preparing: 'Preparing',
  running: 'Working',
  checking: 'Checking',
  cancelling: 'Stopping',
  succeeded: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
  interrupted: 'Interrupted',
};

export function isActiveRun(run: Pick<Run, 'status'>) {
  return ['queued', 'preparing', 'running', 'checking', 'cancelling'].includes(
    run.status,
  );
}

const id = encodeURIComponent;
const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const workspaceApi = {
  rooms: (signal?: AbortSignal) =>
    request<{ rooms: Room[] }>('/rooms', { signal }),
  agents: (signal?: AbortSignal) =>
    request<{ agents: Agent[] }>('/agents', { signal }),
  resources: (signal?: AbortSignal) =>
    request<{ resources: Resource[] }>('/resources', { signal }),
  status: (signal?: AbortSignal) =>
    request<WorkspaceStatus>('/workspace/status', { signal }),
  createRoom: (name: string) =>
    request<{ room: Room }>('/rooms', json('POST', { name, description: '' })),
  updateRoom: (roomId: string, value: Partial<Room>) =>
    request<{ room: Room }>(`/rooms/${id(roomId)}`, json('PATCH', value)),
  members: (roomId: string, signal?: AbortSignal) =>
    request<{ members: Agent[] }>(`/rooms/${id(roomId)}/members`, { signal }),
  membership: (roomId: string, agentId: string, join: boolean) =>
    request<{ ok: boolean }>(
      `/rooms/${id(roomId)}/members/${id(agentId)}`,
      join ? json('PUT', {}) : { method: 'DELETE' },
    ),
  createRoomAgent: (roomId: string, agent: Partial<Agent>) =>
    request<{ agent: Agent }>(
      `/rooms/${id(roomId)}/agents`,
      json('POST', {
        name: agent.name,
        handle: agent.handle,
        icon: agent.icon,
        avatar: agent.avatar,
        instructions: agent.instructions,
        access: agent.access,
        enabled: agent.enabled,
        preferredRunnerId: agent.preferredRunnerId,
      }),
    ),
  saveAgent: (agent: Partial<Agent>) =>
    request<{ agent: Agent }>(
      agent.id ? `/agents/${id(agent.id)}` : '/agents',
      json(agent.id ? 'PATCH' : 'POST', {
        name: agent.name,
        handle: agent.handle,
        icon: agent.icon,
        avatar: agent.avatar,
        instructions: agent.instructions,
        access: agent.access,
        enabled: agent.enabled,
        preferredRunnerId: agent.preferredRunnerId,
        revision: agent.id ? agent.revision : undefined,
      }),
    ),
  messages: (
    roomId: string,
    threadRootId: string,
    after: number,
    signal?: AbortSignal,
  ) =>
    request<{ messages: Message[] }>(
      `/rooms/${id(roomId)}/messages?threadRootId=${id(threadRootId)}&after=${after}`,
      { signal },
    ),
  message: (roomId: string, messageId: string) =>
    request<{ message: Message }>(
      `/rooms/${id(roomId)}/messages/${id(messageId)}`,
    ),
  send: (roomId: string, input: SendInput) =>
    request<{ message: Message; runs: Run[]; replayed: boolean }>(
      `/rooms/${id(roomId)}/messages`,
      json('POST', input),
    ),
  upload: (
    roomId: string,
    requestId: string,
    file: File,
    signal?: AbortSignal,
  ) => {
    const body = new FormData();
    body.append('requestId', requestId);
    body.append('file', file);
    return request<{ reference: Reference }>(`/rooms/${id(roomId)}/uploads`, {
      method: 'POST',
      body,
      signal,
    });
  },
  uploadReceipt: (roomId: string, requestId: string, signal?: AbortSignal) =>
    request<{ reference: Reference }>(
      `/rooms/${id(roomId)}/uploads/${id(requestId)}`,
      { signal },
    ),
  uploads: (roomId: string, before = '', signal?: AbortSignal) =>
    request<{ references: Reference[]; nextCursor: string }>(
      `/rooms/${id(roomId)}/uploads?before=${id(before)}`,
      { signal },
    ),
  runs: (roomId: string, signal?: AbortSignal) =>
    request<{ runs: Run[] }>(`/rooms/${id(roomId)}/runs`, { signal }),
  run: (runId: string, signal?: AbortSignal) =>
    request<{ run: Run }>(`/runs/${id(runId)}`, { signal }),
  events: (runId: string, after: number, signal?: AbortSignal) =>
    request<{ events: RunEvent[] }>(
      `/runs/${id(runId)}/events?after=${after}`,
      { signal },
    ),
  cancel: (runId: string) =>
    request<{ run: Run }>(`/runs/${id(runId)}/cancel`, json('POST', {})),
  importResource: (name: string, path: string) =>
    request<{ resource: Resource }>('/resources', json('POST', { name, path })),
  saveResource: (
    resourceId: string,
    value: { name?: string; environment?: Environment | null },
  ) =>
    request<{ resource: Resource }>(
      `/resources/${id(resourceId)}`,
      json('PATCH', value),
    ),
  files: (resourceId: string, signal?: AbortSignal, commit = '') =>
    request<{ files: string[]; commit: string }>(
      `/resources/${id(resourceId)}/files${commit ? `?commit=${id(commit)}` : ''}`,
      {
        signal,
      },
    ),
  fileURL: (resourceId: string, path: string, commit: string) =>
    `/api/resources/${id(resourceId)}/file?path=${id(path)}&commit=${id(commit)}`,
  document: (documentId: string, signal?: AbortSignal) =>
    request<{ document: ExtractedDocument }>(`/documents/${id(documentId)}`, {
      signal,
    }),
  fileDocument: (
    resourceId: string,
    path: string,
    commit: string,
    signal?: AbortSignal,
  ) =>
    request<{ document: ExtractedDocument | null }>(
      `/resources/${id(resourceId)}/document?path=${id(path)}&commit=${id(commit)}`,
      { signal },
    ),
  prepareDocument: (
    resourceId: string,
    path: string,
    commit: string,
    retry = false,
    signal?: AbortSignal,
  ) =>
    request<{ document: ExtractedDocument }>(
      `/resources/${id(resourceId)}/document`,
      { ...json('POST', { path, commit, retry }), signal },
    ),
  patchURL: (runId: string) => `/api/runs/${id(runId)}/patch`,
};
