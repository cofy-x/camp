import { useEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { useAccount } from '../../team/AuthContext';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { registerDraftWriter } from '../../team/draftRegistry';
import { APIError } from '../../shared/api/http';
import { DraftRecovery } from './DraftRecovery';
import {
  reviewsApi,
  type Review,
  type ReviewAction,
  type ReviewActionInput,
  type ReviewResult,
} from './api';

function originalDecision(receipt: {
  key: string;
  requestId: string;
}): ReviewActionInput {
  const input = JSON.parse(receipt.key) as Partial<ReviewActionInput> | null;
  if (
    !input ||
    ![
      'approve',
      'request_changes',
      'resubmit',
      'deliver',
      'cancel',
      'reassign',
    ].includes(input.action ?? '') ||
    !Number.isSafeInteger(input.revision) ||
    (input.revision ?? 0) < 1 ||
    (input.comment !== undefined && typeof input.comment !== 'string') ||
    (input.commit !== undefined && typeof input.commit !== 'string') ||
    (input.reviewerUserId !== undefined &&
      typeof input.reviewerUserId !== 'string')
  )
    throw new Error(
      'The saved decision could not be restored. Your private draft is preserved.',
    );
  return { ...input, requestId: receipt.requestId } as ReviewActionInput;
}

export function ReviewActions({
  review,
  changed,
  onChange,
}: {
  review: Review;
  changed: boolean;
  onChange: (value: ReviewResult) => void;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const draft = usePrivateDraft<{
    comment: string;
    receipt: { key: string; requestId: string } | null;
  }>('review', `decision:${review.id}`, { comment: '', receipt: null });
  const flushDraft = draft.flush;
  const [pending, setPending] = useState(false);
  const operation = useRef<Promise<void> | null>(null);
  const mounted = useRef(true);
  const [error, setError] = useState('');
  const [reassigning, setReassigning] = useState(false);
  const [reviewerId, setReviewerId] = useState(review.reviewerUserId);
  const canManage = user.id === review.requesterUserId || user.role === 'admin';
  const canDecide =
    user.id === review.reviewerUserId &&
    review.reviewerEnabled &&
    review.status === 'in_review' &&
    !review.outdated;
  const ended = review.status === 'delivered' || review.status === 'cancelled';
  const retained = Boolean(draft.value.receipt);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(
    () =>
      registerDraftWriter(`review-action:${review.id}`, async () => {
        await operation.current;
        // Another draft writer can finish before the action clears its receipt.
        // Recheck that final write before allowing the view to unmount.
        return flushDraft();
      }),
    [review.id, flushDraft],
  );

  async function act(action?: ReviewAction) {
    if (
      (!draft.getValue().receipt && changed) ||
      operation.current ||
      !draft.loaded ||
      draft.error
    )
      return;
    setPending(true);
    const current = performAction(action);
    operation.current = current;
    try {
      await current;
    } finally {
      operation.current = null;
      if (mounted.current) setPending(false);
    }
  }

  async function performAction(action?: ReviewAction) {
    setError('');
    try {
      const value = draft.getValue();
      if (!value.receipt && !action) return;
      const input: ReviewActionInput = value.receipt
        ? originalDecision(value.receipt)
        : {
            action: action!,
            revision: review.revision,
            comment: value.comment.trim(),
            ...(action === 'resubmit' ? { commit: review.currentCommit } : {}),
            ...(action === 'reassign' ? { reviewerUserId: reviewerId } : {}),
            requestId: crypto.randomUUID(),
          };
      if (!value.receipt) {
        const { requestId, ...body } = input;
        draft.setValue({
          ...value,
          receipt: { key: JSON.stringify(body), requestId },
        });
      }
      if (!(await draft.flush())) return;
      let receipt: ReviewResult;
      try {
        receipt = await reviewsApi.act(review.id, input);
      } catch (error) {
        if (
          error instanceof APIError &&
          error.status >= 400 &&
          error.status < 500
        ) {
          draft.setValue({ ...draft.getValue(), receipt: null });
          await draft.flush();
        }
        throw error;
      }
      const result = receipt.replayed
        ? await reviewsApi.get(review.id)
        : receipt;
      draft.setValue({ comment: '', receipt: null });
      await draft.flush();
      team.refresh();
      if (mounted.current) onChange(result);
    } catch (error) {
      if (mounted.current)
        setError(
          error instanceof Error
            ? error.message
            : 'This action could not be saved. Your comment is preserved.',
        );
    }
  }

  const recovery = (
    <>
      <DraftRecovery
        draft={draft}
        preview={(value) => value?.comment || 'No comment saved.'}
      />
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {retained && (
        <div className="camp-review-recovery">
          <p role="status">
            Confirm the previous decision before making another change.
          </p>
          <Button
            disabled={pending || !draft.loaded || Boolean(draft.error)}
            onClick={() => void act()}
          >
            {pending ? 'Checking…' : 'Retry Decision'}
          </Button>
        </div>
      )}
    </>
  );

  const savedComment = draft.value.comment && (
    <section className="camp-review-feedback">
      <h3>{retained ? 'Saved decision comment' : 'Unsent private comment'}</h3>
      {!retained && <p>Saved privately before the review changed.</p>}
      <textarea
        aria-label={
          retained ? 'Saved decision comment' : 'Unsent private comment'
        }
        readOnly
        rows={3}
        value={draft.value.comment}
      />
    </section>
  );

  if (ended)
    return (
      <>
        {savedComment}
        {recovery}
      </>
    );
  return (
    <>
      <fieldset
        className="camp-review-actions"
        aria-label="Review actions"
        disabled={
          changed ||
          pending ||
          retained ||
          !draft.loaded ||
          Boolean(draft.error)
        }
      >
        {review.outdated && (
          <p className="camp-review-notice" role="status">
            Shared files have changed since this review. Submit the latest saved
            version for a new decision before delivery.
          </p>
        )}
        {!review.reviewerEnabled && (
          <p className="camp-review-notice">
            The assigned reviewer is disabled. The requester or an administrator
            can reassign this review.
          </p>
        )}
        {canDecide || canManage ? (
          <>
            <label>
              Review comment
              <textarea
                aria-label="Review comment"
                rows={3}
                maxLength={4000}
                disabled={pending || !draft.loaded}
                value={draft.value.comment}
                onChange={(event) =>
                  draft.setValue({
                    ...draft.getValue(),
                    comment: event.target.value,
                  })
                }
              />
            </label>
            <p className="workspace-description">
              {canDecide
                ? 'Required to request changes. Private until submitted.'
                : 'Private until submitted.'}
            </p>
            <div className="app-actions">
              {canDecide && (
                <>
                  <Button
                    primary
                    disabled={pending || !draft.loaded || Boolean(draft.error)}
                    onClick={() => void act('approve')}
                  >
                    Approve
                  </Button>
                  <Button
                    disabled={
                      pending ||
                      !draft.loaded ||
                      Boolean(draft.error) ||
                      !draft.value.comment.trim()
                    }
                    onClick={() => void act('request_changes')}
                  >
                    Request Changes
                  </Button>
                </>
              )}
              {canManage &&
                (review.outdated || review.status === 'changes_requested') && (
                  <Button
                    primary
                    disabled={
                      pending ||
                      !draft.loaded ||
                      Boolean(draft.error) ||
                      review.currentCommit === review.submittedCommit
                    }
                    onClick={() => void act('resubmit')}
                  >
                    Submit New Version
                  </Button>
                )}
              {canManage && review.status === 'approved' && (
                <Button
                  primary
                  disabled={
                    pending ||
                    review.outdated ||
                    !draft.loaded ||
                    Boolean(draft.error)
                  }
                  onClick={() => void act('deliver')}
                >
                  Deliver
                </Button>
              )}
            </div>
            {canManage && (
              <details className="camp-review-management">
                <summary>Manage Review</summary>
                <div className="app-actions">
                  <Button
                    disabled={pending}
                    onClick={() => setReassigning(!reassigning)}
                  >
                    Reassign
                  </Button>
                  <Button
                    disabled={pending || !draft.loaded || Boolean(draft.error)}
                    onClick={() => void act('cancel')}
                  >
                    Cancel Review
                  </Button>
                </div>
                {reassigning && (
                  <div className="camp-review-reassign">
                    <Select
                      label="New reviewer"
                      value={reviewerId}
                      disabled={pending}
                      onChange={(event) => setReviewerId(event.target.value)}
                    >
                      {team.members
                        .filter((person) => person.enabled)
                        .map((person) => (
                          <option key={person.id} value={person.id}>
                            {person.name}
                          </option>
                        ))}
                    </Select>
                    <Button
                      disabled={
                        pending ||
                        !draft.loaded ||
                        Boolean(draft.error) ||
                        !reviewerId ||
                        reviewerId === review.reviewerUserId
                      }
                      onClick={() => void act('reassign')}
                    >
                      Assign Reviewer
                    </Button>
                  </div>
                )}
              </details>
            )}
          </>
        ) : (
          <p>
            Waiting for{' '}
            {review.outdated || review.status === 'changes_requested'
              ? review.requesterName + ' to submit a new version.'
              : review.status === 'approved'
                ? review.requesterName + ' to deliver this version.'
                : review.reviewerName + ' to review this version.'}
          </p>
        )}
      </fieldset>
      {!canDecide && !canManage && savedComment}
      {recovery}
    </>
  );
}
