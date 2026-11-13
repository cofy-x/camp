import { useCallback, useState } from 'react';

type Screen =
  | { kind: 'chat' | 'agents' | 'start-task' | 'automation' | 'memory' }
  | { kind: 'tasks'; id: string }
  | { kind: 'reviews'; id: string };

type State = { screen: Screen; inspect: string };

export function useWorkspaceView() {
  const [view, setView] = useState<State>({
    screen: { kind: 'chat' },
    inspect: '',
  });
  const setPanel = useCallback(
    (kind: Screen['kind']) =>
      setView((current) => ({
        screen:
          current.screen.kind === kind
            ? current.screen
            : kind === 'tasks' || kind === 'reviews'
              ? { kind, id: '' }
              : { kind },
        inspect: '',
      })),
    [],
  );
  const setTaskId = useCallback(
    (id: string) =>
      setView((current) =>
        id
          ? { screen: { kind: 'tasks', id }, inspect: '' }
          : current.screen.kind === 'tasks'
            ? { ...current, screen: { kind: 'tasks', id: '' } }
            : current,
      ),
    [],
  );
  const setReviewId = useCallback(
    (id: string) =>
      setView((current) =>
        id
          ? { screen: { kind: 'reviews', id }, inspect: '' }
          : current.screen.kind === 'reviews'
            ? { ...current, screen: { kind: 'reviews', id: '' } }
            : current,
      ),
    [],
  );
  const setInspect = useCallback(
    (inspect: string) => setView((current) => ({ ...current, inspect })),
    [],
  );
  return {
    panel: view.screen.kind,
    taskId: view.screen.kind === 'tasks' ? view.screen.id : '',
    reviewId: view.screen.kind === 'reviews' ? view.screen.id : '',
    inspect: view.inspect,
    setPanel,
    setTaskId,
    setReviewId,
    setInspect,
  };
}
