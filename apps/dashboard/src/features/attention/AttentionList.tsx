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
      {items.map((item) => (
        <li key={item.id}>
          <button type="button" onClick={() => onOpen(item)}>
            <span className="attention-title">{item.title}</span>
            {item.summary && item.summary !== item.title && (
              <span className="attention-summary">{item.summary}</span>
            )}
            <small>
              {kindLabels[item.kind]}
              {showRoom && ` · ${item.roomName}`}
            </small>
          </button>
        </li>
      ))}
    </ul>
  );
}
