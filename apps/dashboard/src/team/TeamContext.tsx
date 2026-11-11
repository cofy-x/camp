import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { teamApi, type InboxItem, type User } from './api';
import { useAccount } from './AuthContext';
import { flushPrivateDrafts } from './draftRegistry';
import { useRead, useReadStore } from '../shared/reads/useRead';
import type {
  Message,
  Reference,
  Run,
  WorkspaceEvent,
} from '../features/workspace/api';
import type { SourceEvent } from '../features/automation/api';
import type { AttentionPage } from '../features/attention/api';

export type NavigationTarget =
  | {
      app?: 'workspace';
      roomId: string;
      messageId?: string;
      runId?: string;
      taskId?: string;
      workItemId?: string;
      reviewId?: string;
      threadId?: string;
      reference?: Reference;
      handoff?: { message: Message; run?: Run };
    }
  | {
      app: 'work';
      roomId?: string;
      workItemId?: string;
      sourceMessage?: Message;
      sourceEvent?: SourceEvent;
      taskId?: string;
      view?: 'needs_me';
    }
  | {
      app: 'library';
      resourceId?: string;
      path?: string;
      contextRoomId?: string;
      commit?: string;
    }
  | { app: 'agents'; agentId?: string }
  | { app: 'runners'; runnerId?: string; connect?: boolean }
  | { app: 'settings'; section?: string };

export type NavigationIntent = NavigationTarget & { revision: number };

interface TeamState {
  name: string;
  members: User[];
  inbox: InboxItem[];
  inboxLoading: boolean;
  inboxError: string;
  retryInbox: () => void;
  attention: AttentionPage | undefined;
  attentionError: string;
  reads: Record<string, number>;
  revision: number;
  connection: 'connecting' | 'connected' | 'offline';
  navigation: NavigationIntent | null;
  navigate: (value: NavigationTarget) => Promise<boolean>;
  cancelNavigation: () => void;
  getNavigationRevision: () => number;
  navigationError: string;
  refresh: () => void;
  readRoom: (roomId: string, seq: number) => void;
  readInbox: (id: string) => Promise<void>;
}

const TeamContext = createContext<TeamState | null>(null);

export function useTeam() {
  const state = useContext(TeamContext);
  if (!state) throw new Error('Team state is unavailable.');
  return state;
}

