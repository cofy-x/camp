import { useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { EvidenceRecord } from './WorkItemEvidence';
import { WorkItemHistory } from './WorkItemHistory';
import { WorkItemResult } from './WorkItemResult';
import { decisionLabel, type WorkItem } from './api';

function stamp(value: string) {
  return value ? new Date(value).toLocaleString() : '';
}

export function WorkItemRecords({
  item,
  active,
  earlierResult,
  hasQuestion,
  refreshKey,
  onOpenTask,
}: {
  item: WorkItem;
  active: boolean;
  earlierResult: boolean;
  hasQuestion: boolean;
  refreshKey: number;
  onOpenTask: (id: string) => void;
}) {
  const [history, setHistory] = useState('decisions');
  return (
    <section aria-label="Work records" hidden={!active}>
      {(hasQuestion || earlierResult) && item.result && (
        <details>
          <summary>Files and earlier progress</summary>
          <WorkItemResult item={item} />
        </details>
      )}
      {item.completionCriteria && (
        <details>
          <summary>Complete when</summary>
          <p>{item.completionCriteria}</p>
        </details>
      )}
      <Select
        label="Show records"
        value={history}
        onChange={(event) => setHistory(event.target.value)}
      >
        <option value="decisions">Decisions</option>
        <option value="evidence">Context</option>
        <option value="tasks">Work sessions</option>
        <option value="timers">Reminders</option>
        <option value="actions">External actions</option>
      </Select>
      <WorkItemHistory
        itemId={item.id}
        kind="evidence"
        active={active && history === 'evidence'}
        title="Context"
        initial={item.evidence}

        refreshKey={refreshKey}
        render={(evidence) => (
          <EvidenceRecord
            key={evidence.eventId}
            id={evidence.eventId}
            label={`Source event · ${stamp(evidence.createdAt)}`}
          />
        )}
      />
      <WorkItemHistory
        itemId={item.id}
        kind="tasks"
        active={active && history === 'tasks'}
        title="Work sessions"
        initial={item.tasks}

        refreshKey={refreshKey}
        render={(task) => (
          <div className="work-item-record" key={task.id}>
            <div className="automation-heading">
              <strong>{task.status}</strong>
              <Button onClick={() => onOpenTask(task.id)}>
                Open execution
              </Button>
            </div>
            {task.summary && <p>{task.summary}</p>}
            <small>{stamp(task.createdAt)}</small>
          </div>
        )}
      />
      <WorkItemHistory
        itemId={item.id}
        kind="timers"
        active={active && history === 'timers'}
        title="Reminders"
        initial={item.timers}
        refreshKey={refreshKey}
        render={(timer) => (
          <div className="work-item-record" key={timer.id}>
            <strong>
              {timer.status} · {stamp(timer.dueAt)}
            </strong>
            <p>{timer.condition}</p>
            {timer.eventId && <EvidenceRecord id={timer.eventId} />}
          </div>
        )}
      />
      <WorkItemHistory
        itemId={item.id}
        kind="actions"
        active={active && history === 'actions'}
        title="External actions"
        initial={item.actions}
        refreshKey={refreshKey}
        render={(action) => (
          <div className="work-item-record" key={action.id}>
            <strong>
              {action.kind} · {action.status}
            </strong>
            <pre>{action.body}</pre>
            {action.taskId && (
              <Button onClick={() => onOpenTask(action.taskId!)}>
                Open execution
              </Button>
            )}
            <small>{stamp(action.createdAt)}</small>
          </div>
        )}
      />
      <WorkItemHistory
        itemId={item.id}
        kind="decisions"
        active={active && history === 'decisions'}
        title="Decisions"
        initial={item.decisions}
        refreshKey={refreshKey}
        render={(decision) => (
          <div className="work-item-record" key={decision.id}>
            <strong>{decisionLabel[decision.kind] ?? decision.kind}</strong>
            <p>{decision.reason}</p>
            <small>
              {decision.actorKind} · {stamp(decision.createdAt)}
            </small>
            {decision.taskId && (
              <Button onClick={() => onOpenTask(decision.taskId!)}>
                Open execution
              </Button>
            )}
          </div>
        )}
      />
    </section>
  );
}
