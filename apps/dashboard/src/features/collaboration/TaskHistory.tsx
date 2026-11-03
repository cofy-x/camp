import { useCallback, useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { statusLabel } from '../workspace/api';
import {
  tasksApi,
  type Task,
  type TaskInputPage,
  type TaskRunPage,
} from './api';

// Keep one bounded page and its navigation anchors. Live updates refresh the
// same range, without replacing the user's location or the parent write form.
function useHistoryPage<T, C>(
  initial: C,
  load: (cursor: C, signal: AbortSignal) => Promise<T>,
  active: boolean,
  revision: number,
  freeze?: (cursor: C, page: T) => C,
) {
  const [cursor, setCursor] = useState(initial);
  const [previous, setPrevious] = useState<C[]>([]);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<{ cursor: C; page?: T; error: string }>({
    cursor: initial,
    error: '',
  });
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void load(cursor, controller.signal)
      .then((page) => {
        if (!controller.signal.aborted) {
          const frozen = freeze?.(cursor, page) ?? cursor;
          setState({ cursor: frozen, page, error: '' });
          if (frozen !== cursor) setCursor(frozen);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState((current) => ({
            cursor,
            page: current.cursor === cursor ? current.page : undefined,
            error:
              error instanceof Error
                ? error.message
                : 'Could not load history.',
          }));
      });
    return () => controller.abort();
  }, [active, cursor, load, revision, retry, freeze]);
  return {
    cursor,
    page: state.cursor === cursor ? state.page : undefined,
    error: state.cursor === cursor ? state.error : '',
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
      setRetry((value) => value + 1);
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
  const loadInputs = useCallback(
    (cursor: InputCursor, signal: AbortSignal) =>
      tasksApi.inputs(task.id, cursor.after, cursor.through, signal),
    [task.id],
  );
  const loadRuns = useCallback(
    (cursor: string, signal: AbortSignal) =>
      tasksApi.runs(task.id, cursor, signal),
    [task.id],
  );
  const inputs = useHistoryPage(
    firstInput,
    loadInputs,
    active && task.inputRevision > 0,
    task.revision,
    freezeInput,
  );
  const runs = useHistoryPage<TaskRunPage, string>(
    '',
    loadRuns,
    active,
    task.revision,
  );
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
            <ol>
              {runs.page.items.map((run) => (
                <li key={run.id}>
                  <Button onClick={() => onInspect(run.id)}>
                    {run.agentName} · {statusLabel[run.status]}
                  </Button>
                  <small>
                    {run.runnerName}
                    {run.checksDeferred
                      ? ' · Checks deferred until finish'
                      : ''}
                  </small>
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
