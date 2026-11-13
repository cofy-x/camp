import { useEffect, useId, useState } from 'react';
import { useAccount } from '../../team/AuthContext';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import type { AutomationPolicy } from '../automation/api';
import type { Agent, WorkspaceStatus } from '../workspace/api';
import { suggestedParticipants } from './WorkGrant';
import { utcDateTime } from './workSettings';
import { useWorkDefaults } from './useWorkDefaults';
import type { WorkOperation } from './useWorkItemOperation';
import type { WorkActionInput, WorkActionName, WorkItem } from './api';
import { confirmedWorkAction } from './workRequestRecovery';
import {
  restoreWorkActionDraft,
  workManagementForIntent,
  workSectionIntent,
  type WorkActionDraft,
} from './workActionDraft';

export function useWorkAction({
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
  readOnly: boolean;
}) {
  const { user } = useAccount();
  const [reminderError, setReminderError] = useState('');
  const contentId = useId();
  const questionTextId = useId();
  const waitConditionId = useId();
  const settings = useWorkDefaults(item.roomId, '', readsActive && !readOnly);
  const question =
    item.execution?.question && !item.execution.question.answer
      ? item.execution.question
      : null;
  const hasQuestion = Boolean(question || item.question);
  const completionQuestion = !question && item.questionKind === 'completion';
  const draft = usePrivateDraft<WorkActionDraft | null>(
    'conversation',
    `work-item-decision:${item.id}`,
    null,
  );
  const values = restoreWorkActionDraft(
    draft.value ?? {
      revision: item.revision,
      intent: hasQuestion && !completionQuestion ? 'answer' : '',
      taskId: question ? item.execution?.taskId : undefined,
      taskRevision: question ? item.execution?.revision : undefined,
      questionId: question?.id,
    },
  );
  const savedIntent = values.intent ?? '';
  const section =
    managementSection || workManagementForIntent(savedIntent) || 'allowance';
  const intent =
    view === 'manage'
      ? workSectionIntent(section, savedIntent)
      : view === 'overview'
        ? hasQuestion && !completionQuestion
          ? 'answer'
          : values.section || workManagementForIntent(savedIntent)
            ? ''
            : savedIntent
        : '';
  const content = values.content ?? '';
  const comment = values.comment ?? '';
  const participants =
    values.participantAgentIds ??
    suggestedParticipants(members, settings.defaults, defaultAgentId);
  const first = participants.includes(values.firstAgentId ?? '')
    ? values.firstAgentId!
    : (participants[0] ?? '');
  const newExecution = intent === 'start' || intent === 'answer_continue';
  const contextDependent = newExecution || intent === 'delegate';
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
    const action = confirmedWorkAction(saved);
    if (action && action !== 'authorize' && action !== 'unfollow')
      clearDraft(null);
  }, [operation.confirmation, clearDraft]);

  function edit(change: Partial<WorkActionDraft>) {
    draft.setValue({
      ...values,
      intent,
      section: view === 'manage' ? section : '',
      ...change,
    });
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
    if (!intent || disabled || stale || (contextDependent && !contextReady))
      return;
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
    if (intent === 'completion')
      Object.assign(body, {
        completionMode:
          values.completionMode ??
          (item.completionMode === 'automatic' ? 'automatic' : 'human'),
        completionCriteria:
          values.completionCriteria ?? item.completionCriteria,
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
    completion: 'Save completion settings',
    stop: 'Stop work',
    attach: 'Add context',
    follow: 'Add context and follow',
    delegate: 'Save automatic follow-up',
  };

  return {
    user,
    settings,
    question,
    hasQuestion,
    completionQuestion,
    draft,
    values,
    savedIntent,
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
  };
}
