import { request } from '../../shared/api/http';
import type { Reference, Resource, Room } from '../workspace/api';

export interface RunnerSourceProvenance {
  kind: 'source';
  origin: 'runner';
  resourceId: string;
  commit: string;
  title: string;
  sourceRunnerId: string;
  sourceImportId: string;
  createdAt: string;
}

export interface LibraryResource extends Resource {
  immutable: boolean;
  source: Reference | RunnerSourceProvenance | null;
}

export interface Publication {
  seq: number;
  id: string;
  resourceId: string;
  previousCommit: string;
  commit: string;
  authorUserId: string;
  authorName: string;
  runId: string;
  kind: 'edit' | 'restore' | 'agent';
  path: string;
  checksStatus: 'not_run' | 'not_configured' | 'passed';
  createdAt: string;
}

const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const libraryApi = {
  patchURL: (resourceId: string, from: string, to: string) =>
    `/api/resources/${encodeURIComponent(resourceId)}/patch?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
  list: (signal?: AbortSignal) =>
    request<{ resources: LibraryResource[]; rooms: Room[] }>('/library', {
      signal,
    }),
  history: (resourceId: string, signal?: AbortSignal) =>
    request<{ publications: Publication[] }>(
      `/resources/${encodeURIComponent(resourceId)}/history`,
      { signal },
    ),
  save: (
    resourceId: string,
    value: {
      requestId: string;
      expectedCommit: string;
      path: string;
      content: string;
    },
  ) =>
    request<{ resource: Resource; publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/files`,
      json('PUT', value),
    ),
  restore: (
    resourceId: string,
    expectedCommit: string,
    commit: string,
    requestId: string,
  ) =>
    request<{ resource: Resource; publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/restore`,
      json('POST', { expectedCommit, commit, requestId }),
    ),
  copy: (
    resourceId: string,
    value: {
      requestId: string;
      expectedCommit: string;
      path: string;
      sourceResourceId: string;
      sourceCommit: string;
      sourcePath: string;
    },
  ) =>
    request<{ resource: Resource; publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/copy-file`,
      json('POST', value),
    ),
};
