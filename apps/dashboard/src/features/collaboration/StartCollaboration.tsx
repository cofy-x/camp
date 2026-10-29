import { useId, useRef, useState } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { APIError } from '../../shared/api/http';
import {
  receiptKey,
  readSessionReceipt,
  writeSessionReceipt,
  clearSessionReceipt,
} from '../../shared/sessionReceipt';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { useRunners } from '../runners/useRunners';
import type { Agent, Room } from '../workspace/api';
import { tasksApi, type Task } from './api';

type StartRequest = Parameters<typeof tasksApi.create>[1];

function isStartRequest(value: unknown): value is StartRequest {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<StartRequest>;
  return (
    typeof record.requestId === 'string' &&
    typeof record.goal === 'string' &&
    typeof record.firstAgentId === 'string' &&
    typeof record.threadRootId === 'string' &&
    Array.isArray(record.participantAgentIds) &&
    record.participantAgentIds.every((id) => typeof id === 'string')
  );
}

export function StartCollaboration({
  room,
  members,
  threadId,
  onStarted,
  onClose,
}: {
  room: Room;
  members: Agent[];
  threadId: string;
  onStarted: (task: Task) => void;
  onClose: () => void;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const goalId = useId();
  const runners = useRunners();
  const key = receiptKey(user.id, 'collaboration-start', room.id);
  const [savedRequest] = useState(() =>
    readSessionReceipt(key, isStartRequest),
  );
  const [goal, setGoal] = useState(savedRequest?.goal ?? '');
  const [selected, setSelected] = useState<string[]>(
    savedRequest?.participantAgentIds ?? [],
  );
  const [first, setFirst] = useState(savedRequest?.firstAgentId ?? '');
  const [pending, setPending] = useState(false);
  const [uncertain, setUncertain] = useState(Boolean(savedRequest));
  const [error, setError] = useState(
    savedRequest ? 'A previous start is awaiting confirmation.' : '',
  );
  const request = useRef<StartRequest | null>(savedRequest);
  const available = members.filter((agent) => agent.enabled);

  async function start() {
    if (pending) return;
    setPending(true);
    setError('');
    try {
      if (!uncertain || !request.current)
        request.current = {
          requestId: crypto.randomUUID(),
          goal: goal.trim(),
          participantAgentIds: selected,
          firstAgentId: first,
          threadRootId: threadId,
        };
      writeSessionReceipt(key, request.current);
      const { task } = await tasksApi.create(room.id, request.current);
      clearSessionReceipt(key);
      team.refresh();
      onStarted(task);
    } catch (error) {
      const unconfirmed =
        !(error instanceof APIError) ||
        error.status === 0 ||
        error.status >= 500;
      setUncertain(unconfirmed);
      if (!unconfirmed) {
        clearSessionReceipt(key);
        request.current = null;
      }
      setError(
        error instanceof Error
          ? error.message
          : 'The collaboration could not be started.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section
      className="workspace-panel collaboration-panel collaboration-start"
      aria-label="Start collaboration"
    >
      <div className="workspace-panel-heading">
        <h2>Start collaboration</h2>
        <Button disabled={pending} onClick={onClose}>
          Back
        </Button>
      </div>
      <form
        className="collaboration-form"
        onSubmit={(event) => {
          event.preventDefault();
          void start();
        }}
      >
        <div className="collaboration-input">
          <label htmlFor={goalId}>Shared goal</label>
          <textarea
            id={goalId}
            className="p9-field"
            rows={3}
            required
            maxLength={16000}
            value={goal}
            disabled={pending || uncertain}
            onChange={(event) => setGoal(event.target.value)}
            placeholder="Describe the outcome and how to check it."
          />
        </div>
        <fieldset disabled={pending || uncertain}>
          <legend>Participants</legend>
          {available.map((agent) => {
            const runner = runners.runners.find(
              (item) => item.id === agent.preferredRunnerId,
            );
            return (
              <div className="collaboration-participant" key={agent.id}>
                <Checkbox
                  label={agent.name}
                  checked={selected.includes(agent.id)}
                  onChange={(event) => {
                    const next = event.target.checked
                      ? [...selected, agent.id]
                      : selected.filter((id) => id !== agent.id);
                    setSelected(next);
                    if (!next.includes(first)) setFirst(next[0] ?? '');
                  }}
                />
                <small>
                  {agent.ownerUserId
                    ? (team.members.find(
                        (member) => member.id === agent.ownerUserId,
                      )?.name ?? 'Team member')
                    : 'Team owned'}{' '}
                  ·{' '}
                  {runner
                    ? `${runner.name}${runner.online ? '' : ' · Offline, work will queue'}`
                    : agent.preferredRunnerId
                      ? 'Configured Runner'
                      : agent.ownerUserId
                        ? 'No preferred Runner'
                        : 'Room or team default'}
                </small>
              </div>
            );
          })}
          {!available.length && (
            <p>
              Add an enabled Agent to this Room first.{' '}
              <Button
                type="button"
                onClick={() => void team.navigate({ app: 'agents' })}
              >
                Open Agents
              </Button>
            </p>
          )}
        </fieldset>
        <Select
          label="First Agent"
          value={first}
          disabled={pending || uncertain || !selected.length}
          onChange={(event) => setFirst(event.target.value)}
        >
          <option value="" disabled>
            Choose an Agent
          </option>
          {available
            .filter((agent) => selected.includes(agent.id))
            .map((agent) => (
              <option value={agent.id} key={agent.id}>
                {agent.name}
              </option>
            ))}
        </Select>
        <div className="collaboration-authorization">
          <p>Up to 6 Agent turns and 24 model requests.</p>
          <p>
            Selected Agents can hand off work. Files stay in a task draft until
            completion.
          </p>
        </div>
        {error && (
          <p className="app-error" role="alert">
            {error}
            {uncertain &&
              ' Camp could not confirm the start. Retry to open the same collaboration without starting another.'}
          </p>
        )}
        <div className="app-actions">
          <Button
            primary
            type="submit"
            disabled={pending || !goal.trim() || !first || !selected.length}
          >
            {pending
              ? 'Starting…'
              : uncertain
                ? 'Retry Start'
                : 'Start collaboration'}
          </Button>
          <Button type="button" disabled={pending} onClick={onClose}>
            {uncertain ? 'Back to Collaborations' : 'Cancel'}
          </Button>
        </div>
      </form>
    </section>
  );
}
