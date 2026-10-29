import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, Select, PixelIcon } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { useAccount } from '../../team/AuthContext';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import {
  registerDraftWriter,
  flushPrivateDrafts,
} from '../../team/draftRegistry';
import { FilePreview } from '../workspace/FilePreview';
import { DraftRecovery } from './DraftRecovery';
import {
  reviewsApi,
  reviewLabels,
  type Review,
  type ReviewAction,
  type ReviewActionInput,
  type ReviewResult,
} from './api';

const actionLabels: Record<string, string> = {
  request: 'Requested review',
  create: 'Requested review',
  approve: 'Approved',
  request_changes: 'Requested changes',
  resubmit: 'Submitted a new version',
  deliver: 'Delivered',
  cancel: 'Cancelled',
  reassign: 'Changed reviewer',
};

function ReviewActions({
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
  const [pending, setPending] = useState(false);
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
  useEffect(
    () =>
      registerDraftWriter(`review-action:${review.id}`, () =>
        Promise.resolve(!pending),
      ),
    [review.id, pending],
  );

  async function act(action: ReviewAction) {
    if (changed) return;
    setPending(true);
    setError('');
    try {
      const value = draft.getValue();
      const input: Omit<ReviewActionInput, 'requestId'> = {
        action,
        revision: review.revision,
        comment: value.comment.trim(),
        ...(action === 'resubmit' ? { commit: review.currentCommit } : {}),
        ...(action === 'reassign' ? { reviewerUserId: reviewerId } : {}),
      };
      const key = JSON.stringify(input);
      const requestId =
        value.receipt?.key === key
          ? value.receipt.requestId
          : crypto.randomUUID();
      draft.setValue({ ...value, receipt: { key, requestId } });
      if (!(await draft.flush())) return;
      const receipt = await reviewsApi.act(review.id, { ...input, requestId });
      const result = receipt.replayed
        ? await reviewsApi.get(review.id)
        : receipt;
      draft.setValue({ comment: '', receipt: null });
      await draft.flush();
      team.refresh();
      onChange(result);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'This action could not be saved. Your comment is preserved.',
      );
    } finally {
      setPending(false);
    }
  }

  if (ended)
    return (
      <>
        {draft.value.comment && (
          <section className="camp-review-feedback">
            <h3>Unsent private comment</h3>
            <p>Saved privately before the review ended.</p>
            <textarea
              aria-label="Unsent private comment"
              readOnly
              rows={3}
              value={draft.value.comment}
            />
          </section>
        )}
        <DraftRecovery
          draft={draft}
          preview={(value) => value?.comment ?? 'No comment saved.'}
        />
      </>
    );
  return (
    <fieldset
      className="camp-review-actions"
      aria-label="Review actions"
      disabled={changed}
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
      {review.status === 'changes_requested' && !review.outdated && (
        <p>
          Ask an Agent to revise the file, or edit and publish it in Library.
          Then submit the new saved version here.
        </p>
      )}
      {review.status === 'approved' && !review.outdated && canManage && (
        <p>This version is approved. Deliver it when it is ready to share.</p>
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
          <DraftRecovery
            draft={draft}
            preview={(value) => value?.comment || 'No comment saved.'}
          />
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
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
  );
}

export function ReviewPanel({
  id,
  onClose,
}: {
  id: string;
  onClose: () => void;
}) {
  const team = useTeam();
  const [result, setResult] = useState<ReviewResult | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [incoming, setIncoming] = useState<ReviewResult | null>(null);
  const displayed = useRef<ReviewResult | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const statusId = useId();
  const focusDecision = useRef(false);

  function applyResult(value: ReviewResult) {
    displayed.current = value;
    setResult(value);
    setIncoming(null);
  }

  function applyDecision(value: ReviewResult) {
    focusDecision.current = true;
    applyResult(value);
  }

  useLayoutEffect(() => {
    if (!focusDecision.current) return;
    focusDecision.current = false;
    if (body.current) body.current.scrollTop = 0;
    heading.current?.focus({ preventScroll: true });
  }, [result]);

  useEffect(() => {
    const controller = new AbortController();
    void reviewsApi
      .get(id, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) {
          const current = displayed.current;
          if (!current || current.review.revision === value.review.revision) {
            displayed.current = value;
            setResult(value);
          } else if (value.review.revision > current.review.revision) {
            setIncoming(value);
          }
          setError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load this review.',
          );
      });
    return () => controller.abort();
  }, [id, team.revision, attempt]);
  const review = result?.review;
  const feedback = result?.events
    .filter((event) => event.action === 'request_changes')
    .at(-1);
  return (
    <section className="camp-review-panel" aria-label="Document review">
      <div className="app-toolbar">
        <PixelIcon name="checklist" size={24} />
        <strong>Review & Delivery</strong>
        <Button
          onClick={() =>
            void flushPrivateDrafts().then((saved) => {
              if (saved) onClose();
            })
          }
        >
          Back to Reviews
        </Button>
      </div>
      <div className="camp-review-body" ref={body}>
        <div className="camp-review-content">
          {error && (
            <div role="alert" className="app-error">
              {error}{' '}
              <Button onClick={() => setAttempt((value) => value + 1)}>
                Refresh Review
              </Button>
            </div>
          )}
          {!review && !error && <p role="status">Loading review…</p>}
          {incoming && (
            <div className="camp-review-notice" role="status">
              This review changed in another session. Inspect the latest
              decision and version before acting. Your unfinished comment is
              preserved.{' '}
              <Button
                onClick={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      applyResult(incoming);
                    }
                  })
                }
              >
                Refresh Review
              </Button>
            </div>
          )}
          {review && (
            <>
              <div className="camp-section-heading">
                <h2 ref={heading} tabIndex={-1} aria-describedby={statusId}>
                  {review.title}
                </h2>
                <span className="camp-review-state" id={statusId}>
                  {reviewLabels[review.status]}
                </span>
              </div>
              <dl className="info-list">
                <dt>Requested by</dt>
                <dd>{review.requesterName}</dd>
                <dt>Reviewer</dt>
                <dd>
                  {review.reviewerName}
                  {!review.reviewerEnabled ? ' · disabled' : ''}
                </dd>
                <dt>File</dt>
                <dd>{review.path}</dd>
                <dt>Version</dt>
                <dd>
                  {review.status === 'delivered'
                    ? 'Delivered version'
                    : 'Submitted version'}
                  {review.outdated && review.status !== 'delivered'
                    ? ' · newer shared files available'
                    : ''}
                </dd>
              </dl>
              <div className="app-actions">
                <Button
                  primary={review.status === 'delivered'}
                  onClick={() =>
                    void team.navigate({
                      app: 'library',
                      resourceId: review.resourceId,
                      path: review.path,
                      commit: review.deliveredCommit || review.submittedCommit,
                      contextRoomId: review.roomId,
                    })
                  }
                >
                  Open Document
                </Button>
                <Button
                  onClick={() =>
                    void team.navigate({
                      roomId: review.roomId,
                      threadId: review.threadRootId,
                    })
                  }
                >
                  Open Discussion
                </Button>
                {(review.outdated || review.status === 'changes_requested') && (
                  <Button
                    onClick={() =>
                      void team.navigate({
                        app: 'library',
                        resourceId: review.resourceId,
                        path: review.path,
                        contextRoomId: review.roomId,
                      })
                    }
                  >
                    Edit Latest File
                  </Button>
                )}
              </div>
              {feedback && review.status === 'changes_requested' && (
                <section className="camp-review-feedback">
                  <h3>Changes to address</h3>
                  <p>{feedback.comment}</p>
                  <small>
                    {feedback.actorName} ·{' '}
                    {new Date(feedback.createdAt).toLocaleString()}
                  </small>
                  <div className="app-actions">
                    <Button
                      disabled={Boolean(incoming)}
                      onClick={() =>
                        void team.navigate({
                          roomId: review.roomId,
                          reference: {
                            kind: 'message',
                            messageId: feedback.messageId,
                            title: 'Requested changes',
                          },
                        })
                      }
                    >
                      Ask agent…
                    </Button>
                  </div>
                </section>
              )}
              <details className="camp-reviewed-document" open>
                <summary>
                  {review.status === 'delivered'
                    ? 'Delivered document'
                    : 'Document submitted for review'}
                </summary>
                <FilePreview
                  reference={{
                    kind: 'file',
                    resourceId: review.resourceId,
                    path: review.path,
                    commit: review.deliveredCommit || review.submittedCommit,
                    title: review.path,
                  }}
                />
              </details>
              <ReviewActions
                key={review.id}
                review={review}
                changed={Boolean(incoming)}
                onChange={applyDecision}
              />
              <details className="camp-review-history">
                <summary>Review history</summary>
                <ol>
                  {result?.events.map((event) => (
                    <li key={event.id}>
                      <strong>
                        {actionLabels[event.action] ?? 'Review requested'}
                      </strong>
                      <span>
                        {event.actorName} ·{' '}
                        {new Date(event.createdAt).toLocaleString()}
                      </span>
                      {event.comment && <p>{event.comment}</p>}
                      <Button
                        onClick={() =>
                          void team.navigate({
                            app: 'library',
                            resourceId: review.resourceId,
                            path: review.path,
                            commit: event.commit,
                            contextRoomId: review.roomId,
                          })
                        }
                      >
                        View This Version
                      </Button>
                    </li>
                  ))}
                </ol>
              </details>
            </>
          )}
        </div>
      </div>
    </section>
  );
}
