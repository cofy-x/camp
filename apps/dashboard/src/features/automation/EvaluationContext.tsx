import { useEffect, useState, type ReactNode } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { automationApi } from './api';
import type { Memory } from '../memory/api';
import type { WorkItemSummary } from '../workitems/api';

type Kind = 'work-items' | 'memories';

type ContextItem<K extends Kind> = K extends 'work-items'
  ? WorkItemSummary
  : Memory;

export function EvaluationContext<K extends Kind>({
  evaluationId,
  kind,
  initial,
  total,
  title,
  render,
}: {
  evaluationId: string;
  kind: K;
  initial: ContextItem<K>[];
  total: number;
  title: string;
  render: (value: ContextItem<K>) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [after, setAfter] = useState(0);
  const [previous, setPrevious] = useState<number[]>([]);
  const [page, setPage] = useState<{
    items: ContextItem<K>[];
    nextAfter: number;
    hasMore: boolean;
    total: number;
  } | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void automationApi
      .evaluationContext(evaluationId, kind, after, controller.signal)
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
              : 'Could not read frozen context.',
          );
      });
    return () => controller.abort();
  }, [evaluationId, kind, after, open, retry]);
  const items = page?.items ?? initial;
  return (
    <details onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>{title}</summary>
      {open && (
        <>
          {(after > 0 || items.length < (page?.total ?? total)) && (
            <p>
              {items.length ? `${after + 1}–${after + items.length}` : '0'} of{' '}
              {page?.total ?? total}
            </p>
          )}
          {error && (
            <p className="app-error" role="alert">
              {error}{' '}
              <Button onClick={() => setRetry((value) => value + 1)}>
                Retry Context
              </Button>
            </p>
          )}
          {!page && !error && <p role="status">Loading context…</p>}
          {items.map((value) => render(value))}
          <div className="app-actions">
            {previous.length > 0 && (
              <Button
                onClick={() => {
                  setAfter(previous.at(-1)!);
                  setPrevious(previous.slice(0, -1));
                  setPage(null);
                }}
              >
                Previous Context
              </Button>
            )}
            {page?.hasMore && (
              <Button
                onClick={() => {
                  setPrevious([...previous, after]);
                  setAfter(page.nextAfter);
                  setPage(null);
                }}
              >
                More Context
              </Button>
            )}
          </div>
        </>
      )}
    </details>
  );
}
