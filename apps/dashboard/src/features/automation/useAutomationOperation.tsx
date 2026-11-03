import { useRef, useState } from 'react';
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
import { automationApi, type AutomationReceipt } from './api';

// Only the identity is persisted. Credentials stay in the mounted form, and an
// uncertain write is checked through its actor-scoped receipt before editing.
export function useAutomationOperation(
  scope: string,
  onConfirmed?: (receipt: AutomationReceipt) => void,
) {
  const { user } = useAccount();
  const team = useTeam();
  const key = receiptKey(user.id, 'automation-operation', scope);
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

  function confirmed(receipt: AutomationReceipt) {
    if (receipt.kind === 'cancelled') setNotice('Operation cancelled.');
    else onConfirmed?.(receipt);
    clearSessionReceipt(key);
    pending.current = null;
    setRecovery(null);
    setError('');
    setMissing(false);
    team.refresh();
  }

  async function run(
    action: (requestId: string) => Promise<AutomationReceipt>,
  ) {
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
      confirmed(await action(requestId));
      return true;
    } catch (error) {
      if (
        retained &&
        (!(error instanceof APIError) || !error.status || error.status >= 500)
      ) {
        pending.current = requestId;
        setRecovery(requestId);
      } else if (retained) clearSessionReceipt(key);
      setError(
        error instanceof Error
          ? error.message
          : 'Could not confirm this operation.',
      );
      team.refresh();
      return false;
    } finally {
      active.current = false;
      setBusy(false);
    }
  }

  async function cancel() {
    const requestId = pending.current;
    if (!requestId || active.current) return;
    active.current = true;
    setBusy(true);
    try {
      confirmed(await automationApi.cancelReceipt(requestId));
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not confirm cancellation.',
      );
    } finally {
      active.current = false;
      setBusy(false);
    }
  }

  async function check() {
    const requestId = pending.current;
    if (!requestId || active.current) return;
    active.current = true;
    setBusy(true);
    try {
      confirmed(await automationApi.receipt(requestId));
    } catch (error) {
      setMissing(error instanceof APIError && error.status === 404);
      setError(
        error instanceof APIError && error.status === 404
          ? 'No saved receipt found.'
          : error instanceof Error
            ? error.message
            : 'Could not check this operation.',
      );
    } finally {
      active.current = false;
      setBusy(false);
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
            <Button disabled={busy} onClick={() => void check()}>
              Check Saved Operation
            </Button>
            {missing && (
              <Button disabled={busy} onClick={() => void cancel()}>
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
