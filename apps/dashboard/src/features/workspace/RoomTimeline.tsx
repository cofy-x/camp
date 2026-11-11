import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, PixelIcon, type DesktopAPI } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import type {
  Agent,
  Message,
  Reference,
  Resource,
  Room,
  RunTimeline,
} from './api';
import { isActiveRun, statusLabel, workspaceApi } from './api';
import { AgentResult } from './AgentResult';
import { Composer } from './Composer';
import { References } from './References';
import { WorkActivity } from './WorkActivity';
import { SystemMessage } from './SystemMessage';
import type { ConversationTask, useWorkspace } from './useWorkspace';
import {
  conversationKey,
  readStored,
  subscribeStored,
  writeStored,
} from './conversationState';

const starters = [
  {
    icon: 'document' as const,
    title: 'Draft a document',
    description: 'Draft a proposal, guide, or report.',
    prompt:
      'Draft a one-page customer onboarding guide in onboarding.md for a small team. Include three preparation steps, role owners, relative deadlines, and a measurable go-live checklist. Keep it practical and concise.',
  },
  {
    icon: 'checklist' as const,
    title: 'Make a plan',
    description: 'Plan milestones, owners, and next steps.',
    prompt:
      'Create launch-plan.md for a small team preparing its first product launch. Include a two-week timeline, owners, dependencies, and a short launch-day checklist. State your assumptions.',
  },
  {
    icon: 'conversation' as const,
    title: 'Explore an idea',
    description: 'Compare options and decide what to do next.',
    prompt:
      'Help me think through an idea. Start by asking what I want to achieve and what constraints matter, then help me compare practical options.',
  },
];

export function agentIcon(icon: string) {
  return (
    [
      'agent',
      'computer',
      'document',
      'notes',
      'terminal',
      'checklist',
      'conversation',
      'image',
    ].includes(icon)
      ? icon
      : 'agent'
  ) as
    | 'agent'
    | 'computer'
    | 'document'
    | 'notes'
    | 'terminal'
    | 'checklist'
    | 'conversation'
    | 'image';
}

export function timeLabel(value: string) {
  return new Date(value).toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
  });
}

