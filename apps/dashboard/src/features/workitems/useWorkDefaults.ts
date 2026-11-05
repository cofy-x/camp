import { useEffect, useState } from 'react';
import { workItemsApi, type WorkItemDefaults } from './api';

export function useWorkDefaults(roomId: string, policyId = '') {
  const scope = JSON.stringify([roomId, policyId]);
  const [snapshot, setSnapshot] = useState<{
    scope: string;
    defaults: WorkItemDefaults | null;
    error: string;
  }>({ scope: '', defaults: null, error: '' });
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void workItemsApi
      .defaults(roomId, controller.signal, policyId)
      .then(({ defaults }) => {
        if (!controller.signal.aborted) {
          setSnapshot({ scope, defaults, error: '' });
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setSnapshot((current) => ({
            scope,
            defaults: current.scope === scope ? current.defaults : null,
            error:
              error instanceof Error
                ? error.message
                : 'Could not load work settings.',
          }));
      });
    return () => controller.abort();
  }, [roomId, policyId, scope, retry]);
  return {
    defaults: snapshot.scope === scope ? snapshot.defaults : null,
    error: snapshot.scope === scope ? snapshot.error : '',
    retry: () => setRetry((value) => value + 1),
  };
}
