import { useEffect } from 'react';
import { useRead } from '../../shared/reads/useRead';
import type { GitHubDeliverySpec, GitHubRepository } from './api';

export function GitHubDeliverySummary({
  value,
  onReady,
}: {
  value: GitHubDeliverySpec;
  onReady: (ready: boolean) => void;
}) {
  const read = useRead<{ repositories: GitHubRepository[] }>(
    '/github-repositories',
  );
  const repository = read.data?.repositories.find(
    (item) => item.id === value.connectionId,
  );
  const ready = Boolean(
    repository?.enabled &&
    repository.revision === value.connectionRevision &&
    (value.mode === 'pr' || repository.ciWorkflowId),
  );
  useEffect(() => {
    onReady(ready);
    return () => onReady(false);
  }, [ready, onReady]);
  return (
    <>
      <p className="work-grant-limits">
        {repository
          ? `${repository.private ? 'Private' : 'Public'} ${repository.repository}`
          : 'GitHub repository'}{' '}
        · {value.baseBranch} ·{' '}
        {value.mode === 'release'
          ? `Merge and release ${value.tag ?? ''} after CI`
          : value.mode === 'merge'
            ? 'Merge after CI'
            : 'Open pull request'}
      </p>
      {!ready && (
        <p
          role={read.error ? 'alert' : 'status'}
          className={read.error ? 'app-error' : undefined}
        >
          {read.error ||
            (read.loading
              ? 'Checking delivery settings…'
              : 'Review the current repository connection in Options before starting.')}
        </p>
      )}
    </>
  );
}
