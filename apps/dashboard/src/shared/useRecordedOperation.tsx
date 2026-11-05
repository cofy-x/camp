import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { APIError } from './api/http';
import {
  clearSessionReceipt,
  readSessionReceipt,
  receiptKey,
  writeSessionReceipt,
} from './sessionReceipt';
import { useAccount } from '../team/AuthContext';
import { useTeam } from '../team/TeamContext';

export interface RecordedOperationService<T> {
  receipt: (id: string) => Promise<T>;
  cancelReceipt: (id: string) => Promise<T>;
}

// Persist only the identity. Draft owners retain text and frozen versions;
// credentials stay in mounted fields. Recovery reads the original receipt or
// commits a cancellation tombstone before a new intent can be submitted.
export function useRecordedOperation<T extends { kind: string }>(
  scope: string,
  service: RecordedOperationService<T>,
  onConfirmed?: (receipt: T) => void,
) {
  const { user } = useAccount();
  const team = useTeam();
  const key = receiptKey(user.id, 'recorded-operation', scope);
  const [recovery, setRecovery] = useState(() =>
    readSessionReceipt(
      key,
      (value): value is string => typeof value === 'string',
    ),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [missing, setMissing] = useState(false);
  const [notice, setNotice] = useState('');
  const active = useRef(false);
  const pending = useRef(recovery);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  function confirmed(receipt: T) {
    clearSessionReceipt(key);
    pending.current = null;
    if (mounted.current) {
      setRecovery(null);
      setError('');
      setMissing(false);
      if (receipt.kind === 'cancelled')
        setNotice('Operation cancelled. Your draft is preserved.');
      else onConfirmed?.(receipt);
    }
    team.refresh();
  }

  async function run(action: (requestId: string) => Promise<T>) {
    if (active.current || pending.current) return false;
    active.current = true;
    const requestId = crypto.randomUUID();
    setBusy(true);
    setError('');
    setNotice('');
    setMissing(false);
    let retained = false;
    try {
      writeSessionReceipt(key, requestId);
      retained = true;
      pending.current = requestId;
      setRecovery(requestId);
      confirmed(await action(requestId));
      return true;
    } catch (error) {
      if (
        retained &&
        error instanceof APIError &&
        error.status > 0 &&
        error.status < 500
      ) {
        clearSessionReceipt(key);
        pending.current = null;
        if (mounted.current) setRecovery(null);
      }
      if (mounted.current)
        setError(
          error instanceof Error
            ? error.message
            : 'Could not confirm this operation.',
        );
      team.refresh();
      return false;
    } finally {
      active.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function recover(cancel: boolean) {
    const requestId = pending.current;
    if (!requestId || active.current) return;
    active.current = true;
    setBusy(true);
    try {
      confirmed(
        await (cancel
          ? service.cancelReceipt(requestId)
          : service.receipt(requestId)),
      );
    } catch (error) {
      if (mounted.current) {
        setMissing(error instanceof APIError && error.status === 404);
        setError(
          error instanceof APIError && error.status === 404
            ? 'No saved receipt found.'
            : error instanceof Error
              ? error.message
              : 'Could not recover this operation.',
        );
      }
    } finally {
      active.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  return {
    run,
    blocked: busy || Boolean(recovery),
    busy,
    feedback: (
      <>
        {(error || recovery) && (
          <p className="app-error" role="alert">
            {recovery ? `Save not confirmed. ${error}` : error}
          </p>
        )}
        {recovery && (
          <div className="app-actions">
            <Button disabled={busy} onClick={() => void recover(false)}>
              Check Saved Operation
            </Button>
            {missing && (
              <Button disabled={busy} onClick={() => void recover(true)}>
                Cancel Unconfirmed Operation
              </Button>
            )}
          </div>
        )}
        {notice && <p role="status">{notice}</p>}
      </>
    ),
  };
}
