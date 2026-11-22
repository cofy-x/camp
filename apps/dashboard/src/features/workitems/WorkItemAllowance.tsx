import { useEffect, useId, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { localDateTime, utcDateTime, workBudgetLimits } from './workSettings';
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
  modelRequestsOnly,
  readOnly,
  contextReady,
}: {
  item: WorkItem;
  operation: WorkOperation;
  onRecoveryChange: (problem: string) => void;
  onDraftChange: (hasDraft: boolean) => void;
  modelRequestsOnly: boolean;
  readOnly: boolean;
  contextReady: boolean;
}) {
  const fieldId = useId();
  const expiryId = `${fieldId}-expiry`;
  const noteId = `${fieldId}-note`;
  const resumeHintId = `${fieldId}-resume`;
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
  const changedLimits = workBudgetLimits.filter(
    ({ key }) => values.budget[key] !== item.budget[key],
  );
  // Keep every edited field visible when resuming a draft in the quick form.
  const hasOtherEdits =
    Boolean(values.reason) ||
    changedLimits.some(({ key }) => key !== 'maxModelRequests');
  const [fullDraft, setFullDraft] = useState(false);
  useEffect(() => {
    if (!modelRequestsOnly) setFullDraft(false);
    else if (hasOtherEdits) setFullDraft(true);
  }, [modelRequestsOnly, hasOtherEdits]);
  const focused = modelRequestsOnly && !fullDraft && !hasOtherEdits;
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
  const hasChanges =
    changedLimits.length > 0 || expiryEdited || Boolean(values.reason?.trim());
  const cannotSave = disabled || stale || !hasChanges;
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
            if (cannotSave) return;
            void operation.act(item.id, 'authorize', {
              revision: values.revision,
              comment: values.reason,
              budget: values.budget,
              expiresAt: expiryEdited ? utcDateTime(localExpiry) : undefined,
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
          <fieldset
            className={focused ? 'work-allowance-limits' : undefined}
            disabled={disabled}
          >
            <WorkBudgetEditor
              value={values.budget}
              onChange={(budget) => edit({ budget })}
              current={item}
              modelRequestsOnly={focused}
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
            {!focused && (
              <div className="work-item-limit">
                <label htmlFor={noteId}>Note (optional)</label>
                <textarea
                  id={noteId}
                  className="p9-field"
                  rows={2}
                  value={values.reason ?? ''}
                  onChange={(event) => edit({ reason: event.target.value })}
                />
              </div>
            )}
          </fieldset>
          <div className="app-actions">
            <Button
              primary
              type="submit"
              aria-describedby={modelRequestsOnly ? resumeHintId : undefined}
              disabled={cannotSave}
            >
              Save allowance
            </Button>
            {modelRequestsOnly && (
              <p id={resumeHintId}>Continue work after saving.</p>
            )}
          </div>
        </form>
      </section>
    </>
  );
}
