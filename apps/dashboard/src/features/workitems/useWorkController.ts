import { useEffect, useRef, useState } from 'react';
import type { AppWindowProps } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { useRead } from '../../shared/reads/useRead';
import { type AttentionItem, type AttentionPage } from '../attention/api';
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
import type { tasksApi } from '../collaboration/api';
import { type AutomationPolicy, type SourceEvent } from '../automation/api';
import {
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
      globalDraft?: boolean;
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
  const [cursor, setCursor] = useState('');
  const [previous, setPrevious] = useState<string[]>([]);
  const [actionError, setActionError] = useState('');
  const request = useRef(0);
  const appliedNavigation = useRef(0);
  const roomsRead = useRead<{ rooms: Room[] }>('/rooms');
  const rooms = roomsRead.data?.rooms ?? [];
  const statusRead = useRead<WorkspaceStatus>('/workspace/status', {
    enabled: selection.kind !== 'list' || Boolean(inspection.taskId),
  });
  const availability = statusRead.error
    ? { available: false, reason: statusRead.error }
    : statusRead.data;
  const taskRead = useRead<Awaited<ReturnType<typeof tasksApi.get>>>(
    inspection.taskId
      ? `/tasks/${encodeURIComponent(inspection.taskId)}?view=summary`
      : null,
  );
  const task = taskRead.data?.task;
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
  const membersRead = useRead<{ members: Agent[] }>(
    activeRoomId ? `/rooms/${encodeURIComponent(activeRoomId)}/members` : null,
  );
  const policiesRead = useRead<{ policies: AutomationPolicy[] }>(
    activeRoomId
      ? `/rooms/${encodeURIComponent(activeRoomId)}/automation-policies`
      : null,
  );
  const context =
    membersRead.data && policiesRead.data
      ? {
          roomId: activeRoomId,
          members: membersRead.data.members,
          policies: policiesRead.data.policies,
        }
      : undefined;
  const room = rooms.find((item) => item.id === activeRoomId);
  const query = { ...views[view], roomId: roomFilter, cursor };
  const listVisible = selection.kind === 'list' && !inspection.taskId;
  const listRead = useRead<WorkListPage>(
    `/work-items?${new URLSearchParams({ ...query, limit: '20' })}`,
    { enabled: listVisible && view !== 'needs_me' },
  );
  const page = listRead.data;
  const sharedAttention = view === 'needs_me' && !roomFilter && !cursor;
  const attentionParams = new URLSearchParams({ limit: '20' });
  if (roomFilter) attentionParams.set('roomId', roomFilter);
  if (cursor) attentionParams.set('cursor', cursor);
  const attentionRead = useRead<AttentionPage>(
    `/attention?${attentionParams}`,
    {
      enabled: listVisible && view === 'needs_me' && !sharedAttention,
    },
  );
  const attentionPage =
    view !== 'needs_me'
      ? undefined
      : sharedAttention
        ? team.attention
        : attentionRead.data;
  const listing = view === 'needs_me' ? attentionPage : page;
  const error = sharedAttention
    ? team.attentionError
    : view === 'needs_me'
      ? attentionRead.error
      : listRead.error;

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

  const unresolvedItem =
    selection.kind === 'item' && !selection.roomId ? selection.id : '';
  const itemRead = useRead<{ item: WorkItem }>(
    unresolvedItem ? `/work-items/${encodeURIComponent(unresolvedItem)}` : null,
  );
  useEffect(() => {
    const item = itemRead.data?.item;
    if (!item) return;
    setSelection((current) =>
      current.kind === 'item' && current.id === item.id && !current.roomId
        ? { ...current, roomId: item.roomId }
        : current,
    );
  }, [itemRead.data]);

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
      (current.globalDraft || current.roomId === item.roomId) &&
      (current.sourceMessage?.id ?? current.sourceEvent?.id ?? 'manual') ===
        source
        ? { kind: 'item', id: item.id, roomId: item.roomId }
        : current,
    );
    resetPage();
    team.refresh();
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
    roomsError: roomsRead.error,
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
    itemError: itemRead.error,
    contextError: membersRead.error || policiesRead.error,
    retryContext: () => {
      void membersRead.refresh();
      void policiesRead.refresh();
    },
    taskError: taskMismatch
      ? 'This collaboration belongs to another work item.'
      : itemRead.error || taskRead.error,
    refresh: () => {
      resetPage();
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
        const roomId = roomFilter || (rooms.length === 1 ? rooms[0].id : '');
        setSelection({
          kind: 'create',
          roomId,
          globalDraft: !roomId,
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
