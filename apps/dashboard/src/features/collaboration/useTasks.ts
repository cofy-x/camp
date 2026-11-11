import { useEffect, useState } from 'react';
import { useRead } from '../../shared/reads/useRead';
import type { TaskListItem } from './api';

export function useTasks(roomId: string, enabled: boolean, attention = false) {
  const [positions, setPositions] = useState<Record<string, string[]>>({});
  const cursors = positions[roomId] ?? [''];
  const query = useRead<{ tasks: TaskListItem[]; nextCursor: string }>(
    roomId
      ? `/rooms/${encodeURIComponent(roomId)}/tasks?${new URLSearchParams({ view: 'page', cursor: cursors.at(-1) ?? '', ...(attention ? { attention: '1' } : {}) })}`
      : null,
    { enabled },
  );
  const [accepted, setAccepted] = useState<{
    room: string;
    tasks: TaskListItem[];
  }>();
  useEffect(() => {
    if (query.data) setAccepted({ room: roomId, tasks: query.data.tasks });
  }, [query.data, roomId]);
  return {
    tasks:
      query.data?.tasks ?? (accepted?.room === roomId ? accepted.tasks : []),
    error: query.error,
    loading: query.loading,
    refresh: query.refresh,
    hasOlder: Boolean(query.data?.nextCursor),
    hasNewer: cursors.length > 1,
    older: () => {
      if (query.data?.nextCursor)
        setPositions((current) => ({
          ...current,
          [roomId]: [...cursors, query.data!.nextCursor],
        }));
    },
    newer: () =>
      setPositions((current) => ({
        ...current,
        [roomId]: cursors.slice(0, -1),
      })),
    latest: () => {
      setPositions((current) => ({ ...current, [roomId]: [''] }));
    },
  };
}
