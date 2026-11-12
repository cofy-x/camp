import { useEffect, useState } from 'react';
import { isActiveRun } from './api';
import { useRead } from '../../shared/reads/useRead';
import type { Run, RunActivityWindow, RunTimeline } from './api';

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
  const [position, setPosition] = useState('');
  const [acceptedActivity, setAcceptedActivity] = useState<{
    id: string;
    page: RunActivityWindow;
  }>();
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
  const activity = useRead<RunActivityWindow>(
    `/runs/${encodeURIComponent(id)}/activity?limit=50${position ? `&${position}` : ''}`,
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
    if (activity.data) setAcceptedActivity({ id, page: activity.data });
  }, [activity.data, id]);
  const page = acceptedActivity?.id === id ? acceptedActivity.page : undefined;

  function activityPage(direction: 'earlier' | 'later') {
    const edge = direction === 'earlier' ? page?.items[0] : page?.items.at(-1);
    if (edge && page)
      setPosition(
        new URLSearchParams({
          [direction === 'earlier' ? 'before' : 'after']: String(edge.seq),
          through: String(page.through),
        }).toString(),
      );
  }

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
    events: page?.items ?? [],
    activityWindow: page,
    activityAnchored: Boolean(position),
    activityPage,
    pinActivity() {
      const last = page?.items.at(-1);
      if (active && !position && page && last)
        setPosition(
          new URLSearchParams({
            before: String(last.seq + 1),
            through: String(page.through),
          }).toString(),
        );
    },
    latestActivity() {
      setPosition('');
      if (!position) void activity.refresh();
    },
    eventsPending:
      tab === 'activity' && (activity.loading || activity.refreshing),
    eventsError: activity.error,
    retry: summary.refresh,
    retryDetails: full.refresh,
    retryEvents: activity.refresh,
  };
}
