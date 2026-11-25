import { useState, type ReactNode } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { MarkdownContent } from '../../shared/content/MarkdownContent';
import { ReadingDisclosure } from '../../shared/content/ReadingDisclosure';
import { useRead } from '../../shared/reads/useRead';
import { sourceEventPath, type SourceEvent } from '../automation/api';
import { sourceEventLabels } from '../automation/event-labels';
import './work-records.css';

const recordSources: Record<string, string> = {
  ...sourceEventLabels,
  'room.message': 'Room message',
  'task.result': 'Execution result',
  'workitem.answer': 'Work answer',
  'workitem.confirmation': 'Completion confirmation',
  'workitem.completion_settings': 'Completion settings changed',
  'workitem.deadline': 'Authorization expired',
  'workitem.evidence': 'Context added',
  'timer.fired': 'Reminder event',
};

export function recordLabel(value: string, labels?: Record<string, string>) {
  if (labels && Object.hasOwn(labels, value)) return labels[value];
  return value
    .replace(/[._-]+/g, ' ')
    .replace(/^./u, (first) => first.toUpperCase());
}

function RecordTime({ value, prefix }: { value: string; prefix?: string }) {
  if (!value) return null;
  const date = new Date(value);
  return (
    <time dateTime={value} title={date.toLocaleString()}>
      {prefix && `${prefix} `}
      {date.toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'medium',
      })}
    </time>
  );
}

export function WorkRecord({
  title,
  date,
  datePrefix,
  meta,
  children,
}: {
  title: ReactNode;
  date: string;
  datePrefix?: string;
  meta?: string;
  children?: ReactNode;
}) {
  return (
    <article className="work-record">
      <header className="work-record-heading">
        <h4>{title}</h4>
        <span className="work-record-meta">
          {meta && date ? <span>{meta} ·</span> : meta}
          <RecordTime value={date} prefix={datePrefix} />
        </span>
      </header>
      {children}
    </article>
  );
}

export function RecordNote({
  content,
  label = 'Full note',
  markdown = false,
}: {
  content: string;
  label?: string;
  markdown?: boolean;
}) {
  if (!content.trim()) return null;
  const body = markdown ? (
    <div className="goal-prose">
      <MarkdownContent content={content} />
    </div>
  ) : (
    <p className="work-record-text">{content}</p>
  );
  return content.length > 240 || content.split('\n', 5).length > 4 ? (
    <ReadingDisclosure label={label}>{body}</ReadingDisclosure>
  ) : (
    body
  );
}

export function EvidenceRecord({
  id,
  kind = '',
  linkedAt,
  active = true,
}: {
  id: string;
  kind?: string;
  linkedAt?: string;
  active?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const original = useRead<{ event: SourceEvent }>(sourceEventPath(id), {
    enabled: active && open,
  });
  const event = original.data?.event;
  const actor = event?.actorLogin || event?.actorId;
  return (
    <details
      className="p9-disclosure work-record-source"
      onToggle={(input) => setOpen(input.currentTarget.open)}
    >
      <summary>
        <span className="work-record-heading">
          <span>{recordLabel(kind, recordSources) || 'Source event'}</span>
          {linkedAt && (
            <span className="work-record-meta">
              <RecordTime value={linkedAt} prefix="Added" />
            </span>
          )}
        </span>
      </summary>
      <div className="app-reading-content">
        {original.error && (
          <p className="app-error" role="alert">
            {original.error}{' '}
            <Button onClick={() => void original.refresh()}>
              Retry Original
            </Button>
          </p>
        )}
        {open && !event && !original.error && (
          <p role="status">Loading original…</p>
        )}
        {event && (
          <>
            <p className="work-record-meta">
              {actor && (
                <span>
                  {actor}
                  {event.occurredAt && ' ·'}
                </span>
              )}
              <RecordTime value={event.occurredAt} />
            </p>
            <p className="work-record-text">
              {event.content || 'No text recorded.'}
            </p>
          </>
        )}
      </div>
    </details>
  );
}
