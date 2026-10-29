import { useEffect, useRef, useState } from 'react';
import { Button, PixelIcon, Select, StatusBar } from '@cofy-x/camp-platinum';
import type { AppWindowProps } from '@cofy-x/camp-platinum';
import type { Message, Reference, Run } from './api';
import { isActiveRun, statusLabel, workspaceApi } from './api';
import { AgentsPanel } from './AgentsPanel';
import { RoomTimeline } from './RoomTimeline';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import { RunInspector } from './RunInspector';
import { findRunReply } from './runMessages';
import {
  addDraftReferences,
  conversationKey,
  draftReferences,
  selectDraftReference,
  writeStored,
} from './conversationState';
import { useWorkspace } from './useWorkspace';
import { handoffFiles, selectHandoffReferences } from './handoffFiles';
import { useTeam } from '../../team/TeamContext';
import { useAccount } from '../../team/AuthContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { ReviewPanel } from '../reviews/ReviewPanel';
import { ReviewList } from '../reviews/ReviewList';
import { useTasks } from '../collaboration/useTasks';
import { TaskAttention, TaskList } from '../collaboration/TaskList';
import { StartCollaboration } from '../collaboration/StartCollaboration';
import { TaskDetail } from '../collaboration/TaskDetail';

export function Workspace({ window, desktop }: AppWindowProps) {
  const team = useTeam();
  const { user } = useAccount();
  const handledNavigation = useRef(0);
  const [visible, setVisible] = useState(
    () => document.visibilityState === 'visible',
  );
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', update);
    return () => document.removeEventListener('visibilitychange', update);
  }, []);
  const [panel, setPanel] = useState<
    'chat' | 'agents' | 'reviews' | 'tasks' | 'start-task'
  >('chat');
  const [inspect, setInspect] = useState('');
  const [taskId, setTaskId] = useState('');
  const [reviewId, setReviewId] = useState('');
  const [sourceId, setSourceId] = useState('');
  const [sourceRequest, setSourceRequest] = useState(0);
  const [actionError, setActionError] = useState('');
  const selected = useRef('');
  const focused =
    visible && desktop.activeWindowId === window.id && !window.shaded;

  const workspace = useWorkspace();
  const { room, rooms, roomId, threadId, members, agents, runs } = workspace;
  const collaborations = useTasks(roomId);
  useEffect(
    () => desktop.beforeClose(window.id, flushPrivateDrafts),
    [desktop, window.id],
  );
  useEffect(() => {
    selected.current = roomId;
  }, [roomId, threadId]);
  useEffect(() => {
    desktop.setTitle(
      window.id,
      room ? `${room.name} — Workspace` : 'Workspace',
    );
  }, [desktop, window.id, room]);
  useEffect(() => {
    if (
      focused &&
      room &&
      !threadId &&
      panel === 'chat' &&
      workspace.conversationReady
    )
      team.readRoom(room.id, room.lastMessageSeq);
  }, [focused, room, threadId, panel, workspace.conversationReady, team]);
  useEffect(() => {
    const intent = team.navigation;
    if (
      !intent ||
      (intent.app && intent.app !== 'workspace') ||
      handledNavigation.current === intent.revision
    )
      return;
    handledNavigation.current = intent.revision;
    workspace.selectConversation(intent.roomId, intent.threadId);
    setPanel(intent.taskId ? 'tasks' : intent.reviewId ? 'reviews' : 'chat');
    setTaskId(intent.taskId ?? '');
    setReviewId(intent.reviewId ?? '');
    setInspect(intent.taskId ? '' : (intent.runId ?? ''));
    if (intent.reference) {
      try {
        const key = conversationKey(intent.roomId);
        if (
          roomId !== intent.roomId ||
          panel !== 'chat' ||
          !workspace.conversationReady
        )
          writeStored(
            `draft-seed:${key}`,
            JSON.stringify({ references: [intent.reference] }),
          );
        selectDraftReference(key, intent.reference);
      } catch (error) {
        setActionError(
          error instanceof Error ? error.message : 'Could not add this source.',
        );
      }
    }
    if (
      intent.messageId &&
      !intent.reviewId &&
      !intent.taskId &&
      !intent.threadId
    )
      void workspaceApi
        .message(intent.roomId, intent.messageId)
        .then(({ message }) => {
          if (handledNavigation.current !== intent.revision) return;
          workspace.selectConversation(intent.roomId, message.threadRootId);
          setSourceId(message.id);
          setSourceRequest((value) => value + 1);
        })
        .catch((error: unknown) =>
          setActionError(
            error instanceof Error
              ? error.message
              : 'Could not open this message.',
          ),
        );
  }, [team.navigation, workspace, roomId, panel]);

  async function showRoom(id: string) {
    if (!(await flushPrivateDrafts())) return;
    workspace.selectRoom(id);
    setPanel('chat');
    setInspect('');
    setActionError('');
  }

  async function newRoom() {
    const result = await desktop.dialog({
      title: 'New Room',
      message: `Visible to everyone in ${team.name}.`,
      input: { label: 'Room name' },
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Create Room', value: 'create', default: true },
      ],
    });
    if (result?.button !== 'create' || !result.value.trim()) return;
    try {
      const response = await workspaceApi.createRoom(result.value.trim());
      workspace.refresh();
      await showRoom(response.room.id);
    } catch (error) {
      setActionError(
        error instanceof Error ? error.message : 'Could not create this room.',
      );
    }
  }

  async function renameRoom() {
    if (!room) return;
    const result = await desktop.dialog({
      title: 'Rename Room',
      input: { label: 'Room name', value: room.name },
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Save', value: 'save', default: true },
      ],
    });
    if (result?.button !== 'save' || !result.value.trim()) return;
    try {
      await workspaceApi.updateRoom(room.id, { name: result.value.trim() });
      workspace.refresh();
    } catch (error) {
      setActionError(
        error instanceof Error ? error.message : 'Could not rename this room.',
      );
    }
  }

  function openRoomActions(button: HTMLButtonElement) {
    const rect = button.getBoundingClientRect();
    desktop.contextMenu(
      [
        {
          label: 'Rename Room…',
          disabled: !room,
          action: () => void renameRoom(),
        },
        { label: 'New Room…', action: () => void newRoom() },
        { label: 'Refresh', action: workspace.refresh },
      ],
      { x: rect.left, y: rect.bottom },
    );
  }

  function back() {
    setPanel('chat');
    setInspect('');
  }

  function addReference(reference: Reference) {
    if (!room) return;
    try {
      const key = conversationKey(room.id, threadId);
      writeStored(
        `draft-seed:${key}`,
        JSON.stringify({ references: [reference] }),
      );
      selectDraftReference(key, reference);
      setActionError('');
      back();
    } catch (error) {
      setActionError(
        error instanceof Error ? error.message : 'Could not add reference.',
      );
    }
  }

  async function askAgent(message: Message, run?: Run) {
    if (!(await flushPrivateDrafts())) return;
    if (!room) return;
    const thread = message.threadRootId;
    const key = conversationKey(room.id, thread);
    const author =
      message.authorKind === 'user'
        ? message.authorName || 'Team member'
        : message.authorKind === 'system'
          ? 'Camp'
          : (run?.agentSnapshot.name ??
            agents.find((agent) => agent.id === message.agentId)?.name ??
            'Agent');
    const reference: Reference = {
      kind: 'message',
      messageId: message.id,
      title: author === 'You' ? 'Your message' : `${author}'s message`,
      author,
      content: message.content,
      threadRootId: message.threadRootId,
      sourceRunId: run?.id,
      sourceStatus: run?.status,
      published: run?.published,
    };
    const files = handoffFiles(message, run);
    try {
      const { selected, omitted } = selectHandoffReferences(
        draftReferences(key),
        reference,
        files,
      );
      addDraftReferences(key, selected);
      writeStored(
        `draft-seed:${key}`,
        JSON.stringify({
          references: selected,
          handoff: crypto.randomUUID(),
          recipients: 'null',
        }),
      );
      writeStored(
        `reference-note:${key}`,
        omitted
          ? `${omitted} ${omitted === 1 ? 'file was' : 'files were'} not added because a message allows 8 references. Remove a source to choose another file from Files or Details → Files.`
          : '',
      );
      writeStored(`handoff:${key}`, crypto.randomUUID());
      writeStored(`recipients:${key}`, 'null');
      workspace.selectThread(thread);
      setSourceId('');
      setActionError('');
      back();
    } catch (error) {
      setActionError(
        error instanceof Error
          ? error.message
          : 'Could not prepare the request.',
      );
    }
  }

  async function openSource(messageId: string) {
    if (!room) return;
    const selectedRoom = room.id;
    try {
      const { message } = await workspaceApi.message(selectedRoom, messageId);
      if (selected.current !== selectedRoom) return;
      if (threadId !== message.threadRootId)
        workspace.selectThread(message.threadRootId);
      setSourceId(message.id);
      setSourceRequest((current) => current + 1);
      setActionError('');
      back();
    } catch (error) {
      setActionError(
        error instanceof Error
          ? error.message
          : 'Could not open the source message.',
      );
    }
  }

  const active = runs.filter(isActiveRun);
  return (
    <div className="workspace-app">
      <div className="workspace-body">
        <aside className="workspace-sidebar" aria-label="Workspace navigation">
          <div className="workspace-sidebar-heading">
            <strong>Rooms</strong>
            <Button aria-label="New Room" onClick={() => void newRoom()}>
              +
            </Button>
          </div>
          <nav aria-label="Rooms">
            {rooms.map((item) => (
              <button
                key={item.id}
                className={`workspace-nav-entry ${roomId === item.id ? 'selected' : ''}`}
                aria-current={roomId === item.id ? 'true' : undefined}
                onClick={() => void showRoom(item.id)}
              >
                <PixelIcon name="room" size={18} />
                <span>{item.name}</span>
                {item.lastMessageSeq > (team.reads[item.id] ?? 0) &&
                  !(item.id === roomId && focused && !threadId) && (
                    <span
                      className="workspace-unread"
                      aria-label="Unread messages"
                    >
                      ●
                    </span>
                  )}
              </button>
            ))}
          </nav>
          <div className="workspace-sidebar-footer">
            <PixelIcon name="conversation" size={24} />
            <span>{team.name}</span>
          </div>
        </aside>
        <main className="workspace-main">
          <div className="app-toolbar workspace-toolbar">
            <div className="workspace-room-heading">
              <Select
                className="workspace-room-picker"
                aria-label="Room"
                value={roomId}
                disabled={!rooms.length}
                onChange={(event) => void showRoom(event.target.value)}
              >
                {!rooms.length && <option value="">No Rooms</option>}
                {rooms.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                    {item.lastMessageSeq > (team.reads[item.id] ?? 0) &&
                    !(item.id === roomId && focused && !threadId)
                      ? ' · Unread'
                      : ''}
                  </option>
                ))}
              </Select>
              <PixelIcon name="room" size={20} />
              <strong className="workspace-room-title" title={room?.name}>
                {room?.name ?? 'Workspace'}
              </strong>
            </div>
            <div className="workspace-toolbar-actions">
              <Button
                disabled={!room}
                aria-pressed={panel === 'tasks' || panel === 'start-task'}
                onClick={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      setPanel('tasks');
                      setTaskId('');
                      setInspect('');
                    }
                  })
                }
              >
                Collaborations
              </Button>
              <Button
                disabled={!room}
                onClick={() =>
                  void team.navigate({
                    app: 'library',
                    resourceId: room?.resourceId,
                    contextRoomId: room?.id,
                  })
                }
              >
                Library
              </Button>
              <Button
                disabled={!room}
                aria-pressed={panel === 'reviews'}
                onClick={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      setPanel(panel === 'reviews' ? 'chat' : 'reviews');
                      setReviewId('');
                      setInspect('');
                    }
                  })
                }
              >
                Reviews
              </Button>
              <Button
                aria-pressed={panel === 'agents'}
                onClick={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      setPanel(panel === 'agents' ? 'chat' : 'agents');
                      setInspect('');
                    }
                  })
                }
                disabled={!room}
              >
                Agents
              </Button>
              <Button
                className="workspace-room-actions"
                aria-label="Room actions"
                title="Room actions"
                aria-haspopup="menu"
                onClick={(event) => openRoomActions(event.currentTarget)}
                onKeyDown={(event) => {
                  if (event.key !== 'ArrowDown') return;
                  event.preventDefault();
                  openRoomActions(event.currentTarget);
                }}
              >
                ⋯
              </Button>
            </div>
          </div>
          {room && panel === 'chat' && !inspect && (
            <TaskAttention
              tasks={collaborations.tasks}
              onOpen={(id) => {
                setTaskId(id);
                setPanel('tasks');
              }}
              onAll={() => {
                setTaskId('');
                setPanel('tasks');
              }}
            />
          )}
          {room && panel === 'chat' && !inspect && (
            <div className="workspace-members" aria-label="Room members">
              {!workspace.conversationReady && !members.length ? (
                <span>Loading members…</span>
              ) : members.length ? (
                members.map((agent) => {
                  const work = active.find((run) => run.agentId === agent.id);
                  const state =
                    workspace.status?.agentStates?.[agent.id] ?? work?.status;
                  return (
                    <button
                      key={agent.id}
                      onClick={() =>
                        void team.navigate({ app: 'agents', agentId: agent.id })
                      }
                      title={`@${agent.handle} · ${agent.access === 'write' ? 'Can edit files' : 'Read only'}`}
                    >
                      <IdentityAvatar
                        avatar={agent.avatar}
                        kind="agent"
                        size={24}
                      />
                      <strong>{agent.name}</strong>
                      <span>
                        {!agent.enabled
                          ? 'Disabled'
                          : state
                            ? statusLabel[state]
                            : 'Idle'}
                      </span>
                    </button>
                  );
                })
              ) : (
                <span>No Agents in this Room</span>
              )}
            </div>
          )}
          {(workspace.error || actionError) && (
            <div role="alert" className="app-error workspace-feedback">
              {actionError || workspace.error}
              <Button
                onClick={() => {
                  setActionError('');
                  workspace.refresh();
                }}
              >
                Retry
              </Button>
            </div>
          )}
          {panel !== 'reviews' &&
            !inspect &&
            workspace.status &&
            !workspace.status.available && (
              <details className="workspace-unavailable">
                <summary>
                  Agents are unavailable · you can still post to the room
                </summary>
                {user.role === 'admin' && (
                  <Button
                    onClick={() =>
                      void team.navigate({
                        app: 'settings',
                        section: 'readiness',
                      })
                    }
                  >
                    Open Settings
                  </Button>
                )}
                <p>
                  {workspace.status.reason ||
                    'The execution runtime is not configured.'}
                </p>
              </details>
            )}
          {room ? (
            panel === 'reviews' ? (
              reviewId ? (
                <ReviewPanel
                  key={reviewId}
                  id={reviewId}
                  onClose={() => setReviewId('')}
                />
              ) : (
                <ReviewList
                  roomId={room.id}
                  onOpen={(review) => setReviewId(review.id)}
                  onClose={back}
                />
              )
            ) : inspect ? (
              <RunInspector
                key={inspect}
                id={inspect}
                backLabel={
                  panel === 'tasks' ? 'Back to Collaboration' : 'Back to Chat'
                }
                onClose={() => setInspect('')}
                onReference={addReference}
                onSource={(messageId) => void openSource(messageId)}
                onAsk={(run) => {
                  const reply = workspace.messages.find(
                    (message) =>
                      message.runId === run.id &&
                      message.authorKind === 'agent',
                  );
                  if (reply) void askAgent(reply, run);
                  else
                    void findRunReply(run)
                      .then(async (reply) => {
                        if (selected.current === run.roomId && reply)
                          await askAgent(reply, run);
                      })
                      .catch((error: unknown) =>
                        setActionError(
                          error instanceof Error
                            ? error.message
                            : 'Could not load the result message.',
                        ),
                      );
                }}
              />
            ) : panel === 'tasks' ? (
              taskId ? (
                <TaskDetail
                  key={taskId}
                  id={taskId}
                  desktop={desktop}
                  onClose={() => setTaskId('')}
                  onInspect={setInspect}
                />
              ) : (
                <TaskList
                  {...collaborations}
                  onRetry={collaborations.refresh}
                  onOpen={setTaskId}
                  onStart={() => setPanel('start-task')}
                  onClose={back}
                />
              )
            ) : panel === 'start-task' ? (
              <StartCollaboration
                key={room.id}
                room={room}
                members={members}
                threadId={threadId}
                onClose={() => setPanel('tasks')}
                onStarted={(task) => {
                  setTaskId(task.id);
                  setPanel('tasks');
                }}
              />
            ) : panel === 'agents' ? (
              <AgentsPanel
                key={room.id}
                desktop={desktop}
                room={room}
                agents={agents}
                members={members}
                onChange={workspace.refresh}
                onClose={back}
              />
            ) : !workspace.conversationReady ? (
              <div className="workspace-conversation-loading" role="status">
                {workspace.conversationFailed
                  ? 'This conversation could not be loaded. Use Retry above.'
                  : 'Loading conversation…'}
              </div>
            ) : (
              <RoomTimeline
                key={`${roomId}:${threadId}`}
                room={room}
                threadId={threadId}
                messages={workspace.messages}
                runs={runs}
                agents={agents}
                members={members}
                available={workspace.status?.available ?? false}
                resources={workspace.resources}
                sourceId={sourceId}
                sourceRequest={sourceRequest}
                onReview={(id) =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      setReviewId(id);
                      setPanel('reviews');
                    }
                  })
                }
                onAsk={(message, run) => void askAgent(message, run)}
                onSource={(messageId) => void openSource(messageId)}
                onThread={(id) =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) workspace.selectThread(id);
                  })
                }
                onInspect={(id) =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) setInspect(id);
                  })
                }
                onChange={workspace.refresh}
                onReference={addReference}
                onAgents={() => void team.navigate({ app: 'agents' })}
                onSetup={
                  user.role === 'admin'
                    ? () =>
                        void team.navigate({
                          app: 'settings',
                          section: 'readiness',
                        })
                    : undefined
                }
              />
            )
          ) : (
            <div className="workspace-empty">
              <PixelIcon name="room" size={40} />
              <p>
                {workspace.loading
                  ? 'Opening your workspace…'
                  : 'No rooms are available.'}
              </p>
              {!workspace.loading && (
                <Button onClick={() => void newRoom()}>New Room…</Button>
              )}
            </div>
          )}
        </main>
      </div>
      <StatusBar>
        <span>
          {workspace.connection === 'connected'
            ? 'Live'
            : workspace.connection === 'offline'
              ? 'Reconnecting…'
              : 'Connecting…'}
        </span>
        <span>
          {active.length
            ? `${active.length} active ${active.length === 1 ? 'request' : 'requests'}`
            : (workspace.resource?.name ?? 'Camp Workspace')}
        </span>
      </StatusBar>
    </div>
  );
}
