import { useEffect, useRef, useState } from 'react';
import { Button, PixelIcon, Select, Tabs } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useRead, useReadStore } from '../../shared/reads/useRead';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { type Agent, type Room, type WorkspaceStatus } from '../workspace/api';
import { githubApi, type GitHubSource } from '../github/api';
import { GitHubEvents } from '../github/GitHubEvents';
import { GitHubSources } from '../github/GitHubSources';
import type { AutomationPolicy, SourceEventPage, SourceEvent } from './api';
import { CommentCredential, PolicyEditor } from './AutomationPolicy';
import { FollowEvent } from './FollowEvent';
import { EvaluationDecision } from './EvaluationDecision';
import { feishuApi } from '../feishu/api';
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
  waiting: 'Waiting for evidence',
  waiting_for_person: 'Needs judgment',
  completed: 'Work item completed',
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

function activityPath(roomId: string, sourceId: string, cursor: Cursor) {
  const query = new URLSearchParams({ direction: 'desc', limit: '20' });
  if (cursor.before !== undefined) query.set('before', String(cursor.before));
  if (cursor.through !== undefined)
    query.set('through', String(cursor.through));
  if (sourceId) query.set('sourceId', sourceId);
  return `/rooms/${encodeURIComponent(roomId)}/automation-events?${query}`;
}

function eventStatus(event: SourceEvent) {
  if (event.sourceId === 'task') return eventLabels[event.kind] ?? event.kind;
  if (event.reason === 'explicit_run') return 'Agent run started';
  if (event.reason === 'post_only') return 'Discussion only';
  if (event.reason === 'own_output') return 'Camp comment';
  if (event.status === 'observed') {
    if (
      event.assessmentOutcome === 'accept' ||
      event.assessmentOutcome === 'execute'
    )
      return 'Would start a collaboration';
    if (event.assessmentOutcome === 'ignore') return 'No new work';
  }
  return statusLabels[event.status] ?? event.status;
}

