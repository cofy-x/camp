import { useEffect, useId, useRef, useState } from 'react';
import { registerDraftWriter } from '../../team/draftRegistry';
import { Button, Checkbox, Radio, Select } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { useTeam } from '../../team/TeamContext';
import { DraftRecovery } from '../reviews/DraftRecovery';
import type { Agent } from '../workspace/api';
import { automationApi, type AutomationPolicy, type PolicyValues } from './api';
import { useAutomationOperation } from './useAutomationOperation';

type PolicyDraft = { revision: number; values: PolicyValues; actors: string };

type PolicyChoice = { id: string; name: string; enabled: boolean };

function PolicySelection({
  label,
  prompt,
  choices,
  selected,
  onChange,
  max,
  compact = false,
}: {
  label: string;
  prompt: string;
  choices: PolicyChoice[];
  selected: string[];
  onChange: (ids: string[]) => void;
  max?: number;
  compact?: boolean;
}) {
  const available = choices.filter((choice) => choice.enabled);
  const names = selected.flatMap((id) => {
    const choice = available.find((choice) => choice.id === id);
    return choice ? [choice.name] : [];
  });
  const unavailable = selected.filter(
    (id) => !available.some((choice) => choice.id === id),
  );
  const visibleNames = compact ? names.slice(0, 3) : names;
  const summary =
    visibleNames.join(', ') +
    (names.length > visibleNames.length
      ? ` +${names.length - visibleNames.length} more`
      : '');
  return (
    <fieldset className="automation-policy-selection">
      <legend>{label}</legend>
      <details className="automation-policy-picker">
        <summary>{summary || prompt}</summary>
        <div className="automation-policy-choice-list">
          {available.map((choice) => (
            <Checkbox
              key={choice.id}
              label={choice.name}
              checked={selected.includes(choice.id)}
              disabled={
                !selected.includes(choice.id) &&
                max !== undefined &&
                selected.length >= max
              }
              onChange={(event) =>
                onChange(
                  event.target.checked
                    ? [...selected, choice.id]
                    : selected.filter((id) => id !== choice.id),
                )
              }
            />
          ))}
          {!available.length && <p>No available choices.</p>}
        </div>
      </details>
      {unavailable.map((id) => {
        const name = choices.find((choice) => choice.id === id)?.name ?? id;
        return (
          <div className="automation-policy-unavailable" key={id}>
            <span>{name} · Unavailable</span>
            <Button
              aria-label={`Remove ${name}`}
              onClick={() =>
                onChange(selected.filter((selected) => selected !== id))
              }
            >
              Remove
            </Button>
          </div>
        );
      })}
    </fieldset>
  );
}

function initialValues(policy?: AutomationPolicy): PolicyValues {
  return {
    enabled: policy?.enabled ?? false,
    mode: policy?.mode ?? 'observe',
    rules: policy?.rules ?? '',
    allowedActorIds: policy?.allowedActorIds ?? [],
    participantAgentIds: policy?.participantAgentIds ?? [],
    firstAgentId: policy?.firstAgentId ?? '',
    maxTurns: policy?.maxTurns ?? 6,
    maxModelRequests: policy?.maxModelRequests ?? 24,
    dailyEvaluations: policy?.dailyEvaluations ?? 20,
    dailyTasks: policy?.dailyTasks ?? 5,
    allowAnswers: policy?.allowAnswers ?? false,
    allowComments: policy?.allowComments ?? false,
  };
}

