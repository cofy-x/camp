import { workStatus, type CurrentWorkDecision, type WorkItem } from './api';

const activityLabels: Record<string, string> = {
  create: 'Work created',
  execute: 'Work started',
  start: 'Work started',
  result: 'Result saved',
  complete: 'Completed',
  completion_pending: 'Waiting for delivery',
  completion: 'Completion settings changed',
  answer: 'Answer received',
  answer_continue: 'Work continued',
  wait: 'Waiting',
  request_judgment: 'Decision needed',
  stop: 'Work stopped',
  cancel: 'Work stopped',
  authorize: 'Allowance updated',
  allowance_released: 'Unused allowance released',
  delegate: 'Follow-up changed',
  evidence: 'Context added',
  attach: 'Context added',
  follow: 'Following conversation',
  unfollow: 'Stopped following',
  action_result: 'Delivery updated',
  superseded: 'New context received',
  ignore: 'Observation recorded',
};

export function workActivityLabel(kind: string, fallback = 'Work updated') {
  return Object.hasOwn(activityLabels, kind) ? activityLabels[kind] : fallback;
}

type WorkQuestionSource = Pick<
  WorkItem,
  | 'status'
  | 'activeTaskId'
  | 'question'
  | 'questionKind'
  | 'execution'
  | 'currentDecision'
>;

type PresentedWork = WorkQuestionSource &
  Pick<WorkItem, 'result' | 'githubDelivery' | 'awaitingDelivery'>;

type WorkSnapshot = Pick<
  WorkItem,
  'revision' | 'currentDecision' | 'execution'
>;

function taskProjection(item: WorkSnapshot) {
  const decision = item.currentDecision;
  return decision?.kind === 'task'
    ? { taskId: decision.taskId, revision: decision.taskRevision }
    : item.execution;
}

export function canReplaceWorkSnapshot(
  current: WorkSnapshot | null,
  next: WorkSnapshot,
) {
  if (!current) return true;
  if (next.revision !== current.revision)
    return next.revision > current.revision;
  const before = taskProjection(current);
  const after = taskProjection(next);
  return !(
    before &&
    after &&
    before.taskId === after.taskId &&
    after.revision < before.revision
  );
}

function currentExecution(item: WorkQuestionSource) {
  return item.activeTaskId &&
    item.execution?.taskId === item.activeTaskId &&
    !['completed', 'cancelled', 'stopping'].includes(item.status)
    ? item.execution
    : undefined;
}

export function workItemExecutionError(item: WorkQuestionSource) {
  const execution = item.execution;
  if (!execution?.error || workItemQuestion(item)) return '';
  if (currentExecution(item)) return execution.error;
  // A final interrupted or cancelled task releases its active link. Its
  // failure still describes unresolved Work until a newer question or decision.
  if (
    !item.activeTaskId &&
    item.status === 'needs_attention' &&
    ['interrupted', 'cancelled'].includes(execution.status)
  )
    return execution.error;
  return '';
}

// The current projection can explicitly report no decision. Only replies that
// predate that field need the historical wire fields as a fallback.
export function workItemQuestion(
  item: WorkQuestionSource,
): CurrentWorkDecision | undefined {
  if (['completed', 'cancelled', 'stopping'].includes(item.status))
    return undefined;
  if (item.currentDecision !== undefined)
    return item.currentDecision ?? undefined;
  const execution = currentExecution(item);
  if (
    execution?.status === 'waiting_for_person' &&
    !execution.activeRunId &&
    execution.question?.text &&
    !execution.question.answer
  ) {
    const { id, text, options } = execution.question;
    return {
      kind: 'task',
      id,
      text,
      options,
      taskId: execution.taskId,
      taskRevision: execution.revision,
    };
  }
  if (item.question)
    return {
      kind: item.questionKind === 'completion' ? 'completion' : 'judgment',
      text: item.question,
    };
}

export function workDeliveryActive(item: Pick<WorkItem, 'githubDelivery'>) {
  const delivery = item.githubDelivery;
  return Boolean(
    delivery &&
    delivery.stage !== 'result' &&
    delivery.status !== 'stopped' &&
    delivery.status !== 'succeeded',
  );
}

export function workItemRecovery(
  item: WorkQuestionSource & Pick<WorkItem, 'workRecovery' | 'githubDelivery'>,
) {
  if (
    item.workRecovery &&
    !['completed', 'cancelled', 'stopping'].includes(item.status) &&
    !workItemQuestion(item) &&
    !workDeliveryActive(item)
  )
    return item.workRecovery;
}

export function workItemStatus(item: PresentedWork, delivery = true) {
  const question = workItemQuestion(item);
  if (question)
    return {
      task: 'Needs your answer',
      judgment: 'Needs your judgment',
      completion: 'Needs confirmation',
    }[question.kind];
  if (delivery && item.githubDelivery?.reconcile)
    return 'Delivery not confirmed';
  if (delivery && item.githubDelivery?.status === 'blocked')
    return 'Delivery needs attention';
  if (item.awaitingDelivery) return 'Awaiting delivery';
  const execution = currentExecution(item);
  const executionStatus =
    execution &&
    {
      queued: 'Queued',
      running: 'Working',
      waiting_for_person:
        item.currentDecision === null
          ? execution.activeRunId
            ? 'Working'
            : 'Waiting'
          : 'Needs your answer',
      needs_attention: 'Needs attention',
      interrupted: 'Interrupted',
      publishing: 'Saving to Room files',
      cancelling: 'Stopping',
    }[execution.status];
  if (executionStatus) return executionStatus;
  if (
    item.status === 'open' &&
    !item.activeTaskId &&
    item.execution?.status === 'completed' &&
    !item.execution.error &&
    item.result?.taskId &&
    item.result.taskId === item.execution.taskId
  )
    return 'Result ready';
  if (item.currentDecision === null && item.status === 'waiting_for_person')
    return 'Waiting';
  return workStatus[item.status] ?? item.status;
}

export function workItemOverviewFallback(
  item: WorkQuestionSource &
    Pick<WorkItem, 'waitCondition' | 'result' | 'decisions'>,
  editable: boolean,
): { label: string; createdAt?: string; note?: string } | null {
  const earlierResult =
    item.result &&
    item.activeTaskId &&
    item.result.taskId !== item.activeTaskId;
  const visibleSummary =
    item.execution?.summary && item.execution.summary !== item.result?.summary;
  const visibleResult =
    !earlierResult && (item.result?.commit || item.result?.summary);
  if (
    item.status === 'stopping' ||
    (editable && item.activeTaskId) ||
    workItemQuestion(item) ||
    workItemExecutionError(item) ||
    visibleSummary ||
    item.waitCondition ||
    visibleResult
  )
    return null;

  if (!['completed', 'cancelled'].includes(item.status))
    return editable ? null : { label: 'No result yet.' };

  const kinds = item.status === 'completed' ? ['complete'] : ['stop', 'cancel'];
  const record = item.decisions
    .filter((decision) => kinds.includes(decision.kind))
    .reduce<WorkItem['decisions'][number] | undefined>(
      (latest, decision) =>
        !latest || decision.seq > latest.seq ? decision : latest,
      undefined,
    );
  if (!record) return { label: 'No result to show.' };
  return {
    label: item.status === 'completed' ? 'Marked complete' : 'Stop recorded',
    createdAt: record.createdAt,
    note:
      record.actorKind === 'user' && record.reasonSource === 'user'
        ? record.reason
        : undefined,
  };
}
