import type { WorkOperation } from '../workitems/useWorkItemOperation';
import { useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useReadStore } from '../../shared/reads/useRead';
import { githubDeliveryApi, type GitHubDelivery } from './api';

const labels: Record<string, string> = {
  result: 'Waiting for the work result',
  prepare: 'Preparing source',
  branch: 'Preparing pull request',
  pull_request: 'Opening pull request',
  checks: 'Waiting for CI',
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
}: {
  delivery: GitHubDelivery;
  workId: string;
  editable: boolean;
  operation: WorkOperation;
  workRevision: number;
}) {
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
