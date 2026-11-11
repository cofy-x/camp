import { useEffect, useRef, useState } from 'react';
import { isActiveRun } from './api';
import { useRead } from '../../shared/reads/useRead';
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
  const [tab, setTab] = useState('');
  const [requestOpen, setRequestOpen] = useState(false);
  const [details, setDetails] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const acceptedEvents = useRef<RunEvent[]>([]);
  const [after, setAfter] = useState(0);
  const summary = useRead<{ run: RunTimeline }>(
    `/runs/${encodeURIComponent(id)}?view=summary`,
  );
  const active = !run || isActiveRun(run);
  const wantsDetails =
    Boolean(run) &&
    (tab === 'files' ||
      tab === 'checks' ||
      (tab === 'overview' && requestOpen));
  const full = useRead<{ run: Run }>(`/runs/${encodeURIComponent(id)}`, {
    enabled: wantsDetails,
  });
  const activity = useRead<{ events: RunEvent[] }>(
    `/runs/${encodeURIComponent(id)}/events?after=${after}`,
    { enabled: Boolean(run) && tab === 'activity' },
  );
  useEffect(() => {
    if (!summary.data) return;
    const incoming = summary.data.run;
    setRun((current) => newerRun(current, incoming));
    setTab(
      (current) => current || (isActiveRun(incoming) ? 'activity' : 'overview'),
    );
  }, [summary.data]);
  useEffect(() => {
    const incoming = full.data?.run;
    if (incoming) setDetails((current) => newerRun(current, incoming));
  }, [full.data]);
  useEffect(() => {
    if (!activity.data || tab !== 'activity') return;
    acceptedEvents.current = mergeEvents(
      acceptedEvents.current,
      activity.data.events,
    );
    setEvents(acceptedEvents.current);
    const next = acceptedEvents.current.at(-1)?.seq ?? after;
    if (next > after) setAfter(next);
  }, [activity.data, tab, after]);
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
    error: summary.error,
    tab,
    setTab,
    requestOpen,
    setRequestOpen,
    details: detailsReady ? details : null,
    detailsError: full.error,
    detailsPending: wantsDetails && (full.loading || full.refreshing),
    events,
    eventsPending:
      tab === 'activity' && (activity.loading || activity.refreshing),
    eventsError: activity.error,
    retry: summary.refresh,
    retryDetails: full.refresh,
    retryEvents: activity.refresh,
  };
}
