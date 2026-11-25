import { Button } from '@cofy-x/camp-platinum';
import { MarkdownContent } from '../../shared/content/MarkdownContent';
import { ReadingDisclosure } from '../../shared/content/ReadingDisclosure';
import { readStored, writeStored } from '../workspace/conversationState';
import { WorkItemHistory } from './WorkItemHistory';
import type { WorkActivity } from './api';
import { workActivityLabel } from './presentation';
import './work-activity.css';

const documentLabels: Record<string, string> = {
  create: 'Original goal',
  result: 'Result report',
  complete: 'Completion note',
  completion_pending: 'Completion note',
};

function ActivityEntry({
  entry,
  itemId,
  onOpenTask,
}: {
  entry: WorkActivity;
  itemId: string;
  onOpenTask: (id: string) => void;
}) {
  const title = workActivityLabel(entry.kind);
  const date = new Date(entry.createdAt);
  const long =
    entry.content.length > 260 || entry.content.split('\n', 5).length > 4;
  const stateKey = `work-activity:${itemId}:${entry.id}`;
  const disclosure = entry.kind === 'create' || long;
  return (
    <article className="work-activity-event">
      <header className="work-activity-event-heading">
        {entry.taskId ? (
          <Button
            variant="text"
            aria-label={`${title} · View execution`}
            onClick={() => onOpenTask(entry.taskId)}
          >
            {title}
          </Button>
        ) : (
          <strong>{title}</strong>
        )}
        <time dateTime={entry.createdAt} title={date.toLocaleString()}>
          {date.toLocaleString(undefined, {
            dateStyle: 'medium',
            timeStyle: 'medium',
          })}
        </time>
      </header>
      {entry.content.trim() &&
        (disclosure ? (
          <ReadingDisclosure
            label={documentLabels[entry.kind] ?? 'Full note'}
            defaultOpen={readStored(stateKey) === 'true'}
            onOpenChange={(open) => writeStored(stateKey, open ? 'true' : '')}
          >
            <div className={entry.kind === 'create' ? 'goal-prose' : undefined}>
              <MarkdownContent content={entry.content} />
            </div>
          </ReadingDisclosure>
        ) : (
          <MarkdownContent content={entry.content} />
        ))}
    </article>
  );
}

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
      <h3 className="work-section-title work-activity-title">Activity</h3>
      <WorkItemHistory
        itemId={itemId}
        kind="timeline"
        title="Activity"
        active={active}
        refreshKey={refreshKey}
        render={(entry) => (
          <ActivityEntry
            entry={entry}
            itemId={itemId}
            onOpenTask={onOpenTask}
          />
        )}
      />
    </div>
  );
}
