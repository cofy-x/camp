import { useEffect, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { workspaceApi, type Room } from '../workspace/api';
import type { Runner } from './api';
import { RunnerStorage } from './RunnerStorage';
import { useFirstTask } from './useFirstTask';

export function RunnerReadiness({
  runner,
  active,
  getActivationRevision,
}: {
  runner: Runner;
  active: boolean;
  getActivationRevision: () => number;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const task = useFirstTask(runner, active, getActivationRevision);
  const [open, setOpen] = useState(false);
  const [rooms, setRooms] = useState<Room[]>([]);
  const [roomId, setRoomId] = useState(task.receipt.roomId);
  const [roomsError, setRoomsError] = useState('');
  const { pending, error: taskError } = task;
  const error = taskError || roomsError;
  const lockedRoomId = task.receipt.roomId;
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
    if (!open || !active) return;
    const controller = new AbortController();
    void workspaceApi
      .rooms(controller.signal)
      .then(({ rooms }) => {
        if (controller.signal.aborted) return;
        setRooms(rooms);
        setRoomsError('');
        setRoomId((current) => current || rooms[0]?.id || '');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setRoomsError(
            error instanceof Error ? error.message : 'Could not load Rooms.',
          );
      });
    return () => controller.abort();
  }, [open, active]);

  if (!native) return null;
  return (
    <section
      className="camp-runner-readiness"
      aria-label={`${runner.name} readiness`}
    >
      <RunnerStorage runner={runner} />
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
              <p>Check the Runner on its host machine, then refresh here.</p>
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
                <Button
                  primary
                  onClick={() => {
                    task.resume();
                    setOpen(true);
                  }}
                >
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
            void task.start(roomId);
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
              disabled={
                pending ||
                !roomId ||
                (task.receipt.phase !== 'admitted' && !canTest)
              }
            >
              {pending
                ? 'Starting…'
                : task.receipt.phase === 'admitted'
                  ? 'Open Task'
                  : lockedRoomId
                    ? 'Retry First Task'
                    : 'Run First Task'}
            </Button>
            {task.receipt.runId && !task.finished && (
              <Button
                type="button"
                disabled={pending}
                onClick={() => void task.check()}
              >
                Check Task Status
              </Button>
            )}
            {(task.receipt.phase === 'rejected' || task.finished) && (
              <Button
                type="button"
                disabled={pending}
                onClick={() => {
                  if (task.restart()) setRoomId('');
                }}
              >
                Start Another Sample
              </Button>
            )}
            <Button
              type="button"
              onClick={() => {
                task.dismiss();
                setOpen(false);
              }}
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
