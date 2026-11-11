import { useEffect, useRef, useState } from 'react';
import { workspaceApi } from './api';
import { useTeam } from '../../team/TeamContext';
import {
  useRead,
  useReadStore,
  useReadVisibility,
} from '../../shared/reads/useRead';
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
  const { connection, refresh } = useTeam();
  const store = useReadStore();
  const visible = useReadVisibility();
  const [loading, setLoading] = useState(true);
  const [loadedConversation, setLoadedConversation] = useState('');
  const [failedConversation, setFailedConversation] = useState('');
  const [completeConversation, setCompleteConversation] = useState({
    key: '',
    through: 0,
  });
  const history = useRef(new Map<string, Message[]>());
  const pendingCatalog = useRef('');
  const [readGeneration, setReadGeneration] = useState(0);
  useEffect(() => {
    const show = (event: PageTransitionEvent) => {
      if (event.persisted) setReadGeneration((value) => value + 1);
    };
    window.addEventListener('pageshow', show);
    return () => {
      window.removeEventListener('pageshow', show);
    };
  }, []);
  const roomsRead = useRead<{ rooms: Room[] }>('/rooms');
  const agentsRead = useRead<{ agents: Agent[] }>('/agents');
  const resourcesRead = useRead<{ resources: Resource[] }>('/resources');
  const statusRead = useRead<WorkspaceStatus>('/workspace/status');
  const membersRead = useRead<{ members: Agent[] }>(
    roomId ? `/rooms/${encodeURIComponent(roomId)}/members` : null,
  );
  const historyKey = `${roomId}:${threadId}`;
  const [positions, setPositions] = useState<Record<string, number>>({});
  const after =
    positions[historyKey] ?? history.current.get(historyKey)?.at(-1)?.seq ?? 0;
  const messagesRead = useRead<{ messages: Message[]; through: number }>(
    roomId
      ? `/rooms/${encodeURIComponent(roomId)}/messages?${new URLSearchParams({ threadRootId: threadId, after: String(after) })}`
      : null,
  );
  const runPage = useRead<{ items: RunTimeline[]; nextCursor: string }>(
    roomId
      ? `/rooms/${encodeURIComponent(roomId)}/runs?view=timeline&cursor=`
      : null,
  );

  useEffect(() => {
    if (readGeneration) store.invalidateAll();
  }, [readGeneration, store]);
  useEffect(() => {
    const page = roomsRead.data;
    if (!page) return;
    setRooms(page.rooms);
    if (page.rooms.some((room) => room.id === roomId)) {
      pendingCatalog.current = '';
      return;
    }
    if (roomId && pendingCatalog.current === roomId) return;
    const selected = page.rooms[0]?.id ?? '';
    setRoomId(selected);
    setThreadId('');
    writeStored('room', selected);
    writeStored('thread', '');
  }, [roomsRead.data, roomId]);
  useEffect(() => {
    if (agentsRead.data) setAgents(agentsRead.data.agents);
  }, [agentsRead.data]);
  useEffect(() => {
    if (resourcesRead.data) setResources(resourcesRead.data.resources);
  }, [resourcesRead.data]);
  useEffect(() => {
    if (statusRead.data) setStatus(statusRead.data);
  }, [statusRead.data]);
  useEffect(() => {
    setWorkspaceError(
      roomsRead.error ||
        agentsRead.error ||
        resourcesRead.error ||
        statusRead.error,
    );
    setLoading(roomsRead.loading);
  }, [
    roomsRead.error,
    agentsRead.error,
    resourcesRead.error,
    statusRead.error,
    roomsRead.loading,
  ]);
  useEffect(() => {
    const page = membersRead.data;
    if (page && roomId) {
      setMembers(page.members);
      setLoadedMembers(roomId);
    }
    setMembersError(membersRead.error);
  }, [membersRead.data, membersRead.error, roomId]);
  useEffect(() => {
    const page = messagesRead.data;
    if (!roomId || !page) return;
    const loaded = mergeMessages(
      history.current.get(historyKey) ?? [],
      page.messages,
    );
    history.current.set(historyKey, loaded);
    setMessages(loaded);
    setLoadedConversation(historyKey);
    const next = loaded.at(-1)?.seq ?? after;
    if (page.messages.length < 500)
      setCompleteConversation({
        key: historyKey,
        through: Math.max(page.through, next),
      });
    else setCompleteConversation({ key: '', through: 0 });
    if (next > after)
      setPositions((current) => ({ ...current, [historyKey]: next }));
  }, [messagesRead.data, historyKey, roomId, after]);
  useEffect(() => {
    if (messagesRead.error) {
      setFailedConversation(historyKey);
      setConversationError(messagesRead.error);
    } else if (messagesRead.data) {
      setFailedConversation('');
      setConversationError('');
    }
  }, [messagesRead.error, messagesRead.data, historyKey]);
  useEffect(() => {
    const page = runPage.data;
    if (!page || !roomId || !visible) return;
    const controller = new AbortController();
    const items = [...page.items];
    const retainAccepted = () =>
      setRuns((current) => {
        const merged = new Map(items.map((run) => [run.id, run]));
        for (const old of current)
          if (!merged.has(old.id)) merged.set(old.id, old);
        return [...merged.values()];
      });
    retainAccepted();

    async function rest() {
      let cursor = page!.nextCursor;
      const cursors = new Set<string>();
      while (cursor) {
        if (cursors.has(cursor))
          throw new Error('Could not finish loading work activity. Retry.');
        cursors.add(cursor);
        const next = await workspaceApi.runTimeline(
          roomId,
          cursor,
          controller.signal,
        );
        if (controller.signal.aborted) return;
        items.push(...next.items);
        retainAccepted();
        cursor = next.nextCursor;
      }
      setRuns(items);
      setRunsError('');
    }

    void rest().catch((error: unknown) => {
      if (!controller.signal.aborted)
        setRunsError(
          error instanceof Error
            ? error.message
            : 'Could not load work activity.',
        );
    });
    return () => controller.abort();
  }, [runPage.data, roomId, visible]);
  useEffect(() => {
    if (runPage.error) setRunsError(runPage.error);
  }, [runPage.error]);

  function selectConversation(nextRoom: string, nextThread = '') {
    if (nextRoom === roomId && nextThread === threadId) return;
    pendingCatalog.current = rooms.some((room) => room.id === nextRoom)
      ? ''
      : nextRoom;
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
