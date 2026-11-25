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

export interface LibraryListing {
  resources: LibraryResource[];
  rooms: Room[];
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

export interface RestoreFilesInput {
  expectedCommit: string;
  commit: string;
}

export interface CopyFileInput {
  expectedCommit: string;
  path: string;
  sourceResourceId: string;
  sourceCommit: string;
  sourcePath: string;
}

const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const libraryApi = {
  archiveURL: (resourceId: string, commit: string) =>
    `/api/resources/${encodeURIComponent(resourceId)}/archive?commit=${encodeURIComponent(commit)}`,
  patchURL: (resourceId: string, from: string, to: string) =>
    `/api/resources/${encodeURIComponent(resourceId)}/patch?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
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
    value: RestoreFilesInput & { requestId: string },
  ) =>
    request<{ resource: Resource; publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/restore`,
      json('POST', value),
    ),
  copy: (resourceId: string, value: CopyFileInput & { requestId: string }) =>
    request<{ resource: Resource; publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/copy-file`,
      json('POST', value),
    ),
  publicationReceipt: (resourceId: string, requestId: string) =>
    request<{ publication: Publication }>(
      `/resources/${encodeURIComponent(resourceId)}/publication-requests/${encodeURIComponent(requestId)}`,
    ),
};
