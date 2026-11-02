import { useEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import type { Agent, Room } from '../workspace/api';
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
  onPrepare,
  onOpenTask,
  onClose,
}: {
  id: string;
  sources: GitHubSource[];
  sourcesError: string;
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
  return (
    <section className="github-event-detail" aria-label="GitHub event">
      <div className="workspace-panel-heading">
        <h3>{event?.title || 'GitHub event'}</h3>
        <Button onClick={onClose}>Back to Events</Button>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Event
          </Button>
        </p>
      )}
      {!event && !error && <p role="status">Loading event…</p>}
      {event && (
        <>
          <p>
            {labels[event.status]} · {event.repository} · {event.kind} /{' '}
            {event.action || 'ping'}
          </p>
          <p>
            {event.actorLogin && `GitHub: @${event.actorLogin} · `}Received{' '}
            {new Date(event.receivedAt).toLocaleString()}
          </p>
          <div className="app-actions">
            {event.url && (
              <a href={event.url} target="_blank" rel="noreferrer">
                Open on GitHub
              </a>
            )}
            <a href={githubApi.payloadURL(event.id)} download>
              Download Original Payload
            </a>
          </div>
          {event.body && <pre className="github-event-body">{event.body}</pre>}
          {event.status === 'pending' && (
            <>
              <p>
                Confirm a goal and participants to start Agent work. The
                original event will be included as external context.
              </p>
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
                Start collaboration…
              </Button>
              {!source?.enabled && (
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
}: {
  room: Room;
  members: Agent[];
  onOpenTask: (id: string) => void;
  onClose: () => void;
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
      <div className="workspace-panel-heading">
        <h2>GitHub events</h2>
        <div className="app-actions">
          <Button onClick={() => setRetry((value) => value + 1)}>
            Refresh Events
          </Button>
          <Button onClick={onClose}>Back to Chat</Button>
        </div>
      </div>
      {sourcesError && (
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
          onPrepare={setPreparing}
          onOpenTask={onOpenTask}
          onClose={() => setSelected('')}
        />
      ) : (
        <>
          <p>
            Observation only. Events wait here until a teammate confirms or
            dismisses them.
          </p>
          {sourcesReady && (
            <GitHubSources roomId={room.id} sources={sources} error="" />
          )}
          <Select
            label="Event state"
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
                  <span>{labels[event.status]}</span>
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
