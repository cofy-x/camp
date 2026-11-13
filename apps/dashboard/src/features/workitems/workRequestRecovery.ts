import {
  workItemsApi,
  type WorkItemInput,
  type WorkItemOperation,
  type WorkActionName,
  type WorkActionInput,
} from './api';

export type Pending =
  | {
      kind: 'create';
      roomId: string;
      body: WorkItemInput;
      draftSnapshot?: string;
    }
  | {
      kind: 'action';
      itemId: string;
      action: WorkActionName;
      body: WorkActionInput;
    }
  | { kind: 'decision'; itemId: string; body: WorkItemOperation };

export function isPending(value: unknown): value is Pending {
  if (!value || typeof value !== 'object') return false;
  const pending = value as Partial<Pending>;
  if (!pending.body || typeof pending.body !== 'object') return false;
  const body = pending.body;
  if (typeof body.requestId !== 'string' || !body.requestId) return false;
  if (pending.kind === 'create')
    return (
      typeof pending.roomId === 'string' &&
      (pending.draftSnapshot === undefined ||
        typeof pending.draftSnapshot === 'string') &&
      'goal' in body &&
      typeof body.goal === 'string' &&
      (body.completionCriteria === undefined ||
        typeof body.completionCriteria === 'string') &&
      (body.evidenceIds === undefined ||
        (Array.isArray(body.evidenceIds) &&
          body.evidenceIds.every((id) => typeof id === 'string')))
    );
  if (pending.kind === 'action')
    return (
      typeof pending.itemId === 'string' &&
      typeof pending.action === 'string' &&
      'revision' in body &&
      Number.isSafeInteger(body.revision)
    );
  return (
    pending.kind === 'decision' &&
    typeof pending.itemId === 'string' &&
    'revision' in body &&
    Number.isSafeInteger(body.revision) &&
    'kind' in body &&
    typeof body.kind === 'string' &&
    'reason' in body &&
    typeof body.reason === 'string'
  );
}

export async function submitRetainedWorkRequest(pending: Pending) {
  return pending.kind === 'create'
    ? workItemsApi.create(pending.roomId, pending.body)
    : pending.kind === 'action'
      ? workItemsApi.act(pending.itemId, pending.action, pending.body)
      : workItemsApi.decide(pending.itemId, pending.body);
}

export function confirmedWorkAction(pending: Pending | null): string {
  if (!pending || pending.kind === 'create') return '';
  return pending.kind === 'action' ? pending.action : pending.body.kind;
}
