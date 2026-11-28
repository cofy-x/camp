import { GitHubDeliveryStatus } from '../github-delivery/GitHubDeliveryStatus';
import { useEffect, useRef, useState } from 'react';
import { Button, type DesktopAPI, type MenuItem } from '@cofy-x/camp-platinum';
import { GoalDisclosure } from '../../shared/content/GoalHeading';
import { goalTitle } from '../../shared/content/goal-title';
import { useAccount } from '../../team/AuthContext';
import { useRead } from '../../shared/reads/useRead';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy } from '../automation/api';
import { Report } from '../workspace/Report';
import { WorkItemActivity } from './WorkItemActivity';
import { WorkItemRecords } from './WorkItemRecords';
import { WorkItemResult } from './WorkItemResult';
import { WorkItemActions } from './WorkItemActions';
import { useWorkItemOperation } from './useWorkItemOperation';
import { workItemDetailPath, type WorkItem } from './api';
import { confirmedWorkAction, type Pending } from './workRequestRecovery';
import {
  canReplaceWorkSnapshot,
  workItemExecutionError,
  workItemOverviewFallback,
  workItemQuestion,
  workItemRecovery,
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
  onReviewRule,
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
  onReviewRule: (sourceId?: string) => void;
  onProjectSettings?: () => void;
}) {
  const { user } = useAccount();
  const itemRead = useRead<{ item: WorkItem }>(workItemDetailPath(id), {
    enabled: active,
  });
  const [acceptedItem, setAcceptedItem] = useState<WorkItem | null>(
    () => itemRead.data?.item ?? null,
  );
  const error = itemRead.error;
  // A permission failure clears the shared read's accepted data. The
  // revision merge below must respect that even with a retained receipt.
  const item = error && !itemRead.data ? null : acceptedItem;
  const [retry, setRetry] = useState(0);
  const backButton = useRef<HTMLButtonElement>(null);
  const [recordCategory, setRecordCategory] = useState('decisions');
  const [viewSelection, setViewSelection] = useState({
    request: initialRequest,
    view: 'overview',
    section: 'allowance',
  });
  const view =
    item && viewSelection.request === initialRequest
      ? viewSelection.view
      : 'overview';
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
    const allowanceRestored =
      view === 'manage' &&
      ['allowance', 'model-allowance'].includes(managementSection) &&
      ((recovery &&
        (recovery.kind === 'increase_allowance' ||
          recovery.kind === 'renew_authorization') &&
        next.workRecovery?.kind !== recovery.kind) ||
        (pausedTaskId &&
          next.activeTaskId === pausedTaskId &&
          next.budget.maxModelRequests > next.usage.reservedModelRequests));
    if (
      action &&
      action !== 'unfollow' &&
      action !== 'retry_delivery' &&
      action !== 'repair_delivery' &&
      (action !== 'authorize' || allowanceRestored)
    )
      setView('overview');
    setAcceptedItem((current) =>
      canReplaceWorkSnapshot(current, next) ? next : current,
    );
    setRetry((value) => value + 1);
    void itemRead.refresh();
  }

  const operation = useWorkItemOperation(`decision:${id}`, saved, [
    `allowance:${id}`,
  ]);
  useEffect(() => {
    const item = itemRead.data?.item;
    if (!item) {
      if (error) setAcceptedItem(null);
      return;
    }
    setAcceptedItem((current) =>
      canReplaceWorkSnapshot(current, item) ? item : current,
    );
  }, [error, itemRead.data]);
  const owner = item?.ownerUserId === user.id;
  const editable = item && (owner || user.role === 'admin');
  const closing =
    item && ['completed', 'cancelled', 'stopping'].includes(item.status);
  const selectedView =
    (!editable || closing) && view === 'manage' ? 'overview' : view;
  const question = item && workItemQuestion(item);
  const title = item ? goalTitle(item.goal) : '';
  const recovery = item && workItemRecovery(item);
  const executionError = item && workItemExecutionError(item);
  const summary = item?.execution?.summary;
  const displayStatus = item ? workItemStatus(item, false) : '';
  const inspectDelivery =
    selectedView === 'overview' &&
    item?.status === 'needs_attention' &&
    !item.activeTaskId &&
    item.githubDelivery?.status === 'blocked' &&
    Boolean(item.githubDelivery.reason) &&
    ['checks', 'review', 'deployment'].includes(item.githubDelivery.stage);
  const showStatus =
    selectedView !== 'activity' &&
    !question &&
    !inspectDelivery &&
    !(recovery && selectedView === 'overview');
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
    !question &&
    item?.activeTaskId &&
    item.execution?.activeRunId &&
    item.execution.taskId === item.activeTaskId
      ? item.execution
      : null;
  const workAgent =
    showStatus && item?.activeTaskId ? execution?.currentAgentName : '';
  const overviewFallback = item
    ? workItemOverviewFallback(item, Boolean(editable))
    : null;

  function openRecords(category?: string) {
    if (category) setRecordCategory(category);
    setView('records');
    backButton.current?.focus();
  }

  function manage(section: string) {
    setViewSelection({ request: initialRequest, view: 'manage', section });
  }

  function openRecoveryView(next: () => void) {
    next();
    backButton.current?.focus({ preventScroll: true });
  }

  function recoveryAction() {
    if (!recovery) {
      if (!editable || !pausedTaskId) return;
      return {
        label: workAllowanceUsed
          ? 'Increase allowance…'
          : 'Review and continue…',
        open: () =>
          workAllowanceUsed
            ? openRecoveryView(() => manage('model-allowance'))
            : onOpenTask(pausedTaskId),
      };
    }
    switch (recovery.kind) {
      case 'review_rule':
      case 'daily_limit':
        return {
          label: 'Review rule…',
          open: () => onReviewRule(recovery.sourceId),
        };
      case 'increase_allowance':
      case 'renew_authorization':
        if (editable)
          return {
            label:
              recovery.kind === 'renew_authorization'
                ? 'Review authorization…'
                : 'Increase allowance…',
            open: () =>
              openRecoveryView(() =>
                manage(workAllowanceUsed ? 'model-allowance' : 'allowance'),
              ),
          };
        return;
      case 'review_execution':
        if (recovery.taskId && recovery.taskId === execution?.taskId) {
          const taskId = recovery.taskId;
          return {
            label:
              editable && taskId === pausedTaskId
                ? 'Review and continue…'
                : 'Review execution',
            open: () => onOpenTask(taskId),
          };
        }
        return {
          label: 'View activity',
          open: () => openRecoveryView(() => setView('activity')),
        };
      case 'review_activity':
        return {
          label: 'View activity',
          open: () => openRecoveryView(() => setView('activity')),
        };
      case 'reminder':
        if (editable && !item?.result)
          return {
            label: 'Start work…',
            open: () => openRecoveryView(() => manage('start')),
          };
        return {
          label: 'View activity',
          open: () => openRecoveryView(() => setView('activity')),
        };
    }
  }

  const recoveryMessage =
    recovery?.message ??
    (item &&
    workAllowanceUsed &&
    executionError?.startsWith('The observed model step limit (')
      ? `This attempt reached its step limit and used all ${item.budget.maxModelRequests} model requests for this Work.`
      : executionError);
  const recoveryControl = question ? undefined : recoveryAction();

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
      if (recovery && !item.result && recoveryControl?.label !== 'Start work…')
        items.push({ label: 'Start work…', action: () => manage('start') });
      if (item.result && question?.kind !== 'completion')
        items.push({
          label: 'Request changes…',
          action: () => manage('changes'),
        });
      if (
        !item.completionWait &&
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
    if (selectedView !== 'records')
      items.push({
        label: 'Records',
        action: () => openRecords(),
      });
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

  const actions = item && (
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
      recoveryActive={Boolean(recovery)}
      readOnly={!editable || Boolean(closing)}
    />
  );
  const leadWithDecision = Boolean(question && selectedView === 'overview');

  return (
    <section aria-label="Work item detail" className="work-item-detail">
      <div className="workspace-panel-heading">
        <Button
          ref={backButton}
          onClick={
            selectedView === 'overview' ? onBack : () => setView('overview')
          }
        >
          {selectedView === 'overview' ? 'Back to Work' : 'Back to item'}
        </Button>
        {item && (
          <div className="app-actions">
            {selectedView !== 'activity' &&
              !(
                selectedView === 'overview' &&
                recoveryControl?.label === 'View activity'
              ) && (
                <Button
                  variant="text"
                  onClick={() => {
                    setView('activity');
                    backButton.current?.focus();
                  }}
                >
                  Activity
                </Button>
              )}
            <Button
              aria-haspopup="menu"
              aria-label="More work actions"
              onClick={(event) => more(event.currentTarget)}
            >
              More
            </Button>
          </div>
        )}
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
      {item && (
        <>
          {operation.feedback}
          <div className="work-item-content" aria-label="Work details">
            {leadWithDecision && actions}
            <header key="goal" className="work-item-heading">
              <div className="work-detail-title-line">
                <h2>{title}</h2>
                {showStatus && (
                  <span
                    className="work-detail-status"
                    data-state={item.status}
                    role="status"
                  >
                    {displayStatus}
                    {workAgent && ` · ${workAgent}`}
                  </span>
                )}
              </div>
              {selectedView !== 'activity' &&
                (due || (progress && onViewProgress)) && (
                  <div className="work-item-state">
                    {due && <p>{due}</p>}
                    {progress && onViewProgress && (
                      <Button
                        variant="text"
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
            <div
              key="overview"
              hidden={selectedView !== 'overview'}
              className="work-item-overview"
            >
              {item.status === 'stopping' && (
                <p role="status">
                  Stopping the current work. Future follow-ups are disabled.
                </p>
              )}
              {(recoveryMessage || recoveryControl) && (
                <div
                  className={
                    recoveryMessage && recovery?.kind !== 'reminder'
                      ? 'work-item-recovery app-error'
                      : 'work-item-recovery'
                  }
                >
                  {recoveryMessage && (
                    <p role={recovery ? 'status' : 'alert'}>
                      {recoveryMessage}
                    </p>
                  )}
                  {recoveryControl && (
                    <Button primary onClick={recoveryControl.open}>
                      {recoveryControl.label}
                    </Button>
                  )}
                </div>
              )}
              {item.completionWait === 'delivery' &&
                (!item.githubDelivery ||
                  ['succeeded', 'stopped'].includes(
                    item.githubDelivery.status,
                  )) && (
                  <Button variant="text" onClick={() => openRecords('actions')}>
                    View delivery records
                  </Button>
                )}
              {!question &&
                !item.githubDelivery &&
                !item.completionWait &&
                item.waitCondition && (
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
                !earlierResult && (
                  <WorkItemResult
                    item={item}
                    active={active && selectedView === 'overview'}
                  />
                )}
              {!recovery && overviewFallback && (
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
            {!leadWithDecision && actions}
            <GoalDisclosure
              goal={item.goal}
              title={title}
              hidden={selectedView === 'records' || selectedView === 'activity'}
            />
            <WorkItemRecords
              item={item}
              category={recordCategory}
              onCategoryChange={setRecordCategory}
              active={active && selectedView === 'records'}
              hasQuestion={Boolean(question)}
              earlierResult={earlierResult}
              refreshKey={retry}
              onOpenTask={onOpenTask}
            />
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
