import { useEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import {
  receiptKey,
  readSessionReceipt,
  writeSessionReceipt,
} from '../../shared/sessionReceipt';
import { workspaceApi, type Room } from '../workspace/api';
import type { Runner } from './api';

interface FirstTaskReceipt {
  id: string;
  agentId: string;
  roomId: string;
}

function isFirstTaskReceipt(value: unknown): value is FirstTaskReceipt {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<FirstTaskReceipt>;
  return (
    typeof record.id === 'string' &&
    typeof record.agentId === 'string' &&
    typeof record.roomId === 'string'
  );
}

export function RunnerReadiness({ runner }: { runner: Runner }) {
  const team = useTeam();
  const { user } = useAccount();
  const key = receiptKey(user.id, 'runner-first-task', runner.id);
  const [savedRequest] = useState(() =>
    readSessionReceipt(key, isFirstTaskReceipt),
  );
  const [open, setOpen] = useState(false);
  const [rooms, setRooms] = useState<Room[]>([]);
  const [roomId, setRoomId] = useState(savedRequest?.roomId ?? '');
  const [pending, setPending] = useState(false);
  const [lockedRoomId, setLockedRoomId] = useState(savedRequest?.roomId ?? '');
  const [error, setError] = useState('');
  const request = useRef(
    savedRequest ?? { id: crypto.randomUUID(), agentId: '', roomId: '' },
  );
  const readiness = runner.capabilities.readiness;
  const native =
    runner.runtime === 'native' && runner.capabilities.executor !== 'fixture';
  const own = runner.ownerUserId === user.id;
  const canTest =
    own &&
    runner.enabled &&
    !runner.revoked &&
    native &&
    runner.capabilities.ready &&
    runner.modelConfigured;
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void workspaceApi
      .rooms(controller.signal)
      .then(({ rooms }) => {
        if (controller.signal.aborted) return;
        setRooms(rooms);
        setRoomId((current) => current || rooms[0]?.id || '');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load Rooms.',
          );
      });
    return () => controller.abort();
  }, [open]);

  async function firstTask() {
    if (!roomId || pending) return;
    setPending(true);
    setError('');
    try {
      const attempt = request.current;
      if (!attempt.roomId) attempt.roomId = roomId;
      writeSessionReceipt(key, attempt);
      setLockedRoomId(attempt.roomId);
      // A known handle lets an uncertain create response recover the same Agent.
      const handle = `runner-check-${attempt.id.replaceAll('-', '')}`;
      if (!attempt.agentId) {
        const { agents } = await workspaceApi.agents();
        const existing = agents.find(
          (agent) => agent.handle === handle && agent.ownerUserId === user.id,
        );
        const agent =
          existing ||
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
        attempt.agentId = agent.id;
        writeSessionReceipt(key, attempt);
      }
      await workspaceApi.membership(attempt.roomId, attempt.agentId, true);
      const filename = `camp-first-task-${attempt.id.slice(0, 8)}.md`;
      const { runs } = await workspaceApi.send(attempt.roomId, {
        requestId: attempt.id,
        recipientAgentIds: [attempt.agentId],
        content: `Create ${filename} with the heading "Camp is ready" and one sentence saying this file was created on the Runner. Read the file back using a tool and confirm its exact content. Do not change any other files.`,
      });
      team.refresh();
      await team.navigate({
        app: 'workspace',
        roomId: attempt.roomId,
        runId: runs[0]?.id,
      });
      setOpen(false);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'The first task could not be started.',
      );
    } finally {
      setPending(false);
    }
  }

  if (!native) return null;
  return (
    <section
      className="camp-runner-readiness"
      aria-label={`${runner.name} readiness`}
    >
      {!runner.revoked && runner.enabled && (
        <>
          {!runner.capabilities.ready ? (
            <div className="camp-runner-next-step">
              <h3>Execution needs attention</h3>
              <p className="app-error">
                {runner.capabilities.reason ||
                  'The Runner has not reported that it can execute work.'}
              </p>
              {readiness?.checks.some((check) => check.status !== 'passed') && (
                <ul>
                  {readiness.checks
                    .filter((check) => check.status !== 'passed')
                    .map((check) => (
                      <li key={check.id}>
                        {check.label}: {check.detail || check.status}
                      </li>
                    ))}
                </ul>
              )}
              <p>Check the Runner on its Linux machine, then refresh here.</p>
            </div>
          ) : !runner.modelConfigured ? (
            <div className="camp-runner-next-step">
              <h3>Connect a model</h3>
              <p>
                {user.role === 'admin'
                  ? 'Configure the Server model before running Agent work.'
                  : 'Ask an administrator to configure the Server model before running Agent work.'}
              </p>
              {user.role === 'admin' && (
                <Button
                  onClick={() =>
                    void team.navigate({ app: 'settings', section: 'models' })
                  }
                >
                  Configure Model…
                </Button>
              )}
            </div>
          ) : canTest && !runner.firstTaskSucceededAt ? (
            <div className="camp-runner-next-step">
              <h3>
                {lockedRoomId
                  ? 'Continue your first task'
                  : 'Try your first task'}
              </h3>
              {lockedRoomId ? (
                <p>Continue the same task without starting another.</p>
              ) : (
                !open && <p>Create and check a sample file in a Room.</p>
              )}
              {!runner.online && (
                <p>The task will wait for this Runner to reconnect.</p>
              )}
              {!open && (
                <Button primary onClick={() => setOpen(true)}>
                  Run First Task…
                </Button>
              )}
            </div>
          ) : (
            <div className="camp-runner-next-step">
              <p>
                {runner.online
                  ? 'Ready for Agent work. Choose this Runner in an Agent’s settings.'
                  : 'Assigned work will wait until this Runner reconnects.'}
              </p>
              <Button onClick={() => void team.navigate({ app: 'agents' })}>
                Open Agents
              </Button>
            </div>
          )}
        </>
      )}
      {open && (
        <form
          className="camp-runner-first-task"
          onSubmit={(event) => {
            event.preventDefault();
            void firstTask();
          }}
        >
          <Select
            label="Room"
            value={roomId}
            disabled={pending || Boolean(lockedRoomId)}
            onChange={(event) => setRoomId(event.target.value)}
          >
            <option value="" disabled>
              Choose a Room
            </option>
            {rooms.map((room) => (
              <option key={room.id} value={room.id}>
                {room.name}
              </option>
            ))}
          </Select>
          <p>
            Creates an Assistant shared with this Room. Uses the Server model to
            create and check a sample file.
          </p>
          {!rooms.length && (
            <Button
              type="button"
              onClick={() =>
                void team.navigate({ app: 'workspace', roomId: '' })
              }
            >
              Create a Room in Workspace…
            </Button>
          )}
          <div className="app-actions">
            <Button
              primary
              type="submit"
              disabled={pending || !roomId || !canTest}
            >
              {pending
                ? 'Starting…'
                : lockedRoomId
                  ? 'Retry First Task'
                  : 'Run First Task'}
            </Button>
            <Button
              type="button"
              disabled={pending}
              onClick={() => setOpen(false)}
            >
              Back
            </Button>
          </div>
        </form>
      )}
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
