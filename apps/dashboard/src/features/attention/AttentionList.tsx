import { goalTitle } from '../../shared/content/goal-title';
import type { AttentionItem } from './api';
import './attention.css';

const kindLabels = {
  judgment: 'Decision',
  review: 'Review',
  exception: 'Issue',
};

export function AttentionList({
  items,
  onOpen,
  showRoom = true,
}: {
  items: AttentionItem[];
  onOpen: (item: AttentionItem) => void;
  showRoom?: boolean;
}) {
  return (
    <ul className="attention-list" aria-label="Needs me">
      {items.map((item) => {
        const title =
          item.source === 'review' ? item.title : goalTitle(item.title);
        const summary = item.workRecovery?.message || item.summary;
        return (
          <li key={item.id}>
            <button type="button" onClick={() => onOpen(item)}>
              <span className="attention-title">{title}</span>
              {summary && summary !== item.title && summary !== title && (
                <span className="attention-summary">{summary}</span>
              )}
              {item.workRecovery ? (
                showRoom && <small>{item.roomName}</small>
              ) : (
                <small>
                  {kindLabels[item.kind]}
                  {showRoom && ` · ${item.roomName}`}
                </small>
              )}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
