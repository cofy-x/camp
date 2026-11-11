import { useEffect, useRef, useState } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import type { Agent, Room, WorkspaceStatus } from '../workspace/api';
import { StartCollaboration } from '../collaboration/StartCollaboration';
import {
  githubApi,
  type GitHubEvent,
  type GitHubEventPage,
  type GitHubSource,
} from './api';
import { useGitHubOperation } from './useGitHubOperation';
import './github.css';

const labels = {
  pending: 'Awaiting confirmation',
  accepted: 'Collaboration started',
  dismissed: 'Dismissed',
  ignored: 'Ignored',
};
const reasons: Record<string, string> = {
  ping: 'Webhook connection check',
  unsupported_event: 'Event or action outside the subscribed workflow',
  nonhuman_actor: 'Bot or unresolved GitHub identity',
};

function EventDetail({
  id,
  sources,
  sourcesError,
  sourcesReady,
  onRefreshSources,
  onPrepare,
  onOpenTask,
  onClose,
}: {
  id: string;
  sources: GitHubSource[];
  sourcesError: string;
  sourcesReady: boolean;
  onRefreshSources: () => void;
  onPrepare: (event: GitHubEvent) => void;
  onOpenTask: (id: string) => void;
  onClose: () => void;
}) {
  const read = useRead<{ event: GitHubEvent }>(
    `/github-events/${encodeURIComponent(id)}`,
  );
  const event = read.data?.event;
  const error = read.error;
  const [reason, setReason] = useState('');
  const operation = useGitHubOperation(`dismiss:${id}`);
  const source = sources.find((source) => source.id === event?.sourceId);
  const readError = error || sourcesError;
  return (
    <section className="github-event-detail" aria-label="GitHub event">
      <div className="workspace-panel-heading">
        <h2 className="github-event-title">{event?.title || 'GitHub event'}</h2>
        <div className="app-actions">
          <Button
            onClick={() => {
              void read.refresh();
              onRefreshSources();
            }}
          >
            {readError ? 'Retry Event' : 'Refresh Event'}
          </Button>
          <Button onClick={onClose}>Back to Events</Button>
        </div>
      </div>
      {readError && (
        <p className="app-error" role="alert">
          {readError}
        </p>
      )}
      {!event && !readError && <p role="status">Loading event…</p>}
      {event && (
        <>
          <div className="github-event-meta">
            {event.url && (
              <a href={event.url} target="_blank" rel="noreferrer">
                {event.repository}
                {event.number ? ` #${event.number}` : ''}
              </a>
            )}
            {event.actorLogin && <span>@{event.actorLogin}</span>}
            <span>{labels[event.status]}</span>
          </div>
          {event.body && <pre className="github-event-body">{event.body}</pre>}
          {event.status === 'pending' && (
            <>
              <Button
                primary
                disabled={
                  operation.blocked ||
                  Boolean(error) ||
                  Boolean(sourcesError) ||
                  !source?.enabled
                }
                onClick={() => onPrepare(event)}
              >
                {sourcesReady ? 'Start collaboration…' : 'Loading source…'}
              </Button>
              {sourcesReady && !source?.enabled && (
                <p className="app-error">
                  This source is disabled or unavailable. An administrator must
                  enable it before confirmation.
                </p>
              )}
              <details>
                <summary>Dismiss event</summary>
                <form
                  onSubmit={(form) => {
                    form.preventDefault();
                    void operation.run((requestId) =>
                      githubApi.dismiss(id, { requestId, reason }),
                    );
                  }}
                >
                  <label>
                    Dismissal reason
                    <textarea
                      className="p9-field"
                      rows={2}
                      required
                      maxLength={1024}
                      value={reason}
                      disabled={operation.blocked}
                      onChange={(event) => setReason(event.target.value)}
                    />
                  </label>
                  <Button
                    type="submit"
                    disabled={operation.blocked || !reason.trim()}
                  >
                    Dismiss Event
                  </Button>
                </form>
              </details>
            </>
          )}
          {event.taskId && (
            <Button primary onClick={() => onOpenTask(event.taskId!)}>
              Open Collaboration
            </Button>
          )}
          {event.ignoreReason && (
            <p>{reasons[event.ignoreReason] ?? event.ignoreReason}</p>
          )}
          {event.decisionReason && event.status === 'dismissed' && (
            <p>{event.decisionReason}</p>
          )}
          <details>
            <summary>Event details</summary>
            <dl className="github-facts">
              <dt>Event</dt>
              <dd>
                {event.kind} / {event.action || 'ping'}
              </dd>
              <dt>Repository ID</dt>
              <dd>{event.repositoryId}</dd>
              <dt>Received</dt>
              <dd>{new Date(event.receivedAt).toLocaleString()}</dd>
            </dl>
            <a href={githubApi.payloadURL(event.id)} download>
              Download Original Payload
            </a>
          </details>
          {operation.feedback}
        </>
      )}
    </section>
  );
}

