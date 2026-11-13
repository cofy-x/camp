import { useRead } from '../../shared/reads/useRead';
import type { WorkItemDefaults } from './api';

export function useWorkDefaults(roomId: string, policyId = '', enabled = true) {
  const read = useRead<{ defaults: WorkItemDefaults }>(
    roomId
      ? `/rooms/${encodeURIComponent(roomId)}/work-item-defaults${policyId ? `?policyId=${encodeURIComponent(policyId)}` : ''}`
      : null,
    { enabled },
  );
  return {
    defaults: read.data?.defaults ?? null,
    error: read.error,
    retry: read.refresh,
  };
}
