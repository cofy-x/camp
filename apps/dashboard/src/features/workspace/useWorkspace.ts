import { useEffect, useRef, useState } from 'react';
import { workspaceApi } from './api';
import { useTeam } from '../../team/TeamContext';
import type {
  Agent,
  Message,
  Resource,
  Room,
  Run,
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
  const [runs, setRuns] = useState<Run[]>([]);
  const [workspaceError, setWorkspaceError] = useState('');
  const [conversationError, setConversationError] = useState('');
  const { connection, revision, refresh } = useTeam();
  const [loading, setLoading] = useState(true);
  const [loadedConversation, setLoadedConversation] = useState('');
  const [failedConversation, setFailedConversation] = useState('');
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
      try {
        const [memberList, runList] = await Promise.all([
          workspaceApi.members(roomId, controller.signal),
          workspaceApi.runs(roomId, controller.signal),
        ]);
        if (controller.signal.aborted) return;
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
          const next = page.messages?.at(-1)?.seq ?? after;
          if (page.messages.length < 500 || next <= after) break;
          after = next;
        }
        if (controller.signal.aborted) return;
        history.current.set(historyKey, loaded);
        setMembers(memberList.members ?? []);
        setRuns(runList.runs ?? []);
        setMessages(loaded);
        setLoadedConversation(historyKey);
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

  function selectConversation(nextRoom: string, nextThread = '') {
    if (nextRoom === roomId && nextThread === threadId) return;
    selectionGeneration.current++;
    setRoomId(nextRoom);
    setThreadId(nextThread);
    setMessages([]);
    if (nextRoom !== roomId) {
      setMembers([]);
      setRuns([]);
    }
    setLoadedConversation('');
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
    error: conversationError || workspaceError,
    connection,
    loading,
    conversationReady: loadedConversation === `${roomId}:${threadId}`,
    conversationFailed: failedConversation === `${roomId}:${threadId}`,
    refresh,
    selectConversation,
    selectRoom,
    selectThread,
  };
}
