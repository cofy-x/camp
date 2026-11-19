import { useId } from 'react';
import type { WorkBudget, WorkItem } from './api';
import { workBudgetLimits } from './workSettings';

export function WorkBudgetEditor({
  value,
  onChange,
  label = 'Total allowance',
  current,
}: {
  value: WorkBudget;
  onChange: (value: WorkBudget) => void;
  label?: string;
  current?: Pick<WorkItem, 'budget' | 'usage'>;
}) {
  const groupId = useId();
  const used = current && {
    maxEvaluations: current.usage.evaluations,
    maxTasks: current.usage.tasks,
    maxModelRequests: current.usage.modelRequests,
    maxActions: current.usage.actions,
    maxFollowUps: current.usage.followUps,
  };
  const reserved = current
    ? current.usage.reservedModelRequests - current.usage.modelRequests
    : 0;

  return (
    <fieldset>
      {label && <legend>{label}</legend>}
      <div className="work-item-limits">
        {workBudgetLimits.map(({ key, label, max }) => {
          const id = `${groupId}-${key}`;
          const description = [
            used?.[key] ? `${used[key]} used` : null,
            key === 'maxModelRequests' && reserved > 0
              ? `${reserved} reserved`
              : null,
            current && value[key] !== current.budget[key]
              ? `${current.budget[key]} currently authorized`
              : null,
          ]
            .filter(Boolean)
            .join(' · ');
          return (
            <div key={key} className="work-item-limit">
              <label htmlFor={id}>{label}</label>
              <input
                id={id}
                aria-describedby={description ? `${id}-usage` : undefined}
                className="p9-field"
                type="number"
                min={key === 'maxFollowUps' ? 0 : 1}
                max={max}
                required
                value={value[key]}
                onChange={(event) =>
                  onChange({ ...value, [key]: Number(event.target.value) })
                }
              />
              {description && <small id={`${id}-usage`}>{description}</small>}
            </div>
          );
        })}
      </div>
    </fieldset>
  );
}
