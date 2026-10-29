import { useEffect, useState } from 'react';
import { isActiveRun, workspaceApi } from './api';
import type { Run } from './api';
import { summarizeActivity } from './activity';
import type { ActivitySummary } from './activity';

export function WorkActivity({ run }: { run: Run }) {
  const [recent, setRecent] = useState<ActivitySummary[]>([]);
  const [error, setError] = useState(false);
  const queued = run.status === 'queued';
  const active = isActiveRun(run);
  useEffect(() => {
    if (queued || !active) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let after = 0;

    async function poll() {
      try {
        for (;;) {
          const { events } = await workspaceApi.events(
            run.id,
            after,
            controller.signal,
          );
          if (controller.signal.aborted) return;
          const additions = events.filter((event) => event.seq > after);
          const summaries = additions
            .map(summarizeActivity)
            .filter((item) => item !== null);
          if (summaries.length)
            setRecent((current) => [...current, ...summaries].slice(-6));
          setError(false);
          const next = additions.at(-1)?.seq ?? after;
          if (events.length < 500 || next <= after) {
            after = next;
            break;
          }
          after = next;
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      }
      if (!controller.signal.aborted)
        timer = setTimeout(() => void poll(), 1200);
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [run.id, queued, active]);
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
