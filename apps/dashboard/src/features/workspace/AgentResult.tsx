import { useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { Message, Reference, RunTimeline } from './api';
import { isActiveRun } from './api';
import { AssistantContent } from './AssistantContent';
import { readStored, writeStored } from './conversationState';
import { ResultFiles } from './ResultFiles';
import { runChecksLabel, runFilesLabel } from './runPresentation';

function AgentReply({ message }: { message: Message }) {
  const key = `reply-expanded:${message.id}`;
  const [expanded, setExpanded] = useState(() => readStored(key) === 'true');
  const [opened, setOpened] = useState(expanded);
  const summary = useRef<HTMLElement>(null);
  const long =
    message.content.length > 800 || message.content.split('\n').length > 12;
  const reply = <AssistantContent content={message.content} />;
  if (!long) return <div className="workspace-agent-reply-inline">{reply}</div>;

  function remember(open: boolean) {
    if (open) setOpened(true);
    setExpanded(open);
    writeStored(key, open ? 'true' : '');
  }

  return (
    <details
      className="workspace-agent-reply"
      open={expanded}
      onToggle={(event) => remember(event.currentTarget.open)}
    >
      <summary ref={summary}>
        {expanded ? 'Full reply' : 'Read full reply'}
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
            Collapse reply
          </Button>
        </div>
      )}
    </details>
  );
}

export function AgentResult({
  message,
  run,
  onInspect,
  onReference,
}: {
  message: Message;
  run?: RunTimeline;
  onInspect: (id: string) => void;
  onReference: (reference: Reference) => void;
}) {
  const result = run && !isActiveRun(run) ? run : undefined;
  return (
    <section className="workspace-agent-result" aria-label="Work result">
      {result && (
        <div className="workspace-result-facts">
          <strong>{runFilesLabel(result)}</strong>
          <span>Checks: {runChecksLabel(result)}</span>
          <Button onClick={() => onInspect(result.id)}>
            Details
            {(result.fileCount ?? result.files?.length)
              ? ` · ${result.fileCount ?? result.files.length} ${(result.fileCount ?? result.files.length) === 1 ? 'file' : 'files'}`
              : ''}
          </Button>
        </div>
      )}
      {!run && message.runId && (
        <Button onClick={() => onInspect(message.runId)}>Details</Button>
      )}
      {run?.error && <p className="app-error">{run.error}</p>}
      {result && <ResultFiles run={result} onReference={onReference} />}
      <AgentReply message={message} />
    </section>
  );
}
