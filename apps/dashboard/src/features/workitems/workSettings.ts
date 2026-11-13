import type { WorkBudget } from './api';

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

export const workBudgetLimits: {
  key: keyof WorkBudget;
  label: string;
  max: number;
}[] = [
  { key: 'maxEvaluations', label: 'Assessments', max: 1000 },
  { key: 'maxTasks', label: 'Collaborations', max: 100 },
  { key: 'maxModelRequests', label: 'Model requests', max: 9600 },
  { key: 'maxActions', label: 'Actions', max: 1000 },
  { key: 'maxFollowUps', label: 'Follow-ups', max: 100 },
];
