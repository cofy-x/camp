import { useId } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import type { Agent } from '../workspace/api';
import type { WorkItemDefaults } from './api';
import type { useWorkExecution } from './useWorkExecution';

export function suggestedParticipants(
  members: Agent[],
  defaults: WorkItemDefaults | null,
  defaultAgentId?: string,
) {
  if (
    defaultAgentId &&
    members.some((agent) => agent.id === defaultAgentId && agent.enabled)
  )
    return [defaultAgentId];
  const available = (defaults?.participantAgentIds ?? []).filter((id) =>
    members.some((agent) => agent.id === id && agent.enabled),
  );
  return available.length
    ? available
    : members
        .filter((agent) => agent.enabled)
        .slice(0, 1)
        .map((agent) => agent.id);
}

export function WorkGrant({
  members,
  selected,
  defaults,
  maxRequests,
  firstAgentId,
  onChange,
  onFirstChange,
  disabled = false,
  execution,
}: {
  members: Agent[];
  selected: string[];
  defaults: WorkItemDefaults | null;
  maxRequests?: number;
  firstAgentId: string;
  onChange: (ids: string[]) => void;
  onFirstChange: (id: string) => void;
  disabled?: boolean;
  execution?: ReturnType<typeof useWorkExecution>;
}) {
  const summaryId = useId();
  const agents = selected.flatMap((id) => {
    const agent = members.find((agent) => agent.id === id);
    return agent ? [agent] : [];
  });
  const requests = Math.min(
    defaults?.execution.maxModelRequests ?? 24,
    maxRequests ?? defaults?.budget.maxModelRequests ?? 24,
  );
  return (
    <div className="work-grant">
      <details className="work-grant-agents">
        <summary aria-label="Change Agents" aria-describedby={summaryId}>
          <span className="work-grant-summary" id={summaryId}>
            {agents
              .map((agent) =>
                agents.length > 1
                  ? `${agent.name} (${agent.access === 'write' ? 'edit' : 'read'})`
                  : agent.name,
              )
              .join(', ') || 'Choose an Agent'}
            {agents.length === 1
              ? ` · ${agents.some((agent) => agent.access === 'write') ? 'May edit Room files' : 'Read access'}`
              : ''}
          </span>
          <span className="work-grant-change">Change…</span>
        </summary>
        <fieldset disabled={disabled} className="work-item-participants">
          {members
            .filter((agent) => agent.enabled)
            .map((agent) => (
              <Checkbox
                key={agent.id}
                label={agent.name}
                checked={selected.includes(agent.id)}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? [...selected, agent.id]
                      : selected.filter((id) => id !== agent.id),
                  )
                }
              />
            ))}
          {!members.some((agent) => agent.enabled) && (
            <p>No available Agents in this Room.</p>
          )}
          {selected
            .filter(
              (id) =>
                !members.some((agent) => agent.id === id && agent.enabled),
            )
            .map((id) => (
              <Checkbox
                key={id}
                label={`${members.find((agent) => agent.id === id)?.name ?? id} · Unavailable`}
                checked
                onChange={() =>
                  onChange(selected.filter((agentId) => agentId !== id))
                }
              />
            ))}
          {selected.length > 1 && (
            <Select
              label="First Agent"
              value={firstAgentId}
              onChange={(event) => onFirstChange(event.target.value)}
            >
              {selected.map((id) => (
                <option key={id} value={id}>
                  {members.find((agent) => agent.id === id)?.name ?? id}
                </option>
              ))}
            </Select>
          )}
        </fieldset>
      </details>
      <p className="work-grant-limits">
        {defaults && (
          <span>
            Up to {defaults.execution.maxTurns} turns · {requests} model
            requests
          </span>
        )}
        <span>Team-visible draft</span>
      </p>
      {execution && (
        <ExecutionNotice
          execution={execution}
          agentId={agents.length === 1 ? agents[0].id : undefined}
        />
      )}
      {selected.length > 8 && (
        <p className="app-error" role="alert">
          Choose at most eight Agents.
        </p>
      )}
    </div>
  );
}

function ExecutionNotice({
  execution,
  agentId,
}: {
  execution: ReturnType<typeof useWorkExecution>;
  agentId?: string;
}) {
  const { navigate } = useTeam();
  if (execution.error)
    return (
      <p className="work-execution-notice app-error" role="alert">
        <span>{execution.error}</span>
        <Button onClick={() => void execution.retry()}>
          Retry Runner check
        </Button>
      </p>
    );
  if (execution.issue) {
    const agents =
      execution.issue.code === 'runner_required' ||
      execution.issue.code === 'agent_unavailable';
    return (
      <p className="work-execution-notice app-error" role="status">
        <span>{execution.issue.message}</span>
        <Button
          onClick={() =>
            void navigate(
              agents ? { app: 'agents', agentId } : { app: 'runners' },
            )
          }
        >
          {agents ? 'Open Agents' : 'Open Runners'}
        </Button>
      </p>
    );
  }
  if (execution.checking)
    return (
      <p className="work-execution-notice" role="status">
        Checking Runner access…
      </p>
    );
  return null;
}
