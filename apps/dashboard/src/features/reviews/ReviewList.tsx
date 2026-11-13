import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useRead, useReadVisibility } from '../../shared/reads/useRead';
import { reviewLabels, type Review, type ReviewListPage } from './api';

interface ReviewListProps {
  roomId?: string;
  resourceId?: string;
  path?: string;
  onOpen: (review: Review) => void;
  onClose: () => void;
  backLabel?: string;
  active?: boolean;
}

type Position = { cursor: string; previous: string[] };
type Navigation = Position & { control: HTMLElement };

export function ReviewList(props: ReviewListProps) {
  const scope = JSON.stringify([
    props.roomId ?? '',
    props.resourceId ?? '',
    props.path ?? '',
  ]);
  return <ScopedReviewList key={scope} {...props} />;
}

function ScopedReviewList({
  roomId,
  resourceId,
  path,
  onOpen,
  onClose,
  backLabel = 'Back to Discussion',
  active = true,
}: ReviewListProps) {
  const [position, setPosition] = useState<Position>({
    cursor: '',
    previous: [],
  });
  const [navigation, setNavigation] = useState<Navigation | null>(null);
  const [navigationError, setNavigationError] = useState('');
  const section = useRef<HTMLElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const opened = useRef({ id: '', scrollTop: 0, offset: 0 });
  const navigationTicket = useRef(0);
  const focusAfterPage = useRef<{
    ticket: number;
    focused: Element | null;
    changedPage: boolean;
  } | null>(null);
  const wasActive = useRef(active);
  const visible = useReadVisibility();
  const query = (cursor: string) => {
    const params = new URLSearchParams({ limit: '20' });
    if (roomId) params.set('roomId', roomId);
    if (resourceId) params.set('resourceId', resourceId);
    if (path) params.set('path', path);
    if (cursor) params.set('cursor', cursor);
    return '/reviews?' + params.toString();
  };
  const read = useRead<ReviewListPage>(query(position.cursor), {
    enabled: active,
  });
  const pending = useRead<ReviewListPage>(
    navigation ? query(navigation.cursor) : null,
    { enabled: active },
  );
  const page = read.data;
  const error = navigationError || pending.error || read.error;

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
    const focused = document.activeElement;
    const ownsFocus =
      focused === navigation.control ||
      section.current?.contains(focused) ||
      (focused === document.body &&
        section.current?.closest('.p9-window.is-active'));
    focusAfterPage.current = ownsFocus
      ? {
          ticket: navigationTicket.current,
          focused,
          changedPage: navigation.cursor !== position.cursor,
        }
      : null;
    setPosition({
      cursor: navigation.cursor,
      previous: navigation.previous,
    });
    setNavigation(null);
    setNavigationError('');
  }, [
    navigation,
    pending.data,
    pending.error,
    pending.refreshing,
    active,
    visible,
    position.cursor,
  ]);

  useLayoutEffect(() => {
    const focus = focusAfterPage.current;
    focusAfterPage.current = null;
    if (
      !focus ||
      focus.ticket !== navigationTicket.current ||
      !section.current?.closest('.p9-window.is-active') ||
      !list.current?.getClientRects().length ||
      (document.activeElement !== focus.focused &&
        document.activeElement !== document.body)
    )
      return;
    const rows = Array.from(list.current.querySelectorAll('button'));
    const top = list.current.getBoundingClientRect().top;
    const target = focus.changedPage
      ? rows[0]
      : rows.find((button) => button.getBoundingClientRect().bottom > top);
    if (focus.changedPage) {
      list.current.scrollTop = 0;
      target?.scrollIntoView({ block: 'center' });
    }
    target?.focus({ preventScroll: true });
  }, [position, page]);

  useEffect(() => {
    const returning = active && !wasActive.current;
    wasActive.current = active;
    if (!returning || !opened.current.id) return;
    const frame = requestAnimationFrame(() => {
      const buttons =
        list.current?.querySelectorAll<HTMLButtonElement>('button');
      const button = Array.from(buttons ?? []).find(
        (button) => button.dataset.reviewId === opened.current.id,
      );
      if (!button || !list.current) return;
      list.current.scrollTop = opened.current.scrollTop;
      list.current.scrollTop +=
        button.getBoundingClientRect().top -
        list.current.getBoundingClientRect().top -
        opened.current.offset;
      if (section.current?.closest('.p9-window.is-active'))
        button.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [active]);

  const navigate = (next: Position, control: HTMLElement) => {
    navigationTicket.current++;
    setNavigationError('');
    setNavigation({ ...next, control });
  };
  const pages = Boolean(position.previous.length || page?.nextCursor);
  return (
    <section
      ref={section}
      className="camp-review-list"
      aria-label="Document reviews"
      aria-busy={read.loading || Boolean(navigation && pending.refreshing)}
      hidden={!active}
    >
      <div className="camp-review-list-heading">
        <div className="camp-section-heading camp-review-list-toolbar">
          <Button onClick={onClose}>{backLabel}</Button>
          {pages && (
            <nav className="app-actions" aria-label="Review history pages">
              {position.previous.length > 1 && (
                <Button
                  onClick={(event) =>
                    navigate({ cursor: '', previous: [] }, event.currentTarget)
                  }
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
                Newer
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
                Earlier
              </Button>
            </nav>
          )}
        </div>
        {error ? (
          <div className="app-error" role="alert">
            {error}{' '}
            <Button
              onClick={(event) => {
                const next = navigation ?? position;
                navigate(next, event.currentTarget);
                setNavigationError(error);
                void (navigation ? pending.refresh() : read.refresh());
              }}
            >
              Retry reviews
            </Button>
          </div>
        ) : !page || navigation ? (
          <p role="status">Loading reviews…</p>
        ) : null}
      </div>
      {!error && page && !page.reviews.length ? (
        <p>No reviews yet.</p>
      ) : page && page.reviews.length > 0 ? (
        <ul ref={list}>
          {page.reviews.map((review) => (
            <li key={review.id}>
              <button
                data-review-id={review.id}
                onClick={(event) => {
                  opened.current = {
                    id: review.id,
                    scrollTop: list.current?.scrollTop ?? 0,
                    offset:
                      event.currentTarget.getBoundingClientRect().top -
                      (list.current?.getBoundingClientRect().top ?? 0),
                  };
                  navigationTicket.current++;
                  setNavigation(null);
                  setNavigationError('');
                  onOpen(review);
                }}
              >
                <PixelIcon
                  name={
                    review.status === 'delivered' ? 'document' : 'checklist'
                  }
                  size={24}
                />
                <span>
                  <strong>{review.title}</strong>
                  <small>
                    {review.reviewerName}
                    {!path && <> · {review.path}</>}
                  </small>
                </span>
                <span className="camp-review-state">
                  {reviewLabels[review.status]}
                  {review.outdated &&
                  review.status !== 'delivered' &&
                  review.status !== 'cancelled'
                    ? ' · Newer files'
                    : ''}
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
