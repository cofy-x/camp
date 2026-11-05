import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { APIError } from '../../shared/api/http';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import {
  workItemsApi,
  type WorkItem,
  type WorkItemInput,
  type WorkItemOperation,
  type WorkActionName,
  type WorkActionInput,
} from './api';

type Pending =
  | { kind: 'create'; roomId: string; body: WorkItemInput }
  | {
      kind: 'action';
      itemId: string;
      action: WorkActionName;
      body: WorkActionInput;
    }
  | { kind: 'decision'; itemId: string; body: WorkItemOperation };

function isPending(value: unknown): value is Pending {
  if (!value || typeof value !== 'object') return false;
  const pending = value as Partial<Pending>;
  if (!pending.body || typeof pending.body !== 'object') return false;
  const body = pending.body;
  if (typeof body.requestId !== 'string' || !body.requestId) return false;
  if (pending.kind === 'create')
    return (
      typeof pending.roomId === 'string' &&
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

async function submit(pending: Pending) {
  return pending.kind === 'create'
    ? workItemsApi.create(pending.roomId, pending.body)
    : pending.kind === 'action'
      ? workItemsApi.act(pending.itemId, pending.action, pending.body)
      : workItemsApi.decide(pending.itemId, pending.body);
}

// Persist exact intent before dispatch. Lost responses never free the form to
// create a second authorization; only its original receipt or replay resolves it.
export function useWorkItemOperation(
  scope: string,
  onConfirmed: (item: WorkItem) => void,
  recoveryScopes: string[] = [],
) {
  const { user } = useAccount();
  const team = useTeam();
  const key = receiptKey(user.id, 'work-item-operation', scope);
  const keys = [
    key,
    ...recoveryScopes.map((scope) =>
      receiptKey(user.id, 'work-item-operation', scope),
    ),
  ];

  function readRetained() {
    for (const storageKey of keys) {
      const value = readSessionReceipt(storageKey, isPending);
      if (value) return { storageKey, value };
    }
    return null;
  }

  const [recovery, setRecovery] = useState(readRetained);
  const pending = recovery?.value ?? null;
  const retained = useRef(recovery);
  const active = useRef(false);
  const mounted = useRef(true);
  const callback = useRef(onConfirmed);
  callback.current = onConfirmed;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [missing, setMissing] = useState(false);
  const [confirmation, setConfirmation] = useState<Pending | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  function confirmed(item: WorkItem) {
    const completed = retained.current?.value ?? null;
    clearSessionReceipt(retained.current?.storageKey ?? key);
    const next = readRetained();
    retained.current = next;
    if (mounted.current) {
      setRecovery(next);
      setConfirmation(completed);
      setError('');
      setMissing(false);
      callback.current(item);
    }
    team.refresh();
  }

  async function send(value: Pending, retry: boolean) {
    if (active.current || (!retry && retained.current)) return false;
    active.current = true;
    setBusy(true);
    setError('');
    setMissing(false);
    let saved = retry;
    try {
      const storageKey = retry ? (retained.current?.storageKey ?? key) : key;
      writeSessionReceipt(storageKey, value);
      saved = true;
      retained.current = { storageKey, value };
      setRecovery(retained.current);
      const response = await submit(value);
      confirmed(response.item);
      return true;
    } catch (error) {
      const uncertain =
        !(error instanceof APIError) || !error.status || error.status >= 500;
      if (saved && !uncertain) {
        clearSessionReceipt(retained.current?.storageKey ?? key);
        retained.current = readRetained();
        if (mounted.current) setRecovery(retained.current);
      }
      if (error instanceof APIError && error.status === 409) team.refresh();
      if (mounted.current)
        setError(
          error instanceof Error
            ? error.message
            : 'Could not save this decision.',
        );
      return false;
    } finally {
      active.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function check() {
    const value = retained.current?.value;
    if (!value || active.current) return;
    active.current = true;
    setBusy(true);
    try {
      confirmed((await workItemsApi.receipt(value.body.requestId)).item);
    } catch (error) {
      if (error instanceof APIError && error.code === 'request_cancelled') {
        clearSessionReceipt(retained.current?.storageKey ?? key);
        retained.current = readRetained();
        if (mounted.current) {
          setRecovery(retained.current);
          setMissing(false);
          setError(
            'The retained request was cancelled. Your draft is preserved.',
          );
        }
        return;
      }
      if (mounted.current) {
        setMissing(error instanceof APIError && error.status === 404);
        setError(
          error instanceof APIError && error.status === 404
            ? 'No saved receipt found. Retry the retained request to confirm it.'
            : error instanceof Error
              ? error.message
              : 'Could not read this receipt.',
        );
      }
    } finally {
      active.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function cancel() {
    const value = retained.current?.value;
    if (!value || active.current) return;
    active.current = true;
    setBusy(true);
    try {
      const result = await workItemsApi.cancelReceipt(value.body.requestId);
      if (result.cancelled) {
        clearSessionReceipt(retained.current?.storageKey ?? key);
        retained.current = readRetained();
        if (mounted.current) {
          setRecovery(retained.current);
          setError('Unconfirmed request cancelled. Your draft is preserved.');
          setMissing(false);
        }
      } else {
        confirmed((await workItemsApi.receipt(value.body.requestId)).item);
      }
    } catch (error) {
      if (mounted.current)
        setError(
          error instanceof Error
            ? error.message
            : 'Could not confirm cancellation.',
        );
    } finally {
      active.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  return {
    create: (roomId: string, body: Omit<WorkItemInput, 'requestId'>) =>
      send(
        {
          kind: 'create',
          roomId,
          body: { ...body, requestId: crypto.randomUUID() },
        },
        false,
      ),
    decide: (itemId: string, body: Omit<WorkItemOperation, 'requestId'>) =>
      send(
        {
          kind: 'decision',
          itemId,
          body: { ...body, requestId: crypto.randomUUID() },
        },
        false,
      ),
    act: (
      itemId: string,
      action: WorkActionName,
      body: Omit<WorkActionInput, 'requestId'>,
    ) =>
      send(
        {
          kind: 'action',
          itemId,
          action,
          body: { ...body, requestId: crypto.randomUUID() },
        },
        false,
      ),
    busy,
    confirmation,
    blocked: busy || Boolean(pending),
    feedback: (
      <>
        {(error || pending) && (
          <p className="app-error" role="alert">
            {pending ? 'Camp has not confirmed this action. ' : ''}
            {error}
          </p>
        )}
        {pending && (
          <div className="app-actions">
            <Button disabled={busy} onClick={() => void check()}>
              Check status
            </Button>
            {missing && (
              <>
                <Button
                  disabled={busy}
                  onClick={() => void send(pending, true)}
                >
                  Retry request
                </Button>
                <Button disabled={busy} onClick={() => void cancel()}>
                  Cancel pending request
                </Button>
              </>
            )}
          </div>
        )}
      </>
    ),
  };
}

export type WorkOperation = ReturnType<typeof useWorkItemOperation>;
