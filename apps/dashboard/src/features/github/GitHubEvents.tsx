import { useEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import type { Agent, Room, WorkspaceStatus } from '../workspace/api';
import { StartCollaboration } from '../collaboration/StartCollaboration';
import {
  githubApi,
  type GitHubEvent,
  type GitHubEventPage,
  type GitHubSource,
} from './api';
import { GitHubSources } from './GitHubSources';
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
  const team = useTeam();
  const [event, setEvent] = useState<GitHubEvent | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const [reason, setReason] = useState('');
  const operation = useGitHubOperation(`dismiss:${id}`);
  useEffect(() => {
    const controller = new AbortController();
    void githubApi
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
              : 'Could not load this event.',
          );
      });
    return () => controller.abort();
  }, [id, team.revision, retry]);
  const source = sources.find((source) => source.id === event?.sourceId);
  const readError = error || sourcesError;
  return (
    <section className="github-event-detail" aria-label="GitHub event">
      <div className="workspace-panel-heading">
        <h2 className="github-event-title">{event?.title || 'GitHub event'}</h2>
        <div className="app-actions">
          <Button
            onClick={() => {
              setRetry((value) => value + 1);
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
}: {
  room: Room;
  members: Agent[];
  onOpenTask: (id: string) => void;
  onClose: () => void;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
}) {
  const team = useTeam();
  const [sources, setSources] = useState<GitHubSource[]>([]);
  const [sourcesReady, setSourcesReady] = useState(false);
  const [sourcesError, setSourcesError] = useState('');
  const [page, setPage] = useState<GitHubEventPage | null>(null);
  const [state, setState] = useState('pending');
  const [selected, setSelected] = useState('');
  const [preparing, setPreparing] = useState<GitHubEvent | null>(null);
  const [loading, setLoading] = useState(true);
  const [moreLoading, setMoreLoading] = useState(false);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const generation = useRef(0);
  useEffect(() => {
    const controller = new AbortController();
    void githubApi
      .sources(room.id, controller.signal)
      .then(({ sources }) => {
        if (!controller.signal.aborted) {
          setSources(sources);
          setSourcesReady(true);
          setSourcesError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setSourcesError(
            error instanceof Error
              ? error.message
              : 'Could not load GitHub sources.',
          );
      });
    return () => controller.abort();
  }, [room.id, team.revision, retry]);
  useEffect(() => {
    const controller = new AbortController();
    generation.current++;
    setLoading(true);
    setMoreLoading(false);
    void githubApi
      .events(room.id, state, 0, undefined, controller.signal)
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
              : 'Could not load GitHub events.',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [room.id, state, team.revision, retry]);

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
      setPage((current) =>
        current ? { ...next, items: [...current.items, ...next.items] } : next,
      );
      setError('');
    } catch (error) {
      if (expected === generation.current)
        setError(
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
          <h2>GitHub events</h2>
          <div className="app-actions">
            <Button onClick={() => setRetry((value) => value + 1)}>
              Refresh Events
            </Button>
            <Button onClick={onClose}>Back to Chat</Button>
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
          onRefreshSources={() => setRetry((value) => value + 1)}
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
                setPage(null);
              }}
            >
              <option value="pending">Awaiting confirmation</option>
              <option value="all">All events</option>
              <option value="accepted">Collaboration started</option>
              <option value="dismissed">Dismissed</option>
              <option value="ignored">Ignored</option>
            </Select>
            {sourcesReady && (
              <GitHubSources roomId={room.id} sources={sources} />
            )}
          </div>
          {error && (
            <p className="app-error" role="alert">
              {error}{' '}
              <Button onClick={() => setRetry((value) => value + 1)}>
                Retry Events
              </Button>
            </p>
          )}
          {loading && !page && <p role="status">Loading events…</p>}
          {!loading && !error && !page?.items.length && (
            <p>
              No{' '}
              {state === 'pending'
                ? 'events awaiting confirmation'
                : 'matching events'}
              .
            </p>
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
