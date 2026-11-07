import { useEffect, useRef, useState } from 'react';
import type { AppWindowProps } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { APIError } from '../../shared/api/http';
import {
  attentionApi,
  type AttentionItem,
  type AttentionPage,
} from '../attention/api';
import {
  workspaceApi,
  type Agent,
  type Message,
  type Room,
  type Run,
  type RunTimeline,
  type WorkspaceStatus,
} from '../workspace/api';
import { findRunReply } from '../workspace/runMessages';
import { tasksApi, type Task } from '../collaboration/api';
import {
  automationApi,
  type AutomationPolicy,
  type SourceEvent,
} from '../automation/api';
import {
  workItemsApi,
  type WorkItem,
  type WorkListItem,
  type WorkListPage,
  type WorkListQuery,
} from './api';

type Selection =
  | { kind: 'list' }
  | {
      kind: 'create';
      roomId: string;
      sourceMessage?: Message;
      sourceEvent?: SourceEvent;
    }
  | { kind: 'item'; id: string; roomId: string; request?: number };

export type WorkView = 'mine' | 'needs_me' | 'completed' | 'all';

interface Inspection {
  taskId: string;
  runId: string;
  fromItem?: boolean;
}

const views: Record<WorkView, Pick<WorkListQuery, 'scope' | 'view'>> = {
  mine: { scope: 'mine', view: 'active' },
  needs_me: { scope: 'mine', view: 'needs_you' },
  completed: { scope: 'mine', view: 'done' },
  all: { scope: 'team', view: 'all' },
};

