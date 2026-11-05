import { useId } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { DraftRecovery } from '../reviews/DraftRecovery';
import type { Agent, Message, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy, SourceEvent } from '../automation/api';
import { useWorkItemOperation } from './useWorkItemOperation';
import { EvidenceChooser } from './WorkItemEvidence';
import { WorkGrant, suggestedParticipants } from './WorkGrant';
import { useWorkDefaults } from './useWorkDefaults';
import type { WorkBudget, WorkItem, WorkItemInput } from './api';

export function localDateTime(value: string) {
  if (!value) return '';
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}

export function utcDateTime(value: string) {
  return value ? new Date(value).toISOString() : '';
}

export const initialWorkBudget: WorkBudget = {
  maxEvaluations: 12,
  maxTasks: 4,
  maxModelRequests: 96,
  maxActions: 16,
  maxFollowUps: 8,
};

const limits: { key: keyof WorkBudget; label: string; max: number }[] = [
  { key: 'maxEvaluations', label: 'Assessments', max: 1000 },
  { key: 'maxTasks', label: 'Collaborations', max: 100 },
  { key: 'maxModelRequests', label: 'Model requests', max: 9600 },
  { key: 'maxActions', label: 'Actions', max: 1000 },
  { key: 'maxFollowUps', label: 'Follow-ups', max: 100 },
];

export function BudgetEditor({
  value,
  onChange,
  label = 'Total allowance',
}: {
  value: WorkBudget;
  onChange: (value: WorkBudget) => void;
  label?: string;
}) {
  return (
    <fieldset>
      <legend>{label}</legend>
      <div className="work-item-limits">
        {limits.map(({ key, label, max }) => (
          <label key={key}>
            {label}
            <input
              className="p9-field"
              type="number"
              min={key === 'maxFollowUps' ? 0 : 1}
              max={max}
              required
              value={value[key]}
              onChange={(event) =>
                onChange({ ...value, [key]: Number(event.target.value) })
              }
            />
          </label>
        ))}
      </div>
    </fieldset>
  );
}

type CreateDraft = Omit<WorkItemInput, 'requestId'> & {
  deadline?: string;
  expiryEdited?: boolean;
};

