import { useEffect, useState } from 'react';
import { useTeam } from '../../team/TeamContext';
import { tasksApi, type Task } from './api';

export function useTasks(roomId: string) {
  const { revision, refresh } = useTeam();
  const [state, setState] = useState<{
    roomId: string;
    tasks: Task[];
    error: string;
  }>({ roomId: '', tasks: [], error: '' });
  useEffect(() => {
    if (!roomId) return;
    const controller = new AbortController();
    void tasksApi
      .list(roomId, controller.signal)
      .then(({ tasks }) => {
        if (!controller.signal.aborted)
          setState({ roomId, tasks: tasks ?? [], error: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState((current) => ({
            roomId,
            tasks: current.roomId === roomId ? current.tasks : [],
            error:
              error instanceof Error
                ? error.message
                : 'Could not load collaborations.',
          }));
      });
    return () => controller.abort();
  }, [roomId, revision]);
  return {
    tasks: state.roomId === roomId ? state.tasks : [],
    error: state.roomId === roomId ? state.error : '',
    loading: state.roomId !== roomId,
    refresh,
  };
}
