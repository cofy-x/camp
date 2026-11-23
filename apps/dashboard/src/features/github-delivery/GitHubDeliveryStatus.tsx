import type { WorkOperation } from '../workitems/useWorkItemOperation';
import { useLayoutEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useReadStore } from '../../shared/reads/useRead';
import { useTeam } from '../../team/TeamContext';
import { githubDeliveryApi, type GitHubDelivery } from './api';

const labels: Record<string, string> = {
  result: 'Waiting for the work result',
  prepare: 'Preparing source',
  branch: 'Preparing pull request',
  pull_request: 'Opening pull request',
  update_branch: 'Updating pull request',
  checks: 'Waiting for CI',
  review: 'Waiting for GitHub requirements',
  merge: 'Merging pull request',
  release: 'Publishing release',
  deployment: 'Waiting for deployment',
};

export function GitHubDeliveryStatus({
  delivery,
  workId,
  editable,
  closing = false,
  operation,
  workRevision,
  onReviewScope,
  onProjectSettings,
  canRepair = false,
}: {
  delivery: GitHubDelivery;
  workId: string;
  editable: boolean;
  closing?: boolean;
  operation: WorkOperation;
  workRevision: number;
  onReviewScope?: () => void;
  onProjectSettings?: () => void;
  canRepair?: boolean;
}) {
  const team = useTeam();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const receiptInFlight = useRef(false);
  const region = useRef<HTMLElement>(null);
  const recoveryControl = useRef<HTMLButtonElement>(null);
  const reads = useReadStore();
  const checking =
    delivery.reconcile && ['queued', 'working'].includes(delivery.status);
  const blocked = delivery.status === 'blocked';
  const inspectRun = blocked && delivery.checkUrl;
  const url =
    inspectRun ||
    delivery.deploymentUrl ||
    delivery.releaseUrl ||
    delivery.pullRequestUrl;
  const label = inspectRun
    ? 'Open workflow run'
    : delivery.deploymentUrl
      ? 'Open app'
      : delivery.releaseUrl
        ? 'Open release'
        : 'Open pull request';
  const safeURL = url?.startsWith('https://') ? url : undefined;
  const code = delivery.failureCode;
  const execution = code === 'execution_unavailable';
  const reconnect = code === 'credential' || code === 'connection' || execution;
  const scopeChanged = [
    'project_changed',
    'rule_changed',
    'repository_changed',
  ].includes(code ?? '');
  const hasRepairEvidence =
    Boolean(delivery.repairCause) || code === 'review_changes';
  const allowance =
    [
      'work_budget_exhausted',
      'daily_task_limit',
      'agent_changed',
      'execution_unavailable',
      'work_expired',
    ].includes(code ?? '') ||
    (hasRepairEvidence &&
      ['ci_source_failed', 'review_changes', 'repair_limit'].includes(
        code ?? '',
      ) &&
      (delivery.repairs ?? 0) < 3);
  const canResume =
    !code || ['action_failed', 'connection', 'credential'].includes(code);

  const repairFailure =
    hasRepairEvidence &&
    [
      'ci_source_failed',
      'execution_unavailable',
      'review_changes',
      'repair_limit',
      'work_budget_exhausted',
      'daily_task_limit',
    ].includes(code ?? '');
  const canRecheck =
    ['checks', 'review', 'deployment'].includes(delivery.stage) &&
    ([
      'ci_failed',
      'ci_inspection',
      'deployment_inspection',
      'read_unavailable',
      'check_ended',
    ].includes(code ?? '') ||
      (code === 'repair_limit' &&
        (!hasRepairEvidence || (delivery.repairs ?? 0) >= 3)));

  const editableBlock = editable && !closing && blocked && !delivery.reconcile;
  const showReceiptCheck =
    editable &&
    delivery.reconcile &&
    ['unknown', 'blocked'].includes(delivery.status);
  const showRepair = editableBlock && canRepair && repairFailure;
  const showSettings =
    editableBlock && !showRepair && (reconnect || scopeChanged || allowance);
  const showRetry =
    editableBlock && (canResume || canRecheck) && delivery.stage !== 'result';
  const actionDisabled = pending || operation.blocked;

  useLayoutEffect(() => {
    if (recoveryControl.current?.isConnected !== false) return;
    recoveryControl.current = null;
    const panel = region.current;
    if (
      document.activeElement !== document.body ||
      !panel?.closest('.p9-window.is-active') ||
      panel.closest('[hidden]') ||
      !panel.getClientRects().length
    )
      return;
    (panel.querySelector('a') ?? panel).focus({ preventScroll: true });
  });

  const link = safeURL && (
    <a href={safeURL} target="_blank" rel="noreferrer">
      {label}
    </a>
  );

  async function check(button: HTMLButtonElement) {
    if (receiptInFlight.current) return;
    receiptInFlight.current = true;
    rememberRecoveryFocus(button);
    setPending(true);
    setError('');
    try {
      await githubDeliveryApi.check(workId, delivery.revision);
      await reads.refresh(`/work-items/${workId}`);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'The receipt could not be checked.',
      );
    } finally {
      receiptInFlight.current = false;
      setPending(false);
    }
  }

  function continueDelivery(button: HTMLButtonElement, repair: boolean) {
    if (actionDisabled) return;
    rememberRecoveryFocus(button);
    void operation.act(workId, repair ? 'repair_delivery' : 'retry_delivery', {
      revision: workRevision,
      deliveryRevision: delivery.revision,
    });
  }

  function rememberRecoveryFocus(button: HTMLButtonElement) {
    recoveryControl.current = document.activeElement === button ? button : null;
  }

  function releaseRecoveryFocus(button: HTMLButtonElement) {
    if (recoveryControl.current === button) recoveryControl.current = null;
  }

  return (
    <section
      className="work-item-delivery"
      aria-label="GitHub delivery"
      ref={region}
      tabIndex={-1}
    >
      <div className="work-item-file-heading">
        <span>
          {checking
            ? 'Checking delivery'
            : delivery.status === 'repairing'
              ? 'Repairing the result'
              : delivery.status === 'succeeded'
                ? 'Delivered'
                : delivery.status === 'stopped'
                  ? 'Delivery stopped'
                  : delivery.status === 'unknown'
                    ? 'Delivery not confirmed'
                    : delivery.status === 'blocked'
                      ? inspectRun
                        ? delivery.stage === 'deployment'
                          ? 'GitHub deployment'
                          : 'GitHub CI'
                        : 'Delivery needs attention'
                      : (labels[delivery.stage] ?? 'Delivering')}{' '}
          · {delivery.repository}
        </span>
        {!inspectRun && link}
      </div>
      {['blocked', 'unknown'].includes(delivery.status) && delivery.reason && (
        <p role="alert" className="app-error">
          {delivery.reason}
        </p>
      )}
      {((inspectRun && link) ||
        showReceiptCheck ||
        showRepair ||
        showSettings ||
        showRetry) && (
        <div className="work-item-delivery-actions">
          {inspectRun && link}
          {showReceiptCheck && (
            <Button
              aria-disabled={pending || undefined}
              aria-busy={pending || undefined}
              onClick={(event) => void check(event.currentTarget)}
              onBlur={(event) => releaseRecoveryFocus(event.currentTarget)}
            >
              {pending ? 'Checking…' : 'Check delivery'}
            </Button>
          )}
          {showRepair && (
            <Button
              aria-disabled={actionDisabled || undefined}
              onClick={(event) => continueDelivery(event.currentTarget, true)}
              onBlur={(event) => releaseRecoveryFocus(event.currentTarget)}
            >
              Repair and continue
            </Button>
          )}
          {showSettings && (
            <Button
              primary={showRetry}
              onClick={() =>
                reconnect
                  ? void team.navigate({
                      app: 'settings',
                      section: execution ? 'models' : 'github',
                    })
                  : scopeChanged && onProjectSettings
                    ? onProjectSettings()
                    : onReviewScope?.()
              }
            >
              {reconnect
                ? execution
                  ? 'Configure execution…'
                  : 'Reconnect repository…'
                : scopeChanged
                  ? 'Review My Work defaults…'
                  : 'Review allowance and access…'}
            </Button>
          )}
          {showRetry && (
            <Button
              aria-disabled={actionDisabled || undefined}
              aria-busy={(operation.busy && canRecheck) || undefined}
              onClick={(event) => continueDelivery(event.currentTarget, false)}
              onBlur={(event) => releaseRecoveryFocus(event.currentTarget)}
            >
              {canRecheck
                ? operation.busy
                  ? 'Checking…'
                  : 'Check again'
                : 'Resume delivery'}
            </Button>
          )}
        </div>
      )}
      {error && (
        <p role="alert" className="app-error">
          {error}
        </p>
      )}
    </section>
  );
}
