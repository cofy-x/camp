import { workStatus, type WorkItem } from './api';

type WorkQuestionSource = Pick<
  WorkItem,
  'status' | 'activeTaskId' | 'question' | 'questionKind' | 'execution'
>;

type PresentedWork = WorkQuestionSource &
  Pick<WorkItem, 'result' | 'githubDelivery'>;

function currentExecution(item: WorkQuestionSource) {
  return item.activeTaskId &&
    item.execution?.taskId === item.activeTaskId &&
    !['completed', 'cancelled', 'stopping'].includes(item.status)
    ? item.execution
    : undefined;
}

type CurrentQuestion =
  | {
      kind: 'task';
      id: string;
      text: string;
      options: string[];
      taskId: string;
      taskRevision: number;
    }
  | { kind: 'judgment' | 'completion'; text: string };

// Execution can include the latest historical task. Select reply targets from
// the current task and keep earlier questions as evidence.
export function workItemQuestion(
  item: WorkQuestionSource,
): CurrentQuestion | undefined {
  if (['completed', 'cancelled', 'stopping'].includes(item.status))
    return undefined;
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

export function workItemStatus(item: PresentedWork, delivery = true) {
  if (delivery && item.githubDelivery?.reconcile)
    return 'Delivery not confirmed';
  if (delivery && item.githubDelivery?.status === 'blocked')
    return 'Delivery needs attention';
  const execution = currentExecution(item);
  const executionStatus =
    execution &&
    {
      queued: 'Queued',
      running: 'Working',
      waiting_for_person: 'Needs your answer',
      needs_attention: 'Needs attention',
      interrupted: 'Interrupted',
      publishing: 'Saving to Room files',
      cancelling: 'Stopping',
    }[execution.status];
  if (executionStatus) return executionStatus;
  if (
    item.status === 'open' &&
    !item.activeTaskId &&
    !workItemQuestion(item) &&
    item.execution?.status === 'completed' &&
    !item.execution.error &&
    item.result?.taskId &&
    item.result.taskId === item.execution.taskId
  )
    return 'Result ready';
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
    item.execution?.error ||
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
