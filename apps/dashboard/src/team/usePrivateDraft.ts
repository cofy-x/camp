import { useCallback, useEffect, useRef, useState } from 'react';
import { teamApi, type PrivateDraft } from './api';
import { useAccount } from './AuthContext';
import { registerDraftWriter } from './draftRegistry';

export function usePrivateDraft<T>(kind: string, key: string, initial: T) {
  const { user } = useAccount();
  const storageKey = `camp:private:${user.id}:${kind}:${key}`;
  const [value, setValueState] = useState(initial);
  const [loaded, setLoaded] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [comparison, setComparison] = useState<PrivateDraft<T> | null>(null);
  const state = useRef({
    value: initial,
    revision: 0,
    serial: 0,
    saved: 0,
    loaded: false,
    blocked: false,
    mounted: true,
  });
  const active = useRef<Promise<boolean> | null>(null);
  const initialRef = useRef(initial);
  const backup = useCallback(
    (snapshot: { value: T; revision: number } | null) => {
      try {
        if (snapshot)
          sessionStorage.setItem(storageKey, JSON.stringify(snapshot));
        else sessionStorage.removeItem(storageKey);
      } catch {
        /* Keep the mounted draft when browser storage is full. */
      }
    },
    [storageKey],
  );
  useEffect(() => {
    const controller = new AbortController();
    const current = state.current;
    current.mounted = true;
    void teamApi
      .draft<T>(kind, key, controller.signal)
      .then(({ draft }) => {
        if (controller.signal.aborted) return;
        let local: { value: T; revision: number } | null = null;
        try {
          local = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as {
            value: T;
            revision: number;
          } | null;
        } catch {
          /* Use the server draft. */
        }
        const next = local?.value ?? draft.value ?? initialRef.current;
        current.value = next;
        current.revision = draft.revision;
        current.loaded = true;
        current.serial = local ? 1 : 0;
        current.saved = 0;
        current.blocked = Boolean(
          local &&
          local.revision !== draft.revision &&
          JSON.stringify(local.value) !== JSON.stringify(draft.value),
        );
        setValueState(next);
        setLoaded(true);
        setError(
          current.blocked
            ? 'This private draft changed in another session. Your local version is preserved. Compare the versions before choosing which to keep.'
            : '',
        );
        if (current.blocked) setComparison(draft);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not restore your private draft.',
          );
      });
    return () => {
      controller.abort();
      current.mounted = false;
    };
  }, [kind, key, storageKey, loadAttempt]);
  const flush = useCallback((): Promise<boolean> => {
    const current = state.current;
    if (active.current) return active.current;
    if (!current.loaded)
      return Promise.resolve(current.serial === current.saved);
    if (!current.mounted || current.blocked) return Promise.resolve(false);
    if (current.serial === current.saved) return Promise.resolve(true);
    setSaving(true);
    const task = (async () => {
      try {
        while (current.mounted && current.serial > current.saved) {
          const snapshot = { value: current.value, serial: current.serial };
          const result = await teamApi.saveDraft(
            kind,
            key,
            snapshot.value,
            current.revision,
          );
          current.revision = result.draft.revision;
          current.saved = snapshot.serial;
          backup(
            current.serial === current.saved
              ? null
              : { value: current.value, revision: current.revision },
          );
        }
        if (current.mounted) setError('');
        return current.serial === current.saved;
      } catch (error) {
        current.blocked = true;
        if (current.mounted)
          setError(
            error instanceof Error
              ? error.message
              : 'Your draft could not be saved. Your local version is preserved.',
          );
        return false;
      } finally {
        active.current = null;
        if (current.mounted) setSaving(false);
      }
    })();
    active.current = task;
    return task;
  }, [kind, key, backup]);
  const getValue = useCallback(() => state.current.value, []);
  const setValue = useCallback(
    (next: T) => {
      const current = state.current;
      current.value = next;
      current.serial++;
      backup({ value: next, revision: current.revision });
      setValueState(next);
    },
    [backup],
  );
  const inspectConflict = useCallback(async () => {
    if (!state.current.loaded) {
      setLoadAttempt((value) => value + 1);
      return;
    }
    try {
      const latest = await teamApi.draft<T>(kind, key);
      if (state.current.mounted) setComparison(latest.draft);
    } catch (error) {
      if (state.current.mounted)
        setError(
          error instanceof Error ? error.message : 'Could not compare drafts.',
        );
    }
  }, [kind, key]);
  const resolveConflict = useCallback(async () => {
    if (!comparison) {
      await inspectConflict();
      return false;
    }
    state.current.revision = comparison.revision;
    state.current.blocked = false;
    state.current.serial++;
    setComparison(null);
    return flush();
  }, [comparison, inspectConflict, flush]);
  const useRemote = useCallback(() => {
    if (!comparison) return;
    const next = comparison.value ?? initialRef.current;
    state.current.value = next;
    state.current.revision = comparison.revision;
    state.current.saved = state.current.serial;
    state.current.blocked = false;
    backup(null);
    setValueState(next);
    setComparison(null);
    setError('');
  }, [comparison, backup]);
  useEffect(() => registerDraftWriter(storageKey, flush), [storageKey, flush]);
  useEffect(() => {
    const prevent = (event: BeforeUnloadEvent) => {
      if (state.current.serial !== state.current.saved) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', prevent);
    return () => window.removeEventListener('beforeunload', prevent);
  }, []);
  useEffect(() => {
    if (!loaded) return;
    const timer = setTimeout(() => void flush(), 500);
    return () => clearTimeout(timer);
  }, [value, loaded, flush]);
  return {
    value,
    getValue,
    setValue,
    loaded,
    saving,
    error,
    flush,
    comparison,
    inspectConflict,
    resolveConflict,
    useRemote,
  };
}
