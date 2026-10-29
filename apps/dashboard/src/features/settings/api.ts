import { request } from '../../shared/api/http';

export interface ModelSettings {
  model: string;
  baseUrl: string;
  maxTokens: number;
  maxSteps: number;
  timeoutSeconds: number;
  apiKeyConfigured: boolean;
  revision: string;
  fields: Record<
    string,
    {
      source: 'environment' | 'env-file' | 'managed' | 'default';
      overridden: boolean;
    }
  >;
}

export interface Readiness {
  ready: boolean;
  checkedAt: string;
  components: {
    id: string;
    label: string;
    status: 'ready' | 'missing' | 'error' | 'unknown';
    checkedAt?: string;
    lastSuccessAt?: string;
    lastFailureAt?: string;
    lastSuccess?: string;
    lastFailure?: string;
    detail: string;
  }[];
}

export interface EnvironmentSettings {
  platform: string;
  nodeVersion: string;
  packageManager: string;
  commandNetwork: boolean;
  concurrency: number;
  checks: string;
  limits: Record<string, number>;
}

export interface StorageSettings {
  dataDirectory: string;
  files: string;
  versions: string;
  backups: string;
  importRoots: string[];
}

export const settingsApi = {
  model: () => request<ModelSettings>('/settings/models'),
  saveModel: (
    value: Partial<
      Pick<
        ModelSettings,
        'model' | 'baseUrl' | 'maxTokens' | 'maxSteps' | 'timeoutSeconds'
      >
    > & { apiKey?: string; clearApiKey?: boolean },
  ) =>
    request<ModelSettings>('/settings/models', {
      method: 'PATCH',
      body: JSON.stringify(value),
    }),
  readiness: (recheck = false) =>
    request<Readiness>(
      `/settings/readiness${recheck ? '/recheck' : ''}`,
      recheck ? { method: 'POST', body: '{}' } : {},
    ),
  environments: () => request<EnvironmentSettings>('/settings/environments'),
  storage: () => request<StorageSettings>('/settings/storage'),
};
