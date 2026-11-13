import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import {
  useRead,
  useReadStore,
  useReadVisibility,
} from '../../shared/reads/useRead';
import type { Review, ReviewEventPage } from './api';

const actionLabels: Record<string, string> = {
  request: 'Requested review',
  approve: 'Approved',
  request_changes: 'Requested changes',
  resubmit: 'Submitted a new version',
  deliver: 'Delivered',
  cancel: 'Cancelled',
  reassign: 'Changed reviewer',
};

type Position = { cursor: string; previous: string[] };

export function ReviewHistory({
  review,
  active,
}: {
  review: Review;
  active: boolean;
}) {
  const team = useTeam();
  const store = useReadStore();
  const visible = useReadVisibility();
  const [position, setPosition] = useState<Position>({
    cursor: '',
    previous: [],
  });
  const [navigation, setNavigation] = useState<Position | null>(null);
  const root = useRef<HTMLElement>(null);
  const focus = useRef<Element | null>(null);
  const committed = useRef(false);
  const path = (cursor: string) =>
    `/reviews/${encodeURIComponent(review.id)}/events?${new URLSearchParams({ through: String(review.revision), cursor })}`;
  const read = useRead<ReviewEventPage>(path(position.cursor), {
    enabled: active,
  });
  const pending = useRead<ReviewEventPage>(
    navigation ? path(navigation.cursor) : null,
    { enabled: active },
  );
  const page = read.data;
  const error = pending.error || read.error;

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
    committed.current = true;
    setPosition(navigation);
    setNavigation(null);
  }, [
    navigation,
    pending.data,
    pending.error,
    pending.refreshing,
    active,
    visible,
  ]);

  useLayoutEffect(() => {
    if (!committed.current) return;
    committed.current = false;
    if (
      !root.current?.closest('.p9-window.is-active') ||
      (document.activeElement !== focus.current &&
        document.activeElement !== document.body)
    )
      return;
    const first = root.current.querySelector<HTMLElement>('li');
    first?.focus({ preventScroll: true });
    first?.scrollIntoView({ block: 'nearest' });
  }, [page, position]);

  function navigate(next: Position, control: HTMLElement) {
    focus.current = control;
    setNavigation(next);
  }

  return (
    <section
      ref={root}
      aria-label="Review history records"
      aria-busy={read.loading || Boolean(navigation && pending.refreshing)}
    >
      {error && (
        <p role="alert" className="app-error">
          {error}{' '}
          <Button
            onClick={() =>
              void store.refresh(path(navigation?.cursor ?? position.cursor))
            }
          >
            Retry History
          </Button>
        </p>
      )}
      {(!page || navigation) && !error && <p role="status">Loading history…</p>}
      {(position.previous.length > 0 || page?.nextCursor) && (
        <nav
          className="work-history-navigation"
          aria-label="Review history pages"
        >
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
            Newer
          </Button>
          <Button
            disabled={!page?.nextCursor}
            onClick={(event) =>
              navigate(
                {
                  cursor: page!.nextCursor,
                  previous: [...position.previous, page!.cursor],
                },
                event.currentTarget,
              )
            }
          >
            Earlier
          </Button>
        </nav>
      )}
      <ol>
        {page?.events.map((event) => (
          <li key={event.id} tabIndex={-1}>
            <strong>{actionLabels[event.action] ?? 'Review requested'}</strong>
            <span>
              {event.actorName} · {new Date(event.createdAt).toLocaleString()}
            </span>
            {event.comment && <p>{event.comment}</p>}
            <Button
              onClick={() =>
                void team.navigate({
                  app: 'library',
                  resourceId: review.resourceId,
                  path: review.path,
                  commit: event.commit,
                  contextRoomId: review.roomId,
                })
              }
            >
              View This Version
            </Button>
          </li>
        ))}
      </ol>
    </section>
  );
}
