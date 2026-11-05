import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import {
  PixelIcon,
  type DesktopAPI,
  type MenuItem,
} from '@cofy-x/camp-platinum';
import type { Message, Resource } from './api';
import { References } from './References';

export function SystemMessage({
  desktop,
  message,
  resources,
  highlighted,
  threadLabel,
  onReview,
  onTask,
  taskLabel = 'Open collaboration',
  onInspect,
  onAsk,
  onFollowUp,
  onRemember,
  onThread,
  onSource,
  children,
}: {
  desktop: DesktopAPI;
  message: Message;
  resources: Resource[];
  highlighted: boolean;
  threadLabel: string;
  onReview: (id: string) => void;
  onTask: (id: string) => void;
  taskLabel?: string;
  onInspect: (id: string) => void;
  onAsk: () => void;
  onFollowUp: () => void;
  onRemember: () => void;
  onThread?: () => void;
  onSource: (id: string) => void;
  children?: ReactNode;
}) {
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const sources = useRef<HTMLDivElement>(null);
  const references = message.references ?? [];
  const label = message.reviewId
    ? 'Document review event'
    : message.taskId
      ? 'Collaboration event'
      : 'System event';
  const icon = message.reviewId
    ? 'checklist'
    : message.taskId
      ? 'conversation'
      : 'info';

  useLayoutEffect(() => {
    if (sourcesOpen)
      sources.current?.querySelector('summary')?.focus({ preventScroll: true });
  }, [sourcesOpen]);

  function openActions(button: HTMLButtonElement) {
    const items: MenuItem[] = [];
    if (references.length)
      items.push({
        label: sourcesOpen ? 'Hide referenced files' : 'View referenced files',
        action: () => setSourcesOpen(!sourcesOpen),
      });
    items.push({ label: 'Ask agent…', action: onAsk });
    items.push({ label: 'Follow up…', action: onFollowUp });
    items.push({ label: 'Remember this…', action: onRemember });
    if (onThread) items.push({ label: threadLabel, action: onThread });
    const rect = button.getBoundingClientRect();
    desktop.contextMenu(items, { x: rect.left, y: rect.bottom });
  }

  return (
    <article
      className={`workspace-message author-system workspace-system-event ${highlighted ? 'is-source' : ''}`}
      data-message-id={message.id}
      aria-label={label}
      tabIndex={-1}
    >
      <span className="workspace-system-event-icon" aria-hidden="true">
        <PixelIcon name={icon} size={16} />
      </span>
      <div className="workspace-message-copy">
        <div className="workspace-system-event-row">
          <p className="workspace-message-text">{message.content}</p>
          <div className="workspace-system-event-actions">
            <time
              dateTime={message.createdAt}
              title={new Date(message.createdAt).toLocaleString('en-US')}
            >
              {new Date(message.createdAt).toLocaleTimeString('en-US', {
                hour: 'numeric',
                minute: '2-digit',
              })}
            </time>
            {message.reviewId ? (
              <button
                className="workspace-text-button"
                onClick={() => onReview(message.reviewId!)}
              >
                Open Review
              </button>
            ) : message.taskId ? (
              <button
                className="workspace-text-button"
                onClick={() => onTask(message.taskId!)}
              >
                {taskLabel}
              </button>
            ) : message.runId ? (
              <button
                className="workspace-text-button"
                onClick={() => onInspect(message.runId)}
              >
                Details
              </button>
            ) : null}
            <button
              className="workspace-text-button workspace-system-event-more"
              aria-label="More event actions"
              aria-haspopup="menu"
              onClick={(event) => openActions(event.currentTarget)}
              onKeyDown={(event) => {
                if (event.key !== 'ArrowDown') return;
                event.preventDefault();
                openActions(event.currentTarget);
              }}
            >
              ⋯
            </button>
          </div>
        </div>
        {sourcesOpen && references.length > 0 && (
          <div ref={sources}>
            <References
              references={references}
              resources={resources}
              onSource={onSource}
              showLabel={false}
            />
          </div>
        )}
        {children}
      </div>
    </article>
  );
}
