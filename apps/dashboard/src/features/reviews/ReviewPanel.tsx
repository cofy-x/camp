import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, type DesktopAPI } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { useRead, useReadStore } from '../../shared/reads/useRead';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { FilePreview } from '../workspace/FilePreview';
import { ReviewActions } from './ReviewActions';
import { reviewLabels, reviewPath, type ReviewResult } from './api';
import { ReviewHistory } from './ReviewHistory';

function changedCommit(value: ReviewResult, expected?: string) {
  return Boolean(expected && value.review.submittedCommit !== expected);
}

export function ReviewPanel({
  id,
  minimumRevision = 0,
  expectedCommit,
  desktop,
  onClose,
}: {
  id: string;
  minimumRevision?: number;
  expectedCommit?: string;
  desktop: DesktopAPI;
  onClose: () => void;
}) {
  const team = useTeam();
  const [result, setResult] = useState<ReviewResult | null>(null);
  const store = useReadStore();
  const path = reviewPath(id);
  const read = useRead<ReviewResult>(path);
  const error = read.error;
  const [incoming, setIncoming] = useState<ReviewResult | null>(null);
  const [pinnedCommit, setPinnedCommit] = useState(expectedCommit);
  const [historyOpen, setHistoryOpen] = useState(false);
  const displayed = useRef<ReviewResult | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const statusId = useId();
  const focusDecision = useRef(false);
  const navigation = useRef(0);
  const incomingRef = useRef(incoming);
  const requestedBaseline = useRef(false);
  incomingRef.current = incoming;
  useEffect(() => {
    navigation.current++;
  }, [team.navigation]);
  useEffect(
    () => () => {
      navigation.current++;
    },
    [],
  );

  function applyResult(value: ReviewResult) {
    if (
      displayed.current &&
      displayed.current.review.revision > value.review.revision
    )
      return;
    displayed.current = value;
    setResult(value);
    setIncoming(null);
  }

  function applyDecision(value: ReviewResult) {
    if (
      displayed.current &&
      displayed.current.review.revision > value.review.revision
    )
      return;
    if (changedCommit(value, pinnedCommit)) {
      setIncoming(value);
      return;
    }
    focusDecision.current = true;
    applyResult(value);
  }

  useLayoutEffect(() => {
    if (!focusDecision.current) return;
    focusDecision.current = false;
    if (!body.current?.closest('.p9-window.is-active')) return;
    if (body.current) body.current.scrollTop = 0;
    heading.current?.focus({ preventScroll: true });
  }, [result]);

  useEffect(() => {
    const accepted = store.snapshot<ReviewResult>(path);
    const value = accepted.data;
    const current = displayed.current;
    // Reopening establishes a current baseline before offering actions;
    // later changes still require explicit inspection in the mounted view.
    if (!value || (!current && (accepted.refreshing || accepted.error))) return;
    if (!current && value.review.revision < minimumRevision) {
      if (!requestedBaseline.current) {
        requestedBaseline.current = true;
        void store.refresh(path);
      }
      return;
    }
    if (changedCommit(value, pinnedCommit)) {
      if (!current || value.review.revision >= current.review.revision)
        setIncoming(value);
      return;
    }
    if (!current || current.review.revision === value.review.revision) {
      displayed.current = value;
      setResult(value);
    } else if (value.review.revision > current.review.revision) {
      setIncoming(value);
    }
  }, [
    minimumRevision,
    path,
    pinnedCommit,
    read.data,
    read.error,
    read.refreshing,
    store,
  ]);
  const obsoleteLink = Boolean(
    incoming && changedCommit(incoming, pinnedCommit),
  );

  function inspectIncoming(acceptCurrentVersion: boolean) {
    team.cancelNavigation();
    const globalNavigation = team.getNavigationRevision();
    const expected = ++navigation.current;
    const activation = desktop.getActivationRevision();
    void flushPrivateDrafts().then((saved) => {
      const next = incomingRef.current;
      if (
        !saved ||
        globalNavigation !== team.getNavigationRevision() ||
        expected !== navigation.current ||
        activation !== desktop.getActivationRevision() ||
        !next ||
        (changedCommit(next, pinnedCommit) && !acceptCurrentVersion)
      )
        return;
      if (acceptCurrentVersion) {
        setPinnedCommit(undefined);
        focusDecision.current = true;
      }
      applyResult(next);
    });
  }

  const review = result?.review;
  const feedback = result?.feedback;
  return (
    <section className="camp-review-panel" aria-label="Document review">
      <div className="app-toolbar">
        <Button onClick={onClose}>Back to Reviews</Button>
      </div>
      <div className="camp-review-body" ref={body}>
        <div className="camp-review-content">
          {error && (
            <div role="alert" className="app-error">
              {error}{' '}
              <Button onClick={() => void read.refresh()}>Retry Review</Button>
            </div>
          )}
          {!review &&
            !error &&
            !obsoleteLink &&
            (read.data &&
            read.data.review.revision < minimumRevision &&
            !read.refreshing ? (
              <p role="status">
                Waiting for the current review.{' '}
                <Button onClick={() => void read.refresh()}>
                  Retry Review
                </Button>
              </p>
            ) : (
              <p role="status">Loading review…</p>
            ))}
          {incoming && (
            <div className="camp-review-notice" role="status">
              {obsoleteLink
                ? 'This review now covers a different file version. Read the current review before acting.'
                : 'This review changed in another session. Inspect the latest decision and version before acting. Your unfinished comment is preserved.'}{' '}
              <Button onClick={() => inspectIncoming(obsoleteLink)}>
                {obsoleteLink ? 'Read current review' : 'Refresh Review'}
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
              </dl>
              <div className="app-actions">
                <Button
                  variant="text"
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
                  variant="text"
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
                    variant="text"
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
              <div className="camp-review-decision-layout">
                <div className="camp-review-decision">
                  <ReviewActions
                    key={review.id}
                    review={review}
                    changed={Boolean(incoming)}
                    onChange={applyDecision}
                  />
                </div>
                <details className="camp-reviewed-document" open>
                  <summary>
                    {review.status === 'delivered'
                      ? 'Delivered document'
                      : 'Submitted document'}
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
              </div>
              <details
                className="camp-review-history"
                onToggle={(event) => setHistoryOpen(event.currentTarget.open)}
              >
                <summary>Review history</summary>
                <ReviewHistory
                  key={`${review.id}:${review.revision}`}
                  review={review}
                  active={historyOpen}
                />
              </details>
            </>
          )}
        </div>
      </div>
    </section>
  );
}
