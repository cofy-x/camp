import { useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { AssistantContent } from './AssistantContent';
import { readStored, writeStored } from './conversationState';

// Keep the exact authored report available without parsing and mounting a long
// transcript before the person asks to read it. Saved files and state stay outside.
export function Report({
  content,
  stateKey,
  kind = 'report',
}: {
  content: string;
  stateKey: string;
  kind?: 'reply' | 'report';
}) {
  const [expanded, setExpanded] = useState(
    () => readStored(stateKey) === 'true',
  );
  const [opened, setOpened] = useState(expanded);
  const summary = useRef<HTMLElement>(null);
  const long = content.length > 800 || content.split('\n', 13).length > 12;
  const reply = <AssistantContent content={content} />;
  if (!long) return <div className="workspace-agent-reply-inline">{reply}</div>;
  const lead =
    kind === 'report'
      ? content
          .trim()
          .split(/\r?\n\s*\r?\n/, 2)
          .find(
            (paragraph) =>
              paragraph.length <= 320 && !/^\s*#{1,6}\s/.test(paragraph),
          )
      : undefined;

  function remember(open: boolean) {
    if (open) setOpened(true);
    setExpanded(open);
    writeStored(stateKey, open ? 'true' : '');
  }

  return (
    <>
      {!expanded && lead && (
        <div className="workspace-agent-reply-inline">
          <AssistantContent content={lead} />
        </div>
      )}
      <details
        className="workspace-agent-reply"
        open={expanded}
        onToggle={(event) => remember(event.currentTarget.open)}
      >
        <summary ref={summary}>
          {expanded ? `Full ${kind}` : `Read full ${kind}`}
        </summary>
        {(opened || expanded) && (
          <div className="workspace-agent-reply-body">
            {reply}
            <Button
              className="workspace-reply-collapse"
              onClick={() => {
                remember(false);
                summary.current?.focus({ preventScroll: true });
                requestAnimationFrame(() =>
                  summary.current?.scrollIntoView({ block: 'nearest' }),
                );
              }}
            >
              Collapse {kind}
            </Button>
          </div>
        )}
      </details>
    </>
  );
}
