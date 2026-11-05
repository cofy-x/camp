import { useEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { automationApi, type Evaluation } from './api';
import { taskStatus, type Task } from '../collaboration/api';
import { decisionLabel, workStatus } from '../workitems/api';
import { EvaluationContext } from './EvaluationContext';
import { memorySourceLabel } from '../memory/api';

export function EvaluationDecision({
  id,
  onOpenTask,
  onOpenWorkItem,
}: {
  id: string;
  onOpenTask: (id: string) => void;
  onOpenWorkItem?: (id: string) => void;
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
                : assessment?.outcome === 'accept' ||
                    assessment?.outcome === 'execute'
                  ? evaluation.status === 'observed'
                    ? 'Would start a collaboration in Auto.'
                    : 'Collaboration started.'
                  : assessment?.outcome === 'ignore'
                    ? 'No new work.'
                    : assessment?.outcome === 'unavailable'
                      ? 'Assessment unavailable.'
                      : assessment &&
                          ['wait', 'request_judgment', 'complete'].includes(
                            assessment.outcome,
                          )
                        ? `${decisionLabel[assessment.outcome]}${evaluation.status === 'observed' ? ' (observation only)' : ''}`
                        : 'Assessing…'}
            </dd>
            <dt>Policy</dt>
            <dd>
              Revision {evaluation.policy.revision} ·{' '}
              {evaluation.policy.mode === 'auto' ? 'Auto' : 'Observe'}
            </dd>
            <dt>Snapshot</dt>
            <dd>{new Date(evaluation.createdAt).toLocaleString()}</dd>
            {assessment?.goal && (
              <>
                <dt>Goal</dt>
                <dd>{assessment.goal}</dd>
              </>
            )}
            {assessment?.completionCriteria && (
              <>
                <dt>Complete when</dt>
                <dd>{assessment.completionCriteria}</dd>
              </>
            )}
            {assessment?.waitCondition && (
              <>
                <dt>Waiting</dt>
                <dd>
                  {assessment.waitCondition}
                  {assessment.resumeAt
                    ? ` · ${new Date(assessment.resumeAt).toLocaleString()}`
                    : ''}
                </dd>
              </>
            )}
            {assessment?.question && (
              <>
                <dt>Question</dt>
                <dd>{assessment.question}</dd>
              </>
            )}
          </dl>
          {assessment?.reason && <p>{assessment.reason}</p>}
          <details>
            <summary>Rules assessed</summary>
            <pre className="automation-event-content">
              {evaluation.policy.rules}
            </pre>
          </details>
          {Boolean(
            evaluation.workItems?.length || evaluation.workItemsOmitted,
          ) && (
            <EvaluationContext
              evaluationId={id}
              kind="work-items"
              initial={evaluation.workItems ?? []}
              total={
                evaluation.totalWorkItems ?? evaluation.workItems?.length ?? 0
              }
              title="Work items assessed"
              render={(work) => (
                <div className="work-item-record" key={work.id}>
                  <strong>{work.goal}</strong>
                  <p>
                    {workStatus[work.status] ?? work.status} · Revision{' '}
                    {work.revision}
                  </p>
                  <p>Complete when: {work.completionCriteria}</p>
                  {work.contextLimited && (
                    <p>Earlier results or decisions were omitted.</p>
                  )}
                  {Boolean(work.recentTasks?.length) && (
                    <details>
                      <summary>Execution results assessed</summary>
                      {work.recentTasks?.map((task) => (
                        <div key={task.id} className="work-item-record">
                          <strong>
                            {taskStatus[task.status as Task['status']] ??
                              task.status}
                          </strong>
                          <p>{task.summary}</p>
                          <Button onClick={() => onOpenTask(task.id)}>
                            Open Collaboration
                          </Button>
                        </div>
                      ))}
                    </details>
                  )}
                  {Boolean(work.recentDecisions?.length) && (
                    <details>
                      <summary>Earlier decisions assessed</summary>
                      {work.recentDecisions?.map((decision) => (
                        <div key={decision.id} className="work-item-record">
                          <strong>
                            {decisionLabel[decision.kind] ?? decision.kind}
                          </strong>
                          <p>{decision.reason}</p>
                        </div>
                      ))}
                    </details>
                  )}
                  {Boolean(work.actions?.length || work.actionsLimited) && (
                    <details>
                      <summary>Action receipts assessed</summary>
                      {work.actionsLimited && (
                        <p>Earlier actions were omitted.</p>
                      )}
                      {work.actions?.map((action) => (
                        <p key={action.id}>
                          {action.kind} · {action.status}
                          {action.createdAt
                            ? ` · ${new Date(action.createdAt).toLocaleString()}`
                            : ''}
                        </p>
                      ))}
                    </details>
                  )}
                  {onOpenWorkItem && (
                    <Button onClick={() => onOpenWorkItem(work.id)}>
                      Open Current Work Item
                    </Button>
                  )}
                </div>
              )}
            />
          )}
          {Boolean(
            evaluation.memories?.length || evaluation.memoriesOmitted,
          ) && (
            <EvaluationContext
              evaluationId={id}
              kind="memories"
              initial={evaluation.memories ?? []}
              total={
                evaluation.totalMemories ?? evaluation.memories?.length ?? 0
              }
              title="Memory assessed"
              render={(memory) => (
                <div
                  className="work-item-record"
                  key={`${memory.id}:${memory.revision}`}
                >
                  <pre>{memory.content}</pre>
                  <small>
                    Revision {memory.revision} ·{' '}
                    {memorySourceLabel[memory.provenance.kind]}
                  </small>
                  <details>
                    <summary>Memory data</summary>
                    <dl className="automation-facts">
                      <dt>Memory</dt>
                      <dd>{memory.id}</dd>
                      <dt>Author</dt>
                      <dd>{memory.authorUserId}</dd>
                      <dt>Source</dt>
                      <dd>
                        {memory.provenance.sourceEventId ??
                          memory.provenance.messageId ??
                          memory.provenance.path ??
                          'Human note'}
                      </dd>
                      {memory.provenance.commit && (
                        <>
                          <dt>File revision</dt>
                          <dd>{memory.provenance.commit}</dd>
                        </>
                      )}
                    </dl>
                  </details>
                </div>
              )}
            />
          )}
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