export function GitHubEvents({
  room,
  members,
  onOpenTask,
  onClose,
  availability,
  initialEventId = '',
}: {
  room: Room;
  members: Agent[];
  onOpenTask: (id: string) => void;
  onClose: () => void;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  initialEventId?: string;
}) {
  const [state, setState] = useState('pending');
  const [selected, setSelected] = useState(initialEventId);
  const [preparing, setPreparing] = useState<GitHubEvent | null>(null);
  const [moreLoading, setMoreLoading] = useState(false);
  const [moreError, setMoreError] = useState('');
  const [expanded, setExpanded] = useState<{
    first: GitHubEventPage;
    page: GitHubEventPage;
  } | null>(null);
  const sourceRead = useRead<{ sources: GitHubSource[] }>(
    `/rooms/${encodeURIComponent(room.id)}/github-sources`,
    { enabled: !preparing },
  );
  const eventsRead = useRead<GitHubEventPage>(
    `/rooms/${encodeURIComponent(room.id)}/github-events?${new URLSearchParams({ state, after: '0' })}`,
    { enabled: !selected && !preparing },
  );
  const sources = sourceRead.data?.sources ?? [];
  const sourcesReady = sourceRead.data !== undefined;
  const sourcesError = sourceRead.error;
  const first = eventsRead.data;
  const page = first && expanded?.first === first ? expanded.page : first;
  const loading = eventsRead.loading;
  const error = eventsRead.error || moreError;
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setMoreLoading(false);
    setMoreError('');
  }, [room.id, state, first]);

  function refreshEvents() {
    void sourceRead.refresh();
    void eventsRead.refresh();
  }

  async function more() {
    if (!page?.hasMore || page.nextAfter === null || moreLoading) return;
    const expected = generation.current;
    setMoreLoading(true);
    try {
      const next = await githubApi.events(
        room.id,
        state,
        page.nextAfter,
        page.through,
      );
      if (expected !== generation.current) return;
      if (first)
        setExpanded({
          first,
          page: { ...next, items: [...page.items, ...next.items] },
        });
      setMoreError('');
    } catch (error) {
      if (expected === generation.current)
        setMoreError(
          error instanceof Error
            ? error.message
            : 'Could not load more events.',
        );
    } finally {
      if (expected === generation.current) setMoreLoading(false);
    }
  }

  if (preparing)
    return (
      <StartCollaboration
        key={preparing.id}
        room={room}
        members={members}
        threadId=""
        availability={availability}
        sourceEvent={{
          id: preparing.id,
          title: `Address ${preparing.repository}#${preparing.number}: ${preparing.title}`,
          url: preparing.url,
        }}
        onStarted={(task) => onOpenTask(task.id)}
        onClose={() => setPreparing(null)}
      />
    );
  return (
    <section
      className="workspace-panel github-panel"
      aria-label="GitHub events"
    >
      {!selected && (
        <div className="workspace-panel-heading">
          <h3>GitHub inbox</h3>
          <div className="app-actions">
            <Button onClick={refreshEvents}>Refresh Events</Button>
            <Button onClick={onClose}>Back to Activity</Button>
          </div>
        </div>
      )}
      {sourcesError && !selected && (
        <p className="app-error" role="alert">
          {sourcesError}
        </p>
      )}
      {selected ? (
        <EventDetail
          key={selected}
          id={selected}
          sources={sources}
          sourcesError={sourcesError}
          sourcesReady={sourcesReady}
          onRefreshSources={() => void sourceRead.refresh()}
          onPrepare={setPreparing}
          onOpenTask={onOpenTask}
          onClose={() => setSelected('')}
        />
      ) : (
        <>
          <div className="github-list-controls">
            <Select
              aria-label="Event state"
              value={state}
              onChange={(event) => {
                setState(event.target.value);
                setExpanded(null);
              }}
            >
              <option value="pending">Awaiting confirmation</option>
              <option value="all">All events</option>
              <option value="accepted">Collaboration started</option>
              <option value="dismissed">Dismissed</option>
              <option value="ignored">Ignored</option>
            </Select>
          </div>
          {error && (
            <p className="app-error" role="alert">
              {error} <Button onClick={refreshEvents}>Retry Events</Button>
            </p>
          )}
          {loading && !page && <p role="status">Loading events…</p>}
          {!loading && !error && !page?.items.length && (
            <div className="camp-empty">
              <PixelIcon name="document" size={32} />
              <p>
                {state === 'pending'
                  ? 'No events awaiting confirmation.'
                  : 'No matching events.'}
              </p>
            </div>
          )}
          <ul className="github-event-list">
            {page?.items.map((event) => (
              <li key={event.id}>
                <button
                  className="github-event-link"
                  onClick={() => setSelected(event.id)}
                >
                  <strong>{event.title || event.kind}</strong>
                  {state === 'all' && <span>{labels[event.status]}</span>}
                  <small>
                    {event.repository}
                    {event.number ? ` #${event.number}` : ''} ·{' '}
                    {event.actorLogin || event.actorKind || 'GitHub'} ·{' '}
                    {new Date(event.receivedAt).toLocaleString()}
                  </small>
                </button>
              </li>
            ))}
          </ul>
          {page?.hasMore && (
            <Button
              disabled={moreLoading || loading}
              onClick={() => void more()}
            >
              {moreLoading ? 'Loading…' : 'More Events'}
            </Button>
          )}
        </>
      )}
    </section>
  );
}
