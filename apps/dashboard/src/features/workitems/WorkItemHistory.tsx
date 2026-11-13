import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { Button } from '@cofy-x/camp-platinum';
import {
  useRead,
  useReadStore,
  useReadVisibility,
} from '../../shared/reads/useRead';
import type { WorkActivity, WorkItem } from './api';

type Histories = Pick<
  WorkItem,
  'evidence' | 'decisions' | 'tasks' | 'timers' | 'actions'
> & { timeline: WorkActivity[] };

type Kind = keyof Histories;
type Position = { cursor: string; previous: string[] };

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
  initial: Histories[K];
  render: (item: Histories[K][number]) => ReactNode;
  active: boolean;
  refreshKey: number;
}) {
  const [position, setPosition] = useState<Position>({
    cursor: '',
    previous: [],
  });
  const [navigation, setNavigation] = useState<Position | null>(null);
  const [navigationError, setNavigationError] = useState('');
  const root = useRef<HTMLElement>(null);
  const focus = useRef<{ from: Element | null; control: HTMLElement } | null>(
    null,
  );
  const focusAfterCommit = useRef(false);
  const visible = useReadVisibility();
  const store = useReadStore();
  const query = useCallback(
    (cursor: string) =>
      `/work-items/${encodeURIComponent(itemId)}/history/${kind}?${new URLSearchParams({ cursor, limit: '20' })}`,
    [itemId, kind],
  );
  const read = useRead<{ items: Histories[K]; nextCursor: string }>(
    query(position.cursor),
    { enabled: active },
  );
  const pending = useRead<{ items: Histories[K]; nextCursor: string }>(
    navigation ? query(navigation.cursor) : null,
    { enabled: active },
  );
  const page = read.data;
  const error = navigationError || pending.error || read.error;
  const refreshed = useRef(refreshKey);

  useEffect(() => {
    if (pending.error) setNavigationError(pending.error);
  }, [pending.error]);
  useEffect(() => {
    if (
      !navigation ||
      !pending.data ||
      pending.error ||
      pending.refreshing ||
      !active ||
      !visible
    )
      return;
    focusAfterCommit.current = true;
    setPosition(navigation);
    setNavigation(null);
    setNavigationError('');
  }, [
    navigation,
    pending.data,
    pending.error,
    pending.refreshing,
    active,
    visible,
  ]);
  useLayoutEffect(() => {
    if (!focusAfterCommit.current) return;
    focusAfterCommit.current = false;
    const intent = focus.current;
    focus.current = null;
    if (!intent || !root.current?.closest('.p9-window.is-active')) return;
    const current = document.activeElement;
    if (
      current !== intent.from &&
      current !== intent.control &&
      current !== document.body
    )
      return;
    const target = root.current.querySelector<HTMLElement>(
      '[data-history-record]',
    );
    target?.scrollIntoView({ block: 'nearest' });
    target?.focus({ preventScroll: true });
  }, [position, page]);
  useEffect(() => {
    if (!active || !visible || refreshed.current === refreshKey) return;
    refreshed.current = refreshKey;
    focus.current = null;
    setNavigation(null);
    setNavigationError('');
    setPosition({ cursor: '', previous: [] });
    void store.refresh(query(''));
  }, [refreshKey, store, query, active, visible]);

  function navigate(next: Position, control: HTMLElement) {
    focus.current = { from: document.activeElement, control };
    setNavigationError('');
    setNavigation(next);
  }

  const items = page?.items ?? (position.cursor ? [] : initial);
  const timeline = kind === 'timeline';
  return (
    <section
      ref={root}
      aria-label={title}
      hidden={!active}
      aria-busy={read.loading || Boolean(navigation && pending.refreshing)}
    >
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button
            onClick={(event) => {
              navigate(navigation ?? position, event.currentTarget);
              setNavigationError(error);
              void store.refresh(query(navigation?.cursor ?? position.cursor));
            }}
          >
            Retry {title}
          </Button>
        </p>
      )}
      {(!page || navigation) && !error && (
        <p role="status">Loading {title.toLowerCase()}…</p>
      )}
      {(position.previous.length > 0 || page?.nextCursor) && (
        <nav className="work-history-navigation" aria-label={`${title} pages`}>
          {timeline && position.previous.length > 0 && (
            <Button
              onClick={(event) => {
                void store.refresh(query(''));
                navigate({ cursor: '', previous: [] }, event.currentTarget);
              }}
            >
              Latest
            </Button>
          )}
          <Button
            disabled={!position.previous.length}
            onClick={(event) =>
              navigate(
                {
                  cursor: position.previous.at(-1)!,
                  previous: position.previous.slice(0, -1),
                },
                event.currentTarget,
              )
            }
          >
            {timeline ? 'Newer' : `Previous ${title}`}
          </Button>
          <Button
            disabled={!page?.nextCursor}
            onClick={(event) =>
              navigate(
                {
                  cursor: page!.nextCursor,
                  previous: [...position.previous, position.cursor],
                },
                event.currentTarget,
              )
            }
          >
            {timeline ? 'Earlier' : `More ${title}`}
          </Button>
        </nav>
      )}
      {items.map((entry, index) => (
        <div
          data-history-record
          key={
            'id' in entry
              ? String(entry.id)
              : 'eventId' in entry
                ? String(entry.eventId)
                : index
          }
          tabIndex={-1}
        >
          {render(entry)}
        </div>
      ))}
      {page && !items.length && !error && (
        <p>No {title.toLowerCase()} recorded.</p>
      )}
    </section>
  );
}
