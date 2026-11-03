import { useEffect, useRef, useState } from 'react';
import { Button, Select, Tabs } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import {
  workspaceApi,
  type Agent,
  type Room,
  type WorkspaceStatus,
} from '../workspace/api';
import { githubApi, type GitHubSource } from '../github/api';
import { GitHubEvents } from '../github/GitHubEvents';
import { GitHubSources } from '../github/GitHubSources';
import {
  automationApi,
  type AutomationPolicy,
  type SourceEventPage,
  type SourceEvent,
} from './api';
import { CommentCredential, PolicyEditor } from './AutomationPolicy';
import { FollowEvent } from './FollowEvent';
import { EvaluationDecision } from './EvaluationDecision';
import './automation.css';

const statusLabels: Record<string, string> = {
  pending: 'Awaiting assessment',
  evaluating: 'Assessing',
  accepted: 'Collaboration started',
  observed: 'Observation recorded',
  would_accept: 'Would start a collaboration',
  ignored: 'Ignored',
  unavailable: 'Assessment unavailable',
  needs_attention: 'Needs attention',
  context: 'Context recorded',
  answered: 'Question answered',
  stopped: 'Stopped',
};

const attentionReasons: Record<string, string> = {
  authorization_unavailable:
    'Review the policy’s members, Agents and Runner access.',
  daily_evaluation_limit: 'Daily assessment allowance reached.',
  daily_task_limit: 'Daily collaboration allowance reached.',
  stale_assessment:
    'The policy or collaboration changed during assessment. Review this event.',
};

const eventLabels: Record<string, string> = {
  'room.message': 'Discussion',
  'task.start': 'Collaboration started',
  'task.inputs': 'Input added',
  'task.answers': 'Question answered',
  'task.continue': 'Collaboration continued',
  'task.cancel': 'Collaboration stopped',
  'issues.opened': 'Issue opened',
  'issues.reopened': 'Issue reopened',
  'issues.edited': 'Issue edited',
  'issue_comment.created': 'Comment added',
  'issue_comment.edited': 'Comment edited',
  'pull_request.opened': 'Pull request opened',
  'pull_request.reopened': 'Pull request reopened',
  'pull_request.edited': 'Pull request edited',
  'pull_request.synchronize': 'Pull request updated',
  'pull_request.ready_for_review': 'Pull request ready for review',
  'pull_request.closed': 'Pull request closed',
};

type Cursor = { before?: number; through?: number };

function eventStatus(event: SourceEvent) {
  if (event.sourceId === 'task') return eventLabels[event.kind] ?? event.kind;
  if (event.reason === 'explicit_run') return 'Agent run started';
  if (event.reason === 'post_only') return 'Discussion only';
  if (event.reason === 'own_output') return 'Camp comment';
  if (event.status === 'observed') {
    if (event.assessmentOutcome === 'accept')
      return 'Would start a collaboration';
    if (event.assessmentOutcome === 'ignore') return 'No new work';
  }
  return statusLabels[event.status] ?? event.status;
}

function OriginalEvent({ event }: { event: SourceEvent }) {
  const [content, setContent] = useState(event.content);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (event.content) return;
    const controller = new AbortController();
    const load = event.externalEventId
      ? githubApi
          .event(event.externalEventId, controller.signal)
          .then(({ event }) =>
            [event.title, event.body].filter(Boolean).join('\n\n'),
          )
      : event.messageId
        ? workspaceApi
            .message(event.roomId, event.messageId, controller.signal)
            .then(({ message }) => message.content)
        : Promise.resolve('Original text is unavailable.');
    void load
      .then((text) => {
        if (!controller.signal.aborted) {
          setContent(text);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read the original event.',
          );
      });
    return () => controller.abort();
  }, [
    event.content,
    event.externalEventId,
    event.messageId,
    event.roomId,
    attempt,
  ]);
  return (
    <>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setAttempt((value) => value + 1)}>
            Retry Original
          </Button>
        </p>
      )}
      {!content && !error && <p role="status">Loading original…</p>}
      {content && <pre className="automation-event-content">{content}</pre>}
    </>
  );
}

