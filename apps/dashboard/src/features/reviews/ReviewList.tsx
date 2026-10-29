import { useEffect, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { reviewsApi, reviewLabels, type Review } from './api';

export function ReviewList({
  roomId,
  resourceId,
  path,
  onOpen,
  onClose,
}: {
  roomId?: string;
  resourceId?: string;
  path?: string;
  onOpen: (review: Review) => void;
  onClose?: () => void;
}) {
  const team = useTeam();
  const [reviews, setReviews] = useState<Review[]>([]);
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    void reviewsApi
      .list(
        {
          ...(roomId ? { roomId } : {}),
          ...(resourceId ? { resourceId } : {}),
        },
        controller.signal,
      )
      .then((result) => {
        if (controller.signal.aborted) return;
        setReviews(result.reviews);
        setLoaded(true);
        setError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load reviews.',
          );
      });
    return () => controller.abort();
  }, [roomId, resourceId, team.revision, attempt]);
  const visible = reviews.filter((review) => !path || review.path === path);
  return (
    <section
      className={`camp-review-list ${onClose ? 'is-panel' : ''}`}
      aria-label="Document reviews"
    >
      {(!path || onClose) && (
        <div className="camp-section-heading">
          <h2>{path ? 'Review & Delivery' : 'Reviews & Deliveries'}</h2>
          {onClose && <Button onClick={onClose}>Back to Chat</Button>}
        </div>
      )}
      {error ? (
        <div className="app-error" role="alert">
          {error}{' '}
          <Button onClick={() => setAttempt((value) => value + 1)}>
            Retry
          </Button>
        </div>
      ) : !loaded ? (
        <p role="status">Loading reviews…</p>
      ) : !visible.length ? (
        <p>
          {path
            ? 'No reviews yet. Choose Request Review to start one.'
            : 'No reviews yet. Request one from a shared file in Library.'}
        </p>
      ) : (
        <ul>
          {visible.map((review) => (
            <li key={review.id}>
              <button onClick={() => onOpen(review)}>
                <PixelIcon
                  name={
                    review.status === 'delivered' ? 'document' : 'checklist'
                  }
                  size={24}
                />
                <span>
                  <strong>{review.title}</strong>
                  <small>
                    {review.reviewerName} · {review.path}
                  </small>
                </span>
                <span className="camp-review-state">
                  {reviewLabels[review.status]}
                  {review.outdated &&
                  review.status !== 'delivered' &&
                  review.status !== 'cancelled'
                    ? ' · Newer files'
                    : ''}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
