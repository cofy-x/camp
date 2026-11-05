import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { workItemsApi, type WorkItem } from './api';

type Kind = 'evidence' | 'decisions' | 'tasks' | 'timers' | 'actions';

export function WorkItemHistory<K extends Kind>({
  itemId,
  kind,
  title,
  initial,
  render,
  active,
  refreshKey,
}: {
  itemId: string;
  kind: K;
  title: string;
  initial: WorkItem[K];
  render: (item: WorkItem[K][number]) => ReactNode;
  active: boolean;
  refreshKey: number;
}) {
  const team = useTeam();
  const [cursor, setCursor] = useState('');
  const [previous, setPrevious] = useState<string[]>([]);
  const [page, setPage] = useState<{
    items: WorkItem[K];
    nextCursor: string;
  } | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const refreshed = useRef(refreshKey);
  useEffect(() => {
    if (refreshed.current === refreshKey) return;
    refreshed.current = refreshKey;
    setPrevious([]);
    setCursor('');
    setPage(null);
    setRetry((value) => value + 1);
  }, [refreshKey]);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void workItemsApi
      .history(itemId, kind, cursor, controller.signal)
      .then((page) => {
        if (!controller.signal.aborted) {
          setPage(page);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read this history.',
          );
      });
    return () => controller.abort();
  }, [itemId, kind, cursor, active, retry, team.revision]);
  const items = page?.items ?? (cursor ? [] : initial);
  return (
    <section aria-label={title} hidden={!active}>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry {title}
          </Button>
        </p>
      )}
      {!page && !error && <p role="status">Loading {title.toLowerCase()}…</p>}
      {items.map((entry) => render(entry))}
      {page && !items.length && <p>No {title.toLowerCase()} recorded.</p>}
      <div className="app-actions">
        {previous.length > 0 && (
          <Button
            onClick={() => {
              setCursor(previous.at(-1)!);
              setPrevious(previous.slice(0, -1));
              setPage(null);
            }}
          >
            Previous {title}
          </Button>
        )}
        {page?.nextCursor && (
          <Button
            onClick={() => {
              setPrevious([...previous, cursor]);
              setCursor(page.nextCursor);
              setPage(null);
            }}
          >
            More {title}
          </Button>
        )}
      </div>
    </section>
  );
}
