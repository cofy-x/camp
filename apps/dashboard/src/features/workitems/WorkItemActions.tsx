import { useState } from 'react';
import { Button, Checkbox, Select } from '@cofy-x/camp-platinum';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import type { AutomationPolicy } from '../automation/api';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import { WorkGrant } from './WorkGrant';
import { EvidenceChooser } from './WorkItemEvidence';
import { WorkItemAllowance } from './WorkItemAllowance';
import type { WorkOperation } from './useWorkItemOperation';
import type { WorkItem } from './api';
import { useWorkAction } from './useWorkAction';
import { legacyWorkIntent } from './workActionDraft';

export function WorkItemActions({
  item,
  readsActive,
  members,
  policies,
  contextReady,
  defaultAgentId,
  availability,
  operation,
  view,
  onViewChange,
  managementSection,
  onManage,
  readOnly,
}: {
  item: WorkItem;
  readsActive: boolean;
  members: Agent[];
  policies: AutomationPolicy[];
  contextReady: boolean;
  defaultAgentId?: string;
  availability?: Pick<WorkspaceStatus, 'available' | 'reason'>;
  operation: WorkOperation;
  view: string;
  managementSection: string;
  onManage: (section: string) => void;
  onViewChange: (view: string) => void;
  readOnly: boolean;
}) {
  const [allowanceProblem, setAllowanceProblem] = useState('');
  const [allowanceDraft, setAllowanceDraft] = useState(false);
  const [allowanceRecoveryOpen, setAllowanceRecoveryOpen] = useState(false);
  const {
    settings,
    question,
    hasQuestion,
    completionQuestion,
    draft,
    values,
    section,
    intent,
    content,
    comment,
    participants,
    first,
    newExecution,
    contextDependent,
    invalidParticipants,
    remaining,
    expired,
    exhausted,
    disabled,
    stale,
    active,
    availablePolicies,
    chosenPolicyId,
    policy,
    executionUnavailable,
    futureReminder,
    reminderError,
    setReminderError,
    contentId,
    questionTextId,
    waitConditionId,
    edit,
    choose,
    current,
    submit,
    labels,
  } = useWorkAction({
    item,
    readsActive,
    members,
    policies,
    contextReady,
    defaultAgentId,
    availability,
    operation,
    view,
    managementSection,
    readOnly,
  });
  return (
    <div
      className="work-item-actions"
      hidden={
        view !== 'overview' &&
        view !== 'manage' &&
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
            {!active && !hasQuestion && !item.result && (
              <Button
                primary
                disabled={disabled}
                onClick={() => choose('start')}
              >
                {item.result ? 'Request changes…' : 'Start work…'}
              </Button>
            )}
            {!active &&
              !hasQuestion &&
              item.result &&
              item.completionMode === 'human' && (
                <Button
                  primary
                  disabled={disabled}
                  onClick={() => choose('complete')}
                >
                  Mark done…
                </Button>
              )}
          </div>
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
          {values.kind && !legacyWorkIntent[values.kind] && (
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
            ) : intent === 'completion' ? (
              <>
                <Select
                  label="Completion"
                  value={
                    values.completionMode ??
                    (item.completionMode === 'automatic'
                      ? 'automatic'
                      : 'human')
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
                </Select>
                <label>
                  Complete when
                  <textarea
                    className="p9-field"
                    rows={2}
                    value={values.completionCriteria ?? item.completionCriteria}
                    placeholder="Use the goal"
                    onChange={(event) =>
                      edit({ completionCriteria: event.target.value })
                    }
                  />
                </label>
                <p>
                  Applies to future work sessions. Existing questions and
                  results stay recorded.
                </p>
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
                  disabled={!contextReady}
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
            {newExecution && contextReady && (
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
              <Button onClick={() => void settings.retry()}>
                Retry work settings
              </Button>
            </p>
          )}
          <div className="app-actions">
            <Button
              type="submit"
              primary
              disabled={
                disabled ||
                stale ||
                (contextDependent && !contextReady) ||
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
                  onManage('allowance');
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
          contextReady={contextReady}
        />
      </div>
    </div>
  );
}