export function WorkItemForm({
  roomId,
  members,
  policies,
  defaultAgentId,
  sourceEvent,
  sourceMessage,
  availability,
  onCreated,
  onClose,
}: {
  roomId: string;
  members: Agent[];
  policies: AutomationPolicy[];
  defaultAgentId?: string;
  sourceEvent?: SourceEvent;
  sourceMessage?: Message;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  onCreated: (item: WorkItem) => void;
  onClose: () => void;
}) {
  const { user } = useAccount();
  const goalId = useId();
  const scope = sourceMessage?.id ?? sourceEvent?.id ?? 'manual';
  const draft = usePrivateDraft<CreateDraft | null>(
    'conversation',
    `work-item-create:${roomId}:${scope}`,
    null,
  );
  const values = draft.value ?? {
    goal: sourceMessage?.content ?? sourceEvent?.preview ?? '',
    completionCriteria: '',
    evidenceIds: sourceEvent ? [sourceEvent.id] : [],
    policyId: '',
    follow: Boolean(sourceMessage || sourceEvent),
  };
  const settings = useWorkDefaults(roomId, values.policyId);
  const defaults = settings.defaults;
  const participants =
    values.participantAgentIds ??
    suggestedParticipants(members, defaults, defaultAgentId);
  const first = participants.includes(values.firstAgentId ?? '')
    ? values.firstAgentId!
    : (participants[0] ?? '');
  const operation = useWorkItemOperation(
    `create:${roomId}:${scope}`,
    (item) => {
      draft.setValue(null);
      onCreated(item);
    },
  );
  const delegated = policies.filter(
    (policy) =>
      policy.enabled &&
      policy.mode === 'auto' &&
      policy.ownerUserId === user.id,
  );
  const selectedPolicy = policies.find(
    (policy) => policy.id === values.policyId,
  );
  const disabled = operation.blocked || !draft.loaded || Boolean(draft.error);
  const invalidParticipants =
    !participants.length ||
    participants.some(
      (id) => !members.some((agent) => agent.id === id && agent.enabled),
    );
  const followLabel = sourceMessage
    ? 'Follow replies'
    : sourceEvent?.sourceId === 'room' &&
        sourceEvent.threadKey === `${roomId}:room`
      ? 'Follow Room discussion'
      : 'Follow this conversation';

  function edit(change: Partial<CreateDraft>) {
    draft.setValue({ ...values, ...change });
  }

  function create(start: boolean) {
    const { expiryEdited, deadline, ...input } = values;
    void operation.create(roomId, {
      ...input,
      start,
      policyRevision: values.policyId ? selectedPolicy?.revision : undefined,
      budget: values.budget ?? defaults?.budget,
      expiresAt: expiryEdited
        ? utcDateTime(values.expiresAt ?? '')
        : (values.expiresAt ??
          (deadline ? utcDateTime(deadline) : defaults?.expiresAt)),
      sourceEventId: sourceEvent?.id,
      messageId: sourceMessage?.id,
      participantAgentIds: start ? participants : undefined,
      firstAgentId: start ? first : undefined,
    });
  }

  return (
    <form
      className="work-item-form"
      aria-label="New work"
      onSubmit={(event) => {
        event.preventDefault();
        create(true);
      }}
    >
      <div className="workspace-panel-heading">
        <h3>New work</h3>
        <Button onClick={onClose}>Back to Work</Button>
      </div>
      <DraftRecovery draft={draft} preview={(value) => value?.goal ?? ''} />
      <fieldset disabled={disabled}>
        <label htmlFor={goalId}>What needs to happen?</label>
        <textarea
          id={goalId}
          className="p9-field"
          rows={3}
          required
          maxLength={16000}
          autoFocus
          value={values.goal}
          onChange={(event) => edit({ goal: event.target.value })}
        />
        {(sourceMessage || sourceEvent) && (
          <div className="work-item-context">
            <details>
              <summary>
                {sourceMessage
                  ? `From ${sourceMessage.authorName}`
                  : 'Original context'}
              </summary>
              <p>
                {sourceMessage?.content ??
                  sourceEvent?.content ??
                  sourceEvent?.preview}
              </p>
            </details>
            <Checkbox
              label={followLabel}
              checked={values.follow ?? false}
              onChange={(event) => edit({ follow: event.target.checked })}
            />
          </div>
        )}
        <WorkGrant
          members={members}
          selected={participants}
          defaults={defaults}
          maxRequests={values.budget?.maxModelRequests}
          firstAgentId={first}
          onChange={(participantAgentIds) => edit({ participantAgentIds })}
          onFirstChange={(firstAgentId) => edit({ firstAgentId })}
        />
        <details className="work-item-options">
          <summary>Options</summary>
          <div className="work-item-form">
            <label>
              Due date
              <input
                className="p9-field"
                type="date"
                value={values.dueAt ?? ''}
                onChange={(event) => edit({ dueAt: event.target.value })}
              />
            </label>
            <label>
              Complete when
              <textarea
                className="p9-field"
                rows={2}
                value={values.completionCriteria ?? ''}
                onChange={(event) =>
                  edit({ completionCriteria: event.target.value })
                }
              />
            </label>
            {!values.completionCriteria?.trim() && (
              <p>You will confirm when this work is done.</p>
            )}
            <EvidenceChooser
              roomId={roomId}
              selected={values.evidenceIds ?? []}
              onChange={(evidenceIds) => edit({ evidenceIds })}
            />
            {delegated.length > 0 || values.policyId ? (
              <Select
                label="Future follow-up"
                value={values.policyId ?? ''}
                onChange={(event) => edit({ policyId: event.target.value })}
              >
                <option value="">I'll decide</option>
                {delegated.map((policy) => (
                  <option value={policy.id} key={policy.id}>
                    {policy.sourceId === 'room' ? 'Room rule' : policy.sourceId}
                  </option>
                ))}
                {values.policyId &&
                  !delegated.some(
                    (policy) => policy.id === values.policyId,
                  ) && (
                    <option value={values.policyId}>
                      Saved rule unavailable
                    </option>
                  )}
              </Select>
            ) : null}
            <details>
              <summary>Allowance and access</summary>
              <div className="work-item-form">
                <BudgetEditor
                  value={values.budget ?? defaults?.budget ?? initialWorkBudget}
                  onChange={(budget) => edit({ budget })}
                />
                <label>
                  Authorization expires
                  <input
                    className="p9-field"
                    type="datetime-local"
                    value={
                      values.expiryEdited
                        ? (values.expiresAt ?? '')
                        : localDateTime(
                            values.expiresAt ??
                              values.deadline ??
                              defaults?.expiresAt ??
                              '',
                          )
                    }
                    onChange={(event) =>
                      edit({
                        expiresAt: event.target.value,
                        expiryEdited: true,
                      })
                    }
                  />
                </label>
                <p>
                  Limits apply across all work sessions. Expiry stops new
                  automatic work; it does not set the due date.
                </p>
              </div>
            </details>
          </div>
        </details>
      </fieldset>
      {selectedPolicy && (
        <div className="work-item-delegation">
          <p>
            Future evidence may start work automatically under this rule and the
            total allowance.
          </p>
          {selectedPolicy.allowComments && (
            <p>
              May send questions and results as comments to the configured
              source.
            </p>
          )}
          <details>
            <summary>Automatic follow-up rule</summary>
            <pre>{selectedPolicy.rules}</pre>
          </details>
        </div>
      )}
      {settings.error && (
        <p className="app-error" role="alert">
          {settings.error}{' '}
          <Button onClick={settings.retry}>Retry work settings</Button>
        </p>
      )}
      {availability?.available === false && (
        <p className="app-error" role="alert">
          {availability.reason}
        </p>
      )}
      {operation.feedback}
      <div className="app-actions">
        <Button
          type="submit"
          primary
          disabled={
            disabled ||
            !values.goal.trim() ||
            !defaults ||
            invalidParticipants ||
            availability?.available === false
          }
        >
          {operation.busy ? 'Saving…' : 'Start work'}
        </Button>
        <Button
          disabled={
            disabled ||
            !values.goal.trim() ||
            (Boolean(values.policyId) && !defaults)
          }
          onClick={() => create(false)}
        >
          Keep track
        </Button>
      </div>
    </form>
  );
}
