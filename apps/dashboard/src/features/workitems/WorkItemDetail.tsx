import { useEffect, useState } from 'react';
import { Button, Select, Tabs } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy } from '../automation/api';
import { AssistantContent } from '../workspace/AssistantContent';
import { EvidenceRecord } from './WorkItemEvidence';
import { WorkItemHistory } from './WorkItemHistory';
import { WorkItemActions } from './WorkItemActions';
import { useWorkItemOperation } from './useWorkItemOperation';
import { decisionLabel, workItemsApi, type WorkItem } from './api';
import { workItemOverviewFallback, workItemStatus } from './presentation';

function stamp(value: string) {
  return value ? new Date(value).toLocaleString() : '';
}

function Result({ item }: { item: WorkItem }) {
  const team = useTeam();
  const result = item.result;
  if (!result) return null;
  const onlyFile = result.files.length === 1 ? result.files[0] : undefined;
  const directFile =
    onlyFile &&
    onlyFile.status !== 'D' &&
    onlyFile.status !== 'deleted' &&
    !result.filesLimited;

  function open(path?: string) {
    if (!result) return;
    void team.navigate({
      app: 'library',
      resourceId: result.resourceId,
      commit: result.commit,
      path,
      contextRoomId: item.roomId,
    });
  }

  return (
    <section className="work-item-result" aria-label="Work result">
      {result.commit && (
        <>
          <div className="work-item-file-heading">
            <span>{result.published ? 'Published to Room' : 'Team draft'}</span>
            {!directFile && <Button onClick={() => open()}>Open files</Button>}
          </div>
          <ul className="work-item-files">
            {result.files.map((file) => (
              <li key={file.path}>
                {file.status === 'D' || file.status === 'deleted' ? (
                  <span>{file.path}</span>
                ) : (
                  <Button onClick={() => open(file.path)}>{file.path}</Button>
                )}
                <span>
                  {file.status === 'D' || file.status === 'deleted'
                    ? 'Deleted'
                    : file.status === 'A' || file.status === 'added'
                      ? 'Added'
                      : 'Changed'}
                </span>
              </li>
            ))}
          </ul>
          {result.filesLimited && (
            <p>More files are available in Open files.</p>
          )}
        </>
      )}
      {result.summary && <AssistantContent content={result.summary} />}
    </section>
  );
}