export function PolicyEditor({
  roomId,
  sourceId,
  policy,
  members,
  onSaved,
}: {
  roomId: string;
  sourceId: string;
  policy?: AutomationPolicy;
  members: Agent[];
  onSaved: (policy: AutomationPolicy) => void;
}) {
  const team = useTeam();
  const rulesId = useId();
  const actorsId = useId();
  const modeName = useId();
  const draft = usePrivateDraft<PolicyDraft | null>(
    'conversation',
    `automation-policy:${roomId}:${sourceId}`,
    null,
  );
  const values = draft.value?.values ?? initialValues(policy);
  const revision = draft.value?.revision ?? policy?.revision ?? 0;
  const actors = draft.value?.actors ?? values.allowedActorIds.join('\n');
  const actorIds = [...new Set(actors.split(/[\s,]+/).filter(Boolean))];
  const firstAgentId =
    values.participantAgentIds.length === 1
      ? values.participantAgentIds[0]
      : values.firstAgentId;
  const conflict = revision !== (policy?.revision ?? 0);
  const operation = useAutomationOperation(
    `policy:${roomId}:${sourceId}`,
    (receipt) => {
      if (receipt.policy) onSaved(receipt.policy);
      draft.setValue(null);
    },
  );
  const disabled = operation.blocked || !draft.loaded || Boolean(draft.error);

  function edit(change: Partial<PolicyValues>, actorText = actors) {
    draft.setValue({
      revision,
      values: { ...values, ...change },
      actors: actorText,
    });
  }

  function selectAgents(ids: string[]) {
    edit({
      participantAgentIds: ids,
      firstAgentId: ids.includes(values.firstAgentId)
        ? values.firstAgentId
        : (ids[0] ?? ''),
    });
  }

  const rulesError =
    new TextEncoder().encode(values.rules.trim()).byteLength > 8192
      ? 'Keep rules within 8192 UTF-8 bytes.'
      : '';
  const unavailable = values.participantAgentIds.filter(
    (id) => !members.some((member) => member.id === id && member.enabled),
  );
  const missing =
    values.participantAgentIds.length > 8
      ? 'Choose at most 8 participants.'
      : values.enabled
        ? unavailable.length
          ? 'Remove unavailable participants to enable automation.'
          : !actors.trim() && !values.participantAgentIds.length
            ? 'Select an allowed sender and a participant.'
            : !actors.trim()
              ? 'Select an allowed sender.'
              : !values.participantAgentIds.length
                ? 'Select a participant.'
                : ''
        : '';
  const validAllowance = (value: number, min: number, max: number) =>
    Number.isInteger(value) && value >= min && value <= max;
  const allowanceError = !validAllowance(values.maxTurns, 1, 24)
    ? 'Use 1–24 turns per task.'
    : !validAllowance(values.maxModelRequests, 1, 96)
      ? 'Use 1–96 model requests per task.'
      : !validAllowance(values.dailyEvaluations, values.enabled ? 1 : 0, 1000)
        ? `Use ${values.enabled ? '1' : '0'}–1000 assessments per day.`
        : !validAllowance(values.dailyTasks, values.enabled ? 1 : 0, 100)
          ? `Use ${values.enabled ? '1' : '0'}–100 tasks per day.`
          : '';
  const memberChoices = team.members.map((member) => ({
    ...member,
    name:
      team.members.filter((other) => other.name === member.name).length > 1
        ? `${member.name} (@${member.username})`
        : member.name,
  }));
  const agentChoices = members.map((member) => ({
    ...member,
    name:
      members.filter((other) => other.name === member.name).length > 1
        ? `${member.name} (@${member.handle})`
        : member.name,
  }));
  return (
    <form
      className="automation-form automation-policy-form"
      aria-label="Automation policy"
      aria-busy={operation.busy}
      onSubmit={(event) => {
        event.preventDefault();
        if (
          disabled ||
          conflict ||
          missing ||
          allowanceError ||
          rulesError ||
          (policy && !draft.value)
        )
          return;
        void operation.run(async (requestId) => {
          const result = await automationApi.savePolicy(roomId, sourceId, {
            ...values,
            firstAgentId,
            allowedActorIds: [
              ...new Set(actors.split(/[\s,]+/).filter(Boolean)),
            ],
            requestId,
            revision,
          });
          return { kind: 'policy', policy: result.policy };
        });
      }}
    >
      <fieldset
        disabled={disabled}
        aria-label="Policy settings"
        className="automation-policy-core"
      >
        <fieldset className="automation-policy-mode">
          <legend>Automation mode</legend>
          <div className="automation-policy-mode-options">
            <Radio
              name={modeName}
              value="off"
              label="Off"
              checked={!values.enabled}
              onChange={() => edit({ enabled: false })}
            />
            <Radio
              name={modeName}
              value="observe"
              label="Observe"
              checked={values.enabled && values.mode === 'observe'}
              onChange={() => edit({ enabled: true, mode: 'observe' })}
            />
            <Radio
              name={modeName}
              value="auto"
              label="Auto"
              checked={values.enabled && values.mode === 'auto'}
              onChange={() => edit({ enabled: true, mode: 'auto' })}
            />
          </div>
        </fieldset>
        {values.enabled && (
          <p>
            {values.mode === 'observe'
              ? 'Uses model requests to assess events. Starts no collaborations.'
              : 'Matching requests can start collaborations and update Room files.'}
          </p>
        )}
        <div className="automation-input">
          <label htmlFor={rulesId}>Rules</label>
          <textarea
            id={rulesId}
            className="p9-field"
            rows={3}
            maxLength={8192}
            required={values.enabled}
            value={values.rules}
            onChange={(event) => edit({ rules: event.target.value })}
          />
        </div>
        {rulesError && (
          <p className="app-error" role="alert">
            {rulesError}
          </p>
        )}
        {sourceId === 'room' ? (
          <PolicySelection
            label="Allowed members"
            prompt="Choose members…"
            choices={memberChoices}
            selected={actorIds}
            onChange={(ids) => edit({}, ids.join('\n'))}
            compact
          />
        ) : (
          <div className="automation-input">
            <label htmlFor={actorsId}>Allowed GitHub actor IDs</label>
            <textarea
              id={actorsId}
              className="p9-field"
              rows={2}
              required={values.enabled}
              value={actors}
              placeholder="One numeric GitHub user ID per line"
              onChange={(event) => edit({}, event.target.value)}
            />
          </div>
        )}
      </fieldset>
      <fieldset disabled={disabled} className="automation-policy-participants">
        <PolicySelection
          label="Participants (up to 8)"
          prompt="Choose participants…"
          choices={agentChoices}
          selected={values.participantAgentIds}
          onChange={selectAgents}
          max={8}
        />
        {!members.some((member) => member.enabled) && (
          <p>Add an Agent to this Room before enabling automation.</p>
        )}
        {values.participantAgentIds.length > 1 && (
          <Select
            label="First Agent"
            value={firstAgentId}
            required={values.enabled}
            onChange={(event) => edit({ firstAgentId: event.target.value })}
          >
            <option value="">Choose an Agent</option>
            {agentChoices
              .filter((member) =>
                values.participantAgentIds.includes(member.id),
              )
              .map((member) => (
                <option
                  key={member.id}
                  value={member.id}
                  disabled={!member.enabled}
                >
                  {member.name}
                </option>
              ))}
          </Select>
        )}
      </fieldset>
      <fieldset disabled={disabled}>
        <legend>Allowance</legend>
        <div className="automation-policy-allowance-summary">
          <span>
            {values.maxTurns} turns · {values.maxModelRequests} model requests
            per task
          </span>
          <span>
            {values.dailyEvaluations} assessments · {values.dailyTasks} tasks
            per day
          </span>
        </div>
        <details
          className="automation-policy-allowance-editor"
          onInvalid={(event) => {
            event.currentTarget.open = true;
          }}
        >
          <summary>Adjust allowance</summary>
          <div className="automation-policy-limits">
            <label>
              Turns per task
              <input
                className="p9-field"
                type="number"
                min={1}
                max={24}
                required
                value={values.maxTurns}
                onChange={(event) =>
                  edit({ maxTurns: Number(event.target.value) })
                }
              />
            </label>
            <label>
              Model requests per task
              <input
                className="p9-field"
                type="number"
                min={1}
                max={96}
                required
                value={values.maxModelRequests}
                onChange={(event) =>
                  edit({ maxModelRequests: Number(event.target.value) })
                }
              />
            </label>
            <label>
              Daily assessments
              <input
                className="p9-field"
                type="number"
                min={values.enabled ? 1 : 0}
                max={1000}
                required
                value={values.dailyEvaluations}
                onChange={(event) =>
                  edit({ dailyEvaluations: Number(event.target.value) })
                }
              />
            </label>
            <label>
              Daily tasks
              <input
                className="p9-field"
                type="number"
                min={values.enabled ? 1 : 0}
                max={100}
                required
                value={values.dailyTasks}
                onChange={(event) =>
                  edit({ dailyTasks: Number(event.target.value) })
                }
              />
            </label>
          </div>
        </details>
        {allowanceError && (
          <p className="app-error" role="alert">
            {allowanceError}
          </p>
        )}
        <small>
          Daily limits reset at 00:00 UTC. Request allowances are not billing
          caps.
        </small>
      </fieldset>
      {sourceId !== 'room' && (
        <fieldset disabled={disabled}>
          <legend>GitHub conversation</legend>
          <Checkbox
            label="Allow answers to task questions"
            checked={values.allowAnswers}
            onChange={(event) => edit({ allowAnswers: event.target.checked })}
          />
          {values.allowAnswers && (
            <p>
              Allowed actors can reply with /camp answer &lt;questionId&gt;
              &lt;answer&gt; to the current question.
            </p>
          )}
          <Checkbox
            label="Post task questions and results as GitHub comments"
            checked={values.allowComments}
            onChange={(event) => edit({ allowComments: event.target.checked })}
          />
          {values.allowComments && !policy?.credentialConfigured && (
            <p>Save this policy, then configure a comment credential below.</p>
          )}
        </fieldset>
      )}
      {conflict && (
        <div role="alert" className="app-error">
          <p>The saved policy changed. Your edits are preserved.</p>
          <details>
            <summary>Current Saved Policy</summary>
            <pre>{policy?.rules || 'No saved policy.'}</pre>
            <p>
              Revision {policy?.revision ?? 0} ·{' '}
              {policy?.enabled
                ? policy.mode === 'auto'
                  ? 'Auto'
                  : 'Observe'
                : 'Off'}
            </p>
            {policy && (
              <dl className="automation-facts">
                <dt>Allowed senders</dt>
                <dd>
                  {policy.allowedActorIds
                    .map((id) =>
                      sourceId === 'room'
                        ? (team.members.find((member) => member.id === id)
                            ?.name ?? id)
                        : id,
                    )
                    .join(', ') || 'None'}
                </dd>
                <dt>Participants</dt>
                <dd>
                  {policy.participantAgentIds
                    .map(
                      (id) =>
                        members.find((member) => member.id === id)?.name ?? id,
                    )
                    .join(', ') || 'None'}
                </dd>
                <dt>First Agent</dt>
                <dd>
                  {members.find((member) => member.id === policy.firstAgentId)
                    ?.name ??
                    (policy.firstAgentId || 'None')}
                </dd>
                <dt>Task allowance</dt>
                <dd>
                  {policy.maxTurns} turns · {policy.maxModelRequests} model
                  requests
                </dd>
                <dt>Daily allowance</dt>
                <dd>
                  {policy.dailyEvaluations} assessments · {policy.dailyTasks}{' '}
                  tasks
                </dd>
                <dt>External answers</dt>
                <dd>{policy.allowAnswers ? 'Allowed' : 'Disabled'}</dd>
                <dt>GitHub comments</dt>
                <dd>
                  {policy.allowComments ? 'Allowed' : 'Disabled'} · credential{' '}
                  {policy.credentialConfigured
                    ? 'configured'
                    : 'not configured'}
                </dd>
              </dl>
            )}
          </details>
          <div className="app-actions">
            <Button
              type="button"
              disabled={operation.blocked}
              onClick={() => draft.setValue(null)}
            >
              Reload Saved Policy
            </Button>
            <Button
              type="button"
              disabled={operation.blocked}
              onClick={() =>
                draft.setValue({
                  revision: policy?.revision ?? 0,
                  values,
                  actors,
                })
              }
            >
              Keep My Edits
            </Button>
          </div>
        </div>
      )}
      {(draft.error || draft.comparison) && (
        <fieldset
          disabled={operation.blocked}
          className="automation-draft-recovery"
          aria-label="Private draft recovery"
        >
          <DraftRecovery
            draft={draft}
            preview={(value) => {
              if (!value) return 'No private changes.';
              const policy = value.values;
              return [
                policy.enabled
                  ? policy.mode === 'auto'
                    ? 'Auto'
                    : 'Observe'
                  : 'Off',
                `Rules: ${policy.rules}`,
                `Allowed senders: ${value.actors || 'None'}`,
                `Participants: ${policy.participantAgentIds.map((id) => members.find((member) => member.id === id)?.name ?? id).join(', ') || 'None'}`,
                `First Agent: ${members.find((member) => member.id === policy.firstAgentId)?.name ?? (policy.firstAgentId || 'None')}`,
                `Task allowance: ${policy.maxTurns} turns · ${policy.maxModelRequests} model requests`,
                `Daily allowance: ${policy.dailyEvaluations} assessments · ${policy.dailyTasks} tasks`,
                `External answers: ${policy.allowAnswers ? 'Allowed' : 'Disabled'} · GitHub comments: ${policy.allowComments ? 'Allowed' : 'Disabled'}`,
              ].join('\n\n');
            }}
          />
        </fieldset>
      )}
      {operation.feedback}
      {missing && (
        <p className="app-error" role="alert">
          {missing}
        </p>
      )}
      <div className="app-actions">
        <Button
          primary
          type="submit"
          disabled={
            disabled ||
            conflict ||
            Boolean(allowanceError || rulesError) ||
            (Boolean(policy) && !draft.value) ||
            (values.enabled &&
              (!actors.trim() ||
                !values.participantAgentIds.length ||
                unavailable.length > 0))
          }
        >
          {operation.busy ? 'Saving…' : 'Save Policy'}
        </Button>
        {draft.value && <span>Unsaved policy</span>}
      </div>
    </form>
  );
}

