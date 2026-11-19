import { request } from '../../shared/api/http';

export interface GitHubRepository {
  id: string;
  revision: number;
  repository: string;
  repositoryId: number;
  defaultBranch: string;
  private: boolean;
  actorId: number;
  ciWorkflow?: string;
  ciWorkflowId?: number;
  deploymentWorkflow?: string;
  deploymentWorkflowId?: number;
  enabled: boolean;
}

export interface GitHubDeliverySpec {
  connectionId: string;
  connectionRevision: number;
  baseBranch: string;
  mode: 'pr' | 'merge' | 'release';
  tag?: string;
  prerelease?: boolean;
  waitForDeployment?: boolean;
  maxRepairs?: number;
  versionLine?: string;
}

export interface GitHubDelivery {
  id: string;
  revision: number;
  repository: string;
  spec: GitHubDeliverySpec;
  stage: string;
  status: string;
  reason?: string;
  pullRequestUrl?: string;
  releaseUrl?: string;
  deploymentUrl?: string;
  headSha?: string;
  mergeSha?: string;
  reconcile?: boolean;
  failureCode?: string;
  checkUrl?: string;
  repairCause?: 'source_ci' | 'approved_review';
  repairs?: number;
}

export type RepositoryInput = Pick<
  GitHubRepository,
  'repository' | 'enabled'
> & {
  id?: string;
  revision?: number;
  ciWorkflow?: string;
  deploymentWorkflow?: string;
};

export const githubDeliveryApi = {
  save: (input: RepositoryInput & { requestId: string; token?: string }) =>
    request<{ repository: GitHubRepository }>('/github-repositories', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  receipt: (id: string) =>
    request<{ repository: GitHubRepository }>(
      `/github-repository-requests/${id}`,
    ),
  cancel: (id: string) =>
    request<{ repository: GitHubRepository | null; cancelled: boolean }>(
      `/github-repository-requests/${id}/cancel`,
      { method: 'POST', body: '{}' },
    ),
  check: (workId: string, revision: number) =>
    request(`/work-items/${workId}/github-delivery/check`, {
      method: 'POST',
      body: JSON.stringify({ revision }),
    }),
};
