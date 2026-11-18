import { GitHubDeliverySummary } from '../github-delivery/GitHubDeliverySummary';
import { useId, useState } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { GitHubDeliveryChooser } from '../github-delivery/GitHubDeliveryChooser';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import type { Agent, Message, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy, SourceEvent } from '../automation/api';
import { useWorkItemOperation } from './useWorkItemOperation';
import { EvidenceChooser } from './WorkItemEvidence';
import { WorkGrant, suggestedParticipants } from './WorkGrant';
import { useWorkDefaults } from './useWorkDefaults';
import { useWorkExecution } from './useWorkExecution';
import { matchesCreationDraft } from './creationDraft';
import type { WorkItem, WorkItemInput } from './api';
import { WorkBudgetEditor } from './WorkBudgetEditor';
import { initialWorkBudget, localDateTime, utcDateTime } from './workSettings';

type CreateDraft = Omit<WorkItemInput, 'requestId'> & {
  deadline?: string;
  expiryEdited?: boolean;
  projectDefaultsPending?: boolean;
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
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [deliveryReady, setDeliveryReady] = useState(false);
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
  const baseValues: CreateDraft = {
    ...(saved ?? initial),
    goal:
      draft.value?.goal ?? sourceMessage?.content ?? sourceEvent?.preview ?? '',
  };
  const settings = useWorkDefaults(roomId, baseValues.policyId, contextReady);
  const defaults = settings.defaults;
  const project = defaults?.project?.enabled ? defaults.project : undefined;
  const values: CreateDraft = {
    ...baseValues,
    ...((!saved || saved.projectDefaultsPending) && project
      ? {
          completionMode: project.completionMode,
          participantAgentIds: project.participantAgentIds,
          firstAgentId: project.firstAgentId,
          budget: project.budget,
          githubDelivery: project.githubDelivery,
          projectRevision: project.revision,
        }
      : {}),
  };
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
  const invalidDelivery =
    Boolean(values.githubDelivery) &&
    (!deliveryReady ||
      values.completionMode !== 'automatic' ||
      !values.githubDelivery?.baseBranch.trim() ||
      (values.githubDelivery?.mode === 'release' &&
        !values.githubDelivery.tag?.trim() &&
        !values.githubDelivery.versionLine?.trim()));
  const disabled = operation.blocked || !draft.loaded || Boolean(draft.error);
  const invalidParticipants =
    !participants.length ||
    participants.some(
      (id) => !members.some((agent) => agent.id === id && agent.enabled),
    );
  const execution = useWorkExecution(
    roomId,
    participants,
    values.completionMode,
    contextReady &&
      Boolean(defaults) &&
      !invalidParticipants &&
      availability?.available !== false,
  );
  const canRecord = Boolean(
    !disabled &&
    roomId &&
    contextReady &&
    defaults &&
    values.goal.trim() &&
    !invalidDelivery,
  );
  const canStart =
    canRecord && execution.ready && availability?.available !== false;
  const followLabel = sourceMessage
    ? 'Follow replies'
    : sourceEvent?.sourceId === 'room' &&
        sourceEvent.threadKey === `${roomId}:room`
      ? 'Follow Room discussion'
      : 'Follow this conversation';

  function edit(change: Partial<CreateDraft>) {
    const next = {
      ...values,
      ...change,
      projectDefaultsPending:
        !defaults && (!saved || saved.projectDefaultsPending),
    };
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
    if (!(start ? canStart : canRecord)) return;
    const {
      expiryEdited,
      deadline,
      projectDefaultsPending: _projectDefaultsPending,
      ...input
    } = values;
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
          <fieldset
            className="work-proposal"
            aria-label="Work authorization"
            disabled={!defaults}
          >
            {values.completionMode !== 'automatic' && (
              <p className="work-grant-limits">
                {values.completionMode === 'human'
                  ? "You'll confirm completion"
                  : 'Saved completion criteria apply'}
              </p>
            )}
            <WorkGrant
              members={members}
              selected={participants}
              defaults={defaults}
              maxRequests={values.budget?.maxModelRequests}
              execution={execution}
              firstAgentId={first}
              onChange={(participantAgentIds) => edit({ participantAgentIds })}
              onFirstChange={(firstAgentId) => edit({ firstAgentId })}
            />
            {!defaults && !settings.error && (
              <p role="status">Loading work settings…</p>
            )}
            {values.githubDelivery && (
              <GitHubDeliverySummary
                value={values.githubDelivery}
                onReady={setDeliveryReady}
              />
            )}
            {values.githubDelivery && values.completionMode !== 'automatic' && (
              <p role="alert" className="app-error">
                GitHub delivery requires automatic completion.
              </p>
            )}
            <details
              className="work-item-options"
              onToggle={(event) => setOptionsOpen(event.currentTarget.open)}
            >
              <summary>Options</summary>
              <div className="work-item-form">
                <Select
                  label="Completion"
                  value={
                    values.completionMode ??
                    (values.completionCriteria?.trim() ? 'criteria' : 'human')
                  }
                  onChange={(event) =>
                    edit({
                      completionMode: event.target.value as
                        'automatic' | 'human',
                    })
                  }
                >
                  <option value="automatic">When the goal is met</option>
                  <option value="human">I'll confirm</option>
                  {!values.completionMode &&
                    values.completionCriteria?.trim() && (
                      <option value="criteria">
                        Saved completion criteria
                      </option>
                    )}
                </Select>
                {optionsOpen && (
                  <GitHubDeliveryChooser
                    value={values.githubDelivery}
                    onChange={(githubDelivery) => edit({ githubDelivery })}
                  />
                )}
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
          </fieldset>
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
      {values.projectRevision &&
      project &&
      values.projectRevision !== project.revision ? (
        <p role="alert" className="app-error">
          Project settings changed. Your goal is preserved.{' '}
          <Button
            onClick={() =>
              edit({
                projectRevision: project.revision,
                completionMode: project.completionMode,
                budget: project.budget,
                participantAgentIds: project.participantAgentIds,
                firstAgentId: project.firstAgentId,
                githubDelivery: project.githubDelivery,
              })
            }
          >
            Use current defaults
          </Button>
        </p>
      ) : null}
      {values.projectRevision && defaults && !project ? (
        <p role="alert" className="app-error">
          Project defaults are disabled. Your goal and choices are preserved.{' '}
          <Button onClick={() => edit({ projectRevision: undefined })}>
            Use these choices
          </Button>
        </p>
      ) : null}
      {roomId && contextReady && !defaults && !settings.error && (
        <p role="status">Loading work settings…</p>
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
        <Button type="submit" primary disabled={!canStart}>
          {operation.busy ? 'Saving…' : 'Start work'}
        </Button>
        <Button disabled={!canRecord} onClick={() => create(false)}>
          Keep track
        </Button>
      </div>
    </form>
  );
}
