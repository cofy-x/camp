import type { InboxItem } from './api';

const labels: Record<InboxItem['kind'], string> = {
  mention: 'You were mentioned',
  run_finished: 'An agent finished your request',
  review_requested: 'A document needs your review',
  review_updated: 'A document review was updated',
  task_question: 'Work needs your answer',
  task_attention: 'Work needs attention',
  task_finished: 'Work is ready for review',
  work_item_attention: 'Work needs attention',
  work_item_finished: 'Work completed',
};

export function inboxItemTitle(
  item: Pick<InboxItem, 'kind' | 'title' | 'reviewTitle'>,
) {
  return item.title?.trim() || item.reviewTitle.trim() || labels[item.kind];
}

export function inboxItemDetail(item: Pick<InboxItem, 'question' | 'summary'>) {
  return item.question?.trim() || item.summary?.trim() || '';
}
