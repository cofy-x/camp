import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useRead, useReadStore } from '../shared/reads/useRead';
import type { InboxItem, InboxPage } from './api';
import { inboxItemDetail, inboxItemTitle } from './inboxPresentation';

function activityTime(value: string) {
  const date = new Date(value);
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year:
      date.getFullYear() === new Date().getFullYear() ? undefined : 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function activityPath(cursor: string) {
  return cursor
    ? `/me/inbox?cursor=${encodeURIComponent(cursor)}`
    : '/me/inbox';
}

type Position = { cursor: string; previous: string[] };

export function InboxActivity({
  onOpen,
}: {
  onOpen: (item: InboxItem) => void;
}) {
  const [position, setPosition] = useState<Position>({
    cursor: '',
    previous: [],
  });
  const [navigation, setNavigation] = useState<Position | null>(null);
  const scroll = useRef<HTMLDivElement>(null);
  const moveFocus = useRef<HTMLElement | null>(null);
  const read = useRead<InboxPage>(activityPath(position.cursor));
  const pending = useRead<InboxPage>(
    navigation ? activityPath(navigation.cursor) : null,
  );
  const store = useReadStore();
  const error = pending.error || read.error;
  const accessDenied = pending.accessDenied || read.accessDenied;
  const items = accessDenied ? [] : (read.data?.items ?? []);
  const nextCursor = read.data?.nextCursor;

  useEffect(() => {
    if (navigation && pending.data && !pending.refreshing && !pending.error) {
      setPosition(navigation);
      setNavigation(null);
    }
  }, [navigation, pending.data, pending.refreshing, pending.error]);

  useLayoutEffect(() => {
    if (!moveFocus.current || !scroll.current) return;
    if (
      document.activeElement === moveFocus.current ||
      document.activeElement === document.body
    )
      scroll.current.focus({ preventScroll: true });
    scroll.current.scrollTop = 0;
    moveFocus.current = null;
  }, [position]);

  function navigate(next: Position, control: HTMLElement) {
    moveFocus.current = control;
    void store.refresh(activityPath(next.cursor));
    setNavigation(next);
  }

  const moving = !!navigation && !pending.error;
  return (
    <>
      <div
        className="camp-inbox-scroll"
        ref={scroll}
        tabIndex={-1}
        aria-busy={read.loading || moving}
      >
        {error && (
          <p className="app-error" role="alert">
            {error}{' '}
            <Button
              onClick={() =>
                void (navigation ? pending.refresh() : read.refresh())
              }
            >
              Retry Activity
            </Button>
          </p>
        )}
        {(read.loading || moving) && !error && (
          <p role="status">Loading activity…</p>
        )}
        {read.data && !items.length && !error && !moving && (
          <p className="camp-inbox-empty">No activity yet.</p>
        )}
        <ul className="camp-inbox-activity" aria-label="Activity">
          {items.map((item) => (
            <li key={item.id}>
              <button
                className={item.readAt ? '' : 'is-unread'}
                onClick={() => onOpen(item)}
              >
                <PixelIcon
                  name={
                    item.reviewId
                      ? 'checklist'
                      : item.kind === 'mention'
                        ? 'conversation'
                        : 'agent'
                  }
                  size={24}
                />
                <span className="camp-inbox-copy">
                  <strong>{inboxItemTitle(item)}</strong>
                  {inboxItemDetail(item) && (
                    <span className="camp-inbox-summary">
                      {inboxItemDetail(item)}
                    </span>
                  )}
                  <small className="camp-inbox-meta">
                    <span className="camp-inbox-room" title={item.roomName}>
                      {item.roomName}
                    </span>
                    <span aria-hidden="true">·</span>
                    <time
                      dateTime={item.createdAt}
                      title={new Date(item.createdAt).toLocaleString()}
                      aria-label={new Date(item.createdAt).toLocaleString()}
                    >
                      {activityTime(item.createdAt)}
                    </time>
                  </small>
                </span>
                {!item.readAt && (
                  <span className="camp-inbox-unread" aria-label="Unread" />
                )}
              </button>
            </li>
          ))}
        </ul>
      </div>
      {!accessDenied && (position.previous.length > 0 || nextCursor) && (
        <nav className="camp-inbox-actions" aria-label="Activity pages">
          <Button
            disabled={!position.previous.length || moving}
            onClick={(event) =>
              navigate(
                {
                  cursor: position.previous.at(-1) ?? '',
                  previous: position.previous.slice(0, -1),
                },
                event.currentTarget,
              )
            }
          >
            Newer
          </Button>
          <Button
            disabled={!nextCursor || moving}
            onClick={(event) => {
              if (nextCursor)
                navigate(
                  {
                    cursor: nextCursor,
                    previous: [...position.previous, position.cursor],
                  },
                  event.currentTarget,
                );
            }}
          >
            Earlier
          </Button>
        </nav>
      )}
    </>
  );
}
