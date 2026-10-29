import { useCallback, useEffect, useRef, useState } from 'react';
import { settingsApi, type Readiness } from './api';

interface ReadinessState {
  readiness: Readiness | null;
  loading: boolean;
  rechecking: boolean;
  error: string;
}

export function useReadiness(active: boolean) {
  const [state, setState] = useState<ReadinessState>({
    readiness: null,
    loading: false,
    rechecking: false,
    error: '',
  });
  const requests = useRef({
    mounted: false,
    lifetime: 0,
    visible: false,
    readGeneration: 0,
    pendingRecheck: null as Promise<void> | null,
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
    if (!request.mounted || !request.visible || request.pendingRecheck) return;
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

  const recheck = useCallback(async () => {
    const request = requests.current;
    if (!request.mounted) return;
    if (request.pendingRecheck) return request.pendingRecheck;
    const currentLifetime = request.lifetime;
    request.readGeneration++;
    setState((previous) => ({
      ...previous,
      loading: false,
      rechecking: true,
      error: '',
    }));

    // A deliberate server check continues when another Settings panel opens.
    // Reads cannot replace its result, and repeated clicks share this request.
    const operation = (async () => {
      const current = () =>
        request.mounted && currentLifetime === request.lifetime;
      try {
        const readiness = await settingsApi.readiness(true);
        if (current()) setState((previous) => ({ ...previous, readiness }));
      } catch (error) {
        if (current())
          setState((previous) => ({
            ...previous,
            error:
              error instanceof Error
                ? error.message
                : 'Could not check the working environment.',
          }));
      } finally {
        if (current()) {
          request.pendingRecheck = null;
          setState((previous) => ({ ...previous, rechecking: false }));
        }
      }
    })();
    request.pendingRecheck = operation;
    await operation;
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

  return { ...state, refresh, recheck };
}