function EventRecord({
  event,
  onOpenTask,
  onReviewGitHub,
}: {
  event: SourceEvent;
  onOpenTask: (id: string) => void;
  onReviewGitHub: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const command = event.sourceId === 'task';
  const kind = eventLabels[event.kind] ?? event.kind;
  const outcome = eventStatus(event);
  const attention =
    event.status === 'needs_attention' ||
    event.status === 'unavailable' ||
    event.assessmentOutcome === 'unavailable';
  const attentionReason =
    attention &&
    (attentionReasons[event.reason ?? ''] || event.assessmentReason);
  return (
    <li
      className={
        event.status === 'needs_attention' ? 'automation-event-attention' : ''
      }
    >
      <div className="automation-event-meta">
        <span>
          {event.repository
            ? `${event.repository}${event.number ? ` #${event.number}` : ''}`
            : command
              ? 'Collaboration'
              : 'Room discussion'}
        </span>
        <span>{event.actorLogin || event.actorId}</span>
        <time
          dateTime={event.occurredAt}
          title={
            event.occurredAt
              ? new Date(event.occurredAt).toLocaleString()
              : undefined
          }
        >
          {event.occurredAt
            ? new Date(event.occurredAt).toLocaleString(undefined, {
                dateStyle: 'short',
                timeStyle: 'short',
              })
            : ''}
        </time>
      </div>
      {(!command || event.preview) && (
        <p className="automation-event-title">{event.preview || kind}</p>
      )}
      <div className="automation-heading automation-event-outcome">
        <strong>{outcome}</strong>
        {event.taskId && (
          <Button onClick={() => onOpenTask(event.taskId!)}>
            Open Collaboration
          </Button>
        )}
      </div>
      {attentionReason && <p>{attentionReason}</p>}
      {!command && (
        <details onToggle={(toggle) => setOpen(toggle.currentTarget.open)}>
          <summary>Details</summary>
          {open && <OriginalEvent event={event} />}
          {open && event.evaluationId && (
            <EvaluationDecision
              key={event.evaluationId}
              id={event.evaluationId}
              onOpenTask={onOpenTask}
            />
          )}
          {open &&
            event.externalEventId &&
            !event.taskId &&
            event.reason !== 'own_output' && (
              <Button onClick={() => onReviewGitHub(event.externalEventId!)}>
                Review GitHub Event
              </Button>
            )}
          <details>
            <summary>Event data</summary>
            <dl className="automation-facts">
              <dt>Event</dt>
              <dd>{kind}</dd>
              <dt>Thread</dt>
              <dd>{event.threadKey}</dd>
              <dt>Actor ID</dt>
              <dd>{event.actorId}</dd>
              {event.policyRevision !== undefined && (
                <>
                  <dt>Policy revision</dt>
                  <dd>{event.policyRevision}</dd>
                </>
              )}
              {event.reason && (
                <>
                  <dt>Decision</dt>
                  <dd>{event.reason}</dd>
                </>
              )}
            </dl>
            {event.externalEventId && (
              <a href={githubApi.payloadURL(event.externalEventId)} download>
                Download Original Payload
              </a>
            )}
          </details>
          {open && <FollowEvent event={event} />}
        </details>
      )}
    </li>
  );
}

function EventHistory({
  roomId,
  sourceId,
  sources,
  onSourceChange,
  onOpenInbox,
  onOpenTask,
  onReviewGitHub,
}: {
  roomId: string;
  sourceId: string;
  sources: GitHubSource[];
  onSourceChange: (id: string) => void;
  onOpenInbox: () => void;
  onOpenTask: (id: string) => void;
  onReviewGitHub: (id: string) => void;
}) {
  const team = useTeam();
  const [cursor, setCursor] = useState<Cursor>({});
  const [previous, setPrevious] = useState<Cursor[]>([]);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<{
    cursor: Cursor;
    page?: SourceEventPage;
    error: string;
  }>({ cursor, error: '' });
  useEffect(() => {
    const controller = new AbortController();
    void automationApi
      .activity(roomId, { ...cursor, sourceId }, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        const frozen =
          cursor.through === undefined
            ? { ...cursor, through: page.through }
            : cursor;
        setState({ cursor: frozen, page, error: '' });
        if (frozen !== cursor) setCursor(frozen);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState((current) => ({
            cursor,
            page: current.cursor === cursor ? current.page : undefined,
            error:
              error instanceof Error ? error.message : 'Could not load events.',
          }));
      });
    return () => controller.abort();
  }, [roomId, sourceId, cursor, retry, team.revision]);
  const page = state.cursor === cursor ? state.page : undefined;
  const error = state.cursor === cursor ? state.error : '';
  return (
    <section aria-label="Automation events" className="automation-events">
      <div className="automation-view-controls">
        <Select
          aria-label="Activity source"
          value={sourceId}
          onChange={(event) => onSourceChange(event.target.value)}
        >
          <option value="">All sources</option>
          <option value="room">Room discussion</option>
          <option value="task">Collaboration actions</option>
          {sources.map((source) => (
            <option value={source.id} key={source.id}>
              {source.repository}
              {!source.enabled ? ' (disabled)' : ''}
            </option>
          ))}
        </Select>
        <div className="app-actions">
          <Button
            onClick={() => {
              setPrevious([]);
              setCursor({});
              setRetry((value) => value + 1);
            }}
          >
            Refresh Events
          </Button>
          <Button onClick={onOpenInbox}>GitHub inbox</Button>
        </div>
      </div>
      {error && (
        <p role="alert" className="app-error">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Events
          </Button>
        </p>
      )}
      {!page && !error && <p role="status">Loading events…</p>}
      {page && (
        <>
          {!page.items.length && <p>No activity yet.</p>}
          <ol className="automation-event-list">
            {page.items.map((event) => (
              <EventRecord
                key={event.id}
                event={event}
                onOpenTask={onOpenTask}
                onReviewGitHub={onReviewGitHub}
              />
            ))}
          </ol>
        </>
      )}
      {(previous.length > 0 || page?.hasMore) && (
        <div className="app-actions">
          {previous.length > 0 && (
            <Button
              onClick={() => {
                setCursor(previous.at(-1)!);
                setPrevious(previous.slice(0, -1));
              }}
            >
              Newer Events
            </Button>
          )}
          {page?.hasMore && (
            <Button
              onClick={() => {
                setPrevious([...previous, cursor]);
                setCursor({
                  before: page.nextBefore!,
                  through: page.through,
                });
              }}
            >
              Older Events
            </Button>
          )}
        </div>
      )}
    </section>
  );
}