function WorkCard({
  run,
  avatar,
  onInspect,
  onChange,
  showTaskLink = true,
  workItemId,
}: {
  run: RunTimeline;
  avatar?: string;
  onInspect: (id: string) => void;
  onChange: () => void;
  showTaskLink?: boolean;
  workItemId?: string;
}) {
  const team = useTeam();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');

  async function stop() {
    setPending(true);
    setError('');
    try {
      await workspaceApi.cancel(run.id);
      onChange();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Could not cancel this work.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div
      className={`workspace-work-card state-${run.status}`}
      data-testid="agent-work"
      data-state={run.status}
    >
      <div className="workspace-work-heading">
        <IdentityAvatar
          avatar={avatar ?? run.agentSnapshot.avatar}
          kind="agent"
          size={24}
        />
        <strong>{run.agentSnapshot.name}</strong>
        <span className="workspace-work-status">{statusLabel[run.status]}</span>
        <Button onClick={() => onInspect(run.id)}>
          {isActiveRun(run) ? 'Activity' : 'Details'}
        </Button>
        {run.taskId && showTaskLink && (
          <Button
            onClick={() =>
              void team.navigate({
                app: 'workspace',
                roomId: run.roomId,
                taskId: run.taskId,
                workItemId,
              })
            }
          >
            {workItemId ? 'Open work' : 'Open collaboration'}
          </Button>
        )}
        {isActiveRun(run) && !run.taskId && (
          <Button
            disabled={pending || run.status === 'cancelling'}
            onClick={() => void stop()}
          >
            {pending || run.status === 'cancelling' ? 'Stopping…' : 'Stop'}
          </Button>
        )}
      </div>
      {(run.runnerName || run.runnerId) && (
        <p className="workspace-work-runner">
          Runner: {run.runnerName || run.runnerId}
        </p>
      )}
      <WorkActivity run={run} />
      {run.error && <p className="app-error">{run.error}</p>}
      {error && (
        <p role="alert" className="app-error">
          {error}
        </p>
      )}
    </div>
  );
}

function RoomWelcome({
  draftKey,
  members,
  membersReady,
  available,
  onSetup,
  onAgents,
  onPrepared,
}: {
  draftKey: string;
  members: Agent[];
  membersReady: boolean;
  available: boolean;
  onSetup?: () => void;
  onAgents: () => void;
  onPrepared: () => void;
}) {
  const draft = useSyncExternalStore(
    useCallback(
      (listener) => subscribeStored(`draft:${draftKey}`, listener),
      [draftKey],
    ),
    () => readStored(`draft:${draftKey}`),
    () => '',
  );
  const hasMember = members.some((agent) => agent.enabled);
  return (
    <div className="workspace-room-welcome">
      <PixelIcon name="conversation" size={28} />
      <h2>What would you like to work on?</h2>
      {membersReady && !available && onSetup && (
        <Button primary onClick={onSetup}>
          Set Up Agent Work…
        </Button>
      )}
      {membersReady &&
        (hasMember ? (
          <>
            <div className="workspace-starters" aria-label="Starting points">
              {starters.map((starter) => (
                <Button
                  key={starter.title}
                  aria-label={starter.title}
                  aria-description={starter.description}
                  disabled={Boolean(draft.trim())}
                  onClick={() => {
                    if (readStored(`draft:${draftKey}`).trim()) return;
                    writeStored(`draft:${draftKey}`, starter.prompt);
                    const agent = members.find((member) => member.enabled);
                    if (agent)
                      writeStored(
                        `recipients:${draftKey}`,
                        JSON.stringify([agent.id]),
                      );
                    onPrepared();
                  }}
                >
                  <PixelIcon name={starter.icon} size={24} />
                  <span className="workspace-starter-copy">
                    <span>{starter.title}</span>
                    <span className="workspace-starter-description">
                      {starter.description}
                    </span>
                  </span>
                </Button>
              ))}
            </div>
            <p className="workspace-starter-hint">
              {draft.trim()
                ? 'Edit your draft below, then send when ready.'
                : 'Choose a starting point to edit below.'}
            </p>
          </>
        ) : (
          <div className="workspace-welcome-invite">
            <p>Post a message or add an Agent to help.</p>
            <Button onClick={onAgents}>Add an agent</Button>
          </div>
        ))}
    </div>
  );
}

export function RoomTimeline({
  desktop,
  room,
  threadId,
  messages,
  runs,
  tasks,
  history,
  agents,
  members,
  membersReady = true,
  available,
  onThread,
  onInspect,
  onReview,
  onChange,
  resources,
  sourceId,
  sourceRequest,
  onAsk,
  onFollowUp,
  onRemember,
  onSource,
  onReference,
  onAgents,
  onSetup,
}: {
  desktop: DesktopAPI;
  room: Room;
  threadId: string;
  messages: Message[];
  resources: Resource[];
  sourceId: string;
  sourceRequest: number;
  onAsk: (message: Message, run?: RunTimeline) => void;
  onFollowUp: (message: Message) => void;
  onRemember: (message: Message) => void;
  onSource: (messageId: string) => void;
  onReference: (reference: Reference) => void;
  onAgents: () => void;
  onSetup?: () => void;
  runs: RunTimeline[];
  tasks: ConversationTask[];
  history: ReturnType<typeof useWorkspace>['history'];
  agents: Agent[];
  members: Agent[];
  membersReady?: boolean;
  available: boolean;
  onThread: (id: string) => void;
  onInspect: (id: string) => void;
  onReview: (id: string) => void;
  onChange: () => void;
}) {
  const team = useTeam();
  const scroll = useRef<HTMLDivElement>(null);
  const follow = useRef(true);
  const initialized = useRef(false);
  const focusedSource = useRef(-1);
  const activity = useRef('');
  const acceptedPosition = useRef('');
  const scrollIntent = useRef(false);
  const [newActivity, setNewActivity] = useState(false);
  const key = conversationKey(room.id, threadId);
  const [focusRequest, setFocusRequest] = useState(0);
  const lookup = useMemo(() => {
    const replies = new Set(
      messages
        .filter((message) => message.authorKind === 'agent')
        .map((message) => message.runId),
    );
    const pending = new Map<string, RunTimeline[]>();
    const threads = new Map<string, RunTimeline>();
    for (const run of runs) {
      if (run.threadRootId === threadId && !replies.has(run.id)) {
        const list = pending.get(run.originMessageId) ?? [];
        list.push(run);
        pending.set(run.originMessageId, list);
      }
      const old = threads.get(run.threadRootId);
      if (
        !old ||
        (!isActiveRun(old) &&
          (isActiveRun(run) || run.updatedAt > old.updatedAt))
      )
        threads.set(run.threadRootId, run);
    }
    return {
      runs: new Map(runs.map((run) => [run.id, run])),
      agents: new Map(agents.map((agent) => [agent.id, agent])),
      tasks: new Map(tasks.map((task) => [task.id, task])),
      people: new Map(team.members.map((person) => [person.id, person])),
      pending,
      threads,
    };
  }, [runs, agents, tasks, team.members, messages, threadId]);

  useLayoutEffect(() => {
    const node = scroll.current;
    if (!node) return;
    const conversationRuns = runs.filter(
      (run) => run.threadRootId === threadId,
    );
    const signature = `${messages.at(-1)?.id ?? ''}:${messages.at(-1)?.updatedAt ?? ''}:${conversationRuns.map((run) => `${run.id}:${run.updatedAt}:${run.status}`).join(',')}`;
    const changed = signature !== activity.current;
    activity.current = signature;
    const moved = acceptedPosition.current !== history.position;
    if (!initialized.current || moved) {
      if (!messages.length && !conversationRuns.length) return;
      const saved = readStored(`window-anchor:${key}`);
      let restored = false;
      if (saved) {
        try {
          const anchor = JSON.parse(saved) as { id: string; offset: number };
          const target = node.querySelector<HTMLElement>(
            `[data-message-id="${CSS.escape(anchor.id)}"]`,
          );
          if (target && Number.isFinite(anchor.offset)) {
            node.scrollTop +=
              target.getBoundingClientRect().top -
              node.getBoundingClientRect().top -
              anchor.offset;
            restored = true;
          }
        } catch {
          /* Ignore an obsolete reading position. */
        }
      }
      if (!restored)
        node.scrollTop =
          moved && initialized.current && history.position.includes('&after=')
            ? 0
            : node.scrollHeight;
      acceptedPosition.current = history.position;
      scrollIntent.current = false;
      follow.current =
        !history.anchored &&
        node.scrollHeight - node.clientHeight - node.scrollTop < 80;
      initialized.current = true;
      setNewActivity(false);
    } else if (follow.current && changed) node.scrollTop = node.scrollHeight;
    else if (changed) setNewActivity(true);
  }, [messages, runs, threadId, key, history.position, history.anchored]);

  useLayoutEffect(() => {
    if (!sourceId || focusedSource.current === sourceRequest) return;
    const node = scroll.current?.querySelector<HTMLElement>(
      `[data-message-id="${CSS.escape(sourceId)}"]`,
    );
    if (node) {
      focusedSource.current = sourceRequest;
      node.scrollIntoView({ block: 'center' });
      node.focus({ preventScroll: true });
    }
  }, [sourceId, sourceRequest, messages]);

  function bottom() {
    if (scroll.current) scroll.current.scrollTop = scroll.current.scrollHeight;
    follow.current = true;
    setNewActivity(false);
    history.latest();
  }

  return (
    <section
      className="workspace-conversation"
      aria-label={threadId ? 'Thread conversation' : 'Room conversation'}
    >
      {threadId && (
        <div className="workspace-thread-heading">
          <strong>Thread</strong>
          <Button onClick={() => onThread('')}>Back to Room</Button>
        </div>
      )}
      {(history.hasOlder || history.hasNewer || history.anchored) && (
        <nav
          className="workspace-history-pages app-actions"
          aria-label="Message history"
        >
          {history.hasOlder && (
            <Button disabled={history.loading} onClick={history.earlier}>
              Earlier
            </Button>
          )}
          {history.hasNewer && (
            <Button disabled={history.loading} onClick={history.later}>
              Later
            </Button>
          )}
          {history.anchored && <Button onClick={bottom}>Latest</Button>}
          {history.loading && <span role="status">Loading…</span>}
        </nav>
      )}
      <div
        className="workspace-message-scroll"
        ref={scroll}
        onWheel={() => {
          scrollIntent.current = true;
        }}
        onTouchStart={() => {
          scrollIntent.current = true;
        }}
        onPointerDown={() => {
          scrollIntent.current = true;
        }}
        onKeyDown={(event) => {
          if (
            [
              'PageUp',
              'PageDown',
              'Home',
              'End',
              'ArrowUp',
              'ArrowDown',
            ].includes(event.key)
          )
            scrollIntent.current = true;
        }}
        onScroll={() => {
          const node = scroll.current;
          if (node) {
            const top = node.getBoundingClientRect().top;
            const anchor = [
              ...node.querySelectorAll<HTMLElement>('[data-message-id]'),
            ].find((item) => item.getBoundingClientRect().bottom > top);
            if (anchor)
              writeStored(
                `window-anchor:${key}`,
                JSON.stringify({
                  id: anchor.dataset.messageId,
                  offset: anchor.getBoundingClientRect().top - top,
                }),
              );
            follow.current =
              node.scrollHeight - node.clientHeight - node.scrollTop < 80;
            if (follow.current) setNewActivity(false);
            else if (scrollIntent.current) history.pin();
          }
        }}
      >
        {!messages.length && !threadId && (
          <RoomWelcome
            draftKey={key}
            members={members}
            membersReady={membersReady}
            available={available}
            onSetup={onSetup}
            onAgents={onAgents}
            onPrepared={() => setFocusRequest((value) => value + 1)}
          />
        )}
        {messages.map((message) => {
          const run =
            message.authorKind === 'agent'
              ? lookup.runs.get(message.runId)
              : undefined;
          const agent =
            run?.agentSnapshot ?? lookup.agents.get(message.agentId);
          const currentAgent = lookup.agents.get(message.agentId);
          const relatedTask = lookup.tasks.get(
            message.taskId || run?.taskId || '',
          );
          const linkedTask =
            relatedTask?.roomId === message.roomId ? relatedTask : undefined;
          const linkedItemId =
            message.workOrigin?.workItemId || linkedTask?.workItemId;
          const workOrigin = Boolean(
            message.workOrigin?.workItemId ||
            (linkedTask?.workItemId &&
              linkedTask.originMessageId === message.id),
          );
          const person = lookup.people.get(message.authorUserId);
          const author =
            message.authorKind === 'user'
              ? message.authorName || 'Team member'
              : (agent?.name ?? 'Agent');
          const work = lookup.pending.get(message.id) ?? [];
          const threadWork = lookup.threads.get(message.id);
          const workCards = work.map((item) => (
            <WorkCard
              key={item.id}
              run={item}
              avatar={lookup.agents.get(item.agentId)?.avatar}
              onInspect={onInspect}
              onChange={onChange}
              workItemId={lookup.tasks.get(item.taskId ?? '')?.workItemId}
              showTaskLink={
                (!workOrigin && message.authorKind !== 'system') ||
                Boolean(message.reviewId) ||
                message.taskId !== item.taskId
              }
            />
          ));
          if (message.authorKind === 'system' || workOrigin)
            return (
              <SystemMessage
                key={message.id}
                desktop={desktop}
                message={message}
                resources={resources}
                highlighted={sourceId === message.id}
                threadLabel={
                  threadWork
                    ? `Open thread · ${statusLabel[threadWork.status]}`
                    : 'Reply in thread'
                }
                onReview={onReview}
                onTask={(id) =>
                  void team.navigate({
                    app: 'workspace',
                    roomId: room.id,
                    taskId: id,
                    workItemId:
                      linkedItemId || lookup.tasks.get(id)?.workItemId,
                  })
                }
                taskLabel={linkedItemId ? 'Open work' : 'Open collaboration'}
                workOrigin={workOrigin}
                onInspect={onInspect}
                onAsk={() => onAsk(message)}
                onFollowUp={() => onFollowUp(message)}
                onRemember={() => onRemember(message)}
                onThread={!threadId ? () => onThread(message.id) : undefined}
                onSource={onSource}
              >
                {workCards}
              </SystemMessage>
            );
          return (
            <article
              className={`workspace-message author-${message.authorKind} ${sourceId === message.id ? 'is-source' : ''}`}
              key={message.id}
              data-message-id={message.id}
              aria-label={
                message.authorKind === 'agent' ? `${author} (Agent)` : author
              }
              tabIndex={-1}
            >
              <div className="workspace-message-avatar">
                <IdentityAvatar
                  avatar={
                    message.authorKind === 'agent'
                      ? (currentAgent?.avatar ?? agent?.avatar)
                      : person?.avatar
                  }
                  kind={message.authorKind === 'agent' ? 'agent' : 'member'}
                />
              </div>
              <div className="workspace-message-copy">
                <header>
                  <strong>{author}</strong>
                  <time
                    dateTime={message.createdAt}
                    title={new Date(message.createdAt).toLocaleString('en-US')}
                  >
                    {timeLabel(message.createdAt)}
                  </time>
                  {run && (
                    <span
                      className={`workspace-result-status state-${run.status}`}
                    >
                      {statusLabel[run.status]}
                    </span>
                  )}
                </header>
                {message.authorKind === 'agent' ? (
                  <AgentResult
                    message={message}
                    run={run}
                    onInspect={onInspect}
                    onReference={onReference}
                  />
                ) : (
                  <p className="workspace-message-text">{message.content}</p>
                )}
                <References
                  references={message.references ?? []}
                  resources={resources}
                  onSource={onSource}
                />
                <div className="workspace-message-actions">
                  {(message.taskId || run?.taskId) && (
                    <Button
                      onClick={() =>
                        void team.navigate({
                          app: 'workspace',
                          roomId: room.id,
                          taskId: message.taskId || run?.taskId,
                          workItemId: linkedItemId,
                        })
                      }
                    >
                      {linkedItemId ? 'Open work' : 'Open collaboration'}
                    </Button>
                  )}
                  {message.reviewId && (
                    <Button onClick={() => onReview(message.reviewId!)}>
                      Open Review
                    </Button>
                  )}
                  <button
                    className="workspace-text-button"
                    onClick={() => onAsk(message, run)}
                  >
                    Ask agent…
                  </button>
                  <button
                    className="workspace-text-button"
                    onClick={() => onFollowUp(message)}
                  >
                    Follow up…
                  </button>
                  <button
                    className="workspace-text-button"
                    onClick={() => onRemember(message)}
                  >
                    Remember this…
                  </button>
                  {!threadId && (
                    <button
                      className="workspace-text-button"
                      onClick={() => onThread(message.id)}
                    >
                      {threadWork
                        ? `Open thread · ${statusLabel[threadWork.status]}`
                        : 'Reply in thread'}
                    </button>
                  )}
                </div>
                {workCards}
              </div>
            </article>
          );
        })}
      </div>
      {newActivity && (
        <Button className="workspace-new-activity" onClick={bottom}>
          New activity ↓
        </Button>
      )}
      <Composer
        key={key}
        room={room}
        members={members}
        membersReady={membersReady}
        threadId={threadId}
        onSent={() => {
          follow.current = true;
          history.latest();
          onChange();
        }}
        available={available}
        resources={resources}
        onSource={onSource}
        focusRequest={focusRequest}
      />
    </section>
  );
}
