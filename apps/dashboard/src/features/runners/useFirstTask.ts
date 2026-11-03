import { useEffect, useRef, useState } from 'react';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { APIError } from '../../shared/api/http';
import {
  receiptKey,
  readSessionReceipt,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import { workspaceApi, type SendInput } from '../workspace/api';
import type { Runner } from './api';

interface FirstTaskReceipt {
  id: string;
  agentId: string;
  roomId: string;
  phase?: 'preparing' | 'sending' | 'admitted' | 'rejected';
  input?: SendInput;
  runId?: string;
}

function validReceipt(value: unknown): value is FirstTaskReceipt {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<FirstTaskReceipt>;
  return (
    typeof record.id === 'string' &&
    typeof record.agentId === 'string' &&
    typeof record.roomId === 'string' &&
    (record.phase === undefined ||
      ['preparing', 'sending', 'admitted', 'rejected'].includes(
        record.phase,
      )) &&
    (record.runId === undefined || typeof record.runId === 'string') &&
    (record.input === undefined ||
      (record.input.requestId === record.id &&
        typeof record.input.content === 'string' &&
        record.input.recipientAgentIds?.length === 1 &&
        record.input.recipientAgentIds[0] === record.agentId))
  );
}

const fresh = (): FirstTaskReceipt => ({
  id: crypto.randomUUID(),
  agentId: '',
  roomId: '',
  phase: 'preparing',
});
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'interrupted']);
const rejectedCodes = new Set([
  'invalid_message',
  'invalid_room',
  'invalid_agent',
  'invalid_reference',
  'forbidden',
  'runner_forbidden',
  'not_found',
  'agent_unavailable',
  'runner_required',
  'runner_unavailable',
  'runner_revision_conflict',
  'resource_conflict',
  'unavailable',
]);

export function useFirstTask(
  runner: Runner,
  active: boolean,
  getActivationRevision: () => number,
) {
  const { user } = useAccount();
  const team = useTeam();
  const key = receiptKey(user.id, 'runner-first-task', runner.id);
  const [receipt, setReceipt] = useState(
    () => readSessionReceipt(key, validReceipt) ?? fresh(),
  );
  const request = useRef(receipt);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [finished, setFinished] = useState(false);
  const mounted = useRef(false);
  const epoch = useRef(0);
  const visible = useRef(active);
  visible.current = active;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (!active) epoch.current++;
  }, [active]);

  function dismiss() {
    epoch.current++;
  }

  function operation() {
    const generation = ++epoch.current;
    const activation = getActivationRevision();
    return () =>
      mounted.current &&
      visible.current &&
      epoch.current === generation &&
      getActivationRevision() === activation;
  }

  function save(value: FirstTaskReceipt, current: () => boolean) {
    writeSessionReceipt(key, value);
    request.current = value;
    if (current()) setReceipt(value);
  }

  async function start(roomId: string) {
    if (!roomId || pending) return;
    const current = operation();
    setPending(true);
    setError('');
    let attempt = { ...request.current };
    // A retained sending phase means an earlier response was uncertain. A later
    // rejection cannot prove that the original request was never admitted.
    const uncertain =
      attempt.phase === 'sending' || attempt.phase === undefined;
    let sending = false;
    try {
      attempt.roomId ||= roomId;
      save(attempt, current);
      if (attempt.phase === 'admitted') {
        if (current())
          await team.navigate({
            app: 'workspace',
            roomId: attempt.roomId,
            runId: attempt.runId,
          });
        return;
      }
      if (!attempt.agentId) {
        const { agents } = await workspaceApi.agents();
        if (!current()) return;
        const handle = `runner-check-${attempt.id.replaceAll('-', '')}`;
        const existing = agents.find(
          (agent) => agent.handle === handle && agent.ownerUserId === user.id,
        );
        const agent =
          existing ??
          (
            await workspaceApi.saveAgent({
              name: `${runner.name.slice(0, 70)} Assistant`,
              handle,
              icon: 'agent',
              instructions:
                'Complete the requested file task and read the result back to verify it.',
              access: 'write',
              enabled: true,
              preferredRunnerId: runner.id,
            })
          ).agent;
        attempt = { ...attempt, agentId: agent.id };
        save(attempt, current);
        if (!current()) return;
      }
      await workspaceApi.membership(attempt.roomId, attempt.agentId, true);
      if (!current()) return;
      const filename = `camp-first-task-${attempt.id.slice(0, 8)}.md`;
      attempt = {
        ...attempt,
        phase: 'sending',
        input: attempt.input ?? {
          requestId: attempt.id,
          recipientAgentIds: [attempt.agentId],
          content: `Create ${filename} with the heading "Camp is ready" and one sentence saying this file was created on the Runner. Read the file back using a tool and confirm its exact content. Do not change any other files.`,
        },
      };
      save(attempt, current);
      sending = true;
      const { runs } = await workspaceApi.send(attempt.roomId, attempt.input!);
      attempt = { ...attempt, phase: 'admitted', runId: runs[0]?.id };
      save(attempt, current);
      if (!current()) return;
      team.refresh();
      await team.navigate({
        app: 'workspace',
        roomId: attempt.roomId,
        runId: attempt.runId,
      });
    } catch (error) {
      let failure = error;
      const rejected =
        !uncertain &&
        (!sending ||
          (error instanceof APIError &&
            [400, 403, 404, 409, 413].includes(error.status) &&
            rejectedCodes.has(error.code)));
      if (rejected) {
        attempt = { ...attempt, phase: 'rejected' };
        try {
          save(attempt, current);
        } catch (storageError) {
          failure = storageError;
        }
      }
      if (current())
        setError(
          failure instanceof Error
            ? failure.message
            : 'The first task could not be started.',
        );
    } finally {
      // Releasing the local busy flag never changes a retained operation.
      if (mounted.current) setPending(false);
    }
  }

  async function check() {
    const attempt = request.current;
    if (!attempt.runId || pending) return;
    const current = operation();
    setPending(true);
    setError('');
    try {
      const { run } = await workspaceApi.run(attempt.runId);
      if (!current()) return;
      if (run.id !== attempt.runId || run.roomId !== attempt.roomId)
        throw new Error('The task response does not match the saved request.');
      setFinished(terminal.has(run.status));
      if (!terminal.has(run.status))
        setError('This task is still active. Open it to follow its progress.');
    } catch (error) {
      if (current())
        setError(
          error instanceof Error ? error.message : 'Could not check the task.',
        );
    } finally {
      if (mounted.current) setPending(false);
    }
  }

  function restart() {
    if (pending || (request.current.phase !== 'rejected' && !finished))
      return false;
    try {
      writeSessionReceipt(
        receiptKey(
          user.id,
          'runner-first-task-history',
          `${runner.id}:${request.current.id}`,
        ),
        request.current,
      );
      const next = fresh();
      save(next, () => true);
      epoch.current++;
      setFinished(false);
      setError('');
      return true;
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not retain the previous task.',
      );
      return false;
    }
  }

  function resume() {
    setReceipt(request.current);
    setError('');
  }

  return {
    receipt,
    pending,
    error,
    finished,
    start,
    check,
    restart,
    dismiss,
    resume,
  };
}