export function Automation({
  room,
  members,
  onClose,
  onOpenTask,
  availability,
}: {
  room: Room;
  members: Agent[];
  onClose: () => void;
  onOpenTask: (id: string) => void;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
}) {
  const { user } = useAccount();
  const team = useTeam();
  const [sources, setSources] = useState<GitHubSource[]>([]);
  const [policies, setPolicies] = useState<AutomationPolicy[]>([]);
  const [sourceId, setSourceId] = useState('room');
  const [activitySource, setActivitySource] = useState('');
  const [view, setView] = useState('activity');
  const [settingsVisited, setSettingsVisited] = useState(false);
  const [githubSelection, setGitHubSelection] = useState<string | null>(null);
  const navigation = useRef(0);
  useEffect(
    () => () => {
      navigation.current++;
    },
    [],
  );

  function navigate(action: () => void) {
    const expected = ++navigation.current;
    void flushPrivateDrafts().then((saved) => {
      if (saved && expected === navigation.current) action();
    });
  }

  const [ready, setReady] = useState(false);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      automationApi.policies(room.id, controller.signal),
      githubApi.sources(room.id, controller.signal),
    ])
      .then(([saved, observed]) => {
        if (controller.signal.aborted) return;
        setPolicies((current) => {
          const merged = new Map(
            current.map((policy) => [policy.sourceId, policy]),
          );
          for (const policy of saved.policies) {
            if (
              (merged.get(policy.sourceId)?.revision ?? -1) <= policy.revision
            )
              merged.set(policy.sourceId, policy);
          }
          return [...merged.values()];
        });
        setSources(observed.sources);
        setReady(true);
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load automation.',
          );
      });
    return () => controller.abort();
  }, [room.id, team.revision, retry]);
  const policy = policies.find((item) => item.sourceId === sourceId);

  function saved(policy: AutomationPolicy) {
    setPolicies((current) => {
      if (
        current.some(
          (item) =>
            item.sourceId === policy.sourceId &&
            item.revision > policy.revision,
        )
      )
        return current;
      return [
        ...current.filter((item) => item.sourceId !== policy.sourceId),
        policy,
      ];
    });
  }

  return (
    <section
      className="workspace-panel automation-panel"
      aria-label="Room automation"
    >
      <div className="workspace-panel-heading">
        <h2>Automation</h2>
        <Button onClick={onClose}>Back to Chat</Button>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Automation
          </Button>
        </p>
      )}
      <Tabs
        label="Automation views"
        tabs={[
          { id: 'activity', label: 'Activity' },
          { id: 'settings', label: 'Settings' },
        ]}
        value={view}
        onChange={(next) => {
          // Both views stay mounted, including draft recovery. A blocked
          // background draft must not prevent returning to its recovery view.
          navigation.current++;
          if (next === 'settings') setSettingsVisited(true);
          setView(next);
        }}
      >
        <div hidden={view !== 'activity'}>
          <div hidden={githubSelection !== null}>
            <EventHistory
              key={activitySource}
              roomId={room.id}
              sourceId={activitySource}
              sources={sources}
              onSourceChange={setActivitySource}
              onOpenInbox={() => navigate(() => setGitHubSelection(''))}
              onOpenTask={onOpenTask}
              onReviewGitHub={(id) => navigate(() => setGitHubSelection(id))}
            />
          </div>
          {githubSelection !== null && (
            <GitHubEvents
              key={githubSelection}
              room={room}
              members={members}
              initialEventId={githubSelection}
              availability={availability}
              onOpenTask={onOpenTask}
              onClose={() => navigate(() => setGitHubSelection(null))}
            />
          )}
        </div>
        {settingsVisited && (
          <div hidden={view !== 'settings'} className="automation-settings">
            {!ready && !error && <p role="status">Loading settings…</p>}
            {ready && (
              <>
                <div className="automation-view-controls">
                  <Select
                    label="Event source"
                    value={sourceId}
                    onChange={(event) => {
                      const next = event.target.value;
                      navigate(() => setSourceId(next));
                    }}
                  >
                    <option value="room">Room discussion</option>
                    {sources.map((source) => (
                      <option value={source.id} key={source.id}>
                        {source.repository}
                        {!source.enabled ? ' (disabled)' : ''}
                      </option>
                    ))}
                  </Select>
                  <Button onClick={() => setRetry((value) => value + 1)}>
                    Refresh Policies
                  </Button>
                </div>
                {user.role === 'admin' ? (
                  <PolicyEditor
                    key={sourceId}
                    roomId={room.id}
                    sourceId={sourceId}
                    policy={policy}
                    members={members}
                    onSaved={saved}
                  />
                ) : (
                  <div className="automation-policy-summary">
                    <p>
                      {!policy?.enabled
                        ? 'Off'
                        : policy.mode === 'auto'
                          ? 'Auto'
                          : 'Observe'}
                    </p>
                    {policy?.rules && <p>{policy.rules}</p>}
                  </div>
                )}
                {user.role === 'admin' && policy && sourceId !== 'room' && (
                  <CommentCredential
                    key={sourceId}
                    policy={policy}
                    onSaved={saved}
                    onEditingBlocked={() => setView('settings')}
                  />
                )}
                <GitHubSources roomId={room.id} sources={sources} />
              </>
            )}
          </div>
        )}
      </Tabs>
    </section>
  );
}