export function WorkItemDetail({
  id,
  initialRequest,
  members,
  policies,
  defaultAgentId,
  availability,
  onBack,
  onOpenTask,
  onViewProgress,
  onOpenRoom,
}: {
  id: string;
  initialRequest?: number;
  members: Agent[];
  policies: AutomationPolicy[];
  defaultAgentId?: string;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  onBack: () => void;
  onOpenTask: (id: string) => void;
  onViewProgress?: (taskId: string, runId: string) => void;
  onOpenRoom?: () => void;
}) {
  const { user } = useAccount();
  const team = useTeam();
  const [item, setItem] = useState<WorkItem | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  const [viewSelection, setViewSelection] = useState({
    request: initialRequest,
    view: 'overview',
  });
  const view =
    viewSelection.request === initialRequest ? viewSelection.view : 'overview';
  const [history, setHistory] = useState('decisions');

  function setView(view: string) {
    setViewSelection({ request: initialRequest, view });
  }

  function saved(next: WorkItem) {
    setItem((current) =>
      !current || next.revision >= current.revision ? next : current,
    );
    setRetry((value) => value + 1);
  }

  const operation = useWorkItemOperation(`decision:${id}`, saved, [
    `allowance:${id}`,
  ]);
  useEffect(() => {
    const controller = new AbortController();
    void workItemsApi
      .item(id, controller.signal)
      .then(({ item }) => {
        if (controller.signal.aborted) return;
        setItem((current) =>
          !current || item.revision >= current.revision ? item : current,
        );
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not read this work.',
          );
      });
    return () => controller.abort();
  }, [id, team.revision, retry]);
  const editable =
    item && (item.ownerUserId === user.id || user.role === 'admin');
  const terminal = item && ['completed', 'cancelled'].includes(item.status);
  const selectedView =
    (!editable || terminal || item?.status === 'stopping') && view === 'manage'
      ? 'overview'
      : view;
  const hasQuestion =
    item &&
    Boolean(
      item.question ||
      (item.execution?.question && !item.execution.question.answer),
    );
  const summary = item?.execution?.summary;
  const displayStatus = item ? workItemStatus(item) : '';
  const earlierResult = Boolean(
    item?.result &&
    item.activeTaskId &&
    item.result.taskId !== item.activeTaskId,
  );
  const progress =
    item?.activeTaskId &&
    item.execution?.activeRunId &&
    item.execution.taskId === item.activeTaskId
      ? item.execution
      : null;
  const overviewFallback = item
    ? workItemOverviewFallback(item, Boolean(editable))
    : null;
  return (
    <section aria-label="Work item detail" className="work-item-detail">
      <div className="workspace-panel-heading">
        <Button onClick={onBack}>Back to Work</Button>
        <div className="app-actions">
          {onOpenRoom && <Button onClick={onOpenRoom}>Open Room</Button>}
          <Button onClick={() => setRetry((value) => value + 1)}>
            {error ? 'Retry work' : 'Refresh'}
          </Button>
        </div>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {!item && !error && <p role="status">Loading work…</p>}
      {operation.feedback}
      {item && (
        <>
          <header className="work-item-heading">
            <h3>{item.goal}</h3>
            {(!hasQuestion || item.dueAt || (progress && onViewProgress)) && (
              <div className="work-item-state">
                {!hasQuestion && (
                  <p>
                    {displayStatus}
                    {item.execution?.currentAgentName && item.activeTaskId
                      ? ` · ${item.execution.currentAgentName}`
                      : ''}
                    {item.dueAt
                      ? ` · Due ${item.dueAt.length === 10 ? item.dueAt : new Date(item.dueAt).toLocaleDateString()}`
                      : ''}
                  </p>
                )}
                {hasQuestion && item.dueAt && (
                  <p>
                    Due{' '}
                    {item.dueAt.length === 10
                      ? item.dueAt
                      : new Date(item.dueAt).toLocaleDateString()}
                  </p>
                )}
                {progress && onViewProgress && (
                  <Button
                    onClick={() =>
                      onViewProgress(progress.taskId, progress.activeRunId)
                    }
                  >
                    View progress
                  </Button>
                )}
              </div>
            )}
          </header>
          <Tabs
            label="Work details"
            value={selectedView}
            onChange={setView}
            tabs={[
              { id: 'overview', label: 'Overview' },
              { id: 'activity', label: 'Activity' },
              ...(editable && !terminal
                ? [
                    {
                      id: 'manage',
                      label: 'Manage',
                      disabled: item.status === 'stopping',
                    },
                  ]
                : []),
            ]}
          >
            <div
              hidden={selectedView !== 'overview'}
              className="work-item-overview"
            >
              {item.status === 'stopping' && (
                <p role="status">
                  Stopping the current work. Future follow-ups are disabled.
                </p>
              )}
              {item.execution?.error && (
                <p className="app-error" role="alert">
                  {item.execution.error}
                </p>
              )}
              {editable &&
                item.status !== 'stopping' &&
                item.activeTaskId &&
                !item.execution?.activeRunId &&
                ['needs_attention', 'interrupted'].includes(
                  item.execution?.status ?? '',
                ) && (
                  <div className="app-actions">
                    <Button
                      primary
                      onClick={() => onOpenTask(item.activeTaskId!)}
                    >
                      Review and continue…
                    </Button>
                  </div>
                )}
              {!hasQuestion && item.waitCondition && (
                <p className="work-item-wait">
                  {item.waitCondition}
                  {item.timers.find((timer) => timer.status === 'pending')
                    ? ` · Reminder ${stamp(item.timers.find((timer) => timer.status === 'pending')!.dueAt)}`
                    : ''}
                </p>
              )}
              {!hasQuestion && summary && summary !== item.result?.summary && (
                <AssistantContent content={summary} />
              )}
              {!hasQuestion && !earlierResult && <Result item={item} />}
              {hasQuestion && (!editable || terminal) && (
                <p>{item.execution?.question?.text ?? item.question}</p>
              )}
              {overviewFallback && (
                <div className="work-item-overview-record">
                  <p>
                    {overviewFallback.label}
                    {overviewFallback.createdAt && (
                      <>
                        {' · '}
                        <time dateTime={overviewFallback.createdAt}>
                          {stamp(overviewFallback.createdAt)}
                        </time>
                      </>
                    )}
                  </p>
                  {overviewFallback.note && <p>{overviewFallback.note}</p>}
                </div>
              )}
            </div>
            <WorkItemActions
              key={item.id}
              item={item}
              members={members}
              policies={policies}
              defaultAgentId={defaultAgentId}
              availability={availability}
              operation={operation}
              view={selectedView}
              onViewChange={setView}
              readOnly={!editable || Boolean(terminal)}
            />
            <div
              hidden={selectedView !== 'activity'}
              className="work-item-records"
            >
              {(hasQuestion || earlierResult) && item.result && (
                <details>
                  <summary>Files and earlier progress</summary>
                  <Result item={item} />
                </details>
              )}
              {item.completionCriteria && (
                <details>
                  <summary>Complete when</summary>
                  <p>{item.completionCriteria}</p>
                </details>
              )}
              <Select
                label="Show activity"
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
                active={selectedView === 'activity' && history === 'evidence'}
                title="Context"
                initial={item.evidence}

                refreshKey={retry}
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
                active={selectedView === 'activity' && history === 'tasks'}
                title="Work sessions"
                initial={item.tasks}

                refreshKey={retry}
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
                active={selectedView === 'activity' && history === 'timers'}
                title="Reminders"
                initial={item.timers}
                refreshKey={retry}
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
                active={selectedView === 'activity' && history === 'actions'}
                title="External actions"
                initial={item.actions}
                refreshKey={retry}
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
                active={selectedView === 'activity' && history === 'decisions'}
                title="Decisions"
                initial={item.decisions}
                refreshKey={retry}
                render={(decision) => (
                  <div className="work-item-record" key={decision.id}>
                    <strong>
                      {decisionLabel[decision.kind] ?? decision.kind}
                    </strong>
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
            </div>
          </Tabs>
        </>
      )}
    </section>
  );
}
