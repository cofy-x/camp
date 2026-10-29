import { useEffect, useId, useRef, useState } from 'react';
import { Button, Palette, PixelIcon } from '@cofy-x/camp-platinum';
import { useTeam } from './TeamContext';
import { useAccount } from './AuthContext';
import type { InboxItem } from './api';

export function DesktopInbox() {
  const team = useTeam();
  const { user } = useAccount();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState('');
  const panelId = useId();
  const root = useRef<HTMLElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);

  function dismiss() {
    setOpen(false);
    toggle.current?.focus({ preventScroll: true });
  }

  useEffect(() => {
    if (!open) return;
    closeButton.current?.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target))
        setOpen(false);
    };
    document.addEventListener('pointerdown', outside, true);
    return () => document.removeEventListener('pointerdown', outside, true);
  }, [open]);
  const unread = team.inbox.filter((item) => !item.readAt).length;

  async function openItem(item: InboxItem) {
    setError('');
    try {
      const opened = await team.navigate({
        roomId: item.roomId,
        messageId: item.messageId,
        runId: item.runId,
        reviewId: item.reviewId,
        taskId: item.taskId,
      });
      if (!opened) return;
      setOpen(false);
      await team.readInbox(item.id);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not open this inbox item.',
      );
    }
  }

  return (
    <aside
      ref={root}
      className="camp-inbox"
      aria-label="Personal inbox"
      onKeyDown={(event) => {
        if (open && event.key === 'Escape') {
          event.preventDefault();
          event.stopPropagation();
          dismiss();
        }
      }}
    >
      {!open && (team.navigationError || error) && (
        <p className="camp-inbox-feedback app-error" role="alert">
          {team.navigationError || error}
        </p>
      )}
      {open && (
        <Palette
          id={panelId}
          className="camp-inbox-panel"
          title="Your inbox"
          closeLabel="Close inbox"
          closeButtonRef={closeButton}
          onClose={dismiss}
        >
          <p className="camp-inbox-summary">
            {unread ? `${unread} unread` : 'All read'} · {team.inbox.length}{' '}
            {team.inbox.length === 1 ? 'item' : 'items'}
          </p>
          {team.navigationError && (
            <p className="app-error" role="alert">
              {team.navigationError}
            </p>
          )}
          {!team.inbox.length && (
            <p>
              You’re all caught up. Reviews, agent results, and mentions will
              appear here.
            </p>
          )}
          <ul>
            {team.inbox.map((item) => (
              <li key={item.id}>
                <button
                  className={item.readAt ? '' : 'is-unread'}
                  onClick={() => void openItem(item)}
                >
                  <PixelIcon
                    name={
                      item.reviewId
                        ? 'checklist'
                        : item.kind === 'mention'
                          ? 'conversation'
                          : 'agent'
                    }
                    size={24}
                  />
                  <span className="camp-inbox-copy">
                    <strong>
                      {item.kind === 'task_question'
                        ? 'A collaboration needs your answer'
                        : item.kind === 'task_attention'
                          ? 'A collaboration needs attention'
                          : item.kind === 'task_finished'
                            ? 'Collaboration ready for review'
                            : item.kind === 'mention'
                              ? 'You were mentioned'
                              : item.kind === 'review_requested'
                                ? 'A document needs your review'
                                : item.kind === 'review_updated'
                                  ? 'A document review was updated'
                                  : 'An agent finished your request'}
                    </strong>
                    <small>
                      {item.reviewTitle ? `${item.reviewTitle} · ` : ''}
                      {item.roomName}
                    </small>
                    <small>{new Date(item.createdAt).toLocaleString()}</small>
                  </span>
                  {!item.readAt && (
                    <span className="camp-inbox-unread" aria-label="Unread" />
                  )}
                </button>
              </li>
            ))}
          </ul>
          {error && (
            <p role="alert" className="app-error">
              {error}
            </p>
          )}
        </Palette>
      )}
      <Button
        ref={toggle}
        className="camp-inbox-toggle"
        onClick={() => {
          setError('');
          setOpen(!open);
        }}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Inbox${unread ? `, ${unread} unread` : ''}`}
      >
        <PixelIcon name="document" size={16} />
        <span className="camp-inbox-account" title={user.name}>
          {user.name}
        </span>
        <span>· Inbox{unread ? ` (${unread})` : ''}</span>
        <span className="camp-inbox-arrow" aria-hidden="true">
          {open ? '▾' : '▴'}
        </span>
      </Button>
    </aside>
  );
}
