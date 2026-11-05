import { useEffect, useId, useState } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { DraftRecovery } from '../reviews/DraftRecovery';
import type { AutomationPolicy } from '../automation/api';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import { WorkGrant, suggestedParticipants } from './WorkGrant';
import { EvidenceChooser } from './WorkItemEvidence';
import { WorkItemAllowance } from './WorkItemAllowance';
import { utcDateTime } from './WorkItemForm';
import { useWorkDefaults } from './useWorkDefaults';
import type { WorkOperation } from './useWorkItemOperation';
import type { WorkActionInput, WorkActionName, WorkItem } from './api';

type ActionDraft = Omit<WorkActionInput, 'requestId'> & {
  intent?: WorkActionName | '';
  // Keep earlier private drafts readable across this presentation change.
  kind?: string;
  reason?: string;
  question?: string;
};

export function WorkItemActions({
  item,
  members,
  policies,
  defaultAgentId,
  availability,
  operation,
  view,
  onViewChange,
  readOnly,
}: {
  item: WorkItem;
  members: Agent[];
  policies: AutomationPolicy[];
  defaultAgentId?: string;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  operation: WorkOperation;
  view: string;
  onViewChange: (view: string) => void;
  readOnly: boolean;
}) {
  const { user } = useAccount();
  const [managementSection, setManagementSection] = useState('');
  const [allowanceProblem, setAllowanceProblem] = useState('');
  const [allowanceDraft, setAllowanceDraft] = useState(false);
  const [allowanceRecoveryOpen, setAllowanceRecoveryOpen] = useState(false);
  const [reminderError, setReminderError] = useState('');
  const contentId = useId();
  const questionTextId = useId();
  const waitConditionId = useId();
  const settings = useWorkDefaults(item.roomId);
  const question =
    item.execution?.question && !item.execution.question.answer
      ? item.execution.question
      : null;
  const hasQuestion = Boolean(question || item.question);
  const completionQuestion = !question && item.questionKind === 'completion';
  const draft = usePrivateDraft<ActionDraft | null>(
    'conversation',
    `work-item-decision:${item.id}`,
    null,
  );
  const values: ActionDraft = draft.value ?? {
    revision: item.revision,
    intent: hasQuestion && !completionQuestion ? 'answer' : '',
    taskId: question ? item.execution?.taskId : undefined,
    taskRevision: question ? item.execution?.revision : undefined,
    questionId: question?.id,
  };
  const legacyIntent: Record<string, WorkActionName> = {
    execute: 'start',
    answer: 'answer',
    wait: 'wait',
    evidence: 'attach',
    follow: 'follow',
    cancel: 'stop',
    complete: 'complete',
  };
  const savedIntent = values.intent ?? legacyIntent[values.kind ?? ''] ?? '';
  const sectionIntent: Record<string, WorkActionName> = {
    reminder: 'wait',
    context: savedIntent === 'follow' ? 'follow' : 'attach',
    automatic: 'delegate',
    stop: 'stop',
    reply: 'answer',
    continue: 'answer_continue',
  };
  const savedSection: Record<string, string> = {
    wait: 'reminder',
    attach: 'context',
    follow: 'context',
    delegate: 'automatic',
    stop: 'stop',
    answer_continue: 'continue',
  };
  const section = managementSection || savedSection[savedIntent] || 'allowance';
  const intent =
    view === 'manage'
      ? (sectionIntent[section] ?? '')
      : view === 'overview'
        ? hasQuestion && !completionQuestion
          ? 'answer'
          : savedSection[savedIntent]
            ? ''
            : savedIntent
        : '';
  const content =
    values.content ??
    (intent === 'answer' || intent === 'answer_continue'
      ? (values.reason ?? '')
      : '');
  const comment = values.comment ?? values.reason ?? '';
  const participants =
    values.participantAgentIds ??
    suggestedParticipants(members, settings.defaults, defaultAgentId);
  const first = participants.includes(values.firstAgentId ?? '')
    ? values.firstAgentId!
    : (participants[0] ?? '');
  const newExecution = intent === 'start' || intent === 'answer_continue';
  const invalidParticipants =
    !participants.length ||
    participants.some(
      (id) => !members.some((agent) => agent.id === id && agent.enabled),
    );
  const remaining =
    item.budget.maxModelRequests - item.usage.reservedModelRequests;
  const expired = Date.parse(item.expiresAt ?? item.deadline) <= Date.now();
  const exhausted = item.usage.tasks >= item.budget.maxTasks || remaining <= 0;
  const disabled =
    readOnly ||
    item.status === 'stopping' ||
    operation.blocked ||
    !draft.loaded ||
    Boolean(draft.error);
  const taskChanged = question
    ? values.taskId !== item.execution?.taskId ||
      values.taskRevision !== item.execution?.revision ||
      values.questionId !== question.id
    : Boolean(values.taskId);
  const stale = values.revision !== item.revision || taskChanged;
  const active = Boolean(item.activeTaskId);
  const availablePolicies = policies.filter(
    (policy) =>
      policy.enabled &&
      policy.mode === 'auto' &&
      policy.ownerUserId === user.id,
  );
  const chosenPolicyId = values.policyId ?? item.policyId ?? '';
  const policy = policies.find((policy) => policy.id === chosenPolicyId);
  const executionUnavailable =
    availability?.available === false || active || expired || exhausted;
  const futureReminder = Date.parse(values.resumeAt ?? '') > Date.now();

  const clearDraft = draft.setValue;
  useEffect(() => {
    const saved = operation.confirmation;
    if (
      saved &&
      saved.kind !== 'create' &&
      (saved.kind === 'action'
        ? saved.action !== 'authorize' && saved.action !== 'unfollow'
        : saved.body.kind !== 'authorize')
    )
      clearDraft(null);
  }, [operation.confirmation, clearDraft]);

  function edit(change: Partial<ActionDraft>) {
    draft.setValue({ ...values, ...change });
  }

  function choose(intent: WorkActionName | '') {
    edit({ intent, policyId: chosenPolicyId });
  }

  function current() {
    edit({
      revision: item.revision,
      taskId: question ? item.execution?.taskId : undefined,
      taskRevision: question ? item.execution?.revision : undefined,
      questionId: question?.id,
    });
  }

  function submit() {
    if (!intent) return;
    if (
      intent === 'wait' &&
      !(Date.parse(values.resumeAt ?? '') > Date.now())
    ) {
      setReminderError('Choose a future time for this reminder.');
      return;
    }
    const body: Omit<WorkActionInput, 'requestId'> = {
      revision: values.revision,
    };
    if (intent === 'answer' || intent === 'answer_continue') {
      Object.assign(body, {
        content,
        taskId: values.taskId,
        taskRevision: values.taskRevision,
        questionId: values.questionId,
      });
    } else if (comment.trim()) body.comment = comment;
    if (newExecution)
      Object.assign(body, {
        participantAgentIds: participants,
        firstAgentId: first,
      });
    if (intent === 'wait')
      Object.assign(body, {
        waitCondition: values.waitCondition,
        resumeAt: utcDateTime(values.resumeAt ?? ''),
      });
    if (intent === 'attach' || intent === 'follow')
      Object.assign(body, {
        evidenceIds: values.evidenceIds,
        sourceEventId:
          intent === 'follow' ? values.evidenceIds?.[0] : undefined,
      });
    if (intent === 'delegate')
      Object.assign(body, {
        policyId: chosenPolicyId,
        policyRevision: policy?.revision,
      });
    void operation.act(item.id, intent, body);
  }

  const labels: Partial<Record<WorkActionName, string>> = {
    start: 'Start work',
    answer: question ? 'Answer and continue' : 'Send answer',
    answer_continue: 'Answer and start work',
    wait: 'Save reminder',
    complete: 'Mark done',
    stop: 'Stop work',
    attach: 'Add context',
    follow: 'Add context and follow',
    delegate: 'Save automatic follow-up',
  };

  return (
    <div
      className="work-item-actions"
      hidden={
        view === 'activity' &&
        !draft.error &&
        !draft.comparison &&
        !allowanceProblem &&
        !(readOnly && (draft.value || allowanceDraft))
      }
    >
      <DraftRecovery
        draft={draft}
        preview={(value) =>
          value?.content ?? value?.comment ?? value?.reason ?? ''
        }
      />
      {!readOnly && view === 'overview' && hasQuestion && (
        <section className="work-item-question" aria-label="Question for you">
          <h4 id={questionTextId}>{question?.text ?? item.question}</h4>
          {question?.options?.length ? (
            <div className="app-actions">
              {question.options.map((option) => (
                <Button
                  key={option}
                  disabled={disabled}
                  onClick={() => edit({ intent: 'answer', content: option })}
                >
                  {option}
                </Button>
              ))}
            </div>
          ) : null}
          {!completionQuestion &&
            intent !== 'answer' &&
            intent !== 'answer_continue' && (
              <Button disabled={disabled} onClick={() => choose('answer')}>
                Answer…
              </Button>
            )}
        </section>
      )}
      {!readOnly &&
        view === 'overview' &&
        !intent &&
        (!hasQuestion || completionQuestion) &&
        item.status !== 'stopping' && (
          <div className="app-actions">
            {completionQuestion && (
              <Button
                primary
                disabled={disabled}
                onClick={() =>
                  void operation.act(item.id, 'complete', {
                    revision: item.revision,
                  })
                }
              >
                Confirm done
              </Button>
            )}
            {completionQuestion &&
              !active &&
              availability?.available !== false && (
                <Button disabled={disabled} onClick={() => choose('start')}>
                  Request changes…
                </Button>
              )}
            {!active && !hasQuestion && (
              <Button
                primary
                disabled={disabled}
                onClick={() => choose('start')}
              >
                {item.result ? 'Request changes…' : 'Start work…'}
              </Button>
            )}
            {!active && !hasQuestion && (
              <Button disabled={disabled} onClick={() => choose('complete')}>
                Mark done…
              </Button>
            )}
            {active && (
              <Button
                disabled={disabled}
                onClick={() => {
                  setManagementSection('stop');
                  onViewChange('manage');
                }}
              >
                Stop…
              </Button>
            )}
          </div>
        )}
      {!readOnly && view === 'manage' && (
        <Select
          label="Manage work"
          value={section}
          onChange={(event) => {
            const next = event.target.value;
            setManagementSection(next);
            if (sectionIntent[next]) choose(sectionIntent[next]);
          }}
        >
          <option value="allowance">Allowance and access</option>
          <option value="reminder" disabled={active}>
            Reminder
          </option>
          <option value="context">Context</option>
          <option value="following">Following</option>
          {(availablePolicies.length > 0 || item.policyId) && (
            <option value="automatic">Automatic follow-up</option>
          )}
          {completionQuestion && <option value="reply">Reply</option>}
          {hasQuestion && !question && (
            <option
              value="continue"
              disabled={
                availability?.available === false || expired || exhausted
              }
            >
              Answer and start work
            </option>
          )}
          <option value="stop">
            {active ? 'Stop work' : 'Stop following up'}
          </option>
        </Select>
      )}
      {!readOnly && intent && (
        <form
          className="work-item-form"
          aria-label="Next step"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          {values.kind && !legacyIntent[values.kind] && (
            <details>
              <summary>Earlier saved decision</summary>
              <p>{values.reason}</p>
              <p>{values.question}</p>
            </details>
          )}
          {stale && (
            <p className="app-error" role="alert">
              This work changed. Your input is preserved. Review its current
              state before continuing.{' '}
              <Button disabled={disabled} onClick={current}>
                Use current state
              </Button>
            </p>
          )}
          <fieldset disabled={disabled}>
            {intent === 'answer' || intent === 'answer_continue' ? (
              <>
                <label
                  htmlFor={contentId}
                  className={
                    view === 'overview' && hasQuestion
                      ? 'visually-hidden'
                      : undefined
                  }
                >
                  Your answer
                </label>
                <textarea
                  id={contentId}
                  aria-describedby={
                    view === 'overview' && hasQuestion
                      ? questionTextId
                      : undefined
                  }
                  className="p9-field"
                  rows={3}
                  required
                  maxLength={16000}
                  value={content}
                  onChange={(event) => edit({ content: event.target.value })}
                />
                {question && (
                  <p>
                    Continues the current work session within its existing
                    allowance.
                  </p>
                )}
                {!question && intent === 'answer' && item.policyId && (
                  <p>The automatic rule may continue work after your answer.</p>
                )}
              </>
            ) : intent === 'wait' ? (
              <>
                <label>
                  Remind me at
                  <input
                    className="p9-field"
                    type="datetime-local"
                    required
                    step={1}
                    value={values.resumeAt ?? ''}
                    onChange={(event) => {
                      setReminderError('');
                      edit({ resumeAt: event.target.value });
                    }}
                  />
                </label>
                <div className="automation-input">
                  <label htmlFor={waitConditionId}>
                    Waiting for (optional)
                  </label>
                  <textarea
                    id={waitConditionId}
                    className="p9-field"
                    rows={2}
                    value={values.waitCondition ?? ''}
                    onChange={(event) =>
                      edit({ waitCondition: event.target.value })
                    }
                  />
                </div>
                <p>
                  {item.policyId
                    ? 'The automatic rule may resume work at this time.'
                    : 'The reminder will appear in your Inbox.'}
                </p>
              </>
            ) : intent === 'attach' || intent === 'follow' ? (
              <>
                <EvidenceChooser
                  roomId={item.roomId}
                  selected={values.evidenceIds ?? []}
                  onChange={(evidenceIds) =>
                    edit({
                      evidenceIds,
                      intent:
                        evidenceIds.length === 1 && intent === 'follow'
                          ? 'follow'
                          : 'attach',
                    })
                  }
                  initiallyOpen
                />
                {values.evidenceIds?.length === 1 && (
                  <Checkbox
                    label="Follow this conversation"
                    checked={intent === 'follow'}
                    onChange={(event) =>
                      edit({
                        intent: event.target.checked ? 'follow' : 'attach',
                      })
                    }
                  />
                )}
                {intent === 'follow' && (
                  <p>
                    Future events in this conversation will be added as context.
                    {item.policyId ? ' The automatic rule may start work.' : ''}
                  </p>
                )}
              </>
            ) : intent === 'delegate' ? (
              <>
                <Select
                  label="Automatic follow-up"
                  value={chosenPolicyId}
                  onChange={(event) => edit({ policyId: event.target.value })}
                >
                  <option value="">I'll decide</option>
                  {availablePolicies.map((policy) => (
                    <option key={policy.id} value={policy.id}>
                      {policy.sourceId === 'room'
                        ? 'Room rule'
                        : policy.sourceId}
                    </option>
                  ))}
                  {chosenPolicyId &&
                    !availablePolicies.some(
                      (policy) => policy.id === chosenPolicyId,
                    ) && (
                      <option value={chosenPolicyId}>
                        Saved rule unavailable
                      </option>
                    )}
                </Select>
                {chosenPolicyId && (
                  <>
                    <p>
                      Future evidence may start work automatically within the
                      remaining allowance. Saving does not start work or replay
                      earlier events.
                    </p>
                    <details>
                      <summary>Rule and permissions</summary>
                      <pre>{policy?.rules ?? 'This rule is unavailable.'}</pre>
                      <p>
                        {policy?.participantAgentIds
                          .map(
                            (id) =>
                              members.find((agent) => agent.id === id)?.name ??
                              id,
                          )
                          .join(', ')}
                      </p>
                      <p>
                        {policy?.allowComments
                          ? 'May send comments to the configured source.'
                          : 'No external comments.'}
                      </p>
                    </details>
                  </>
                )}
              </>
            ) : (
              <>
                {intent === 'complete' && (
                  <>
                    <p>
                      {item.completionCriteria ||
                        'Confirm that this work is done.'}
                    </p>
                    <p>Unconfirmed external actions keep this work waiting.</p>
                  </>
                )}
                {intent === 'stop' && (
                  <p>
                    Stops the current work and future follow-ups. Saved results
                    remain available.
                  </p>
                )}
                <label>
                  {intent === 'start'
                    ? 'What should happen next? (optional)'
                    : 'Note (optional)'}
                  <textarea
                    className="p9-field"
                    rows={2}
                    maxLength={16000}
                    value={comment}
                    onChange={(event) => edit({ comment: event.target.value })}
                  />
                </label>
              </>
            )}
            {newExecution && (
              <WorkGrant
                members={members}
                selected={participants}
                defaults={settings.defaults}
                maxRequests={remaining}
                firstAgentId={first}
                onChange={(participantAgentIds) =>
                  edit({ participantAgentIds })
                }
                onFirstChange={(firstAgentId) => edit({ firstAgentId })}
              />
            )}
          </fieldset>
          {intent === 'wait' &&
            (reminderError || (values.resumeAt && !futureReminder)) && (
              <p className="app-error" role="alert">
                Choose a future time for this reminder.
              </p>
            )}
          {newExecution && (expired || exhausted) && (
            <p className="app-error" role="alert">
              {expired
                ? 'Authorization has expired.'
                : 'The work allowance has been used.'}{' '}
              Review Allowance and access in Manage before starting again.
            </p>
          )}
          {newExecution && availability?.available === false && (
            <p className="app-error" role="alert">
              {availability.reason}
            </p>
          )}
          {newExecution && settings.error && (
            <p className="app-error" role="alert">
              {settings.error}{' '}
              <Button onClick={settings.retry}>Retry work settings</Button>
            </p>
          )}
          <div className="app-actions">
            <Button
              type="submit"
              primary
              disabled={
                disabled ||
                stale ||
                (newExecution &&
                  (executionUnavailable ||
                    invalidParticipants ||
                    !settings.defaults)) ||
                ((intent === 'answer' || intent === 'answer_continue') &&
                  !content.trim()) ||
                (intent === 'wait' && !futureReminder) ||
                ((intent === 'attach' || intent === 'follow') &&
                  !values.evidenceIds?.length) ||
                (intent === 'delegate' &&
                  Boolean(chosenPolicyId) &&
                  !availablePolicies.some(
                    (policy) => policy.id === chosenPolicyId,
                  ))
              }
            >
              {operation.busy ? 'Saving…' : (labels[intent] ?? 'Save')}
            </Button>
            {!(view === 'overview' && intent === 'answer' && hasQuestion) && (
              <Button
                disabled={disabled}
                onClick={() =>
                  view === 'manage' ? onViewChange('overview') : choose('')
                }
              >
                Cancel
              </Button>
            )}
          </div>
        </form>
      )}
      {readOnly && draft.value && (
        <section aria-label="Your saved input">
          <p>Your saved input</p>
          <pre>
            {values.content ||
              comment ||
              values.waitCondition ||
              values.question ||
              (values.evidenceIds ?? []).join('\n')}
          </pre>
        </section>
      )}
      {(allowanceProblem || (readOnly && allowanceDraft)) &&
        (readOnly
          ? !allowanceRecoveryOpen
          : view !== 'manage' || section !== 'allowance') && (
          <p
            className={allowanceProblem ? 'app-error' : undefined}
            role={allowanceProblem ? 'alert' : undefined}
          >
            {allowanceProblem || 'Your allowance draft is saved.'}{' '}
            <Button
              onClick={() => {
                if (readOnly) {
                  setAllowanceRecoveryOpen(true);
                  onViewChange('overview');
                } else {
                  onViewChange('manage');
                  setManagementSection('allowance');
                }
              }}
            >
              Review allowance
            </Button>
          </p>
        )}
      <div hidden={readOnly || view !== 'manage' || section !== 'following'}>
        {!(item.watches ?? []).some((watch) => watch.enabled) && (
          <p>No conversations followed.</p>
        )}
        {(item.watches ?? [])
          .filter((watch) => watch.enabled)
          .map((watch) => (
            <div
              className="work-item-watch"
              key={`${watch.sourceId}:${watch.threadKey}`}
            >
              <span>
                {watch.sourceId === 'room'
                  ? watch.threadKey === `${item.roomId}:room`
                    ? 'Following Room discussion'
                    : 'Following replies'
                  : 'Following source conversation'}
              </span>
              <Button
                disabled={disabled}
                onClick={() =>
                  void operation.act(item.id, 'unfollow', {
                    revision: item.revision,
                    sourceId: watch.sourceId,
                    threadKey: watch.threadKey,
                  })
                }
              >
                Stop following
              </Button>
            </div>
          ))}
      </div>
      <div
        hidden={
          readOnly
            ? !allowanceRecoveryOpen
            : view !== 'manage' || section !== 'allowance'
        }
      >
        <WorkItemAllowance
          item={item}
          operation={operation}
          onRecoveryChange={setAllowanceProblem}
          onDraftChange={setAllowanceDraft}
          readOnly={readOnly}
        />
      </div>
    </div>
  );
}
