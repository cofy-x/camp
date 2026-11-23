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

interface WorkParticipantsProps {
  members: Agent[];
  selected: string[];
  firstAgentId: string;
  onChange: (ids: string[]) => void;
  onFirstChange: (id: string) => void;
  disabled?: boolean;
}

export function workAgentSummary(agents: Agent[]) {
  const labels = agents.map((agent) =>
    agents.length > 1
      ? `${agent.name} (${agent.access === 'write' ? 'edit' : 'read'})`
      : agent.name,
  );
  if (agents.length === 1)
    return `${labels[0]} · ${agents[0].access === 'write' ? 'May edit Room files' : 'Read access'}`;
  return labels.join(', ') || 'Choose an Agent';
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
  editable = true,
  policyScoped = false,
  showTotalAllowance = false,
  execution,
  executionNoticeId,
}: WorkParticipantsProps & {
  defaults: WorkItemDefaults | null;
  maxRequests?: number;
  editable?: boolean;
  policyScoped?: boolean;
  showTotalAllowance?: boolean;
  execution?: ReturnType<typeof useWorkExecution>;
  executionNoticeId?: string;
}) {
  const summaryId = useId();
  const agents = selected.flatMap((id) => {
    const agent = members.find((agent) => agent.id === id);
    return agent ? [agent] : [];
  });
  const requests = maxRequests ?? defaults?.budget.maxModelRequests;
  const perCollaboration = defaults?.execution.maxModelRequests;
  const showRequestCap =
    perCollaboration !== undefined &&
    requests !== undefined &&
    perCollaboration < requests &&
    (policyScoped || !defaults?.project?.enabled);
  const requestLimit = Math.min(perCollaboration ?? 24, requests ?? 24);
  return (
    <div className="work-grant">
      {editable ? (
        <details className="work-grant-agents">
          <summary aria-label="Change Agents" aria-describedby={summaryId}>
            <span className="work-grant-summary" id={summaryId}>
              {workAgentSummary(agents)}
            </span>
            <span className="work-grant-change">Change…</span>
          </summary>
          <WorkParticipants
            members={members}
            selected={selected}
            firstAgentId={firstAgentId}
            onChange={onChange}
            onFirstChange={onFirstChange}
            disabled={disabled}
          />
        </details>
      ) : (
        <div className="work-grant-summary">{workAgentSummary(agents)}</div>
      )}
      <p className="work-grant-limits">
        {defaults &&
          (showTotalAllowance ? (
            <span>
              Up to {defaults.execution.maxTurns} turns
              {showRequestCap && ` · ${perCollaboration} model requests`} per
              collaboration
            </span>
          ) : (
            <span>
              Up to {defaults.execution.maxTurns} turns · {requestLimit} model
              requests
            </span>
          ))}
        {showTotalAllowance && requests !== undefined && (
          <span>{requests} model requests total</span>
        )}
        <span>Team-visible draft</span>
      </p>
      {execution && (
        <ExecutionNotice
          execution={execution}
          id={executionNoticeId}
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

export function WorkParticipants({
  members,
  selected,
  firstAgentId,
  onChange,
  onFirstChange,
  disabled = false,
}: WorkParticipantsProps) {
  return (
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
          (id) => !members.some((agent) => agent.id === id && agent.enabled),
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
  );
}

function ExecutionNotice({
  execution,
  agentId,
  id,
}: {
  execution: ReturnType<typeof useWorkExecution>;
  agentId?: string;
  id?: string;
}) {
  const { navigate } = useTeam();
  if (execution.error)
    return (
      <p className="work-execution-notice app-error" role="alert" id={id}>
        <span>{execution.error}</span>
        <Button onClick={() => void execution.retry()}>
          Retry Runner check
        </Button>
      </p>
    );
  if (execution.issue) {
    const assign = execution.issue.code === 'runner_required';
    const agents = assign || execution.issue.code === 'agent_unavailable';
    return (
      <p className="work-execution-notice app-error" role="status" id={id}>
        <span>{execution.issue.message}</span>
        <Button
          onClick={() =>
            void navigate(
              agents ? { app: 'agents', agentId } : { app: 'runners' },
            )
          }
        >
          {assign ? 'Assign Runner…' : agents ? 'Open Agents' : 'Open Runners'}
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
