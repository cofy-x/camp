import { useCallback, useEffect, useRef, useState } from 'react';
import { settingsApi, type Readiness } from './api';

interface ReadinessState {
  readiness: Readiness | null;
  loading: boolean;
  error: string;
}

export function useReadiness(active: boolean) {
  const [state, setState] = useState<ReadinessState>({
    readiness: null,
    loading: false,
    error: '',
  });
  const requests = useRef({
    mounted: false,
    lifetime: 0,
    visible: false,
    readGeneration: 0,
  });

  useEffect(() => {
    const current = requests.current;
    current.mounted = true;
    current.lifetime++;
    return () => {
      current.mounted = false;
      current.lifetime++;
      current.readGeneration++;
    };
  }, []);

  const refresh = useCallback(async () => {
    const request = requests.current;
    if (!request.mounted || !request.visible) return;
    const generation = ++request.readGeneration;
    const currentLifetime = request.lifetime;
    const current = () =>
      request.mounted &&
      request.visible &&
      currentLifetime === request.lifetime &&
      generation === request.readGeneration;
    setState((previous) => ({ ...previous, loading: true, error: '' }));
    try {
      const readiness = await settingsApi.readiness();
      if (current())
        setState((previous) => ({ ...previous, readiness, loading: false }));
    } catch (error) {
      if (current())
        setState((previous) => ({
          ...previous,
          loading: false,
          error:
            error instanceof Error
              ? error.message
              : 'Could not load preparation status.',
        }));
    }
  }, []);

  useEffect(() => {
    const request = requests.current;
    request.visible = active;
    if (active) void refresh();
    else setState((previous) => ({ ...previous, loading: false }));
    return () => {
      request.visible = false;
      request.readGeneration++;
    };
  }, [active, refresh]);

  return { ...state, refresh };
}
