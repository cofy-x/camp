import { useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { Message, Reference, Run } from './api';
import { CitedReply } from './CitedReply';
import { readStored, writeStored } from './conversationState';
import { ResultFiles } from './ResultFiles';
import { runChecksLabel, runFilesLabel } from './runPresentation';

function AgentReply({
  message,
  references,
  onReference,
}: {
  message: Message;
  references: Reference[];
  onReference: (reference: Reference) => void;
}) {
  const key = `reply-expanded:${message.id}`;
  const [expanded, setExpanded] = useState(() => readStored(key) === 'true');
  const summary = useRef<HTMLElement>(null);
  const long =
    message.content.length > 800 || message.content.split('\n').length > 12;
  const reply = (
    <CitedReply
      content={message.content}
      references={references}
      onReference={onReference}
    />
  );
  if (!long) return <div className="workspace-agent-reply-inline">{reply}</div>;

  function remember(open: boolean) {
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
  run: Run;
  onInspect: (id: string) => void;
  onReference: (reference: Reference) => void;
}) {
  return (
    <section className="workspace-agent-result" aria-label="Work result">
      <div className="workspace-result-facts">
        <strong>{runFilesLabel(run)}</strong>
        <span>Checks: {runChecksLabel(run)}</span>
        <Button onClick={() => onInspect(run.id)}>
          Details
          {run.files?.length
            ? ` · ${run.files.length} ${run.files.length === 1 ? 'file' : 'files'}`
            : ''}
        </Button>
      </div>
      {run.error && <p className="app-error">{run.error}</p>}
      <ResultFiles run={run} onReference={onReference} />
      <AgentReply
        message={message}
        references={run.references ?? []}
        onReference={onReference}
      />
    </section>
  );
}
