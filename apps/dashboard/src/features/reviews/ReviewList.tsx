import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import { reviewLabels, type Review } from './api';

export function ReviewList({
  roomId,
  resourceId,
  path,
  onOpen,
  onClose,
  backLabel = 'Back to Discussion',
  active = true,
}: {
  roomId?: string;
  resourceId?: string;
  path?: string;
  onOpen: (review: Review) => void;
  onClose?: () => void;
  backLabel?: string;
  active?: boolean;
}) {
  const params = new URLSearchParams();
  if (roomId) params.set('roomId', roomId);
  if (resourceId) params.set('resourceId', resourceId);
  const read = useRead<{ reviews: Review[] }>(`/reviews?${params}`, {
    enabled: active,
  });
  const reviews = read.data?.reviews;
  const error = read.error;
  const visible = reviews?.filter((review) => !path || review.path === path);
  return (
    <section
      className={`camp-review-list ${onClose ? 'is-panel' : ''}`}
      aria-label="Document reviews"
    >
      {(!path || onClose) && (
        <div className="camp-section-heading">
          {!onClose && (
            <h2>{path ? 'Review & Delivery' : 'Reviews & Deliveries'}</h2>
          )}
          {onClose && <Button onClick={onClose}>{backLabel}</Button>}
        </div>
      )}
      {error ? (
        <div className="app-error" role="alert">
          {error} <Button onClick={() => void read.refresh()}>Retry</Button>
        </div>
      ) : !reviews ? (
        <p role="status">Loading reviews…</p>
      ) : null}
      {!error && visible && !visible.length ? (
        <p>
          {path
            ? 'No reviews yet.'
            : 'No reviews yet. Request one from a shared file in Library.'}
        </p>
      ) : visible && visible.length > 0 ? (
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
      ) : null}
    </section>
  );
}
