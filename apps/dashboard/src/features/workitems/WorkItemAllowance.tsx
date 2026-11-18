import { useEffect, useId } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { localDateTime, utcDateTime } from './workSettings';
import type { WorkOperation } from './useWorkItemOperation';
import { confirmedWorkAction } from './workRequestRecovery';
import type { WorkBudget, WorkItem } from './api';

type AllowanceDraft = {
  revision: number;
  budget: WorkBudget;
  deadline?: string;
  expiresAt?: string;
  reason?: string;
  deadlineEdited?: boolean;
};

export function WorkItemAllowance({
  item,
  operation,
  onRecoveryChange,
  onDraftChange,
  readOnly,
  contextReady,
}: {
  item: WorkItem;
  operation: WorkOperation;
  onRecoveryChange: (problem: string) => void;
  onDraftChange: (hasDraft: boolean) => void;
  readOnly: boolean;
  contextReady: boolean;
}) {
  const expiryId = useId();
  const draft = usePrivateDraft<AllowanceDraft | null>(
    'conversation',
    `work-item-allowance:${item.id}`,
    null,
  );
  const currentExpiry = item.expiresAt ?? item.deadline;
  const currentLocalExpiry = localDateTime(currentExpiry);
  const values = draft.value ?? {
    revision: item.revision,
    budget: item.budget,
    expiresAt: currentLocalExpiry,
    reason: '',
  };
  const localExpiry = values.expiresAt ?? values.deadline ?? '';
  const expiryEdited =
    values.deadlineEdited && localExpiry !== currentLocalExpiry;
  const disabled =
    readOnly ||
    !contextReady ||
    item.status === 'stopping' ||
    operation.blocked ||
    !draft.loaded ||
    Boolean(draft.error);
  const stale = values.revision !== item.revision;
  const clearDraft = draft.setValue;
  const problem =
    draft.error || (draft.comparison ? 'Review your allowance draft.' : '');
  useEffect(() => onRecoveryChange(problem), [problem, onRecoveryChange]);
  const hasDraft = draft.value !== null;
  useEffect(() => onDraftChange(hasDraft), [hasDraft, onDraftChange]);
  useEffect(() => {
    const saved = operation.confirmation;
    if (confirmedWorkAction(saved) === 'authorize') clearDraft(null);
  }, [operation.confirmation, clearDraft]);

  function edit(change: Partial<AllowanceDraft>) {
    draft.setValue({ ...values, ...change });
  }

  return (
    <>
      <DraftRecovery draft={draft} preview={(value) => value?.reason ?? ''} />
      <section aria-label="Allowance and access">
        <form
          className="work-item-form"
          aria-label="Work allowance"
          onSubmit={(event) => {
            event.preventDefault();
            if (disabled || stale) return;
            void operation.act(item.id, 'authorize', {
              revision: values.revision,
              comment: values.reason,
              budget: values.budget,
              expiresAt: values.deadlineEdited
                ? utcDateTime(localExpiry)
                : undefined,
            });
          }}
        >
          {stale && (
            <p className="app-error" role="alert">
              This work changed. Review current usage before changing its
              limits.{' '}
              <Button
                disabled={disabled}
                onClick={() => edit({ revision: item.revision })}
              >
                Use current limits version
              </Button>
            </p>
          )}
          <fieldset disabled={disabled}>
            <WorkBudgetEditor
              value={values.budget}
              onChange={(budget) => edit({ budget })}
              current={item}
            />
            <div className="work-item-limit">
              <label htmlFor={expiryId}>Authorization expires</label>
              <input
                id={expiryId}
                aria-describedby={
                  expiryEdited ? `${expiryId}-current` : undefined
                }
                className="p9-field"
                type="datetime-local"
                required
                value={localExpiry}
                onChange={(event) =>
                  edit({ expiresAt: event.target.value, deadlineEdited: true })
                }
              />
              {expiryEdited && (
                <small id={`${expiryId}-current`}>
                  Current expiry {new Date(currentExpiry).toLocaleString()}
                </small>
              )}
            </div>
            <label>
              Note (optional)
              <textarea
                className="p9-field"
                rows={2}
                value={values.reason ?? ''}
                onChange={(event) => edit({ reason: event.target.value })}
              />
            </label>
          </fieldset>
          <p>Saving changes the limits. Continue work separately.</p>
          <div className="app-actions">
            <Button primary type="submit" disabled={disabled || stale}>
              Save allowance
            </Button>
          </div>
        </form>
      </section>
    </>
  );
}
