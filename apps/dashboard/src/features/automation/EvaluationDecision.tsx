import { useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { automationApi, type Evaluation } from './api';
import { taskStatus, type Task } from '../collaboration/api';

export function EvaluationDecision({
  id,
  onOpenTask,
}: {
  id: string;
  onOpenTask: (id: string) => void;
}) {
  const team = useTeam();
  const [open, setOpen] = useState(false);
  const [evaluation, setEvaluation] = useState<Evaluation | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void automationApi
      .evaluation(id, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) {
          setEvaluation(result);
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read this assessment.',
          );
      });
    return () => controller.abort();
  }, [id, open, retry, team.revision]);
  const assessment = evaluation?.assessment;
  return (
    <details
      onToggle={(event) => setOpen(event.currentTarget.open)}
      className="automation-evaluation"
    >
      <summary>Assessment decision</summary>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setRetry((value) => value + 1)}>
            Retry Assessment
          </Button>
        </p>
      )}
      {open && !evaluation && !error && (
        <p role="status">Loading assessment…</p>
      )}
      {evaluation && (
        <>
          <dl className="automation-facts">
            <dt>Decision</dt>
            <dd>
              {evaluation.status === 'needs_attention'
                ? 'Needs attention. No collaboration started.'
                : assessment?.outcome === 'accept'
                  ? evaluation.status === 'observed'
                    ? 'Would start a collaboration in Auto.'
                    : 'Collaboration started.'
                  : assessment?.outcome === 'ignore'
                    ? 'No new work.'
                    : assessment?.outcome === 'unavailable'
                      ? 'Assessment unavailable.'
                      : 'Assessing…'}
            </dd>
            <dt>Policy</dt>
            <dd>
              Revision {evaluation.policy.revision} ·{' '}
              {evaluation.policy.mode === 'auto' ? 'Auto' : 'Observe'}
            </dd>
          </dl>
          {assessment?.goal && (
            <>
              <h4>Proposed goal</h4>
              <p>{assessment.goal}</p>
            </>
          )}
          {assessment?.reason && (
            <>
              <p>{assessment.reason}</p>
            </>
          )}
          <h4>Rules assessed</h4>
          <pre className="automation-event-content">
            {evaluation.policy.rules}
          </pre>
          {(evaluation.tasks.length > 0 || evaluation.tasksOmitted) && (
            <>
              <h4>Related collaborations</h4>
              {evaluation.tasksOmitted && (
                <p>
                  Showing {evaluation.tasks.length} of {evaluation.totalTasks}{' '}
                  related collaborations.
                </p>
              )}
              <ul>
                {evaluation.tasks.map((task) => (
                  <li key={task.id}>
                    <Button onClick={() => onOpenTask(task.id)}>
                      {task.goal}
                    </Button>{' '}
                    · {taskStatus[task.status as Task['status']] ?? task.status}
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </details>
  );
}
