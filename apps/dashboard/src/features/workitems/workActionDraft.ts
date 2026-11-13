import type { WorkActionInput, WorkActionName } from './api';

export type WorkActionDraft = Omit<WorkActionInput, 'requestId'> & {
  intent?: WorkActionName | '';
  section?: string;
  // Keep earlier private drafts readable across this presentation change.
  kind?: string;
  reason?: string;
  question?: string;
};

export const legacyWorkIntent: Record<string, WorkActionName> = {
  execute: 'start',
  answer: 'answer',
  wait: 'wait',
  evidence: 'attach',
  follow: 'follow',
  cancel: 'stop',
  complete: 'complete',
};
const management: Record<string, string> = {
  completion: 'completion',
  wait: 'reminder',
  attach: 'context',
  follow: 'context',
  delegate: 'automatic',
  stop: 'stop',
  answer_continue: 'continue',
};

export function workManagementForIntent(intent: WorkActionName | '') {
  return management[intent] ?? '';
}

export function workSectionIntent(
  section: string,
  saved: WorkActionName | '',
): WorkActionName | '' {
  const intents: Record<string, WorkActionName> = {
    reminder: 'wait',
    context: saved === 'follow' ? 'follow' : 'attach',
    automatic: 'delegate',
    stop: 'stop',
    reply: 'answer',
    continue: 'answer_continue',
    changes: 'start',
    complete: 'complete',
    completion: 'completion',
  };
  return intents[section] ?? '';
}

export function restoreWorkActionDraft(
  value: WorkActionDraft,
): WorkActionDraft {
  const intent = value.intent ?? legacyWorkIntent[value.kind ?? ''] ?? '';
  return {
    ...value,
    intent,
    content:
      value.content ??
      (intent === 'answer' || intent === 'answer_continue'
        ? value.reason
        : undefined),
    comment: value.comment ?? value.reason,
  };
}
