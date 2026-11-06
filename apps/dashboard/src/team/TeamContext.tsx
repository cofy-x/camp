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
import { APIError } from '../shared/api/http';
import type {
  Message,
  Reference,
  Run,
  WorkspaceEvent,
} from '../features/workspace/api';
import type { SourceEvent } from '../features/automation/api';
import { attentionApi, type AttentionPage } from '../features/attention/api';

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
  attention: AttentionPage | undefined;
  attentionError: string;
  reads: Record<string, number>;
  revision: number;
  connection: 'connecting' | 'connected' | 'offline';
  event: WorkspaceEvent | null;
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
}: {
  children: ReactNode;
  openApp: (appId: string) => void;
  getActivationRevision: () => number;
}) {
  const { user } = useAccount();
  const [name, setName] = useState('Camp');
  const [members, setMembers] = useState<User[]>([]);
  const [inbox, setInbox] = useState<InboxItem[]>([]);
  const [attentionSnapshot, setAttentionSnapshot] = useState<{
    userId: string;
    page: AttentionPage;
  }>();
  const [attentionFailure, setAttentionFailure] = useState({
    userId: '',
    message: '',
  });
  const [reads, setReads] = useState<Record<string, number>>({});
  const [revision, setRevision] = useState(0);
  const [event, setEvent] = useState<WorkspaceEvent | null>(null);
  const [navigation, setNavigation] = useState<NavigationIntent | null>(null);
  const navigationRequest = useRef(0);
  const [navigationError, setNavigationError] = useState('');
  const [connection, setConnection] =
    useState<TeamState['connection']>('connecting');
  const cursor = useRef(0);
  const readPending = useRef(new Map<string, number>());
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const cancelNavigation = useCallback(() => {
    navigationRequest.current++;
  }, []);
  const getNavigationRevision = useCallback(
    () => navigationRequest.current,
    [],
  );
  const attention =
    attentionSnapshot?.userId === user.id ? attentionSnapshot.page : undefined;
  const attentionError =
    attentionFailure.userId === user.id ? attentionFailure.message : '';
  useEffect(() => {
    const controller = new AbortController();
    void attentionApi
      .list({}, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        setAttentionSnapshot({ userId: user.id, page });
        setAttentionFailure({ userId: user.id, message: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          if (
            error instanceof APIError &&
            (error.status === 401 || error.status === 403)
          )
            setAttentionSnapshot(undefined);
          setAttentionFailure({
            userId: user.id,
            message:
              error instanceof Error
                ? error.message
                : 'Could not load what needs your attention.',
          });
        }
      });
    return () => controller.abort();
  }, [revision, user.id]);
  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      teamApi.members(controller.signal),
      teamApi.inbox(controller.signal),
      teamApi.reads(controller.signal),
      teamApi.team(),
    ])
      .then(([people, notifications, reading, team]) => {
        if (controller.signal.aborted) return;
        setName(team.team.name);
        setMembers(people.members);
        setInbox(notifications.items);
        setReads((current) =>
          Object.fromEntries(
            reading.reads.map((item) => [
              item.roomId,
              Math.max(item.seq, current[item.roomId] ?? 0),
            ]),
          ),
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) setConnection('offline');
      });
    return () => controller.abort();
  }, [revision, user.id]);
  useEffect(() => {
    const source = new EventSource(
      `/api/workspace/events?after=${cursor.current}`,
    );
    let timer: ReturnType<typeof setTimeout> | undefined;
    source.onopen = () => {
      setConnection('connected');
      refresh();
    };
    source.onerror = () => {
      setConnection('offline');
      refresh();
    };
    source.addEventListener('workspace', (message: MessageEvent<string>) => {
      try {
        const next = JSON.parse(message.data) as WorkspaceEvent;
        if (!Number.isSafeInteger(next.id) || next.id <= cursor.current) return;
        cursor.current = next.id;
        setEvent(next);
        if (!timer)
          timer = setTimeout(() => {
            timer = undefined;
            refresh();
          }, 180);
      } catch {
        setConnection('offline');
      }
    });
    const fallback = setInterval(refresh, 15000);
    return () => {
      source.close();
      clearTimeout(timer);
      clearInterval(fallback);
    };
  }, [refresh]);
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
      refresh();
    },
    [refresh],
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
      attention,
      attentionError,
      reads,
      revision,
      connection,
      event,
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
      attention,
      attentionError,
      reads,
      revision,
      connection,
      event,
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