export function CommentCredential({
  policy,
  onSaved,
  onEditingBlocked,
}: {
  policy: AutomationPolicy;
  onSaved: (policy: AutomationPolicy) => void;
  onEditingBlocked?: () => void;
}) {
  const guardKey = useId();
  const details = useRef<HTMLDetailsElement>(null);
  const [leaveError, setLeaveError] = useState('');
  const [token, setToken] = useState('');
  const [actorText, setActorText] = useState<string | null>(null);
  const actorId =
    actorText ?? (policy.commentActorId ? String(policy.commentActorId) : '');
  const [editingRevision, setEditingRevision] = useState<number | null>(null);
  const revision = editingRevision ?? policy.revision;
  const conflict = revision !== policy.revision;
  const operation = useAutomationOperation(
    `credential:${policy.sourceId}`,
    (receipt) => {
      if (receipt.policy) onSaved(receipt.policy);
      setToken('');
      setActorText(null);
      setEditingRevision(null);
    },
  );
  const hasEdits = Boolean(token) || actorText !== null;
  useEffect(() => {
    if (!hasEdits || operation.blocked) return;
    return registerDraftWriter(
      `automation-credential:${guardKey}`,
      async () => {
        setLeaveError('Save or clear these credential edits before leaving.');
        if (details.current) details.current.open = true;
        onEditingBlocked?.();
        return false;
      },
    );
  }, [guardKey, hasEdits, operation.blocked, onEditingBlocked]);
  useEffect(() => {
    if (!hasEdits || operation.blocked) return;
    const protect = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', protect);
    return () => window.removeEventListener('beforeunload', protect);
  }, [hasEdits, operation.blocked]);
  return (
    <details className="automation-credential" ref={details}>
      <summary>
        Comment credential ·{' '}
        {policy.credentialConfigured ? 'Configured' : 'Not configured'}
      </summary>
      <form
        className="automation-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (conflict) return;
          void operation.run((requestId) =>
            automationApi.credential(policy.sourceId, {
              requestId,
              revision,
              token,
              actorId: Number(actorId),
            }),
          );
        }}
      >
        <label>
          GitHub account ID
          <input
            className="p9-field"
            required
            type="number"
            min={1}
            value={actorId}
            disabled={operation.blocked}
            onChange={(event) => {
              setEditingRevision((current) => current ?? policy.revision);
              setActorText(event.target.value);
            }}
          />
        </label>
        <label>
          GitHub comment token
          <input
            className="p9-field"
            required
            type="password"
            minLength={20}
            maxLength={1024}
            autoComplete="new-password"
            value={token}
            disabled={operation.blocked}
            onChange={(event) => {
              setEditingRevision((current) => current ?? policy.revision);
              setToken(event.target.value);
            }}
          />
        </label>
        <p>
          Use a repository-restricted comment token. It stays on the Server.
        </p>
        <Button
          type="submit"
          disabled={operation.blocked || conflict || !token || !actorId}
        >
          Save Comment Credential
        </Button>
        {hasEdits && !operation.blocked && (
          <Button
            onClick={() => {
              setToken('');
              setActorText(null);
              setEditingRevision(null);
              setLeaveError('');
            }}
          >
            Clear Credential Edits
          </Button>
        )}
        {leaveError && hasEdits && (
          <p className="app-error" role="alert">
            {leaveError}
          </p>
        )}
        {operation.feedback}
        {conflict && !operation.blocked && (
          <div className="app-error" role="alert">
            <p>
              The policy changed while you edited this credential. Review the
              saved policy before continuing.
            </p>
            <Button onClick={() => setEditingRevision(policy.revision)}>
              Use Current Policy
            </Button>
          </div>
        )}
      </form>
    </details>
  );
}
