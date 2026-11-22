import { GitHubDeliveryStatus } from '../github-delivery/GitHubDeliveryStatus';
import { useEffect, useState } from 'react';
import { Button, type DesktopAPI, type MenuItem } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useRead } from '../../shared/reads/useRead';
import { GoalHeading } from '../../shared/content/GoalHeading';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy } from '../automation/api';
import { Report } from '../workspace/Report';
import { WorkItemActivity } from './WorkItemActivity';
import { WorkItemRecords } from './WorkItemRecords';
import { WorkItemResult } from './WorkItemResult';
import { WorkItemActions } from './WorkItemActions';
import { useWorkItemOperation } from './useWorkItemOperation';
import type { WorkItem } from './api';
import { confirmedWorkAction, type Pending } from './workRequestRecovery';
import {
  workItemOverviewFallback,
  workItemQuestion,
  workItemStatus,
} from './presentation';

function stamp(value: string) {
  return value ? new Date(value).toLocaleString() : '';
}

export function WorkItemDetail({
  id,
  active = true,
  desktop,
  initialRequest,
  members,
  policies,
  contextReady,
  defaultAgentId,
  availability,
  onBack,
  onOpenTask,
  onViewProgress,
  onOpenRoom,
  onProjectSettings,
}: {
  id: string;
  active?: boolean;
  desktop: DesktopAPI;
  initialRequest?: number;
  members: Agent[];
  policies: AutomationPolicy[];
  contextReady: boolean;
  defaultAgentId?: string;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  onBack: () => void;
  onOpenTask: (id: string) => void;
  onViewProgress?: (taskId: string, runId: string) => void;
  onOpenRoom?: () => void;
  onProjectSettings?: () => void;
}) {
  const { user } = useAccount();
  const [item, setItem] = useState<WorkItem | null>(null);
  const itemRead = useRead<{ item: WorkItem }>(
    `/work-items/${encodeURIComponent(id)}`,
    { enabled: active },
  );
  const error = itemRead.error;
  const [retry, setRetry] = useState(0);
  const [viewSelection, setViewSelection] = useState({
    request: initialRequest,
    view: 'overview',
    section: 'allowance',
  });
  const view =
    viewSelection.request === initialRequest ? viewSelection.view : 'overview';
  const managementSection = viewSelection.section;

  function setView(view: string) {
    setViewSelection((current) => ({
      ...current,
      request: initialRequest,
      view,
    }));
  }

  function saved(next: WorkItem, request: Pending) {
    const action = confirmedWorkAction(request);
    if (
      action &&
      action !== 'unfollow' &&
      action !== 'retry_delivery' &&
      action !== 'repair_delivery' &&
      (action !== 'authorize' ||
        (pausedTaskId &&
          next.activeTaskId === pausedTaskId &&
          next.budget.maxModelRequests > next.usage.reservedModelRequests))
    )
      setView('overview');
    setItem((current) =>
      !current || next.revision >= current.revision ? next : current,
    );
    setRetry((value) => value + 1);
    void itemRead.refresh();
  }

  const operation = useWorkItemOperation(`decision:${id}`, saved, [
    `allowance:${id}`,
  ]);
  useEffect(() => {
    const item = itemRead.data?.item;
    if (!item) return;
    setItem((current) =>
      !current || item.revision >= current.revision ? item : current,
    );
  }, [itemRead.data]);
  const owner = item?.ownerUserId === user.id;
  const editable = item && (owner || user.role === 'admin');
  const closing =
    item && ['completed', 'cancelled', 'stopping'].includes(item.status);
  const selectedView =
    (!editable || closing) && view === 'manage' ? 'overview' : view;
  const question = item && workItemQuestion(item);
  const summary = item?.execution?.summary;
  const displayStatus = item ? workItemStatus(item, false) : '';
  const inspectDelivery =
    selectedView === 'overview' &&
    item?.status === 'needs_attention' &&
    !item.activeTaskId &&
    item.githubDelivery?.status === 'blocked' &&
    Boolean(item.githubDelivery.reason) &&
    ['checks', 'review', 'deployment'].includes(item.githubDelivery.stage);
  const showStatus = !question && !inspectDelivery;
  const due = item?.dueAt
    ? `Due ${item.dueAt.length === 10 ? item.dueAt : new Date(item.dueAt).toLocaleDateString()}`
    : '';
  const waitingTimer = item?.waitCondition
    ? item.timers.find((timer) => timer.status === 'pending')
    : undefined;
  const execution = item?.execution;
  const pausedTaskId =
    !closing &&
    execution &&
    execution.taskId === item?.activeTaskId &&
    !execution.activeRunId &&
    ['needs_attention', 'interrupted'].includes(execution.status)
      ? execution.taskId
      : '';
  const workAllowanceUsed = Boolean(
    pausedTaskId &&
    item &&
    item.usage.modelRequests >= item.budget.maxModelRequests,
  );
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

  function manage(section: string) {
    setViewSelection({ request: initialRequest, view: 'manage', section });
  }

  function more(button: HTMLButtonElement) {
    const items: MenuItem[] = [];
    if (editable && !closing) {
      items.push(
        { label: 'Allowance and access…', action: () => manage('allowance') },
        {
          label: 'Completion…',
          disabled: Boolean(item?.activeTaskId),
          action: () => manage('completion'),
        },
        {
          label: item?.policyId ? 'Schedule follow-up…' : 'Set reminder…',
          disabled: Boolean(item?.activeTaskId),
          action: () => manage('reminder'),
        },
        { label: 'Add context…', action: () => manage('context') },
        { label: 'Following…', action: () => manage('following') },
        ...(policies.some(
          (policy) =>
            policy.enabled &&
            policy.mode === 'auto' &&
            policy.ownerUserId === user.id,
        ) || item?.policyId
          ? [
              {
                label: 'Automatic follow-up…',
                disabled: !contextReady,
                action: () => manage('automatic'),
              },
            ]
          : []),
        {
          label: item?.activeTaskId ? 'Stop work…' : 'Stop following up…',
          action: () => manage('stop'),
        },
      );
    }
    if (editable && !closing && item && !item.activeTaskId) {
      if (item.result && question?.kind !== 'completion')
        items.push({
          label: 'Request changes…',
          action: () => manage('changes'),
        });
      if (
        question?.kind !== 'completion' &&
        (!item.result || item.completionMode !== 'human')
      )
        items.push({ label: 'Mark done…', action: () => manage('complete') });
    }
    if (editable && !closing && question && !item?.activeTaskId) {
      items.push({
        label: 'Answer and start work…',
        action: () => manage('continue'),
      });
      if (question.kind === 'completion')
        items.push({ label: 'Reply…', action: () => manage('reply') });
    }
    const delivery = item?.githubDelivery;
    if (
      delivery?.pullRequestUrl &&
      (delivery.releaseUrl || delivery.deploymentUrl) &&
      delivery.pullRequestUrl.startsWith('https://')
    )
      items.push({
        label: 'Open pull request',
        action: () => {
          window.open(delivery.pullRequestUrl, '_blank', 'noopener,noreferrer');
        },
      });
    if (
      delivery?.releaseUrl &&
      delivery.deploymentUrl &&
      delivery.releaseUrl.startsWith('https://')
    )
      items.push({
        label: 'Open release',
        action: () => {
          window.open(delivery.releaseUrl, '_blank', 'noopener,noreferrer');
        },
      });
    items.push({ label: 'Records…', action: () => setView('records') });
    if (onOpenRoom) items.push({ label: 'Open Room', action: onOpenRoom });
    items.push({
      label: 'Refresh',
      action: () => {
        setRetry((value) => value + 1);
        void itemRead.refresh();
      },
    });
    const rect = button.getBoundingClientRect();
    desktop.contextMenu(items, { x: rect.left, y: rect.bottom });
  }

  return (
    <section aria-label="Work item detail" className="work-item-detail">
      <div className="workspace-panel-heading">
        <Button
          onClick={
            selectedView === 'overview' ? onBack : () => setView('overview')
          }
        >
          {selectedView === 'overview' ? 'Back to Work' : 'Back to item'}
        </Button>
        <div className="app-actions">
          {selectedView !== 'activity' && (
            <Button onClick={() => setView('activity')}>Activity</Button>
          )}
          <Button
            aria-haspopup="menu"
            aria-label="More work actions"
            onClick={(event) => more(event.currentTarget)}
          >
            More
          </Button>
        </div>
      </div>
      {error && (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button
            onClick={() => {
              setRetry((value) => value + 1);
              void itemRead.refresh();
            }}
          >
            Retry work
          </Button>
        </p>
      )}
      {!item && !error && <p role="status">Loading work…</p>}
      {operation.feedback}
      {item && (
        <>
          <header className="work-item-heading">
            <GoalHeading goal={item.goal} />
            {(showStatus || due || (progress && onViewProgress)) && (
              <div className="work-item-state">
                {(showStatus || due) && (
                  <p>
                    {showStatus && displayStatus}
                    {showStatus &&
                      item.execution?.currentAgentName &&
                      item.activeTaskId &&
                      ` · ${item.execution.currentAgentName}`}
                    {due && (showStatus ? ` · ${due}` : due)}
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
          <div className="work-item-content" aria-label="Work details">
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
              {editable && pausedTaskId && (
                <div className="app-actions">
                  <Button
                    primary
                    onClick={() =>
                      workAllowanceUsed
                        ? manage('allowance')
                        : onOpenTask(pausedTaskId)
                    }
                  >
                    {workAllowanceUsed
                      ? 'Increase allowance…'
                      : 'Review and continue…'}
                  </Button>
                </div>
              )}
              {!question && !item.githubDelivery && item.waitCondition && (
                <p className="work-item-wait">
                  {item.waitCondition}
                  {waitingTimer &&
                    ` · ${item.policyId ? 'Follow-up' : 'Reminder'} ${stamp(waitingTimer.dueAt)}`}
                </p>
              )}
              {!question && summary && summary !== item.result?.summary && (
                <Report
                  content={summary}
                  stateKey={`work-update:${item.id}:${item.activeTaskId}`}
                />
              )}
              {item.githubDelivery &&
                (item.githubDelivery.stage !== 'result' ||
                  item.githubDelivery.status === 'blocked') && (
                  <GitHubDeliveryStatus
                    delivery={item.githubDelivery}
                    operation={operation}
                    workRevision={item.revision}
                    workId={item.id}
                    editable={owner}
                    closing={Boolean(closing)}
                    onReviewScope={() => manage('allowance')}
                    onProjectSettings={onProjectSettings}
                    canRepair={
                      !closing &&
                      !item.activeTaskId &&
                      item.completionMode === 'automatic' &&
                      (item.githubDelivery.repairs ?? 0) < 3 &&
                      item.usage.tasks < item.budget.maxTasks &&
                      item.usage.reservedModelRequests <
                        item.budget.maxModelRequests &&
                      item.usage.actions < item.budget.maxActions &&
                      availability?.available !== false
                    }
                  />
                )}
              {(!question || question.kind === 'completion') &&
                !earlierResult && <WorkItemResult item={item} />}
              {question && !editable && <p>{question.text}</p>}
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
              readsActive={active}
              members={members}
              policies={policies}
              contextReady={contextReady}
              defaultAgentId={defaultAgentId}
              availability={availability}
              operation={operation}
              view={selectedView}
              onViewChange={setView}
              managementSection={managementSection}
              onManage={manage}
              readOnly={!editable || Boolean(closing)}
            />
            <div
              hidden={selectedView !== 'records'}
              className="work-item-records"
            >
              <WorkItemRecords
                item={item}
                active={active && selectedView === 'records'}
                hasQuestion={Boolean(question)}
                earlierResult={earlierResult}
                refreshKey={retry}
                onOpenTask={onOpenTask}
              />
            </div>
            <WorkItemActivity
              itemId={id}
              active={active && selectedView === 'activity'}
              refreshKey={retry}
              onOpenTask={onOpenTask}
            />
          </div>
        </>
      )}
    </section>
  );
}
