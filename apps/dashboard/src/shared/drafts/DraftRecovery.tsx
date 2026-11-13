import { Button } from '@cofy-x/camp-platinum';
import type { usePrivateDraft } from '../../team/usePrivateDraft';

export function DraftRecovery<T>({
  draft,
  preview,
}: {
  draft: ReturnType<typeof usePrivateDraft<T>>;
  preview: (value: T | null) => string;
}) {
  return (
    <>
      {draft.error && (
        <p className="app-error" role="alert">
          {draft.error}
        </p>
      )}
      {draft.error && !draft.comparison && (
        <Button onClick={() => void draft.inspectConflict()}>
          {draft.loaded ? 'Compare Private Drafts' : 'Retry Restore'}
        </Button>
      )}
      {draft.comparison && (
        <details open className="camp-draft-comparison">
          <summary>Private draft in the other session</summary>
          <pre>{preview(draft.comparison.value)}</pre>
          <p>
            Your local text stays in the form. Choose which private draft to
            keep.
          </p>
          <div className="app-actions">
            <Button onClick={draft.useRemote}>Use Other Draft</Button>
            <Button onClick={() => void draft.resolveConflict()}>
              Save My Draft
            </Button>
          </div>
        </details>
      )}
    </>
  );
}