// The mounted Work window owns selection, context, paging and draft-safe moves.
export function useWorkController({ desktop, window }: AppWindowProps) {
  const team = useTeam();
  const [view, setView] = useState<WorkView>(
    window.params.view === 'needs_me'
      ? 'needs_me'
      : window.params.roomId
        ? 'all'
        : 'mine',
  );
  const [roomFilter, setRoomFilter] = useState(window.params.roomId ?? '');
  const [selection, setSelection] = useState<Selection>(
    window.params.workItemId
      ? { kind: 'item', id: window.params.workItemId, roomId: '' }
      : { kind: 'list' },
  );
  const [inspection, setInspection] = useState<Inspection>({
    taskId: '',
    runId: '',
  });
  const [taskSnapshot, setTaskSnapshot] =
    useState<Pick<Task, 'id' | 'roomId' | 'workItemId'>>();
  const [taskError, setTaskError] = useState({ id: '', message: '' });
  const [rooms, setRooms] = useState<Room[]>([]);
  const [roomsError, setRoomsError] = useState('');
  const [availability, setAvailability] =
    useState<Pick<WorkspaceStatus, 'available' | 'reason'>>();
  const [cursor, setCursor] = useState('');
  const [previous, setPrevious] = useState<string[]>([]);
  const [pageSnapshot, setPageSnapshot] = useState<{
    key: string;
    page: WorkListPage;
  }>();
  const [attentionSnapshot, setAttentionSnapshot] = useState<{
    key: string;
    page: AttentionPage;
  }>();
  const [listError, setListError] = useState({ key: '', message: '' });
  const [itemError, setItemError] = useState({ id: '', message: '' });
  const [contextSnapshot, setContextSnapshot] = useState<{
    roomId: string;
    members: Agent[];
    policies: AutomationPolicy[];
  }>();
  const [contextError, setContextError] = useState({ roomId: '', message: '' });
  const [contextAttempt, setContextAttempt] = useState(0);
  const [actionError, setActionError] = useState('');
  const [retry, setRetry] = useState(0);
  const request = useRef(0);
  const appliedNavigation = useRef(0);
  const task =
    taskSnapshot?.id === inspection.taskId ? taskSnapshot : undefined;
  const activeRoomId =
    selection.kind === 'list' ? (task?.roomId ?? '') : selection.roomId;
  const taskMismatch =
    task &&
    selection.kind === 'item' &&
    Boolean(
      (selection.roomId && task.roomId !== selection.roomId) ||
      (task.workItemId && task.workItemId !== selection.id),
    );
  const inspectionReady = Boolean(task && activeRoomId && !taskMismatch);
  const context =
    contextSnapshot?.roomId === activeRoomId ? contextSnapshot : undefined;
  const room = rooms.find((item) => item.id === activeRoomId);
  const query = { ...views[view], roomId: roomFilter, cursor };
  const queryKey = JSON.stringify(query);
  const page = pageSnapshot?.key === queryKey ? pageSnapshot.page : undefined;
  const sharedAttention = view === 'needs_me' && !roomFilter && !cursor;
  const attentionPage =
    view !== 'needs_me'
      ? undefined
      : sharedAttention
        ? team.attention
        : attentionSnapshot?.key === queryKey
          ? attentionSnapshot.page
          : undefined;
  const listing = view === 'needs_me' ? attentionPage : page;
  const error = sharedAttention
    ? team.attentionError
    : listError.key === queryKey
      ? listError.message
      : '';

  function resetPage() {
    setCursor('');
    setPrevious([]);
  }

  async function move(action: () => void) {
    team.cancelNavigation();
    const teamRequest = team.getNavigationRevision();
    const expected = ++request.current;
    const activation = desktop.getActivationRevision();
    const saved = await flushPrivateDrafts();
    if (
      expected !== request.current ||
      teamRequest !== team.getNavigationRevision() ||
      activation !== desktop.getActivationRevision()
    )
      return;
    if (!saved) {
      setActionError(
        'A private draft could not be saved. Resolve its error before leaving.',
      );
      return;
    }
    setActionError('');
    action();
  }

  useEffect(() => {
    const intent = team.navigation;
    if (!intent || appliedNavigation.current === intent.revision) return;
    appliedNavigation.current = intent.revision;
    request.current++;
    if (intent.app !== 'work') return;
    setActionError('');
    const roomId =
      intent.sourceMessage?.roomId ??
      intent.sourceEvent?.roomId ??
      intent.roomId ??
      '';
    if (intent.view === 'needs_me') {
      setRoomFilter(intent.roomId ?? '');
      setView('needs_me');
      setCursor('');
      setPrevious([]);
    } else if (
      intent.roomId &&
      !intent.workItemId &&
      !intent.taskId &&
      !intent.sourceMessage &&
      !intent.sourceEvent
    ) {
      setRoomFilter(intent.roomId);
      setView('all');
      setCursor('');
      setPrevious([]);
    }
    setInspection({ taskId: intent.taskId ?? '', runId: '' });
    if (intent.sourceMessage || intent.sourceEvent) {
      setSelection({
        kind: 'create',
        roomId,
        sourceMessage: intent.sourceMessage,
        sourceEvent: intent.sourceEvent,
      });
    } else if (intent.workItemId) {
      // Resolve the owning Room from the item rather than trusting a hint.
      setSelection((current) => ({
        kind: 'item',
        id: intent.workItemId!,
        roomId:
          current.kind === 'item' && current.id === intent.workItemId
            ? current.roomId
            : '',
        request: intent.revision,
      }));
    } else setSelection({ kind: 'list' });
  }, [team.navigation]);

  useEffect(() => {
    const controller = new AbortController();
    void workspaceApi
      .rooms(controller.signal)
      .then(({ rooms }) => {
        if (controller.signal.aborted) return;
        setRooms(rooms);
        setRoomsError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setRoomsError(
            error instanceof Error ? error.message : 'Could not load Rooms.',
          );
      });
    void workspaceApi
      .status(controller.signal)
      .then((status) => {
        if (!controller.signal.aborted) setAvailability(status);
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setAvailability({
            available: false,
            reason: 'Could not read Agent availability.',
          });
      });
    return () => controller.abort();
  }, [team.revision, retry]);

  useEffect(() => {
    if (view === 'needs_me') return;
    const controller = new AbortController();
    void workItemsApi
      .work(JSON.parse(queryKey) as WorkListQuery, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        setPageSnapshot({ key: queryKey, page });
        setListError({ key: queryKey, message: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setListError({
            key: queryKey,
            message:
              error instanceof Error ? error.message : 'Could not load work.',
          });
      });
    return () => controller.abort();
  }, [queryKey, view, team.revision, retry]);

  useEffect(() => {
    if (view !== 'needs_me' || sharedAttention) return;
    const controller = new AbortController();
    void attentionApi
      .list({ roomId: roomFilter, cursor }, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        setAttentionSnapshot({ key: queryKey, page });
        setListError({ key: queryKey, message: '' });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && [401, 403, 404].includes(error.status))
          setAttentionSnapshot(undefined);
        setListError({
          key: queryKey,
          message:
            error instanceof Error
              ? error.message
              : 'Could not load what needs your attention.',
        });
      });
    return () => controller.abort();
  }, [
    view,
    sharedAttention,
    roomFilter,
    cursor,
    queryKey,
    team.revision,
    retry,
  ]);

  const unresolvedItem =
    selection.kind === 'item' && !selection.roomId ? selection.id : '';
  useEffect(() => {
    if (!unresolvedItem) return;
    const controller = new AbortController();
    void workItemsApi
      .item(unresolvedItem, controller.signal)
      .then(({ item }) => {
        if (controller.signal.aborted) return;
        setSelection((current) =>
          current.kind === 'item' && current.id === item.id && !current.roomId
            ? { ...current, roomId: item.roomId }
            : current,
        );
        setItemError({ id: unresolvedItem, message: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setItemError({
            id: unresolvedItem,
            message:
              error instanceof Error
                ? error.message
                : 'Could not read this work.',
          });
      });
    return () => controller.abort();
  }, [unresolvedItem, retry]);

  useEffect(() => {
    if (!inspection.taskId) return;
    const controller = new AbortController();
    const id = inspection.taskId;
    void tasksApi
      .get(id, controller.signal)
      .then(({ task }) => {
        if (controller.signal.aborted) return;
        setTaskSnapshot({
          id: task.id,
          roomId: task.roomId,
          workItemId: task.workItemId,
        });
        setTaskError({ id, message: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setTaskError({
            id,
            message:
              error instanceof Error
                ? error.message
                : 'Could not read this collaboration.',
          });
      });
    return () => controller.abort();
  }, [inspection.taskId, retry]);

  useEffect(() => {
    if (!activeRoomId) return;
    const controller = new AbortController();
    void Promise.all([
      workspaceApi.members(activeRoomId, controller.signal),
      automationApi.policies(activeRoomId, controller.signal),
    ])
      .then(([people, rules]) => {
        if (controller.signal.aborted) return;
        setContextSnapshot({
          roomId: activeRoomId,
          members: people.members,
          policies: rules.policies,
        });
        setContextError({ roomId: activeRoomId, message: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setContextError({
            roomId: activeRoomId,
            message:
              error instanceof Error
                ? error.message
                : 'Could not load Room settings.',
          });
      });
    return () => controller.abort();
  }, [activeRoomId, team.revision, retry, contextAttempt]);

  const location =
    room?.name ?? rooms.find((item) => item.id === roomFilter)?.name;
  useEffect(() => {
    desktop.setTitle(
      window.id,
      selection.kind === 'create'
        ? 'New Work'
        : location
          ? `${location} — Work`
          : 'Work',
    );
  }, [desktop, window.id, location, selection.kind]);
  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        team.cancelNavigation();
        request.current++;
        return flushPrivateDrafts();
      }),
    [desktop, window.id, team],
  );
  useEffect(
    () => () => {
      request.current++;
    },
    [],
  );

  function showList() {
    void move(() => {
      setSelection({ kind: 'list' });
      setInspection({ taskId: '', runId: '' });
    });
  }

  function selectItem(item: WorkListItem) {
    void move(() => {
      setSelection({ kind: 'item', id: item.id, roomId: item.roomId });
      setInspection({ taskId: '', runId: '' });
    });
  }

  function created(item: WorkItem, source: string) {
    setSelection((current) =>
      current.kind === 'create' &&
      current.roomId === item.roomId &&
      (current.sourceMessage?.id ?? current.sourceEvent?.id ?? 'manual') ===
        source
        ? { kind: 'item', id: item.id, roomId: item.roomId }
        : current,
    );
    resetPage();
    setRetry((value) => value + 1);
  }

  async function ask(run: Run | RunTimeline) {
    team.cancelNavigation();
    const teamRequest = team.getNavigationRevision();
    const expected = ++request.current;
    const activation = desktop.getActivationRevision();
    const current = () =>
      expected === request.current &&
      teamRequest === team.getNavigationRevision() &&
      activation === desktop.getActivationRevision();
    try {
      const fullRun = (await workspaceApi.run(run.id)).run;
      if (!current()) return;
      const message = await findRunReply(fullRun);
      if (!current()) return;
      if (!message) throw new Error('The Agent reply is not available yet.');
      await team.navigate({
        roomId: message.roomId,
        threadId: message.threadRootId,
        handoff: { message, run: fullRun },
      });
    } catch (error) {
      if (current())
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not open the Agent reply.',
        );
    }
  }

  return {
    team,
    selection,
    inspection,
    inspectionReady,
    rooms,
    roomFilter,
    view,
    roomsError,
    availability,
    activeRoomId,
    room,
    context,
    page,
    attentionPage,
    listing,
    error,
    actionError,
    previous,
    itemError: itemError.id === unresolvedItem ? itemError.message : '',
    contextError:
      contextError.roomId === activeRoomId ? contextError.message : '',
    retryContext: () => setContextAttempt((value) => value + 1),
    taskError: taskMismatch
      ? 'This collaboration belongs to another work item.'
      : unresolvedItem && itemError.id === unresolvedItem && itemError.message
        ? itemError.message
        : taskError.id === inspection.taskId
          ? taskError.message
          : '',
    refresh: () => {
      resetPage();
      setRetry((value) => value + 1);
      team.refresh();
    },
    showList,
    selectItem,
    openAttention: (item: AttentionItem) => void team.navigate(item.target),
    created,
    ask,
    changeView: (next: WorkView) =>
      void move(() => {
        setView(next);
        resetPage();
      }),
    changeRoom: (roomId: string) =>
      void move(() => {
        setRoomFilter(roomId);
        resetPage();
      }),
    newWork: () =>
      void move(() => {
        setSelection({
          kind: 'create',
          roomId: roomFilter || (rooms.length === 1 ? rooms[0].id : ''),
        });
        setInspection({ taskId: '', runId: '' });
      }),
    creationRoom: (roomId: string) =>
      void move(() =>
        setSelection((current) =>
          current.kind === 'create' ? { ...current, roomId } : current,
        ),
      ),
    openTask: (taskId: string) =>
      void move(() => setInspection({ taskId, runId: '' })),
    viewProgress: (taskId: string, runId: string) =>
      void move(() => setInspection({ taskId, runId, fromItem: true })),
    inspectRun: (runId: string) =>
      void move(() => setInspection((current) => ({ ...current, runId }))),
    closeRun: () =>
      void move(() =>
        setInspection((current) =>
          current.fromItem
            ? { taskId: '', runId: '' }
            : { ...current, runId: '' },
        ),
      ),
    closeTask: () => void move(() => setInspection({ taskId: '', runId: '' })),
    previousPage: () =>
      void move(() => {
        setCursor(previous.at(-1) ?? '');
        setPrevious(previous.slice(0, -1));
      }),
    nextPage: () =>
      void move(() => {
        if (!listing?.hasMore) return;
        setPrevious([...previous, cursor]);
        setCursor(listing.nextCursor);
      }),
  };
}
