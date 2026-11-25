import { useState } from 'react';
import { Button, Tabs } from '@cofy-x/camp-platinum';
import { ReadingDisclosure } from '../../shared/content/ReadingDisclosure';
import { taskStatus } from '../collaboration/api';
import { WorkItemHistory } from './WorkItemHistory';
import { WorkItemResult } from './WorkItemResult';
import {
  EvidenceRecord,
  RecordNote,
  WorkRecord,
  recordLabel,
} from './WorkRecord';
import type { WorkItem } from './api';
import { workActivityLabel } from './presentation';

const categories = {
  decisions: 'Decisions',
  evidence: 'Context',
  tasks: 'Sessions',
  timers: 'Reminders',
  actions: 'Actions',
};
const tabs = Object.entries(categories).map(([id, label]) => ({ id, label }));
const actorLabels = { user: 'Team member' };
const reminderLabels = { pending: 'Scheduled', fired: 'Triggered' };
const actionLabels = {
  inbox: 'Inbox notification',
  'github.comment': 'GitHub comment',
};

export function WorkItemRecords({
  item,
  active,
  earlierResult,
  hasQuestion,
  refreshKey,
  onOpenTask,
}: {
  item: WorkItem;
  active: boolean;
  earlierResult: boolean;
  hasQuestion: boolean;
  refreshKey: number;
  onOpenTask: (id: string) => void;
}) {
  const [history, setHistory] = useState('decisions');
  const [filesOpen, setFilesOpen] = useState(false);
  const hasEarlierFiles = (hasQuestion || earlierResult) && item.result;
  const contextActive = active && history === 'evidence';
  const remindersActive = active && history === 'timers';

  function executionAction(taskId?: string) {
    return (
      taskId && (
        <Button variant="text" onClick={() => onOpenTask(taskId)}>
          Open execution
        </Button>
      )
    );
  }

  return (
    <section
      className="work-records"
      aria-label="Work records"
      hidden={!active}
    >
      <h3 className="work-section-title">Records</h3>
      <Tabs
        label="Record categories"
        tabs={tabs}
        value={history}
        onChange={setHistory}
        variant="flat"
      >
        <WorkItemHistory
          itemId={item.id}
          kind="decisions"
          active={active && history === 'decisions'}
          title={categories.decisions}
          refreshKey={refreshKey}
          render={(decision) => {
            const taskId = decision.taskId;
            const title =
              decision.kind === 'result'
                ? 'Execution ended'
                : workActivityLabel(decision.kind, recordLabel(decision.kind));
            return (
              <WorkRecord
                title={
                  taskId ? (
                    <Button
                      variant="text"
                      aria-label={`${title} · Open execution`}
                      onClick={() => onOpenTask(taskId)}
                    >
                      {title}
                    </Button>
                  ) : (
                    title
                  )
                }
                date={decision.createdAt}
                meta={recordLabel(decision.actorKind, actorLabels)}
              >
                <RecordNote
                  content={decision.reason}
                  label={
                    decision.kind === 'create' ? 'Original goal' : undefined
                  }
                  markdown={decision.kind === 'create'}
                />
              </WorkRecord>
            );
          }}
        />
        <WorkItemHistory
          itemId={item.id}
          kind="evidence"
          active={contextActive}
          title={categories.evidence}
          refreshKey={refreshKey}
          render={(evidence) => (
            <EvidenceRecord
              id={evidence.eventId}
              kind={evidence.kind}
              linkedAt={evidence.createdAt}
              active={contextActive}
            />
          )}
        />
        <WorkItemHistory
          itemId={item.id}
          kind="tasks"
          active={active && history === 'tasks'}
          title={categories.tasks}
          refreshKey={refreshKey}
          render={(task) => (
            <WorkRecord
              title={recordLabel(task.status, taskStatus)}
              date={task.createdAt}
            >
              <RecordNote
                content={task.summary}
                label="Session summary"
                markdown
              />
              {executionAction(task.id)}
            </WorkRecord>
          )}
        />
        <WorkItemHistory
          itemId={item.id}
          kind="timers"
          active={remindersActive}
          title={categories.timers}
          refreshKey={refreshKey}
          render={(timer) => (
            <WorkRecord
              title={recordLabel(timer.status, reminderLabels)}
              date={timer.dueAt}
              datePrefix="Due"
            >
              <RecordNote content={timer.condition} />
              {timer.eventId && (
                <EvidenceRecord
                  id={timer.eventId}
                  kind="timer.fired"
                  active={remindersActive}
                />
              )}
            </WorkRecord>
          )}
        />
        <WorkItemHistory
          itemId={item.id}
          kind="actions"
          active={active && history === 'actions'}
          title={categories.actions}
          refreshKey={refreshKey}
          render={(action) => (
            <WorkRecord
              title={recordLabel(action.kind, actionLabels)}
              meta={recordLabel(action.status)}
              date={action.createdAt}
            >
              <RecordNote content={action.body} label="Full message" />
              {executionAction(action.taskId)}
            </WorkRecord>
          )}
        />
      </Tabs>
      {(hasEarlierFiles || item.completionCriteria) && (
        <div className="work-record-supplement">
          {hasEarlierFiles && (
            <ReadingDisclosure
              label="Files and earlier progress"
              onOpenChange={setFilesOpen}
            >
              <WorkItemResult item={item} active={active && filesOpen} />
            </ReadingDisclosure>
          )}
          {item.completionCriteria && (
            <ReadingDisclosure label="Complete when">
              <p className="work-record-text">{item.completionCriteria}</p>
            </ReadingDisclosure>
          )}
        </div>
      )}
    </section>
  );
}
