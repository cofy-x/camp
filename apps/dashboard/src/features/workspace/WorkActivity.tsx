import { isActiveRun } from './api';
import { useRead } from '../../shared/reads/useRead';
import type { Run, RunActivityWindow } from './api';
import { summarizeActivity } from './activity';

export function WorkActivity({
  run,
}: {
  run: Pick<Run, 'id' | 'status' | 'waitingReason'>;
}) {
  const queued = run.status === 'queued';
  const active = isActiveRun(run);
  const activity = useRead<RunActivityWindow>(
    `/runs/${encodeURIComponent(run.id)}/activity?limit=6`,
    { enabled: !queued && active },
  );
  const error = Boolean(activity.error);
  const recent = activity.data?.items.map(summarizeActivity) ?? [];
  const latest = recent.at(-1);
  const stage =
    run.status === 'preparing'
      ? 'Preparing room files'
      : run.status === 'checking'
        ? 'Running checks'
        : run.status === 'cancelling'
          ? 'Stopping work and cleaning up'
          : '';
  return (
    <div className="workspace-work-activity">
      <p>
        {!active
          ? 'Work ended.'
          : stage ||
            latest?.label ||
            (queued
              ? run.waitingReason || 'Waiting for an execution slot…'
              : 'Waiting for Agent activity…')}
      </p>
      {active && !stage && latest?.detail && <code>{latest.detail}</code>}
      {recent.length > 0 && (
        <details>
          <summary>Recent activity</summary>
          <ol>
            {recent.map((item) => (
              <li key={item.seq}>
                <span>{item.label}</span>
                {item.detail && <code>{item.detail}</code>}
              </li>
            ))}
          </ol>
        </details>
      )}
      {error && active && (
        <p className="workspace-description">
          Reconnecting to activity. The last reported state is shown.
        </p>
      )}
    </div>
  );
}
