import { useEffect, useRef, useState } from 'react';
import type { DesktopAPI } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { APIError } from '../../shared/api/http';
import {
  receiptKey,
  readSessionReceipt,
  writeSessionReceipt,
  clearSessionReceipt,
} from '../../shared/sessionReceipt';
import { useTeam } from '../../team/TeamContext';
import { useRead } from '../../shared/reads/useRead';
import type { Agent } from '../workspace/api';
import { tasksApi, type Task, type TaskRecoveryRun } from './api';

interface PendingAction {
  inputProtocol?: 1;
  key: string;
  kind: 'input' | 'answer' | 'continue' | 'cancel';
  requestId: string;
  revision: number;
  content: string;
  agentId: string;
  questionId: string;
  addTurns: number;
  addModelRequests: number;
  fromLatest: boolean;
  fromRunId?: string;
  refreshModelSettings?: boolean;
  participantAgentIds?: string[];
}

function isPendingAction(value: unknown): value is PendingAction {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<PendingAction>;
  return (
    (record.inputProtocol === undefined || record.inputProtocol === 1) &&
    ['input', 'answer', 'continue', 'cancel'].includes(record.kind ?? '') &&
    ['key', 'requestId', 'content', 'agentId', 'questionId'].every(
      (key) => typeof record[key as keyof PendingAction] === 'string',
    ) &&
    Number.isInteger(record.revision) &&
    Number.isInteger(record.addTurns) &&
    Number.isInteger(record.addModelRequests) &&
    typeof record.fromLatest === 'boolean' &&
    (record.fromRunId === undefined || typeof record.fromRunId === 'string') &&
    (record.refreshModelSettings === undefined ||
      typeof record.refreshModelSettings === 'boolean') &&
    (record.participantAgentIds === undefined ||
      (Array.isArray(record.participantAgentIds) &&
        record.participantAgentIds.every((id) => typeof id === 'string')))
  );
}

