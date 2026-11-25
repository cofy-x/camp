import { useEffect, useId, useRef, useState } from 'react';
import { Button, Palette, PixelIcon, Tabs } from '@cofy-x/camp-platinum';
import { useTeam } from './TeamContext';
import { useAccount } from './AuthContext';
import type { InboxItem } from './api';
import { InboxActivity } from './InboxActivity';
import { AttentionList } from '../features/attention/AttentionList';
import type { AttentionItem } from '../features/attention/api';

export function DesktopInbox() {
  const team = useTeam();
  const { user } = useAccount();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState('');
  const [view, setView] = useState('needs_me');
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
  const unread = team.unreadActivity;
  const attentionCount = team.attentionError
    ? '?'
    : (team.attention?.total ?? '…');
  const attentionLabel = team.attentionError
    ? 'attention count unavailable'
    : team.attention
      ? `${team.attention.total} need attention`
      : 'loading attention count';

  async function openAttention(item?: AttentionItem) {
    setError('');
    try {
      if (
        await team.navigate(item?.target ?? { app: 'work', view: 'needs_me' })
      )
        setOpen(false);
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Could not open this item.',
      );
    }
  }

  async function openItem(item: InboxItem) {
    setError('');
    try {
      const opened = await team.navigate(
        item.workItemId
          ? {
              app: 'work',
              roomId: item.roomId,
              workItemId: item.workItemId,
            }
          : {
              roomId: item.roomId,
              messageId: item.messageId,
              runId: item.runId,
              reviewId: item.reviewId,
              taskId: item.taskId,
            },
      );
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
          {team.navigationError && (
            <p className="app-error" role="alert">
              {team.navigationError}
            </p>
          )}
          <Tabs
            label="Inbox views"
            value={view}
            onChange={setView}
            tabs={[
              { id: 'needs_me', label: 'Needs me' },
              {
                id: 'activity',
                label: unread ? `Activity (${unread})` : 'Activity',
              },
            ]}
          >
            {view === 'needs_me' ? (
              <div className="camp-inbox-scroll">
                {team.attentionError && (
                  <p className="app-error" role="alert">
                    {team.attentionError}{' '}
                    <Button onClick={team.retryAttention}>
                      Retry attention
                    </Button>
                  </p>
                )}
                {!team.attention && !team.attentionError && (
                  <p role="status">Loading…</p>
                )}
                {team.attention && (
                  <>
                    {!team.attention.items.length && !team.attentionError && (
                      <p className="camp-inbox-empty">
                        Nothing needs your attention.
                      </p>
                    )}
                    <AttentionList
                      items={team.attention.items}
                      onOpen={(item) => void openAttention(item)}
                    />
                  </>
                )}
              </div>
            ) : (
              <InboxActivity onOpen={(item) => void openItem(item)} />
            )}
            {view === 'needs_me' && team.attention?.hasMore && (
              <div className="camp-inbox-actions">
                <Button onClick={() => void openAttention()}>
                  View all in Work
                </Button>
              </div>
            )}
          </Tabs>
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
          if (!open) setView('needs_me');
          setOpen(!open);
        }}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Needs me, ${attentionLabel}${unread ? `, ${unread} unread activity` : ''}`}
      >
        <PixelIcon name="document" size={16} />
        <span className="camp-inbox-account" title={user.name}>
          {user.name}
        </span>
        <span className="camp-inbox-caption">Needs me ({attentionCount})</span>
        {!!unread && (
          <span className="camp-inbox-unread" aria-label="Unread activity" />
        )}
        <span className="camp-inbox-arrow" aria-hidden="true">
          {open ? '▾' : '▴'}
        </span>
      </Button>
    </aside>
  );
}
