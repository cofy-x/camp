import { Button } from '@cofy-x/camp-platinum';
import type { Message, Reference, RunTimeline } from './api';
import { isActiveRun } from './api';
import { Report } from './Report';
import { ResultFiles } from './ResultFiles';
import { runChecksLabel, runFilesLabel } from './runPresentation';

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
      <Report
        content={message.content}
        kind="reply"
        stateKey={`reply-expanded:${message.id}`}
      />
    </section>
  );
}
