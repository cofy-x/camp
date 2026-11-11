import { useCallback, useEffect, useId, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import { statusLabel } from '../workspace/api';
import { type Task, type TaskInputPage, type TaskRunPage } from './api';

// Keep one bounded page and its navigation anchors. Live updates refresh the
// same range, without replacing the user's location or the parent write form.
function useHistoryPage<T, C>(
  initial: C,
  path: (cursor: C) => string,
  active: boolean,
  freeze?: (cursor: C, page: T) => C,
) {
  const [cursor, setCursor] = useState(initial);
  const [previous, setPrevious] = useState<C[]>([]);
  const read = useRead<T>(path(cursor), { enabled: active });
  const page = read.data;
  useEffect(() => {
    if (!page || !freeze) return;
    const frozen = freeze(cursor, page);
    if (frozen !== cursor) setCursor(frozen);
  }, [cursor, page, freeze]);
  return {
    cursor,
    page,
    error: read.error,
    canBack: previous.length > 0,
    next(next: C, current = cursor) {
      setPrevious((anchors) => [...anchors, current]);
      setCursor(next);
    },
    back() {
      const last = previous.at(-1);
      if (last === undefined) return;
      setPrevious(previous.slice(0, -1));
      setCursor(last);
    },
    retry() {
      void read.refresh();
    },
  };
}

type InputCursor = { after: number; through?: number };
const firstInput: InputCursor = { after: 0 };
const freezeInput = (cursor: InputCursor, page: TaskInputPage): InputCursor =>
  cursor.through === undefined ? { ...cursor, through: page.through } : cursor;

export function TaskHistory({
  task,
  active,
  onInspect,
}: {
  task: Task;
  active: boolean;
  onInspect: (id: string) => void;
}) {
  const descriptionId = useId();
  const loadInputs = useCallback(
    (cursor: InputCursor) => {
      const query = new URLSearchParams({ after: String(cursor.after) });
      if (cursor.through !== undefined)
        query.set('through', String(cursor.through));
      return `/tasks/${encodeURIComponent(task.id)}/inputs?${query}`;
    },
    [task.id],
  );
  const loadRuns = useCallback(
    (cursor: string) =>
      `/tasks/${encodeURIComponent(task.id)}/runs?${new URLSearchParams({ cursor })}`,
    [task.id],
  );
  const inputs = useHistoryPage(
    firstInput,
    loadInputs,
    active && task.inputRevision > 0,
    freezeInput,
  );
  const runs = useHistoryPage<TaskRunPage, string>('', loadRuns, active);
  const inputPage = inputs.page;
  const frozenCursor = inputPage
    ? { after: inputs.cursor.after, through: inputPage.through }
    : inputs.cursor;
  return (
    <div className="collaboration-tab-content" hidden={!active}>
      <section
        className="collaboration-history"
        aria-label="Collaboration turns"
      >
        <h3>Turns</h3>
        {!runs.page && !runs.error && <p role="status">Loading turns…</p>}
        {runs.error && (
          <p className="app-error" role="alert">
            {runs.error} <Button onClick={runs.retry}>Retry Turns</Button>
          </p>
        )}
        {runs.page && (
          <>
            {!runs.page.items.length && <p>No execution has started yet.</p>}
            <ol className="collaboration-turns">
              {runs.page.items.map((run) => (
                <li key={run.id}>
                  <button
                    type="button"
                    className="collaboration-turn"
                    aria-label={`${run.agentName} · ${statusLabel[run.status]}`}
                    aria-describedby={
                      run.runnerName || run.checksDeferred
                        ? `${descriptionId}-${run.id}`
                        : undefined
                    }
                    onClick={() => onInspect(run.id)}
                  >
                    <span className="collaboration-turn-agent">
                      {run.agentName}
                    </span>
                    <span className="collaboration-turn-status">
                      {statusLabel[run.status]}
                    </span>
                    <span className="collaboration-turn-arrow" aria-hidden>
                      ›
                    </span>
                    {(run.runnerName || run.checksDeferred) && (
                      <small id={`${descriptionId}-${run.id}`}>
                        {run.runnerName}
                        {run.checksDeferred
                          ? `${run.runnerName ? ' · ' : ''}Checks deferred until finish`
                          : ''}
                      </small>
                    )}
                  </button>
                </li>
              ))}
            </ol>
          </>
        )}
        {(runs.canBack || runs.page?.nextCursor) && (
          <div className="app-actions">
            {runs.canBack && (
              <Button onClick={runs.back}>Previous Turns</Button>
            )}
            {runs.page?.nextCursor && (
              <Button onClick={() => runs.next(runs.page!.nextCursor)}>
                Next Turns
              </Button>
            )}
          </div>
        )}
      </section>
      {task.inputRevision > 0 && (
        <section className="collaboration-history" aria-label="Team input">
          <h3>Team input</h3>
          {!inputPage && !inputs.error && <p role="status">Loading inputs…</p>}
          {inputs.error && (
            <p className="app-error" role="alert">
              {inputs.error}{' '}
              <Button onClick={inputs.retry}>Retry Inputs</Button>
            </p>
          )}
          {inputPage && (
            <>
              {inputPage.items.map((input) => (
                <article key={input.id}>
                  <strong>{input.authorName}</strong>
                  {input.externalActorId && (
                    <small>External actor {input.externalActorId}</small>
                  )}
                  {input.authorKind === 'external' &&
                    !input.externalActorId && <small>External context</small>}
                  <p>{input.content}</p>
                </article>
              ))}
            </>
          )}
          {(inputs.canBack ||
            inputPage?.hasMore ||
            (inputPage && task.inputRevision > inputPage.through)) && (
            <div className="app-actions">
              {inputs.canBack && (
                <Button onClick={inputs.back}>Previous Inputs</Button>
              )}
              {inputPage?.hasMore && (
                <Button
                  onClick={() =>
                    inputs.next(
                      {
                        after: inputPage.nextAfter!,
                        through: inputPage.through,
                      },
                      frozenCursor,
                    )
                  }
                >
                  Next Inputs
                </Button>
              )}
              {inputPage && task.inputRevision > inputPage.through && (
                <Button
                  onClick={() =>
                    inputs.next({ after: inputPage.through }, frozenCursor)
                  }
                >
                  View New Inputs
                </Button>
              )}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