function OriginalEvent({ event }: { event: SourceEvent }) {
  const read = useRead<{ event: SourceEvent }>(
    `/automation-events/${encodeURIComponent(event.id)}`,
    { enabled: !event.content },
  );
  const content =
    event.content ||
    (read.data ? read.data.event.content || 'This event has no text.' : '');
  const error = read.error;
  return (
    <>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => void read.refresh()}>Retry Original</Button>
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
  onTrackEvent,
  onOpenWorkItem,
}: {
  event: SourceEvent;
  onOpenTask: (id: string) => void;
  onReviewGitHub: (id: string) => void;
  onTrackEvent: (event: SourceEvent) => void;
  onOpenWorkItem: (id: string) => void;
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
            : event.feishu
              ? `Feishu · ${event.feishu.chatId}`
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
        {event.workItemId && (
          <Button onClick={() => onOpenWorkItem(event.workItemId!)}>
            Open Work Item
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
              onOpenWorkItem={onOpenWorkItem}
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
            {event.feishu && (
              <a href={feishuApi.payloadURL(event.feishu.eventId)} download>
                Download Original Payload
              </a>
            )}
          </details>
          {open && <FollowEvent event={event} />}
          {open && (
            <Button onClick={() => onTrackEvent(event)}>
              Track Work Item…
            </Button>
          )}
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
  onTrackEvent,
  onOpenWorkItem,
  active,
}: {
  active: boolean;
  roomId: string;
  sourceId: string;
  sources: { id: string; label: string; enabled: boolean }[];
  onSourceChange: (id: string) => void;
  onOpenInbox: () => void;
  onOpenTask: (id: string) => void;
  onReviewGitHub: (id: string) => void;
  onTrackEvent: (event: SourceEvent) => void;
  onOpenWorkItem: (id: string) => void;
}) {
  const store = useReadStore();
  const [cursor, setCursor] = useState<Cursor>({});
  const [previous, setPrevious] = useState<Cursor[]>([]);
  const generation = useRef(0);
  const [state, setState] = useState<{
    cursor: Cursor;
    page?: SourceEventPage;
    error: string;
  }>({ cursor, error: '' });
  const read = useRead<SourceEventPage>(
    activityPath(roomId, sourceId, cursor),
    { enabled: active },
  );
  const cachedHead = useRef(read.data);
  useEffect(() => {
    // Subscribing can begin revalidation after render and before this effect.
    // Pin the currently accepted snapshot rather than that earlier render.
    const accepted = store.snapshot<SourceEventPage>(
      activityPath(roomId, sourceId, cursor),
    );
    if (accepted.data) {
      // A newly selected filter may reuse its accepted head while checking
      // newer events. Freeze its range only after that check has finished.
      if (
        cursor.through === undefined &&
        accepted.refreshing &&
        accepted.data === cachedHead.current
      )
        return;
      const frozen =
        cursor.through === undefined
          ? { ...cursor, through: accepted.data.through }
          : cursor;
      setState({ cursor: frozen, page: accepted.data, error: accepted.error });
      if (frozen !== cursor) setCursor(frozen);
    } else if (accepted.error) setState({ cursor, error: accepted.error });
  }, [cursor, read.data, read.error, read.refreshing, roomId, sourceId, store]);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );

  async function refreshEvents() {
    const expected = ++generation.current;
    const firstPath = activityPath(roomId, sourceId, {});
    await store.refresh(firstPath);
    const first = store.snapshot<SourceEventPage>(firstPath);
    if (expected !== generation.current) return;
    if (!first.data || first.error) {
      setState((current) => ({ ...current, error: first.error }));
      return;
    }
    const frozen = { through: first.data.through };
    const frozenPath = activityPath(roomId, sourceId, frozen);
    await store.refresh(frozenPath);
    if (expected !== generation.current) return;
    const accepted = store.snapshot<SourceEventPage>(frozenPath);
    setPrevious([]);
    setCursor(frozen);
    setState({
      cursor: frozen,
      page: accepted.data ?? first.data,
      error: accepted.error,
    });
  }

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
          <option value="timer">Follow-up timers</option>
          <option value="workitem">Work item events</option>
          <option value="action">Action receipts</option>
          {sources.map((source) => (
            <option value={source.id} key={source.id}>
              {source.label}
              {!source.enabled ? ' (disabled)' : ''}
            </option>
          ))}
        </Select>
        <div className="app-actions">
          <Button onClick={() => void refreshEvents()}>Refresh Events</Button>
          <Button onClick={onOpenInbox}>GitHub inbox</Button>
        </div>
      </div>
      {error && (
        <p role="alert" className="app-error">
          {error}{' '}
          <Button onClick={() => void read.refresh()}>Retry Events</Button>
        </p>
      )}
      {!page && !error && <p role="status">Loading events…</p>}
      {page && (
        <>
          {!page.items.length && (
            <div className="camp-empty">
              <PixelIcon name="document" size={32} />
              <p>No activity yet.</p>
            </div>
          )}
          <ol className="automation-event-list">
            {page.items.map((event) => (
              <EventRecord
                key={event.id}
                event={event}
                onOpenTask={onOpenTask}
                onReviewGitHub={onReviewGitHub}
                onTrackEvent={onTrackEvent}
                onOpenWorkItem={onOpenWorkItem}
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
                generation.current++;
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
                generation.current++;
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
  initialView = 'settings',
  initialRequest,
  onTrackEvent,
  onOpenWorkItem,
}: {
  room: Room;
  members: Agent[];
  onClose: () => void;
  onOpenTask: (id: string) => void;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  initialView?: 'settings' | 'activity';
  initialRequest?: number;
  onTrackEvent: (event: SourceEvent) => void;
  onOpenWorkItem: (id: string) => void;
}) {
  const { user } = useAccount();
  const sourcesRead = useRead<{ sources: GitHubSource[] }>(
    `/rooms/${encodeURIComponent(room.id)}/github-sources`,
  );
  const sources = sourcesRead.data?.sources ?? [];
  const [policies, setPolicies] = useState<AutomationPolicy[]>([]);
  const [sourceId, setSourceId] = useState('room');
  const [activitySource, setActivitySource] = useState('');
  const [view, setView] = useState<string>(initialView);
  const [settingsVisited, setSettingsVisited] = useState(
    initialView === 'settings',
  );
  const [githubSelection, setGitHubSelection] = useState<string | null>(null);
  const navigation = useRef(0);
  useEffect(() => {
    setView(initialView);
    if (initialView === 'settings') setSettingsVisited(true);
  }, [initialView, initialRequest]);
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
  const policiesRead = useRead<{ policies: AutomationPolicy[] }>(
    `/rooms/${encodeURIComponent(room.id)}/automation-policies`,
    { enabled: settingsVisited },
  );
  const error = policiesRead.error || sourcesRead.error;
  useEffect(() => {
    if (!policiesRead.data) return;
    setPolicies((current) => {
      const merged = new Map(
        current.map((policy) => [policy.sourceId, policy]),
      );
      for (const policy of policiesRead.data!.policies)
        if ((merged.get(policy.sourceId)?.revision ?? -1) <= policy.revision)
          merged.set(policy.sourceId, policy);
      return [...merged.values()];
    });
  }, [policiesRead.data]);
  useEffect(() => {
    if (policiesRead.data && sourcesRead.data) setReady(true);
  }, [policiesRead.data, sourcesRead.data]);

  function refreshPolicies() {
    void policiesRead.refresh();
    void sourcesRead.refresh();
  }

  const policy = policies.find((item) => item.sourceId === sourceId);
  const sourceChoices = [
    ...sources.map((source) => ({
      id: source.id,
      label: source.repository,
      enabled: source.enabled,
    })),
  ];

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
      aria-label="Room management"
    >
      <div className="workspace-panel-heading">
        <Button onClick={onClose}>Back to Discussion</Button>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error} <Button onClick={refreshPolicies}>Retry</Button>
        </p>
      )}
      <Tabs
        label="Room management views"
        tabs={[
          { id: 'settings', label: 'Sources & rules' },
          { id: 'activity', label: 'Activity' },
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
        <div className="automation-activity" hidden={view !== 'activity'}>
          <div
            className="automation-event-page"
            hidden={githubSelection !== null}
          >
            <EventHistory
              key={activitySource}
              active={view === 'activity' && githubSelection === null}
              roomId={room.id}
              sourceId={activitySource}
              sources={sourceChoices}
              onSourceChange={setActivitySource}
              onOpenInbox={() => navigate(() => setGitHubSelection(''))}
              onOpenTask={onOpenTask}
              onReviewGitHub={(id) => navigate(() => setGitHubSelection(id))}
              onTrackEvent={onTrackEvent}
              onOpenWorkItem={onOpenWorkItem}
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
                    {sourceChoices.map((source) => (
                      <option value={source.id} key={source.id}>
                        {source.label}
                        {!source.enabled ? ' (disabled)' : ''}
                      </option>
                    ))}
                  </Select>
                  <Button onClick={refreshPolicies}>Refresh Policies</Button>
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
