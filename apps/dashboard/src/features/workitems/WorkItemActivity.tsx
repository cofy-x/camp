import { Report } from '../workspace/Report';
import { WorkItemHistory } from './WorkItemHistory';

const labels: Record<string, string> = {
  create: 'Work created',
  execute: 'Work started',
  start: 'Work started',
  result: 'Result saved',
  complete: 'Completed',
  completion_pending: 'Waiting for delivery',
  answer: 'Answer received',
  answer_continue: 'Work continued',
  wait: 'Waiting',
  request_judgment: 'Decision needed',
  stop: 'Work stopped',
  cancel: 'Work stopped',
  authorize: 'Allowance updated',
  delegate: 'Follow-up changed',
  evidence: 'Context added',
  attach: 'Context added',
  follow: 'Following conversation',
  unfollow: 'Stopped following',
  action_result: 'Delivery updated',
  superseded: 'New context received',
  ignore: 'Observation recorded',
};

export function WorkItemActivity({
  itemId,
  active,
  refreshKey,
  onOpenTask,
}: {
  itemId: string;
  active: boolean;
  refreshKey: number;
  onOpenTask: (id: string) => void;
}) {
  return (
    <div className="work-item-activity" hidden={!active}>
      <WorkItemHistory
        itemId={itemId}
        kind="timeline"
        title="Activity"
        initial={[]}
        active={active}
        refreshKey={refreshKey}
        render={(entry) => (
          <article className="work-activity-entry" key={entry.id}>
            {entry.taskId ? (
              <button
                type="button"
                className="work-activity-heading"
                onClick={() => onOpenTask(entry.taskId)}
              >
                <strong>{labels[entry.kind] ?? 'Work updated'}</strong>
                <time dateTime={entry.createdAt}>
                  {new Date(entry.createdAt).toLocaleString()}
                </time>
              </button>
            ) : (
              <div className="work-activity-heading">
                <strong>{labels[entry.kind] ?? 'Work updated'}</strong>
                <time dateTime={entry.createdAt}>
                  {new Date(entry.createdAt).toLocaleString()}
                </time>
              </div>
            )}
            {entry.content && (
              <Report
                content={entry.content}
                stateKey={`work-activity:${itemId}:${entry.id}`}
              />
            )}
          </article>
        )}
      />
    </div>
  );
}
