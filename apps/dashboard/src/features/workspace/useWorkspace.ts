import { useEffect, useRef, useState } from 'react';
import { useTeam } from '../../team/TeamContext';
import { useRead, useReadStore } from '../../shared/reads/useRead';
import type {
  Agent,
  Message,
  Resource,
  Room,
  RunTimeline,
  WorkspaceStatus,
} from './api';
import { readStored, writeStored } from './conversationState';

interface MessageWindow {
  messages: Message[];
  through: number;
  hasOlder: boolean;
  hasNewer: boolean;
}

export interface ConversationTask {
  id: string;
  roomId: string;
  originMessageId: string;
  workItemId: string;
  status: string;
}

export function useWorkspace() {
  const [rooms, setRooms] = useState<Room[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [status, setStatus] = useState<WorkspaceStatus | null>(null);
  const [roomId, setRoomId] = useState(() => readStored('room'));
  const [threadId, setThreadId] = useState(() => readStored('thread'));
  const [members, setMembers] = useState<Agent[]>([]);
  const [workspaceError, setWorkspaceError] = useState('');
  const [membersError, setMembersError] = useState('');
  const [loadedMembers, setLoadedMembers] = useState('');
  const { connection, refresh } = useTeam();
  const store = useReadStore();
  const [loading, setLoading] = useState(true);
  const [accepted, setAccepted] = useState<{
    key: string;
    path: string;
    page: MessageWindow;
  }>();
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
  const [positions, setPositions] = useState<Record<string, string>>({});
  const position = positions[historyKey] ?? readStored(`window:${historyKey}`);
  const path = roomId
    ? `/rooms/${encodeURIComponent(roomId)}/messages?${new URLSearchParams({ view: 'window', threadRootId: threadId })}${position ? `&${position}` : ''}`
    : null;
  const messagesRead = useRead<MessageWindow>(path);
  const page = accepted?.key === historyKey ? accepted.page : undefined;
  const messages = page?.messages ?? [];
  const messageIds = messages.map((message) => message.id).join(',');
  const activityRead = useRead<{
    items: RunTimeline[];
    tasks: ConversationTask[];
  }>(
    roomId && messageIds
      ? `/rooms/${encodeURIComponent(roomId)}/runs?${new URLSearchParams({ view: 'conversation', messageIds })}`
      : null,
  );
  const runs = activityRead.data?.items ?? [];

  function selectWindow(value: string, key = historyKey) {
    setPositions((current) => ({ ...current, [key]: value }));
    writeStored(`window:${key}`, value);
  }

  function latest() {
    selectWindow('');
    if (!position) void messagesRead.refresh();
  }

  function pinWindow() {
    const last = messages.filter((message) => message.id !== threadId).at(-1);
    if (!position && last) selectWindow(`before=${last.seq + 1}`);
  }

  function historyPage(direction: 'earlier' | 'later') {
    const rows = messages.filter((message) => message.id !== threadId);
    const edge = direction === 'earlier' ? rows[0] : rows.at(-1);
    if (edge)
      selectWindow(
        `${direction === 'earlier' ? 'before' : 'after'}=${edge.seq}`,
      );
  }

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
    if (path && messagesRead.data)
      setAccepted({ key: historyKey, path, page: messagesRead.data });
  }, [messagesRead.data, historyKey, path]);

  function selectConversation(nextRoom: string, nextThread = '', around = '') {
    if (around)
      selectWindow(
        new URLSearchParams({ around }).toString(),
        `${nextRoom}:${nextThread}`,
      );
    if (nextRoom === roomId && nextThread === threadId) return;
    pendingCatalog.current = rooms.some((room) => room.id === nextRoom)
      ? ''
      : nextRoom;
    setRoomId(nextRoom);
    setThreadId(nextThread);
    if (nextRoom !== roomId) {
      setMembers([]);
      setLoadedMembers('');
      setMembersError('');
    }
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
    error:
      messagesRead.error ||
      membersError ||
      activityRead.error ||
      workspaceError,
    connection,
    loading,
    membersReady: loadedMembers === roomId,
    membersFailed: Boolean(membersError),
    conversationComplete: Boolean(
      page && accepted?.path === path && !page.hasNewer && !messagesRead.error,
    ),
    conversationReadSeq: page?.through ?? 0,
    conversationReady: Boolean(page),
    conversationFailed: Boolean(messagesRead.error),
    conversationTasks: activityRead.data?.tasks ?? [],
    history: {
      hasOlder: page?.hasOlder ?? false,
      hasNewer: page?.hasNewer ?? false,
      loading: messagesRead.loading || messagesRead.refreshing,
      anchored: Boolean(position),
      position: accepted?.path ?? '',
      earlier: () => historyPage('earlier'),
      later: () => historyPage('later'),
      latest,
      pin: pinWindow,
    },
    refresh,
    selectConversation,
    selectRoom,
    selectThread,
  };
}
