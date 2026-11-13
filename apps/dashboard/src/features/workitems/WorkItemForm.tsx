import { useId } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import type { Agent, Message, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy, SourceEvent } from '../automation/api';
import { useWorkItemOperation } from './useWorkItemOperation';
import { EvidenceChooser } from './WorkItemEvidence';
import { WorkGrant, suggestedParticipants } from './WorkGrant';
import { useWorkDefaults } from './useWorkDefaults';
import { matchesCreationDraft } from './creationDraft';
import type { WorkItem, WorkItemInput } from './api';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { initialWorkBudget, localDateTime, utcDateTime } from './workSettings';

type CreateDraft = Omit<WorkItemInput, 'requestId'> & {
  deadline?: string;
  expiryEdited?: boolean;
};

type UnassignedDraft = {
  goal: string;
  rooms: Record<string, Omit<CreateDraft, 'goal'>>;
};

export function WorkItemForm({
  roomId,
  globalDraft = false,
  contextReady = true,
  members,
  policies,
  defaultAgentId,
  sourceEvent,
  sourceMessage,
  availability,
  onCreated,
  onClose,
  showHeader = true,
}: {
  roomId: string;
  globalDraft?: boolean;
  contextReady?: boolean;
  members: Agent[];
  policies: AutomationPolicy[];
  defaultAgentId?: string;
  sourceEvent?: SourceEvent;
  sourceMessage?: Message;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  onCreated: (item: WorkItem) => void;
  onClose: () => void;
  showHeader?: boolean;
}) {
  const { user } = useAccount();
  const goalId = useId();
  const scope = sourceMessage?.id ?? sourceEvent?.id ?? 'manual';
  const draftRoom = globalDraft ? '' : roomId;
  const draft = usePrivateDraft<CreateDraft | UnassignedDraft | null>(
    'conversation',
    `work-item-create:${draftRoom}:${scope}`,
    null,
  );
  const unassigned = draft.value && 'rooms' in draft.value ? draft.value : null;
  const saved = globalDraft
    ? unassigned?.rooms[roomId]
    : draft.value && !('rooms' in draft.value)
      ? draft.value
      : null;
  const initial = {
    completionMode: 'automatic' as const,
    completionCriteria: '',
    evidenceIds: sourceEvent ? [sourceEvent.id] : [],
    policyId: '',
    follow: Boolean(sourceMessage || sourceEvent),
  };
  const values: CreateDraft = {
    ...(saved ?? initial),
    goal:
      draft.value?.goal ?? sourceMessage?.content ?? sourceEvent?.preview ?? '',
  };
  const settings = useWorkDefaults(roomId, values.policyId, contextReady);
  const defaults = settings.defaults;
  const participants =
    values.participantAgentIds ??
    suggestedParticipants(members, defaults, defaultAgentId);
  const first = participants.includes(values.firstAgentId ?? '')
    ? values.firstAgentId!
    : (participants[0] ?? '');
  const operation = useWorkItemOperation(
    `create:${draftRoom}:${scope}`,
    onCreated,
    [],
    async (_item, { draftSnapshot }) => {
      if (!draft.loaded) return false;
      // Only clear the private draft that supplied this request. A newer draft
      // selected through conflict recovery belongs to its own future work.
      if (
        draft.getValue() !== null &&
        matchesCreationDraft(draft.getValue(), draftSnapshot)
      )
        draft.setValue(null);
      return draft.flush();
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
    const next = { ...values, ...change };
    if (globalDraft) {
      const { goal, ...roomValues } = next;
      draft.setValue({
        goal,
        rooms: {
          ...unassigned?.rooms,
          ...(roomId ? { [roomId]: roomValues } : {}),
        },
      });
    } else draft.setValue(next);
  }

  function create(start: boolean) {
    if (
      disabled ||
      !roomId ||
      !contextReady ||
      !values.goal.trim() ||
      (Boolean(values.policyId) && !defaults) ||
      (start &&
        (!defaults || invalidParticipants || availability?.available === false))
    )
      return;
    const { expiryEdited, deadline, ...input } = values;
    void operation.create(
      roomId,
      {
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
      },
      JSON.stringify(draft.getValue()),
    );
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
      {showHeader && (
        <div className="workspace-panel-heading">
          <h3>New work</h3>
          <Button onClick={onClose}>Back to Work</Button>
        </div>
      )}
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
        {roomId && contextReady && (
          <>
            <Select
              label="Completion"
              value={
                values.completionMode ??
                (values.completionCriteria?.trim() ? 'criteria' : 'human')
              }
              onChange={(event) =>
                edit({
                  completionMode: event.target.value as 'automatic' | 'human',
                })
              }
            >
              <option value="automatic">When the goal is met</option>
              <option value="human">I'll confirm</option>
              {!values.completionMode && values.completionCriteria?.trim() && (
                <option value="criteria">Saved completion criteria</option>
              )}
            </Select>
            <WorkGrant
              members={members}
              selected={participants}
              defaults={defaults}
              maxRequests={values.budget?.maxModelRequests}
              firstAgentId={first}
              onChange={(participantAgentIds) => edit({ participantAgentIds })}
              onFirstChange={(firstAgentId) => edit({ firstAgentId })}
            />
            {!defaults && !settings.error && (
              <p role="status">Loading work settings…</p>
            )}
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
                    placeholder={
                      values.completionMode === 'automatic'
                        ? 'Use the goal above'
                        : undefined
                    }
                    value={values.completionCriteria ?? ''}
                    onChange={(event) =>
                      edit({ completionCriteria: event.target.value })
                    }
                  />
                </label>
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
                        {policy.sourceId === 'room'
                          ? 'Room rule'
                          : policy.sourceId}
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
                    <WorkBudgetEditor
                      value={
                        values.budget ?? defaults?.budget ?? initialWorkBudget
                      }
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
          </>
        )}
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
          <Button onClick={() => void settings.retry()}>
            Retry work settings
          </Button>
        </p>
      )}
      {roomId && contextReady && availability?.available === false && (
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
            !roomId ||
            !contextReady ||
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
            !roomId ||
            !contextReady ||
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
