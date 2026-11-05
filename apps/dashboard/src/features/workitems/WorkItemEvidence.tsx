import { useEffect, useState } from 'react';
import { Button, Checkbox } from '@cofy-x/camp-platinum';
import {
  automationApi,
  type SourceEvent,
  type SourceEventPage,
} from '../automation/api';

function eventTitle(event: SourceEvent) {
  return event.preview || event.content || event.kind;
}

export function EvidenceChooser({
  roomId,
  selected,
  onChange,
  seed,
  initiallyOpen = false,
}: {
  roomId: string;
  selected: string[];
  onChange: (ids: string[]) => void;
  seed?: SourceEvent;
  initiallyOpen?: boolean;
}) {
  const [open, setOpen] = useState(initiallyOpen || Boolean(seed));
  const [cursor, setCursor] = useState<{ before?: number; through?: number }>(
    {},
  );
  const [previous, setPrevious] = useState<(typeof cursor)[]>([]);
  const [page, setPage] = useState<SourceEventPage | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void automationApi
      .activity(roomId, cursor, controller.signal)
      .then((page) => {
        if (!controller.signal.aborted) {
          setPage(page);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load evidence.',
          );
      });
    return () => controller.abort();
  }, [roomId, cursor, open, retry]);
  const items = page?.items ?? [];
  const events =
    seed && !items.some((event) => event.id === seed.id)
      ? [seed, ...items]
      : items;
  return (
    <details
      className="work-item-evidence-picker"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>
        Evidence{selected.length ? ` (${selected.length} selected)` : ''}
      </summary>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Evidence
          </Button>
        </p>
      )}
      {!page && !error && open && <p role="status">Loading evidence…</p>}
      {open && (
        <div className="work-item-evidence-options">
          {events.map((event) => (
            <Checkbox
              key={event.id}
              checked={selected.includes(event.id)}
              onChange={(input) =>
                onChange(
                  input.target.checked
                    ? [...selected, event.id]
                    : selected.filter((id) => id !== event.id),
                )
              }
              label={`${eventTitle(event).slice(0, 240)} · ${event.actorLogin || event.actorId}`}
            />
          ))}
          {page && !events.length && <p>No events in this range.</p>}
        </div>
      )}
      {open && (
        <div className="app-actions">
          <Button
            onClick={() => {
              setCursor({});
              setPrevious([]);
              setPage(null);
              setRetry((value) => value + 1);
            }}
          >
            Refresh Evidence
          </Button>
          {previous.length > 0 && (
            <Button
              onClick={() => {
                setCursor(previous.at(-1)!);
                setPrevious(previous.slice(0, -1));
                setPage(null);
              }}
            >
              Newer Evidence
            </Button>
          )}
          {page?.hasMore && (
            <Button
              onClick={() => {
                setPrevious([...previous, cursor]);
                setCursor({ before: page.nextBefore!, through: page.through });
                setPage(null);
              }}
            >
              Older Evidence
            </Button>
          )}
        </div>
      )}
      {selected.length > 0 && (
        <details>
          <summary>Selected event identities</summary>
          {selected.map((id) => (
            <div key={id} className="work-item-evidence-id">
              {id}{' '}
              <Button
                onClick={() =>
                  onChange(selected.filter((eventId) => eventId !== id))
                }
              >
                Remove
              </Button>
            </div>
          ))}
        </details>
      )}
    </details>
  );
}

export function EvidenceRecord({ id, label }: { id: string; label?: string }) {
  const [event, setEvent] = useState<SourceEvent | null>(null);
  const [error, setError] = useState('');
  const [open, setOpen] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void automationApi
      .event(id, controller.signal)
      .then(({ event }) => {
        if (!controller.signal.aborted) {
          setEvent(event);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read this evidence.',
          );
      });
    return () => controller.abort();
  }, [id, open, retry]);
  return (
    <details onToggle={(input) => setOpen(input.currentTarget.open)}>
      <summary>
        {event
          ? `${event.kind} · ${event.actorLogin || event.actorId}`
          : (label ?? 'Source event')}
      </summary>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Original
          </Button>
        </p>
      )}
      {open && !event && !error && <p role="status">Loading original…</p>}
      {event && (
        <>
          <p>
            {event.actorLogin || event.actorId} ·{' '}
            {new Date(event.occurredAt).toLocaleString()}
          </p>
          <pre>{event.content}</pre>
        </>
      )}
    </details>
  );
}
