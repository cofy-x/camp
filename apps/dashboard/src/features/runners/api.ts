import { request } from '../../shared/api/http';

export interface Runner {
  id: string;
  name: string;
  ownerUserId?: string;
  teamShared?: boolean;
  enabled: boolean;
  revoked: boolean;
  online: boolean;
  lastSeenAt: string;
  firstTaskRunId?: string;
  firstTaskSucceededAt?: string;
  earlierTaskRunId?: string;
  earlierTaskSucceededAt?: string;
  modelConfigured?: boolean;
  modelCheckedAt?: string;
  slots?: number;
  runtime: 'native' | 'fixture' | '';
  capabilities: {
    readiness?: {
      ready: boolean;
      checkedAt: string;
      checks: { id: string; label: string; status: string; detail?: string }[];
    };
    executor?: 'native' | 'fixture';
    ready?: boolean;
    reason?: string;
    slots?: number;
    dshVersion?: string;
    layerfsVersion?: string;
  };
}

export interface RunnerSource {
  id: string;
  alias: string;
  revision: string;
  available: boolean;
  reason?: string;
}

export interface ExecutionDefaults {
  personalRunnerId: string;
  personalRevision: number;
  teamRunnerId: string;
  teamRevision: number;
}

export interface EnrollmentRecord {
  ownerUserId?: string;
  id: string;
  name: string;
  expiresAt: string;
  revoked?: boolean;
  runnerId?: string;
}

export interface Enrollment {
  enrollment: EnrollmentRecord;
  token: string;
}

export interface SourceImportInput {
  requestId: string;
  runnerId: string;
  sourceId: string;
  expectedSourceRevision: string;
  roomName: string;
}

export interface SourceImport {
  id: string;
  status:
    | 'queued'
    | 'capturing'
    | 'uploading'
    | 'cancelling'
    | 'succeeded'
    | 'failed'
    | 'cancelled'
    | 'interrupted';
  runnerId: string;
  sourceId: string;
  sourceAlias: string;
  roomName: string;
  roomId?: string;
  resourceId?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

const id = encodeURIComponent;
const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});

export const runnersApi = {
  installation: (signal?: AbortSignal) =>
    request<{
      platform: string;
      version?: string;
      source?: 'github' | 'server';
      available: boolean;
      downloadUrl: string;
      filename: string;
      sha256?: string;
      checksumUrl?: string;
      bytes?: number;
      runtime?: { bytes: number; unpackedBytes: number };
      offline?: {
        available: boolean;
        filename: string;
        downloadUrl: string;
        checksumUrl: string;
        sha256: string;
        bytes: number;
      };
    }>('/runner-installation', { signal }),
  defaults: (signal?: AbortSignal) =>
    request<ExecutionDefaults>('/execution-defaults', { signal }),
  saveDefault: (
    scope: 'personal' | 'team',
    runnerId: string,
    revision: number,
  ) =>
    request<ExecutionDefaults>(
      '/execution-defaults',
      json('PATCH', { scope, runnerId, revision }),
    ),
  agents: (runnerId: string, signal?: AbortSignal) =>
    request<{ agentIds: string[] }>(`/runners/${id(runnerId)}/agents`, {
      signal,
    }),
  grantAgent: (runnerId: string, agentId: string, allow: boolean) =>
    request<{ agentIds: string[] }>(
      `/runners/${id(runnerId)}/agents/${id(agentId)}`,
      json(allow ? 'PUT' : 'DELETE', {}),
    ),
  list: (signal?: AbortSignal) =>
    request<{ runners: Runner[] }>('/runners', { signal }),
  enroll: (name: string) =>
    request<Enrollment>('/runner-enrollments', json('POST', { name })),
  enrollments: (signal?: AbortSignal) =>
    request<{ enrollments: EnrollmentRecord[] }>('/runner-enrollments', {
      signal,
    }),
  revokeEnrollment: (enrollmentId: string) =>
    request<Record<string, never>>(
      `/runner-enrollments/${id(enrollmentId)}/revoke`,
      json('POST', {}),
    ),
  update: (
    runnerId: string,
    input: {
      name?: string;
      enabled?: boolean;
      revoke?: boolean;
      teamShared?: boolean;
    },
  ) =>
    request<{ runner: Runner }>(
      `/runners/${id(runnerId)}`,
      json('PATCH', input),
    ),
  sources: (runnerId: string, signal?: AbortSignal) =>
    request<{ sources: RunnerSource[] }>(`/runners/${id(runnerId)}/sources`, {
      signal,
    }),
  importSource: (input: SourceImportInput) =>
    request<{ sourceImport: SourceImport }>(
      '/source-imports',
      json('POST', input),
    ),
  sourceImport: (importId: string, signal?: AbortSignal) =>
    request<{ sourceImport: SourceImport }>(`/source-imports/${id(importId)}`, {
      signal,
    }),
  sourceImports: (signal?: AbortSignal) =>
    request<{ sourceImports: SourceImport[] }>('/source-imports', { signal }),
  cancelImport: (importId: string) =>
    request<{ sourceImport: SourceImport }>(
      `/source-imports/${id(importId)}/cancel`,
      json('POST', {}),
    ),
};
