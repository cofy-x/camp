import { useCallback } from 'react';
import { useRead } from '../../shared/reads/useRead';
import type { Runner } from './api';

export function useRunners() {
  const {
    data,
    loading,
    error,
    refresh: refreshRead,
  } = useRead<{ runners: Runner[] }>('/runners');
  const refresh = useCallback(() => void refreshRead(), [refreshRead]);
  return {
    runners: data?.runners ?? [],
    loading,
    error,
    refresh,
  };
}
