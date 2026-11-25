import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { useRead } from '../../shared/reads/useRead';
import { useTeam } from '../../team/TeamContext';
import {
  reviewLabels,
  workResultReviewsPath,
  type WorkResultReviews,
} from '../reviews/api';
import { Report } from '../workspace/Report';
import type { WorkItem } from './api';

const deleted = (status: string) => status === 'D' || status === 'deleted';
const reviewActions = { review: 'Review File…', deliver: 'Open Delivery…' };

export function WorkItemResult({
  item,
  active = true,
}: {
  item: WorkItem;
  active?: boolean;
}) {
  const team = useTeam();
  const result = item.result;
  const reviewRead = useRead<WorkResultReviews>(
    result?.published &&
      result.commit &&
      !item.githubDelivery &&
      result.files.some((file) => !deleted(file.status))
      ? workResultReviewsPath(
          item.roomId,
          item.id,
          result.resourceId,
          result.commit,
        )
      : null,
    { enabled: active },
  );
  if (!result) return null;
  const reviewState = reviewRead.data;
  const onlyFile = result.files.length === 1 ? result.files[0] : undefined;
  const hasFiles = result.files.length > 0 || result.filesLimited;
  const directFile =
    onlyFile && !deleted(onlyFile.status) && !result.filesLimited;

  function open(path?: string, requestReview?: true) {
    if (!result) return;
    void team.navigate({
      app: 'library',
      resourceId: result.resourceId,
      commit: result.commit,
      path,
      contextRoomId: item.roomId,
      requestReview,
    });
  }

  const report = result.summary ? (
    <Report
      key={`${result.taskId}:${result.commit}`}
      content={result.summary}
      stateKey={`work-report:${item.id}:${result.taskId}:${result.commit}`}
      compact={Boolean(result.commit && hasFiles && !item.githubDelivery)}
    />
  ) : null;
  if (item.githubDelivery)
    return (
      <section className="work-item-result" aria-label="Work result">
        <h3 className="work-section-title">Result</h3>
        {report}
        {result.commit && (
          <div className="app-actions">
            <Button variant="text" onClick={() => open()}>
              Source files…
            </Button>
          </div>
        )}
      </section>
    );

  return (
    <section className="work-item-result" aria-label="Work result">
      <div className="work-item-file-heading">
        <h3 className="work-section-title">Result</h3>
        {result.commit && hasFiles && (
          <div className="work-item-result-meta">
            <span>{result.published ? 'Published to Room' : 'Team draft'}</span>
            {!directFile && (
              <Button variant="text" onClick={() => open()}>
                Open files
              </Button>
            )}
          </div>
        )}
      </div>
      {result.commit && hasFiles && (
        <>
          <ul className="work-item-files">
            {result.files.map((file) => {
              const removed = deleted(file.status);
              const review =
                !removed &&
                reviewState &&
                Object.hasOwn(reviewState.reviews, file.path)
                  ? reviewState.reviews[file.path]
                  : undefined;
              const reviewAction = review?.nextAction
                ? reviewActions[review.nextAction]
                : 'View Review';
              return (
                <li key={file.path}>
                  <div className="work-result-file">
                    <PixelIcon name="document" size={18} aria-hidden="true" />
                    {removed ? (
                      <span>{file.path}</span>
                    ) : (
                      <Button variant="text" onClick={() => open(file.path)}>
                        {file.path}
                      </Button>
                    )}
                    {file.status !== 'saved' && (
                      <small>
                        {removed
                          ? 'Deleted'
                          : file.status === 'A' || file.status === 'added'
                            ? 'Added'
                            : 'Changed'}
                      </small>
                    )}
                  </div>
                  {review ? (
                    <div className="work-result-file-review">
                      <span>
                        {review.status !== 'delivered' &&
                        !reviewState?.currentVersion
                          ? 'Review needs a newer version'
                          : reviewLabels[review.status]}
                      </span>
                      <Button
                        variant="text"
                        aria-label={`${reviewAction} for ${file.path}`}
                        onClick={() =>
                          void team.navigate({
                            roomId: item.roomId,
                            reviewId: review.id,
                            reviewRevision: review.revision,
                            reviewCommit: result.commit,
                          })
                        }
                      >
                        {reviewAction}
                      </Button>
                    </div>
                  ) : (
                    !removed &&
                    reviewState?.canRequest &&
                    !reviewRead.error && (
                      <Button
                        variant="text"
                        className="work-result-file-request"
                        aria-label={`Request Review… for ${file.path}`}
                        onClick={() => open(file.path, true)}
                      >
                        Request Review…
                      </Button>
                    )
                  )}
                </li>
              );
            })}
          </ul>
          {reviewRead.error && (
            <p className="work-result-file-review app-error" role="alert">
              Could not check file reviews.{' '}
              <Button onClick={() => void reviewRead.refresh()}>
                Retry file reviews
              </Button>
            </p>
          )}
          {result.filesLimited && (
            <p>More files are available in Open files.</p>
          )}
        </>
      )}
      {report}
    </section>
  );
}