export function TeamProvider({
  children,
  openApp,
  getActivationRevision,
  initialCursor,
}: {
  children: ReactNode;
  openApp: (appId: string) => void;
  getActivationRevision: () => number;
  initialCursor: number;
}) {
  const { user } = useAccount();
  const store = useReadStore();
  const people = useRead<{ members: User[] }>('/team/members');
  const team = useRead<{ team: { name: string } }>('/team');
  const activity = useRead<{ items: InboxItem[] }>('/me/inbox');
  const responsibility = useRead<AttentionPage>('/attention?limit=20');
  const reading = useRead<{ reads: { roomId: string; seq: number }[] }>(
    '/me/room-reads',
  );
  const name = team.data?.team.name ?? 'Camp';
  const members = useMemo(() => people.data?.members ?? [], [people.data]);
  const inbox = useMemo(() => activity.data?.items ?? [], [activity.data]);
  const inboxLoading = activity.loading;
  const inboxError = activity.error;
  const attention = responsibility.data;
  const attentionError = responsibility.error;
  const retryInbox = activity.refresh;
  const [reads, setReads] = useState<Record<string, number>>({});
  const [revision, setRevision] = useState(0);
  const [navigation, setNavigation] = useState<NavigationIntent | null>(null);
  const navigationRequest = useRef(0);
  const [navigationError, setNavigationError] = useState('');
  const [connection, setConnection] =
    useState<TeamState['connection']>('connecting');
  const cursor = useRef(initialCursor);
  const readPending = useRef(new Map<string, number>());
  const refresh = useCallback(() => {
    store.invalidateAll();
    setRevision((value) => value + 1);
  }, [store]);
  const cancelNavigation = useCallback(() => {
    navigationRequest.current++;
  }, []);
  const getNavigationRevision = useCallback(
    () => navigationRequest.current,
    [],
  );
  useEffect(() => {
    const page = reading.data;
    if (!page) return;
    setReads((current) =>
      Object.fromEntries(
        page.reads.map((item) => [
          item.roomId,
          Math.max(item.seq, current[item.roomId] ?? 0),
        ]),
      ),
    );
  }, [reading.data]);
  useEffect(() => {
    const source = new EventSource(
      `/api/workspace/events?after=${cursor.current}`,
    );
    let opened = false;
    let batch: WorkspaceEvent[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    let fallback: ReturnType<typeof setTimeout> | undefined;
    let delay = 5000;
    const recover = () => {
      store.invalidateAll();
      delay = Math.min(delay * 2, 60000);
      fallback = setTimeout(recover, delay);
    };
    source.onopen = () => {
      setConnection('connected');
      clearTimeout(fallback);
      fallback = undefined;
      delay = 5000;
      if (opened) store.invalidateAll();
      opened = true;
    };
    source.onerror = () => {
      setConnection('offline');
      if (!fallback) fallback = setTimeout(recover, delay);
    };
    source.addEventListener('workspace', (message: MessageEvent<string>) => {
      const next = JSON.parse(message.data) as WorkspaceEvent;
      if (next.id <= cursor.current) return;
      cursor.current = next.id;
      if (next.type === 'personal_read.updated' && next.entityId !== user.id)
        return;
      batch.push(next);
      if (!timer)
        timer = setTimeout(() => {
          timer = undefined;
          store.invalidate(batch);
          batch = [];
        }, 180);
    });
    const visible = () => {
      if (document.visibilityState !== 'hidden') store.invalidateAll();
    };
    document.addEventListener('visibilitychange', visible);
    return () => {
      source.close();
      clearTimeout(timer);
      clearTimeout(fallback);
      document.removeEventListener('visibilitychange', visible);
    };
  }, [initialCursor, store, user.id]);
  const readRoom = useCallback((roomId: string, seq: number) => {
    if (seq <= (readPending.current.get(roomId) ?? 0)) return;
    readPending.current.set(roomId, seq);
    void teamApi
      .readRoom(roomId, seq)
      .then(() =>
        setReads((current) => ({
          ...current,
          [roomId]: Math.max(current[roomId] ?? 0, seq),
        })),
      )
      .catch(() => readPending.current.delete(roomId));
  }, []);
  const readInbox = useCallback(
    async (id: string) => {
      await teamApi.readInbox(id);
      await store.refresh('/me/inbox');
    },
    [store],
  );
  const navigate = useCallback(
    async (value: NavigationTarget): Promise<boolean> => {
      const target: NavigationTarget =
        (!value.app || value.app === 'workspace') && value.workItemId
          ? {
              app: 'work',
              roomId: value.roomId,
              workItemId: value.workItemId,
            }
          : value;
      const request = ++navigationRequest.current;
      const activation = getActivationRevision();
      const saved = await flushPrivateDrafts();
      if (
        request !== navigationRequest.current ||
        activation !== getActivationRevision()
      )
        return false;
      if (!saved) {
        setNavigationError(
          'A private draft could not be saved. Resolve its error before switching documents.',
        );
        return false;
      }
      setNavigationError('');
      setNavigation((current) => ({
        ...target,
        revision: (current?.revision ?? 0) + 1,
      }));
      openApp(target.app ?? 'workspace');
      return true;
    },
    [openApp, getActivationRevision],
  );
  const state = useMemo(
    () => ({
      name,
      members,
      inbox,
      inboxLoading,
      inboxError,
      retryInbox,
      attention,
      attentionError,
      reads,
      revision,
      connection,
      navigation,
      navigationError,
      navigate,
      cancelNavigation,
      getNavigationRevision,
      refresh,
      readRoom,
      readInbox,
    }),
    [
      name,
      members,
      inbox,
      inboxLoading,
      inboxError,
      retryInbox,
      attention,
      attentionError,
      reads,
      revision,
      connection,
      navigation,
      navigationError,
      navigate,
      cancelNavigation,
      getNavigationRevision,
      refresh,
      readRoom,
      readInbox,
    ],
  );
  return <TeamContext.Provider value={state}>{children}</TeamContext.Provider>;
}
