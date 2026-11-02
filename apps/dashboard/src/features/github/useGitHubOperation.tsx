import { useState } from 'react';
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
import { githubApi } from './api';

// Configuration and dismissal start no model work. Keep only their receipt ID
// in tab storage: webhook secrets must never be persisted in browser recovery.
export function useGitHubOperation(scope: string) {
  const { user } = useAccount();
  const team = useTeam();
  const key = receiptKey(user.id, 'github-operation', scope);
  const [recovery, setRecovery] = useState(() =>
    readSessionReceipt(
      key,
      (value): value is string => typeof value === 'string',
    ),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [missing, setMissing] = useState(false);

  function confirmed() {
    clearSessionReceipt(key);
    setRecovery(null);
    setError('');
    setMissing(false);
    team.refresh();
  }

  async function run(action: (requestId: string) => Promise<unknown>) {
    if (busy || recovery) return false;
    const requestId = crypto.randomUUID();
    setBusy(true);
    setError('');
    try {
      writeSessionReceipt(key, requestId);
      await action(requestId);
      confirmed();
      return true;
    } catch (error) {
      if (!(error instanceof APIError) || !error.status || error.status >= 500)
        setRecovery(requestId);
      else clearSessionReceipt(key);
      setError(
        error instanceof Error
          ? error.message
          : 'The operation could not be confirmed.',
      );
      team.refresh();
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function check() {
    if (!recovery || busy) return;
    setBusy(true);
    try {
      await githubApi.receipt(recovery);
      confirmed();
    } catch (error) {
      setMissing(error instanceof APIError && error.status === 404);
      setError(
        error instanceof APIError && error.status === 404
          ? 'No saved receipt yet. Check again before making another change.'
          : error instanceof Error
            ? error.message
            : 'Could not check the saved operation.',
      );
    } finally {
      setBusy(false);
    }
  }

  const feedback = (
    <>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {recovery && (
        <div className="github-recovery">
          <p>A previous operation is awaiting confirmation.</p>
          <Button disabled={busy} onClick={() => void check()}>
            Check Saved Operation
          </Button>
          {missing && (
            <details>
              <summary>Clear recovery</summary>
              <p>
                This does not cancel an in-flight settings change or decision.
                Current revisions and event decisions still apply.
              </p>
              <Button disabled={busy} onClick={confirmed}>
                Refresh and Clear Recovery
              </Button>
            </details>
          )}
        </div>
      )}
    </>
  );
  return { run, feedback, blocked: busy || Boolean(recovery), busy };
}
