import type { WorkBudget } from './api';
import { workBudgetLimits } from './workSettings';

export function WorkBudgetEditor({
  value,
  onChange,
  label = 'Total allowance',
}: {
  value: WorkBudget;
  onChange: (value: WorkBudget) => void;
  label?: string;
}) {
  return (
    <fieldset>
      <legend>{label}</legend>
      <div className="work-item-limits">
        {workBudgetLimits.map(({ key, label, max }) => (
          <label key={key}>
            {label}
            <input
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
          </label>
        ))}
      </div>
    </fieldset>
  );
}
