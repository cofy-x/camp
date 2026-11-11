import { useRead } from '../../shared/reads/useRead';
import type { Readiness } from './api';

export function useReadiness(active: boolean) {
  const read = useRead<Readiness>('/settings/readiness', { enabled: active });
  return {
    readiness: read.data ?? null,
    loading: active && (read.loading || read.refreshing),
    error: read.error,
    refresh: read.refresh,
  };
}
