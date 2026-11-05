import { workStatus, type WorkItem } from './api';

export function workItemStatus(
  item: Pick<
    WorkItem,
    'status' | 'activeTaskId' | 'question' | 'execution' | 'result'
  >,
) {
  const question =
    item.question ||
    (item.execution?.question && !item.execution.question.answer);
  if (
    item.status === 'open' &&
    !item.activeTaskId &&
    !question &&
    item.execution?.status === 'completed' &&
    !item.execution.error &&
    item.result?.taskId &&
    item.result.taskId === item.execution.taskId
  )
    return 'Result ready';
  return workStatus[item.status] ?? item.status;
}

export function workItemOverviewFallback(
  item: Pick<
    WorkItem,
    | 'status'
    | 'activeTaskId'
    | 'question'
    | 'waitCondition'
    | 'execution'
    | 'result'
    | 'decisions'
  >,
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
    item.question ||
    (item.execution?.question && !item.execution.question.answer) ||
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
