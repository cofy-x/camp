import { Checkbox, Select } from '@cofy-x/camp-platinum';
import type { Agent } from '../workspace/api';
import type { WorkItemDefaults } from './api';

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
}: {
  members: Agent[];
  selected: string[];
  defaults: WorkItemDefaults | null;
  maxRequests?: number;
  firstAgentId: string;
  onChange: (ids: string[]) => void;
  onFirstChange: (id: string) => void;
  disabled?: boolean;
}) {
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
      <p>
        {agents.map((agent) => agent.name).join(', ') || 'Choose an Agent'}
        {agents.length
          ? ` · ${agents.some((agent) => agent.access === 'write') ? 'May edit Room files' : 'Read access'}`
          : ''}
      </p>
      {defaults && (
        <p>
          Up to {defaults.execution.maxTurns} turns and {requests} model
          requests.
        </p>
      )}
      <p>Work is saved in a team-visible draft.</p>
      <details>
        <summary>Change Agents</summary>
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
    </div>
  );
}
