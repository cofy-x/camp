import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import type { WorkItem } from './api';

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
  const [cursor, setCursor] = useState('');
  const [previous, setPrevious] = useState<string[]>([]);
  const read = useRead<{ items: WorkItem[K]; nextCursor: string }>(
    `/work-items/${encodeURIComponent(itemId)}/history/${kind}?${new URLSearchParams({ cursor, limit: '20' })}`,
    { enabled: active },
  );
  const page = read.data ?? null;
  const error = read.error;
  const refreshed = useRef(refreshKey);
  useEffect(() => {
    if (refreshed.current === refreshKey) return;
    refreshed.current = refreshKey;
    setPrevious([]);
    setCursor('');
  }, [refreshKey]);
  const items = page?.items ?? (cursor ? [] : initial);
  return (
    <section aria-label={title} hidden={!active}>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => void read.refresh()}>Retry {title}</Button>
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
            }}
          >
            More {title}
          </Button>
        )}
      </div>
    </section>
  );
}