// Owns reads, mutations and recovery for this mounted view.
export function useTaskController(id: string, desktop: DesktopAPI) {
  const team = useTeam();
  const { user } = useAccount();
  const key = receiptKey(user.id, 'task-action', id);
  const [savedRequest] = useState(() =>
    readSessionReceipt(key, isPendingAction),
  );
  const [task, setTask] = useState<Task | null>(null);
  const [recovery, setRecovery] = useState<{
    revision: number;
    run?: TaskRecoveryRun | null;
  }>();
  const [error, setError] = useState(
    savedRequest ? 'A previous action is awaiting confirmation.' : '',
  );
  const taskRead = useRead<Awaited<ReturnType<typeof tasksApi.get>>>(
    `/tasks/${encodeURIComponent(id)}?view=summary`,
  );
  const loadError = taskRead.error;
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState(false);
  const [content, setContent] = useState(savedRequest?.content ?? '');
  const [agentId, setAgentId] = useState(savedRequest?.agentId ?? '');
  const [continueOpen, setContinueOpen] = useState(
    savedRequest?.kind === 'continue',
  );
  const [fromLatest, setFromLatest] = useState(
    savedRequest?.fromLatest ?? false,
  );
  const [fromRunId, setFromRunId] = useState(savedRequest?.fromRunId ?? '');
  const [refreshRoutes, setRefreshRoutes] = useState(
    Boolean(savedRequest?.participantAgentIds),
  );
  const [refreshModelSettings, setRefreshModelSettings] = useState(
    savedRequest?.refreshModelSettings ?? false,
  );
  const [addTurns, setAddTurns] = useState(savedRequest?.addTurns ?? 0);
  const [addModelRequests, setAddModelRequests] = useState(
    savedRequest?.addModelRequests ?? 0,
  );
  const [retryAvailable, setRetryAvailable] = useState(Boolean(savedRequest));
  const request = useRef<PendingAction | null>(savedRequest);
  const [participantIds, setParticipantIds] = useState<string[] | null>(
    savedRequest?.participantAgentIds ?? null,
  );
  const [roomAgents, setRoomAgents] = useState<Agent[] | null>(null);

  const roomId = task?.roomId;
  const activeRunId = task?.activeRunId;
  const membersRead = useRead<{ members: Agent[] }>(
    roomId ? `/rooms/${encodeURIComponent(roomId)}/members` : null,
    { enabled: continueOpen && refreshRoutes && !activeRunId },
  );
  const membersError = membersRead.error;
  useEffect(() => {
    if (continueOpen && refreshRoutes && membersRead.data)
      setRoomAgents(membersRead.data.members);
  }, [continueOpen, refreshRoutes, membersRead.data]);
  const selectedParticipantIds =
    participantIds ?? task?.participants.map(({ agent }) => agent.id) ?? [];
  const nextAgents = refreshRoutes
    ? (roomAgents ?? []).filter(
        (agent) => agent.enabled && selectedParticipantIds.includes(agent.id),
      )
    : (task?.participants.map(({ agent }) => agent) ?? []);
  const nextAgentId = agentId || task?.currentAgentId || '';
  const invalidParticipants =
    refreshRoutes &&
    (Boolean(activeRunId) ||
      !roomAgents ||
      Boolean(membersError) ||
      !selectedParticipantIds.length ||
      selectedParticipantIds.some(
        (id) => !roomAgents.some((agent) => agent.id === id && agent.enabled),
      ) ||
      !nextAgents.some((agent) => agent.id === nextAgentId));
  const participantOptions = [
    ...(roomAgents ?? []).filter((agent) => agent.enabled),
    ...(task?.participants
      .map(({ agent }) => agent)
      .filter(
        (agent) =>
          !roomAgents?.some(
            (current) => current.id === agent.id && current.enabled,
          ),
      ) ?? []),
  ];

  useEffect(() => {
    const result = taskRead.data;
    if (!result) return;
    setTask((current) =>
      !current || result.task.revision >= current.revision
        ? result.task
        : current,
    );
    setRecovery({ revision: result.task.revision, run: result.recoveryRun });
  }, [taskRead.data]);

  async function submit(
    kind: 'input' | 'answer' | 'continue' | 'cancel',
    retry = false,
  ) {
    if (
      !task ||
      pending ||
      (retryAvailable && !retry) ||
      (!retry && kind === 'continue' && invalidParticipants)
    )
      return;
    const actionKey = JSON.stringify({
      kind,
      questionId: kind === 'answer' ? task.question?.id : undefined,
      content: kind === 'cancel' ? undefined : content,
      agentId: kind === 'continue' ? agentId || task.currentAgentId : undefined,
      fromLatest: kind === 'continue' ? fromLatest : undefined,
      fromRunId: kind === 'continue' && fromRunId ? fromRunId : undefined,
      refreshModelSettings:
        kind === 'continue' && refreshModelSettings ? true : undefined,
      participantAgentIds:
        kind === 'continue' && refreshRoutes
          ? selectedParticipantIds
          : undefined,
      addTurns: kind === 'continue' ? addTurns : undefined,
      addModelRequests: kind === 'continue' ? addModelRequests : undefined,
    });
    // Preserve the original request and revision across uncertain responses,
    // including when live updates have already advanced the task.
    if (!retry && request.current?.key !== actionKey)
      request.current = {
        inputProtocol: 1,
        key: actionKey,
        kind,
        requestId: crypto.randomUUID(),
        revision: task.revision,
        content,
        agentId: agentId || task.currentAgentId,
        questionId: task.question?.id || '',
        addTurns,
        addModelRequests,
        fromLatest,
        fromRunId: kind === 'continue' && fromRunId ? fromRunId : undefined,
        refreshModelSettings:
          kind === 'continue' && refreshModelSettings ? true : undefined,
        participantAgentIds: refreshRoutes ? selectedParticipantIds : undefined,
      };
    const saved = request.current;
    if (!saved) return;
    kind = saved.kind;
    const common = {
      inputProtocol: saved.inputProtocol,
      requestId: saved.requestId,
      revision: saved.revision,
    };
    setPending(true);
    setError('');
    setNotice('');
    try {
      writeSessionReceipt(key, saved);
      const result =
        kind === 'answer'
          ? await tasksApi.answer(id, {
              ...common,
              questionId: saved.questionId,
              content: saved.content,
            })
          : kind === 'continue'
            ? await tasksApi.continue(id, {
                ...common,
                agentId: saved.agentId,
                content: saved.content,
                addTurns: saved.addTurns,
                addModelRequests: saved.addModelRequests,
                fromLatest: saved.fromLatest,
                fromRunId: saved.fromRunId,
                refreshModelSettings: saved.refreshModelSettings,
                participantAgentIds: saved.participantAgentIds,
              })
            : kind === 'cancel'
              ? await tasksApi.cancel(id, {
                  requestId: saved.requestId,
                  revision: saved.revision,
                })
              : await tasksApi.input(id, {
                  inputProtocol: saved.inputProtocol,
                  requestId: common.requestId,
                  content: saved.content,
                });
      setTask((current) =>
        !current || result.task.revision >= current.revision
          ? result.task
          : current,
      );
      setContent('');
      setContinueOpen(false);
      setAddTurns(0);
      setAddModelRequests(0);
      setFromLatest(false);
      setFromRunId('');
      setRefreshRoutes(false);
      setRefreshModelSettings(false);
      setParticipantIds(null);
      setRoomAgents(null);
      clearSessionReceipt(key);
      request.current = null;
      setRetryAvailable(false);
      team.refresh();
      setNotice(
        kind === 'input'
          ? 'Input saved for the next turn.'
          : kind === 'answer'
            ? 'Answer saved.'
            : kind === 'cancel'
              ? 'Stop requested.'
              : 'Continuation requested.',
      );
    } catch (error) {
      const uncertain =
        !(error instanceof APIError) ||
        error.status === 0 ||
        error.status >= 500;
      setRetryAvailable(uncertain);
      if (!uncertain) {
        clearSessionReceipt(key);
        request.current = null;
      }
      if (
        error instanceof APIError &&
        ['revision_conflict', 'question_answered', 'task_finished'].includes(
          error.code,
        )
      )
        team.refresh();
      setError(
        error instanceof Error
          ? error.message
          : 'The action could not be saved.',
      );
    } finally {
      setPending(false);
    }
  }

  async function cancel() {
    const answer = await desktop.dialog({
      title: 'Stop collaboration',
      message:
        'Stop queued work and ask the active Runner to clean up. Saved task drafts remain available.',
      buttons: [
        { label: 'Keep Working', value: 'keep', cancel: true },
        { label: 'Stop', value: 'stop', default: true },
      ],
    });
    if (answer?.button === 'stop') await submit('cancel');
  }

  async function retry() {
    if (request.current) await submit(request.current.kind, true);
  }

  return {
    team,
    user,
    task,
    error,
    loadError,
    notice,
    pending,
    content,
    setContent,
    setAgentId,
    continueOpen,
    setContinueOpen,
    fromLatest,
    setFromLatest,
    fromRunId,
    setFromRunId,
    recoveryRun:
      recovery?.revision === task?.revision ? recovery?.run : undefined,
    recoveryLoaded: recovery?.revision === task?.revision,
    refreshRoutes,
    setRefreshRoutes,
    refreshModelSettings,
    setRefreshModelSettings,
    addTurns,
    setAddTurns,
    addModelRequests,
    setAddModelRequests,
    retryAvailable,
    setParticipantIds,
    roomAgents,
    setRoomAgents,
    membersError,
    retryMembers: () => void membersRead.refresh(),
    selectedParticipantIds,
    nextAgents,
    nextAgentId,
    invalidParticipants,
    participantOptions,
    submit,
    retry,
    cancel,
  };
}
