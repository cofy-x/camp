import {
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import type { Agent, Message, Reference, Resource, Room, Run } from './api';
import { isActiveRun, statusLabel, workspaceApi } from './api';
import { AgentResult } from './AgentResult';
import { CitedReply } from './CitedReply';
import { Composer } from './Composer';
import { References } from './References';
import { WorkActivity } from './WorkActivity';
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
}: {
  run: Run;
  avatar?: string;
  onInspect: (id: string) => void;
  onChange: () => void;
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
        <span>{statusLabel[run.status]}</span>
        <Button onClick={() => onInspect(run.id)}>
          {isActiveRun(run) ? 'Activity' : 'Details'}
        </Button>
        {run.taskId && (
          <Button
            onClick={() =>
              void team.navigate({
                app: 'workspace',
                roomId: run.roomId,
                taskId: run.taskId,
              })
            }
          >
            Open collaboration
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

export function RoomTimeline({
  room,
  threadId,
  messages,
  runs,
  agents,
  members,
  available,
  onThread,
  onInspect,
  onReview,
  onChange,
  resources,
  sourceId,
  sourceRequest,
  onAsk,
  onSource,
  onReference,
  onAgents,
  onSetup,
}: {
  room: Room;
  threadId: string;
  messages: Message[];
  resources: Resource[];
  sourceId: string;
  sourceRequest: number;
  onAsk: (message: Message, run?: Run) => void;
  onSource: (messageId: string) => void;
  onReference: (reference: Reference) => void;
  onAgents: () => void;
  onSetup?: () => void;
  runs: Run[];
  agents: Agent[];
  members: Agent[];
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
  const [newActivity, setNewActivity] = useState(false);
  const key = conversationKey(room.id, threadId);
  const [focusRequest, setFocusRequest] = useState(0);
  const draft = useSyncExternalStore(
    useCallback((listener) => subscribeStored(`draft:${key}`, listener), [key]),
    () => readStored(`draft:${key}`),
    () => '',
  );
  const hasMember = members.some((agent) => agent.enabled);
  const conversationRuns = runs.filter((run) => run.threadRootId === threadId);
  useLayoutEffect(() => {
    const node = scroll.current;
    if (!node) return;
    const conversationRuns = runs.filter(
      (run) => run.threadRootId === threadId,
    );
    const signature = `${messages.at(-1)?.id ?? ''}:${messages.at(-1)?.updatedAt ?? ''}:${conversationRuns.map((run) => `${run.id}:${run.updatedAt}:${run.status}`).join(',')}`;
    const changed = signature !== activity.current;
    activity.current = signature;
    const saved = readStored(`scroll:${key}`);
    const position = Number(saved);
    if (!initialized.current) {
      if (!messages.length && !conversationRuns.length) return;
      node.scrollTop = saved ? position : node.scrollHeight;
      follow.current =
        node.scrollHeight - node.clientHeight - node.scrollTop < 80;
      initialized.current = true;
    } else if (follow.current && changed) node.scrollTop = node.scrollHeight;
    else if (changed) setNewActivity(true);
  }, [messages, runs, threadId, key]);

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
      <div
        className="workspace-message-scroll"
        ref={scroll}
        onScroll={() => {
          const node = scroll.current;
          if (node) {
            writeStored(`scroll:${key}`, String(node.scrollTop));
            follow.current =
              node.scrollHeight - node.clientHeight - node.scrollTop < 80;
            if (follow.current) setNewActivity(false);
          }
        }}
      >
        {!messages.length && !threadId && (
          <div className="workspace-room-welcome">
            <PixelIcon name="conversation" size={28} />
            <h2>What would you like to work on?</h2>
            {!available && onSetup && (
              <Button primary onClick={onSetup}>
                Set Up Agent Work…
              </Button>
            )}
            {hasMember ? (
              <>
                <div
                  className="workspace-starters"
                  aria-label="Starting points"
                >
                  {starters.map((starter) => (
                    <Button
                      key={starter.title}
                      aria-label={starter.title}
                      aria-description={starter.description}
                      disabled={Boolean(draft.trim())}
                      onClick={() => {
                        if (readStored(`draft:${key}`).trim()) return;
                        writeStored(`draft:${key}`, starter.prompt);
                        const agent = members.find((member) => member.enabled);
                        if (agent)
                          writeStored(
                            `recipients:${key}`,
                            JSON.stringify([agent.id]),
                          );
                        setFocusRequest((value) => value + 1);
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
            )}
          </div>
        )}
        {messages.map((message) => {
          const run =
            message.authorKind === 'agent'
              ? runs.find((item) => item.id === message.runId)
              : undefined;
          const agent =
            run?.agentSnapshot ??
            agents.find((item) => item.id === message.agentId);
          const currentAgent = agents.find(
            (item) => item.id === message.agentId,
          );
          const person = team.members.find(
            (item) => item.id === message.authorUserId,
          );
          const author =
            message.authorKind === 'user'
              ? message.authorName || 'Team member'
              : message.authorKind === 'system'
                ? 'Camp'
                : (agent?.name ?? 'Agent');
          const work = conversationRuns.filter(
            (item) =>
              item.originMessageId === message.id &&
              !messages.some(
                (reply) =>
                  reply.runId === item.id && reply.authorKind === 'agent',
              ),
          );
          const threadRuns = runs.filter(
            (item) => item.threadRootId === message.id,
          );
          const threadWork =
            threadRuns.find(isActiveRun) ??
            [...threadRuns].sort((a, b) =>
              b.updatedAt.localeCompare(a.updatedAt),
            )[0];
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
                {message.authorKind === 'system' ? (
                  <PixelIcon name="info" size={32} />
                ) : (
                  <IdentityAvatar
                    avatar={
                      message.authorKind === 'agent'
                        ? (currentAgent?.avatar ?? agent?.avatar)
                        : person?.avatar
                    }
                    kind={message.authorKind === 'agent' ? 'agent' : 'member'}
                  />
                )}
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
                {run && !isActiveRun(run) ? (
                  <AgentResult
                    message={message}
                    run={run}
                    onInspect={onInspect}
                    onReference={onReference}
                  />
                ) : message.authorKind === 'agent' ? (
                  <CitedReply
                    content={message.content}
                    references={run?.references ?? []}
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
                        })
                      }
                    >
                      Open collaboration
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
                {work.map((item) => (
                  <WorkCard
                    key={item.id}
                    run={item}
                    avatar={
                      agents.find((agent) => agent.id === item.agentId)?.avatar
                    }
                    onInspect={onInspect}
                    onChange={onChange}
                  />
                ))}
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
        threadId={threadId}
        onSent={() => {
          follow.current = true;
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
