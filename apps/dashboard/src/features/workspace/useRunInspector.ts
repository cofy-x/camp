import { useEffect, useRef, useState } from 'react';
import { isActiveRun, workspaceApi } from './api';
import type { Run, RunEvent, RunTimeline } from './api';
import { mergeEvents } from './events';

export function newerRun<T extends Pick<Run, 'status' | 'updatedAt'>>(
  current: T | null,
  incoming: T,
): T {
  if (
    current &&
    (Date.parse(incoming.updatedAt) < Date.parse(current.updatedAt) ||
      (!isActiveRun(current) && isActiveRun(incoming)) ||
      (current.status === 'cancelling' &&
        isActiveRun(incoming) &&
        incoming.status !== 'cancelling'))
  )
    return current;
  return incoming;
}

export function useRunInspector(id: string) {
  const [run, setRun] = useState<RunTimeline | null>(null);
  const observedRun = useRef<RunTimeline | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [tab, setTab] = useState('');
  const [requestOpen, setRequestOpen] = useState(false);
  const [details, setDetails] = useState<Run | null>(null);
  const detailsRef = useRef<Run | null>(null);
  const [detailsError, setDetailsError] = useState('');
  const detailsFailed = useRef(false);
  const [detailsAttempt, setDetailsAttempt] = useState(0);
  const [detailsPending, setDetailsPending] = useState(false);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const acceptedEvents = useRef<RunEvent[]>([]);
  const [eventsPending, setEventsPending] = useState(false);
  const [eventsError, setEventsError] = useState('');
  const [eventsAttempt, setEventsAttempt] = useState(0);
  const active = !run || isActiveRun(run);
  const wantsDetails =
    Boolean(run) &&
    (tab === 'files' ||
      tab === 'checks' ||
      (tab === 'overview' && requestOpen));
  const pollChecks = tab === 'checks' && run?.status === 'checking';
  const detailsEpoch =
    run && !isActiveRun(run)
      ? `${run.updatedAt}:${run.resultCommit}:${run.published}:${run.fileCount ?? run.files.length}`
      : 'active';

  useEffect(() => {
    observedRun.current = run;
  }, [run]);

  // Current state is a small projection. Raw output and file bodies never
  // participate in progress polling or block the initial overview.
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      let ongoing = true;
      try {
        const result = await workspaceApi.runSummary(id, controller.signal);
        if (controller.signal.aborted) return;
        ongoing = isActiveRun(result.run);
        setRun((current) => newerRun(current, result.run));
        setTab((current) => current || (ongoing ? 'activity' : 'overview'));
        setError('');
      } catch (error) {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load this result.',
          );
      }
      if (!controller.signal.aborted && ongoing)
        timer = setTimeout(() => void poll(), 1200);
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id, attempt]);

  useEffect(() => {
    const current = observedRun.current;
    if (!wantsDetails || !current) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const cached = detailsRef.current;
    if (
      cached?.id === id &&
      !pollChecks &&
      !detailsFailed.current &&
      (isActiveRun(current) ||
        (!isActiveRun(cached) &&
          cached.updatedAt >= current.updatedAt &&
          cached.resultCommit === current.resultCommit &&
          cached.published === current.published))
    )
      return;

    async function read() {
      setDetailsPending(true);
      try {
        const { run: incoming } = await workspaceApi.run(id, controller.signal);
        if (controller.signal.aborted) return;
        const accepted = newerRun(detailsRef.current, incoming);
        detailsRef.current = accepted;
        setDetails(accepted);
        detailsFailed.current = false;
        setDetailsError('');
      } catch (error) {
        if (!controller.signal.aborted) {
          detailsFailed.current = true;
          setDetailsError(
            error instanceof Error
              ? error.message
              : 'Could not load execution details.',
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setDetailsPending(false);
          if (pollChecks) timer = setTimeout(() => void read(), 1200);
        }
      }
    }

    void read();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
    // The active epoch is stable: slow reads can finish while metadata advances.
    // A terminal snapshot refreshes the accepted file/check version once.
  }, [id, wantsDetails, pollChecks, detailsEpoch, detailsAttempt]);

  // Activity has its own read/error owner and runs only while that tab is open.
  // Accepted pages survive tab changes; incomplete reads never certify export.
  useEffect(() => {
    if (!observedRun.current || tab !== 'activity') return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      setEventsPending(true);
      try {
        for (;;) {
          const after = acceptedEvents.current.at(-1)?.seq ?? 0;
          const page = await workspaceApi.events(id, after, controller.signal);
          if (controller.signal.aborted) return;
          acceptedEvents.current = mergeEvents(
            acceptedEvents.current,
            page.events ?? [],
          );
          setEvents(acceptedEvents.current);
          const next = acceptedEvents.current.at(-1)?.seq ?? after;
          if (page.events.length < 500) break;
          if (next <= after)
            throw new Error('Could not finish reading activity. Retry.');
        }
        setEventsError('');
      } catch (error) {
        if (!controller.signal.aborted)
          setEventsError(
            error instanceof Error
              ? error.message
              : 'Could not load execution activity.',
          );
      } finally {
        if (!controller.signal.aborted) {
          setEventsPending(false);
          if (active) timer = setTimeout(() => void poll(), 1200);
        }
      }
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
    // Status changes inside the active phase do not interrupt a slow page.
  }, [id, tab, active, eventsAttempt]);

  const detailsReady = Boolean(
    details?.id === id &&
    run &&
    (active ||
      (!isActiveRun(details) &&
        details.updatedAt >= run.updatedAt &&
        details.resultCommit === run.resultCommit &&
        details.published === run.published)),
  );
  return {
    run,
    setRun,
    error,
    tab,
    setTab,
    requestOpen,
    setRequestOpen,
    details: detailsReady ? details : null,
    detailsError,
    detailsPending,
    events,
    eventsPending,
    eventsError,
    retry: () => setAttempt((value) => value + 1),
    retryDetails: () => setDetailsAttempt((value) => value + 1),
    retryEvents: () => setEventsAttempt((value) => value + 1),
  };
}
