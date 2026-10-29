import { useEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useTeam } from '../../team/TeamContext';
import { useAccount } from '../../team/AuthContext';
import { DraftRecovery } from './DraftRecovery';
import { registerDraftWriter } from '../../team/draftRegistry';
import { APIError } from '../../shared/api/http';
import { reviewsApi, type Review } from './api';

type RequestInput = Parameters<typeof reviewsApi.create>[1];

interface RequestDraft {
  title: string;
  reviewerUserId: string;
  receipt: RequestInput | null;
  completed: Review | null;
}

export function RequestReview({
  roomId,
  resourceId,
  path,
  commit,
  currentCommit,
  onClose,
  onCreated,
}: {
  roomId: string;
  resourceId: string;
  path: string;
  commit: string;
  currentCommit: string;
  onClose: () => void;
  onCreated: (review: Review) => void;
}) {
  const team = useTeam();
  const { user } = useAccount();
  const people = team.members.filter((person) => person.enabled);
  const draft = usePrivateDraft<RequestDraft>(
    'review',
    `new:${roomId}:${resourceId}:${path}`,
    {
      title: path.split('/').at(-1) ?? path,
      reviewerUserId:
        people.find((person) => person.id !== user.id)?.id ?? user.id,
      receipt: null,
      completed: null,
    },
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const submitting = useRef(false);
  const outdated = currentCommit !== commit;
  useEffect(
    () =>
      registerDraftWriter(`request-review:${resourceId}:${path}`, () =>
        Promise.resolve(!submitting.current),
      ),
    [resourceId, path],
  );

  async function submit() {
    if (submitting.current) return;
    submitting.current = true;
    setPending(true);
    setError('');
    try {
      const value = draft.getValue();
      const input = value.receipt ?? {
        requestId: crypto.randomUUID(),
        title: value.title,
        reviewerUserId: value.reviewerUserId,
        resourceId,
        path,
        commit,
      };
      // Keep the exact submitted body until its durable receipt is known.
      draft.setValue({ ...value, receipt: input });
      if (!(await draft.flush())) return;
      const result = await reviewsApi.create(roomId, input);
      draft.setValue({
        ...draft.getValue(),
        receipt: null,
        completed: result.review,
      });
      const saved = await draft.flush();
      submitting.current = false;
      team.refresh();
      if (saved) onCreated(result.review);
    } catch (error) {
      // A protocol rejection is definite. Network and server failures keep the
      // original body so a retry can recover a committed response without replay.
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500
      ) {
        draft.setValue({ ...draft.getValue(), receipt: null });
        await draft.flush();
      }
      setError(
        error instanceof Error
          ? error.message
          : 'Could not confirm this request. Its original details are preserved.',
      );
    } finally {
      submitting.current = false;
      setPending(false);
    }
  }

  return (
    <form
      className="camp-review-request"
      aria-label="Request document review"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <fieldset disabled={pending || !draft.loaded}>
        <legend>Request Review</legend>
        <p>
          Ask a person to review a saved version of <strong>{path}</strong>.
          Discussion stays in one room thread. No agent starts automatically.
        </p>
        {draft.value.completed ? (
          <>
            <p role="status">
              Review requested: {draft.value.completed.title}. Open it to see
              the latest decision, or start a separate review.
            </p>
            <div className="app-actions">
              <Button
                type="button"
                disabled={Boolean(draft.error)}
                onClick={() => onCreated(draft.value.completed!)}
              >
                Open Review
              </Button>
              <Button
                type="button"
                disabled={Boolean(draft.error)}
                onClick={() => {
                  draft.setValue({
                    ...draft.getValue(),
                    completed: null,
                    receipt: null,
                  });
                  setError('');
                }}
              >
                New Review
              </Button>
            </div>
          </>
        ) : (
          <>
            {draft.value.receipt && (
              <p role="status">
                The previous request has not been confirmed. Retry Request uses
                its original version, title and reviewer, even if shared files
                have changed.
              </p>
            )}
            {outdated && !draft.value.receipt && (
              <p role="status">
                Shared files have changed since this form opened. Cancel and
                reopen Request Review from the latest file version. Your title
                and reviewer are preserved.
              </p>
            )}
            <label>
              Title
              <input
                className="p9-field"
                required
                maxLength={160}
                disabled={Boolean(draft.value.receipt)}
                value={draft.value.title}
                onChange={(event) =>
                  draft.setValue({
                    ...draft.getValue(),
                    title: event.target.value,
                  })
                }
              />
            </label>
            <Select
              label="Reviewer"
              disabled={Boolean(draft.value.receipt)}
              value={draft.value.reviewerUserId}
              onChange={(event) =>
                draft.setValue({
                  ...draft.getValue(),
                  reviewerUserId: event.target.value,
                })
              }
            >
              {people.map((person) => (
                <option value={person.id} key={person.id}>
                  {person.name}
                  {person.id === user.id ? ' (you)' : ''}
                </option>
              ))}
            </Select>
          </>
        )}
        {error && (
          <p className="app-error" role="alert">
            {error}
          </p>
        )}
        <DraftRecovery
          draft={draft}
          preview={(value) => value?.title ?? 'No draft saved.'}
        />
        <div className="app-actions">
          <Button type="button" onClick={onClose}>
            Cancel
          </Button>
          {!draft.value.completed && (
            <Button
              type="submit"
              primary
              disabled={
                !draft.value.title.trim() ||
                Boolean(draft.error) ||
                (outdated && !draft.value.receipt)
              }
            >
              {draft.value.receipt ? 'Retry Request' : 'Request Review'}
            </Button>
          )}
        </div>
      </fieldset>
    </form>
  );
}
