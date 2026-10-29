import { useEffect, useState } from 'react';
import { useTeam } from '../../team/TeamContext';
import { runnersApi, type Runner } from './api';

export function useRunners() {
  const { revision, refresh } = useTeam();
  const [runners, setRunners] = useState<Runner[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void runnersApi
      .list(controller.signal)
      .then(({ runners }) => {
        if (controller.signal.aborted) return;
        setRunners(runners ?? []);
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load Runners.',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [revision]);
  return { runners, loading, error, refresh };
}
