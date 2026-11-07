import { useEffect, useRef, useState } from 'react';
import { workspaceApi } from './api';
import { useTeam } from '../../team/TeamContext';
import type {
  Agent,
  Message,
  Resource,
  Room,
  RunTimeline,
  WorkspaceStatus,
} from './api';
import { mergeMessages, readStored, writeStored } from './conversationState';

export function useWorkspace() {
  const [rooms, setRooms] = useState<Room[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [status, setStatus] = useState<WorkspaceStatus | null>(null);
  const [roomId, setRoomId] = useState(() => readStored('room'));
  const [threadId, setThreadId] = useState(() => readStored('thread'));
  const [members, setMembers] = useState<Agent[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [runs, setRuns] = useState<RunTimeline[]>([]);
  const [workspaceError, setWorkspaceError] = useState('');
  const [conversationError, setConversationError] = useState('');
  const [membersError, setMembersError] = useState('');
  const [runsError, setRunsError] = useState('');
  const [loadedMembers, setLoadedMembers] = useState('');
  const { connection, revision, refresh } = useTeam();
  const [loading, setLoading] = useState(true);
  const [loadedConversation, setLoadedConversation] = useState('');
  const [failedConversation, setFailedConversation] = useState('');
  const [completeConversation, setCompleteConversation] = useState({
    key: '',
    through: 0,
  });
  const roomFrontiers = useRef(new Map<string, number>());
  const history = useRef(new Map<string, Message[]>());
  const selectionGeneration = useRef(0);
  const pageActive = useRef(true);
  const [readGeneration, setReadGeneration] = useState(0);
  useEffect(() => {
    const hide = () => {
      pageActive.current = false;
    };
    const show = (event: PageTransitionEvent) => {
      pageActive.current = true;
      if (event.persisted) setReadGeneration((value) => value + 1);
    };
    window.addEventListener('pagehide', hide);
    window.addEventListener('pageshow', show);
    return () => {
      window.removeEventListener('pagehide', hide);
      window.removeEventListener('pageshow', show);
    };
  }, []);
  useEffect(() => {
    if (!pageActive.current) return;
    const controller = new AbortController();
    const abort = () => controller.abort();
    window.addEventListener('pagehide', abort);
    const requestedSelection = selectionGeneration.current;
    void Promise.all([
      workspaceApi.rooms(controller.signal),
      workspaceApi.agents(controller.signal),
      workspaceApi.resources(controller.signal),
      workspaceApi.status(controller.signal),
    ])
      .then(([roomList, agentList, resourceList, availability]) => {
        if (controller.signal.aborted) return;
        setRooms(roomList.rooms ?? []);
        for (const room of roomList.rooms ?? [])
          roomFrontiers.current.set(room.id, room.lastMessageSeq);
        setAgents(agentList.agents ?? []);
        setResources(resourceList.resources ?? []);
        setStatus(availability);
        // A refresh may have read its Room list before a new Room was created.
        // Preserve any explicit navigation made while that refresh was pending.
        if (requestedSelection === selectionGeneration.current) {
          if (!roomList.rooms.some((room) => room.id === readStored('room'))) {
            writeStored('room', roomList.rooms[0]?.id ?? '');
            writeStored('thread', '');
            setThreadId('');
          }
          setRoomId((current) =>
            roomList.rooms.some((room) => room.id === current)
              ? current
              : (roomList.rooms[0]?.id ?? ''),
          );
        }
        setWorkspaceError('');
      })
      .catch((error) => {
        if (!controller.signal.aborted)
          setWorkspaceError(
            error instanceof Error
              ? error.message
              : 'Could not load the workspace.',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => {
      window.removeEventListener('pagehide', abort);
      abort();
    };
  }, [revision, readGeneration]);
  useEffect(() => {
    if (!roomId || !pageActive.current) return;
    const controller = new AbortController();
    const abort = () => controller.abort();
    window.addEventListener('pagehide', abort);

    async function load() {
      const historyKey = `${roomId}:${threadId}`;
      const requestedThrough = roomFrontiers.current.get(roomId) ?? 0;
      try {
        let loaded = history.current.get(historyKey) ?? [];
        let after = loaded.at(-1)?.seq ?? 0;
        for (;;) {
          if (controller.signal.aborted) return;
          const page = await workspaceApi.messages(
            roomId,
            threadId,
            after,
            controller.signal,
          );
          if (controller.signal.aborted) return;
          if (page.messages?.length)
            loaded = mergeMessages(loaded, page.messages);
          // Publish each accepted page without waiting for older execution
          // metadata or the rest of a long conversation. Retry resumes here.
          history.current.set(historyKey, loaded);
          setMessages(loaded);
          setLoadedConversation(historyKey);
          setFailedConversation('');
          setConversationError('');
          const next = page.messages?.at(-1)?.seq ?? after;
          if (page.messages.length < 500 || next <= after) break;
          after = next;
        }
        if (controller.signal.aborted) return;
        setCompleteConversation({
          key: historyKey,
          through: Math.max(requestedThrough, loaded.at(-1)?.seq ?? 0),
        });
        setFailedConversation('');
        setConversationError('');
      } catch (error) {
        if (!controller.signal.aborted) {
          setFailedConversation(historyKey);
          setConversationError(
            error instanceof Error
              ? error.message
              : 'Could not load this conversation.',
          );
        }
      }
    }

    void load();
    return () => {
      window.removeEventListener('pagehide', abort);
      abort();
    };
  }, [roomId, threadId, revision, readGeneration]);

  // Room metadata is independent of the selected Thread and never gates
  // reading or typing. Each owner retains matching accepted data on failure.
  useEffect(() => {
    if (!roomId || !pageActive.current) return;
    const controller = new AbortController();
    const abort = () => controller.abort();
    window.addEventListener('pagehide', abort);
    void workspaceApi
      .members(roomId, controller.signal)
      .then(({ members }) => {
        if (controller.signal.aborted) return;
        setMembers(members ?? []);
        setLoadedMembers(roomId);
        setMembersError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setMembersError(
            error instanceof Error
              ? error.message
              : 'Could not load Room members.',
          );
      });
    return () => {
      window.removeEventListener('pagehide', abort);
      abort();
    };
  }, [roomId, revision, readGeneration]);

  useEffect(() => {
    if (!roomId || !pageActive.current) return;
    const controller = new AbortController();
    const abort = () => controller.abort();
    window.addEventListener('pagehide', abort);

    async function load() {
      try {
        const items: RunTimeline[] = [];
        const cursors = new Set<string>();
        let cursor = '';
        for (;;) {
          const page = await workspaceApi.runTimeline(
            roomId,
            cursor,
            controller.signal,
          );
          if (controller.signal.aborted) return;
          items.push(...(page.items ?? []));
          if (!page.nextCursor) break;
          if (cursors.has(page.nextCursor))
            throw new Error('Could not finish loading work activity. Retry.');
          cursors.add(page.nextCursor);
          cursor = page.nextCursor;
        }
        setRuns(items);
        setRunsError('');
      } catch (error) {
        if (!controller.signal.aborted)
          setRunsError(
            error instanceof Error
              ? error.message
              : 'Could not load work activity.',
          );
      }
    }

    void load();
    return () => {
      window.removeEventListener('pagehide', abort);
      abort();
    };
  }, [roomId, revision, readGeneration]);

  function selectConversation(nextRoom: string, nextThread = '') {
    if (nextRoom === roomId && nextThread === threadId) return;
    selectionGeneration.current++;
    setRoomId(nextRoom);
    setThreadId(nextThread);
    setMessages([]);
    if (nextRoom !== roomId) {
      setMembers([]);
      setRuns([]);
      setLoadedMembers('');
      setMembersError('');
      setRunsError('');
    }
    setLoadedConversation('');
    setCompleteConversation({ key: '', through: 0 });
    setFailedConversation('');
    setConversationError('');
    writeStored('room', nextRoom);
    writeStored('thread', nextThread);
  }

  function selectRoom(next: string) {
    selectConversation(next);
  }

  function selectThread(next: string) {
    selectConversation(roomId, next);
  }

  const room = rooms.find((item) => item.id === roomId);
  return {
    rooms,
    agents,
    resources,
    status,
    roomId,
    threadId,
    members,
    messages,
    runs,
    room,
    resource: resources.find((item) => item.id === room?.resourceId),
    error: conversationError || membersError || runsError || workspaceError,
    connection,
    loading,
    membersReady: loadedMembers === roomId,
    membersFailed: Boolean(membersError),
    conversationComplete: completeConversation.key === `${roomId}:${threadId}`,
    conversationReadSeq: completeConversation.through,
    conversationReady: loadedConversation === `${roomId}:${threadId}`,
    conversationFailed: failedConversation === `${roomId}:${threadId}`,
    refresh,
    selectConversation,
    selectRoom,
    selectThread,
  };
}
