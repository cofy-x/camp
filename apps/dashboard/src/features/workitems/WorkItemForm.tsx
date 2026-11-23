import { GitHubDeliverySummary } from '../github-delivery/GitHubDeliverySummary';
import { useId, useRef, useState } from 'react';
import { Button, Checkbox, Radio } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import type { Agent, Message, WorkspaceStatus } from '../workspace/api';
import type { AutomationPolicy, SourceEvent } from '../automation/api';
import { useWorkItemOperation } from './useWorkItemOperation';
import { WorkCreationOptions } from './WorkCreationOptions';
import { WorkGrant, suggestedParticipants } from './WorkGrant';
import { useWorkDefaults } from './useWorkDefaults';
import { useWorkExecution } from './useWorkExecution';
import {
  matchesCreationDraft,
  matchesWorkDefaults,
  workDefaultsFields,
  type WorkDefaultsFields,
} from './creationDraft';
import type { WorkItem, WorkItemInput } from './api';
import { utcDateTime } from './workSettings';

type CreateDraft = Omit<WorkItemInput, 'requestId'> & {
  deadline?: string;
  expiryEdited?: boolean;
  projectDefaultsPending?: boolean;
  customDefaults?: WorkDefaultsFields;
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
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'> & {
    retry?: () => Promise<void>;
  };
  onCreated: (item: WorkItem, fromRoom: string) => void;
  onClose: () => void;
  showHeader?: boolean;
}) {
  const { user } = useAccount();
  const { navigate } = useTeam();
  const goalId = useId();
  const startNoticeId = useId();
  const recordNoticeId = useId();
  const settingsModeId = useId();
  const revealingInvalid = useRef(false);
  const [openOptionsRoom, setOpenOptionsRoom] = useState<string>();
  const [githubDestinationRoom, setGitHubDestinationRoom] = useState<string>();
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
          ...workDefaultsFields(project),
          projectRevision: project.revision,
        }
      : {}),
  };
  const usesProject = Boolean(
    project &&
    values.projectRevision === project.revision &&
    matchesWorkDefaults(values, project),
  );
  const staleProject = Boolean(
    values.projectRevision &&
    defaults &&
    values.projectRevision !== project?.revision,
  );
  const useGitHub =
    Boolean(values.githubDelivery) || githubDestinationRoom === roomId;
  const incompleteGitHub = useGitHub && !values.githubDelivery;
  const setOptionsOpen = (open: boolean) =>
    setOpenOptionsRoom(open ? roomId : undefined);
  const participants =
    values.participantAgentIds ??
    suggestedParticipants(members, defaults, defaultAgentId);
  const first = participants.includes(values.firstAgentId ?? '')
    ? values.firstAgentId!
    : (participants[0] ?? '');
  const operation = useWorkItemOperation(
    `create:${draftRoom}:${scope}`,
    (item, _request, fromRoom) => onCreated(item, fromRoom ?? item.roomId),
    [],
    async (item, { draftSnapshot }) => {
      if (!draft.loaded) return false;
      // Only clear the private draft that supplied this request. A newer draft
      // selected through conflict recovery belongs to its own future work.
      const current = draft.getValue();
      if (current && matchesCreationDraft(current, draftSnapshot)) {
        if ('rooms' in current && roomId && roomId !== item.roomId) {
          const rooms = { ...current.rooms };
          delete rooms[item.roomId];
          draft.setValue({ ...current, rooms });
        } else draft.setValue(null);
      }
      return draft.flush();
    },
    roomId,
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
    !staleProject &&
    !incompleteGitHub &&
    !invalidDelivery,
  );
  const canStart =
    canRecord && execution.ready && availability?.available !== false;
  const blockedStart =
    roomId && contextReady && availability?.available === false
      ? availability
      : null;
  const recordingOnly = Boolean(
    blockedStart || execution.issue || execution.error,
  );
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

  function usePersonalDefaults() {
    if (!project) return;
    edit({
      ...workDefaultsFields(project),
      projectRevision: project.revision,
      customDefaults: staleProject
        ? values.customDefaults
        : workDefaultsFields(values),
    });
    setGitHubDestinationRoom(undefined);
    setOptionsOpen(false);
  }

  function customizeWork() {
    edit({
      ...(usesProject ? values.customDefaults : undefined),
      projectRevision: undefined,
      customDefaults: undefined,
    });
    setGitHubDestinationRoom(undefined);
    setOptionsOpen(true);
  }

  function setDeliveryDestination(github: boolean) {
    setGitHubDestinationRoom(github ? roomId : undefined);
    if (!github && values.githubDelivery) edit({ githubDelivery: undefined });
  }

  function create(start: boolean) {
    if (!(start ? canStart : canRecord)) return;
    const {
      expiryEdited,
      deadline,
      customDefaults: _customDefaults,
      projectDefaultsPending: _projectDefaultsPending,
      ...input
    } = values;
    void operation.create(
      roomId,
      {
        ...input,
        projectRevision: usesProject ? input.projectRevision : undefined,
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
      className="work-item-form new-work-form"
      aria-label="New work"
      onInvalid={(event) => {
        const field = event.target;
        if (revealingInvalid.current || !(field instanceof HTMLElement)) return;
        const section = field.closest<HTMLDetailsElement>(
          '.work-creation-option-section',
        );
        if (!section) return;
        revealingInvalid.current = true;
        const options =
          section.closest<HTMLDetailsElement>('.work-item-options');
        if (options) options.open = true;
        section.open = true;
        requestAnimationFrame(() => {
          revealingInvalid.current = false;
          if (field.isConnected && section.open && options?.open) field.focus();
        });
      }}
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
            {project && !staleProject && (
              <fieldset className="work-default-mode">
                <legend>Settings for this Work</legend>
                <div>
                  <Radio
                    name={settingsModeId}
                    label="Use my defaults"
                    checked={usesProject}
                    onChange={usePersonalDefaults}
                  />
                  <Radio
                    name={settingsModeId}
                    label="This Work only"
                    checked={!usesProject}
                    onChange={customizeWork}
                  />
                </div>
              </fieldset>
            )}
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
              editable={!usesProject && !staleProject}
              policyScoped={Boolean(values.policyId)}
              showTotalAllowance
              execution={execution}
              executionNoticeId={blockedStart ? undefined : startNoticeId}
              firstAgentId={first}
              onChange={(participantAgentIds) => edit({ participantAgentIds })}
              onFirstChange={(firstAgentId) => edit({ firstAgentId })}
            />
            {blockedStart && (
              <div
                className="work-execution-notice app-error"
                role="alert"
                id={startNoticeId}
              >
                <span>
                  <strong>Start work unavailable.</strong> {blockedStart.reason}
                </span>
                {blockedStart.retry ? (
                  <Button onClick={() => void blockedStart.retry?.()}>
                    Retry availability
                  </Button>
                ) : (
                  user.role === 'admin' && (
                    <Button
                      onClick={() =>
                        void navigate({ app: 'settings', section: 'readiness' })
                      }
                    >
                      Open Getting Ready
                    </Button>
                  )
                )}
              </div>
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
            {incompleteGitHub && (
              <p role="status">
                Choose a GitHub repository or switch to Room files.
              </p>
            )}
            <WorkCreationOptions
              values={values}
              roomId={roomId}
              defaults={defaults}
              delegated={delegated}
              custom={!usesProject && !staleProject}
              open={openOptionsRoom === roomId}
              onOpenChange={setOptionsOpen}
              useGitHub={useGitHub}
              onDeliveryDestinationChange={setDeliveryDestination}
              onChange={edit}
            />
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
      {staleProject && project && (
        <p role="alert" className="app-error">
          Work defaults changed.{' '}
          <Button onClick={usePersonalDefaults}>Use current defaults</Button>{' '}
          <Button onClick={customizeWork}>
            Keep these choices for this Work
          </Button>
        </p>
      )}
      {staleProject && !project && (
        <p role="alert" className="app-error">
          Work defaults are disabled. Your goal and choices are preserved.{' '}
          <Button onClick={customizeWork}>
            Keep these choices for this Work
          </Button>
        </p>
      )}
      {roomId && contextReady && !defaults && !settings.error && (
        <p role="status">Loading work settings…</p>
      )}
      {settings.error && (
        <p className="app-error" role="alert">
          {settings.error}{' '}
          <Button onClick={() => void settings.retry()}>
            Retry work settings
          </Button>
          {values.policyId && (
            <Button onClick={() => edit({ policyId: '' })}>
              Clear follow-up rule
            </Button>
          )}
        </p>
      )}
      {operation.feedback}
      <div className="app-actions work-create-actions">
        <Button
          type="submit"
          primary={!recordingOnly}
          disabled={!canStart}
          aria-describedby={recordingOnly ? startNoticeId : undefined}
        >
          {operation.busy ? 'Saving…' : 'Start work'}
        </Button>
        <Button
          primary={recordingOnly}
          disabled={!canRecord}
          aria-describedby={recordingOnly ? recordNoticeId : undefined}
          onClick={(event) => {
            if (event.currentTarget.form?.reportValidity()) create(false);
          }}
        >
          Keep track
        </Button>
        {recordingOnly && (
          <span id={recordNoticeId}>Records this without running Agents.</span>
        )}
      </div>
    </form>
  );
}
