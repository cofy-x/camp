import type { WorkOperation } from '../workitems/useWorkItemOperation';
import { useState } from 'react';
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
  operation,
  workRevision,
  onReviewScope,
  onProjectSettings,
  canRepair = false,
}: {
  delivery: GitHubDelivery;
  workId: string;
  editable: boolean;
  operation: WorkOperation;
  workRevision: number;
  onReviewScope?: () => void;
  onProjectSettings?: () => void;
  canRepair?: boolean;
}) {
  const team = useTeam();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const reads = useReadStore();
  const checking =
    delivery.reconcile && ['queued', 'working'].includes(delivery.status);
  const url =
    delivery.deploymentUrl || delivery.releaseUrl || delivery.pullRequestUrl;
  const label = delivery.deploymentUrl
    ? 'Open app'
    : delivery.releaseUrl
      ? 'Open release'
      : 'Open pull request';
  const safeURL = url?.startsWith('https://') ? url : undefined;
  const code = delivery.failureCode;
  const execution = code === 'execution_unavailable';
  const reconnect = code === 'credential' || code === 'connection' || execution;
  const scopeChanged = ['project_changed', 'rule_changed'].includes(code ?? '');
  const allowance = [
    'work_budget_exhausted',
    'daily_task_limit',
    'agent_changed',
    'repair_limit',
    'execution_unavailable',
  ].includes(code ?? '');
  const canResume =
    !code || ['action_failed', 'connection', 'credential'].includes(code);

  const repairFailure = [
    'ci_failed',
    'execution_unavailable',
    'review_changes',
    'repair_limit',
    'work_budget_exhausted',
    'daily_task_limit',
  ].includes(code ?? '');

  async function check() {
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
      setPending(false);
    }
  }

  return (
    <section className="work-item-delivery" aria-label="GitHub delivery">
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
                      ? 'Delivery needs attention'
                      : (labels[delivery.stage] ?? 'Delivering')}{' '}
          · {delivery.repository}
        </span>
        {safeURL && (
          <a href={safeURL} target="_blank" rel="noreferrer">
            {label}
          </a>
        )}
      </div>
      {['blocked', 'unknown'].includes(delivery.status) && delivery.reason && (
        <p role="alert" className="app-error">
          {delivery.reason}
        </p>
      )}
      {editable &&
        delivery.reconcile &&
        ['unknown', 'blocked'].includes(delivery.status) && (
          <Button disabled={pending} onClick={() => void check()}>
            Check delivery
          </Button>
        )}
      {editable &&
        delivery.status === 'blocked' &&
        !delivery.reconcile &&
        canRepair &&
        repairFailure && (
          <Button
            disabled={pending || operation.blocked}
            onClick={() =>
              void operation.act(workId, 'repair_delivery', {
                revision: workRevision,
                deliveryRevision: delivery.revision,
              })
            }
          >
            Repair and continue
          </Button>
        )}
      {editable &&
        delivery.status === 'blocked' &&
        !delivery.reconcile &&
        !(canRepair && repairFailure) &&
        (reconnect || scopeChanged || allowance) && (
          <Button
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
                ? 'Review project settings…'
                : 'Review allowance and access…'}
          </Button>
        )}
      {editable &&
        delivery.status === 'blocked' &&
        !delivery.reconcile &&
        canResume &&
        delivery.stage !== 'result' && (
          <Button
            disabled={pending || operation.blocked}
            onClick={() =>
              void operation.act(workId, 'retry_delivery', {
                revision: workRevision,
                deliveryRevision: delivery.revision,
              })
            }
          >
            Resume delivery
          </Button>
        )}
      {error && (
        <p role="alert" className="app-error">
          {error}
        </p>
      )}
    </section>
  );
}
