import { useRead } from '../../shared/reads/useRead';
import type { Task } from './api';

export function useTasks(roomId: string) {
  const query = useRead<{ tasks: Task[] }>(
    roomId ? `/rooms/${encodeURIComponent(roomId)}/tasks?view=summary` : null,
  );
  return {
    tasks: query.data?.tasks ?? [],
    error: query.error,
    loading: query.loading,
    refresh: query.refresh,
  };
}
