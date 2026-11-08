import { useEffect } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { DraftRecovery } from '../reviews/DraftRecovery';
import { BudgetEditor, localDateTime, utcDateTime } from './WorkItemForm';
import type { WorkOperation } from './useWorkItemOperation';
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
  const draft = usePrivateDraft<AllowanceDraft | null>(
    'conversation',
    `work-item-allowance:${item.id}`,
    null,
  );
  const values = draft.value ?? {
    revision: item.revision,
    budget: item.budget,
    expiresAt: localDateTime(item.expiresAt ?? item.deadline),
    reason: '',
  };
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
    if (
      saved &&
      saved.kind !== 'create' &&
      (saved.kind === 'action'
        ? saved.action === 'authorize'
        : saved.body.kind === 'authorize')
    )
      clearDraft(null);
  }, [operation.confirmation, clearDraft]);

  function edit(change: Partial<AllowanceDraft>) {
    draft.setValue({ ...values, ...change });
  }

  return (
    <>
      <DraftRecovery draft={draft} preview={(value) => value?.reason ?? ''} />
      <section aria-label="Allowance and access">
        <dl className="automation-facts">
          <dt>Work sessions</dt>
          <dd>
            {item.usage.tasks} / {item.budget.maxTasks}
          </dd>
          <dt>Model requests</dt>
          <dd>
            {item.usage.modelRequests} used · {item.usage.reservedModelRequests}{' '}
            reserved / {item.budget.maxModelRequests}
          </dd>
          <dt>Assessments</dt>
          <dd>
            {item.usage.evaluations} / {item.budget.maxEvaluations}
          </dd>
          <dt>External actions</dt>
          <dd>
            {item.usage.actions} / {item.budget.maxActions}
          </dd>
          <dt>Follow-ups</dt>
          <dd>
            {item.usage.followUps} / {item.budget.maxFollowUps}
          </dd>
          <dt>Authorization expires</dt>
          <dd>{new Date(item.expiresAt ?? item.deadline).toLocaleString()}</dd>
        </dl>
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
                ? utcDateTime(values.expiresAt ?? values.deadline ?? '')
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
            <BudgetEditor
              value={values.budget}
              onChange={(budget) => edit({ budget })}
            />
            <label>
              Authorization expires
              <input
                className="p9-field"
                type="datetime-local"
                required
                value={values.expiresAt ?? values.deadline ?? ''}
                onChange={(event) =>
                  edit({ expiresAt: event.target.value, deadlineEdited: true })
                }
              />
            </label>
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
          <p>
            Used and reserved allowance stays charged. Saving starts no work.
          </p>
          <Button primary type="submit" disabled={disabled || stale}>
            Save allowance
          </Button>
        </form>
      </section>
    </>
  );
}
