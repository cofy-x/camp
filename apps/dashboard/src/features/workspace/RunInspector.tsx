import { useEffect, useState } from 'react';
import { Button, Tabs } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { isActiveRun, statusLabel, workspaceApi } from './api';
import type { Message, Reference, Run, RunEvent } from './api';
import { mergeEvents } from './events';
import { summarizeActivity } from './activity';
import { CitedReply } from './CitedReply';
import { References } from './References';
import { ResultFiles } from './ResultFiles';
import { RunDiagnostics } from './RunDiagnostics';
import { findRunReply } from './runMessages';
import {
  checkResultLabel,
  runChecksLabel,
  runFilesLabel,
} from './runPresentation';

function newerRun(current: Run | null, incoming: Run): Run {
  if (
    current &&
    (Date.parse(incoming.updatedAt) < Date.parse(current.updatedAt) ||
      (!isActiveRun(current) && isActiveRun(incoming)) ||
      (current.status === 'cancelling' &&
        isActiveRun(incoming) &&
        incoming.status !== 'cancelling'))
  )
    return current;
  return incoming;
}

function activityLabel(event: RunEvent) {
  const summary = summarizeActivity(event);
  if (summary) return summary;
  if (event.type === 'status') {
    const status = event.text.trim();
    return {
      label: Object.hasOwn(statusLabel, status)
        ? statusLabel[status as Run['status']]
        : 'Status updated',
      detail: '',
    };
  }
  if (event.type === 'error')
    return { label: 'Execution error', detail: event.text };
  return null;
}

export function RunInspector({
  id,
  onClose,
  backLabel = 'Back to Chat',
  onReference,
  onAsk,
  onSource,
}: {
  id: string;
  onClose: () => void;
  backLabel?: string;
  onReference: (reference: Reference) => void;
  onAsk: (run: Run) => void;
  onSource: (messageId: string) => void;
}) {
  const team = useTeam();
  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [eventsLoaded, setEventsLoaded] = useState(false);
  const [error, setError] = useState('');
  const [stopError, setStopError] = useState('');
  const [stopping, setStopping] = useState(false);
  const [tab, setTab] = useState('');
  const [reply, setReply] = useState<Message | null>(null);
  const [replyError, setReplyError] = useState('');
  const [replyLoading, setReplyLoading] = useState(false);
  const [replyAttempt, setReplyAttempt] = useState(0);
  const [replyExpanded, setReplyExpanded] = useState(false);
  const active = !run || isActiveRun(run);
  const roomId = run?.roomId;
  const threadRootId = run?.threadRootId;
  useEffect(() => {
    if (active || !roomId || threadRootId === undefined) return;
    const controller = new AbortController();
    setReplyLoading(true);
    setReplyError('');
    void findRunReply({ id, roomId, threadRootId }, controller.signal)
      .then((message) => {
        if (controller.signal.aborted) return;
        setReply(message);
        setReplyExpanded(
          Boolean(
            message &&
            message.content.length <= 800 &&
            message.content.split('\n').length <= 12,
          ),
        );
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setReplyError(
            error instanceof Error
              ? error.message
              : 'Could not load the Agent reply.',
          );
      })
      .finally(() => {
        if (!controller.signal.aborted) setReplyLoading(false);
      });
    return () => controller.abort();
  }, [id, active, roomId, threadRootId, replyAttempt]);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let saved: RunEvent[] = [];

    async function poll() {
      let active = true;
      try {
        const result = await workspaceApi.run(id, controller.signal);
        if (controller.signal.aborted) return;
        active = isActiveRun(result.run);
        setRun((current) => newerRun(current, result.run));
        setTab((current) => current || (active ? 'activity' : 'overview'));
        setEventsLoaded(false);
        for (;;) {
          const page = await workspaceApi.events(
            id,
            saved.at(-1)?.seq ?? 0,
            controller.signal,
          );
          if (controller.signal.aborted) return;
          saved = mergeEvents(saved, page.events ?? []);
          setEvents(saved);
          if (page.events.length < 500) break;
        }
        // Completeness belongs to this fetch's terminal snapshot. A concurrent
        // cancel response cannot make an earlier active scan complete.
        setEventsLoaded(!active);
        setError('');
      } catch (error) {
        active = true;
        if (!controller.signal.aborted) {
          setEventsLoaded(false);
          setError(
            error instanceof Error
              ? error.message
              : 'Could not load this result.',
          );
        }
      } finally {
        if (!controller.signal.aborted && active)
          timer = setTimeout(() => void poll(), 1200);
      }
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id]);

  async function stop() {
    setStopping(true);
    setStopError('');
    try {
      const result = await workspaceApi.cancel(id);
      setRun((current) => newerRun(current, result.run));
    } catch (error) {
      setStopError(
        error instanceof Error ? error.message : 'Could not stop this work.',
      );
    } finally {
      setStopping(false);
    }
  }

  const activity = events.flatMap((event) => {
    const summary = activityLabel(event);
    return summary ? [{ ...summary, event }] : [];
  });
  return (
    <section
      className="workspace-inspector workspace-panel"
      aria-label="Work details"
    >
      <div className="workspace-inspector-content">
        <div className="workspace-panel-heading">
          <h2>{run?.agentSnapshot.name ?? 'Agent'} · Work details</h2>
          <Button onClick={onClose}>{backLabel}</Button>
        </div>
        {error && (
          <p className="app-error" role="alert">
            {error} Existing output is preserved.
          </p>
        )}
        {run ? (
          <>
            <div className="workspace-run-summary">
              <div className="workspace-run-outcome">
                <strong>{statusLabel[run.status]}</strong>
                <span>
                  {isActiveRun(run) ? 'Results pending' : runFilesLabel(run)}
                </span>
                <span>Checks: {runChecksLabel(run)}</span>
              </div>
              <div className="app-actions">
                {run.taskId ? (
                  <Button
                    onClick={() =>
                      void team.navigate({
                        app: 'workspace',
                        roomId: run.roomId,
                        taskId: run.taskId,
                      })
                    }
                  >
                    Open collaboration
                  </Button>
                ) : isActiveRun(run) ? (
                  <Button
                    disabled={stopping || run.status === 'cancelling'}
                    onClick={() => void stop()}
                  >
                    {stopping || run.status === 'cancelling'
                      ? 'Stopping…'
                      : 'Stop'}
                  </Button>
                ) : (
                  <Button onClick={() => onAsk(run)}>Ask agent…</Button>
                )}
              </div>
            </div>
            <div className="workspace-run-context">
              <span>
                Runner:{' '}
                {run.runnerName ||
                  (run.runnerId
                    ? 'Assigned Runner'
                    : 'Server (local execution)')}
              </span>
              <span>
                {run.finishedAt ? 'Finished' : 'Requested'}{' '}
                <time dateTime={run.finishedAt || run.createdAt}>
                  {new Date(run.finishedAt || run.createdAt).toLocaleString()}
                </time>
              </span>
              {run.agentSnapshot.access === 'read' && (
                <span>Read-only access</span>
              )}
            </div>
            {run.waitingReason && isActiveRun(run) && (
              <p role="status">{run.waitingReason}</p>
            )}
            {run.status === 'cancelling' && (
              <p role="status">
                Stopping work and waiting for cleanup confirmation.
              </p>
            )}
            {run.sessionMode === 'new' && run.sessionGeneration > 1 && (
              <p className="workspace-run-notice">
                This task rebuilt its context from visible conversation
                messages.
              </p>
            )}
            {run.error && (
              <p role="alert" className="app-error">
                {run.error}
              </p>
            )}
            {stopError && (
              <p role="alert" className="app-error">
                {stopError}
              </p>
            )}
            <Tabs
              label="Work details"
              tabs={[
                { id: 'overview', label: 'Overview' },
                {
                  id: 'files',
                  label: `Files (${run.files?.length ?? 0})`,
                },
                { id: 'activity', label: 'Activity' },
                { id: 'checks', label: 'Checks' },
              ]}
              value={tab}
              onChange={setTab}
            >
              {tab === 'overview' && (
                <div className="workspace-run-overview">
                  <ResultFiles run={run} onReference={onReference} />
                  {!run.files?.length && (
                    <p>
                      {isActiveRun(run)
                        ? 'Files will appear here after work finishes.'
                        : 'No file changes were collected.'}
                    </p>
                  )}
                  {run.files?.length > 0 && !run.resultCommit && (
                    <p>
                      File changes were reported, but no captured version is
                      available.
                    </p>
                  )}
                  {run.files?.length > 0 &&
                    run.files.every((file) => file.status === 'deleted') && (
                      <p>
                        Files were removed. Open Files to inspect the removals.
                      </p>
                    )}
                  {replyLoading && <p role="status">Loading Agent reply…</p>}
                  {replyError && (
                    <p className="app-error" role="alert">
                      {replyError}{' '}
                      <Button
                        onClick={() => setReplyAttempt((value) => value + 1)}
                      >
                        Retry reply
                      </Button>
                    </p>
                  )}
                  {reply && (
                    <details
                      className="workspace-inspector-reply"
                      open={replyExpanded}
                      onToggle={(event) =>
                        setReplyExpanded(event.currentTarget.open)
                      }
                    >
                      <summary>Agent reply</summary>
                      <CitedReply
                        content={reply.content}
                        references={run.references ?? []}
                        onReference={onReference}
                      />
                    </details>
                  )}
                  <details className="workspace-run-request">
                    <summary>Original request</summary>
                    <p className="workspace-message-text">{run.prompt}</p>
                    <References
                      references={run.references ?? []}
                      resources={[]}
                      onSource={onSource}
                    />
                    <Button onClick={() => onSource(run.originMessageId)}>
                      View request in chat
                    </Button>
                  </details>
                </div>
              )}
              {tab === 'activity' && (
                <>
                  <ol
                    className="workspace-event-log"
                    aria-label="Execution activity"
                  >
                    {activity.map(({ event, label, detail }) => (
                      <li key={event.seq}>
                        <header>
                          <strong>{label}</strong>
                          <time dateTime={event.createdAt}>
                            {new Date(event.createdAt).toLocaleTimeString()}
                          </time>
                        </header>
                        {detail && <p>{detail}</p>}
                      </li>
                    ))}
                  </ol>
                  {!activity.length && (
                    <p>
                      {events.length
                        ? 'No summarized activity is available. Recorded logs are included in diagnostics.'
                        : 'No activity recorded yet.'}
                    </p>
                  )}
                </>
              )}
              {tab === 'files' && (
                <>
                  {run.resultCommit && Boolean(run.files?.length) && (
                    <div className="app-actions">
                      <a
                        href={workspaceApi.patchURL(run.id)}
                        download={`${run.id}.patch`}
                      >
                        Download Patch
                      </a>
                    </div>
                  )}
                  {!run.files?.length && (
                    <p>
                      {isActiveRun(run)
                        ? 'Changes are collected after the agent finishes.'
                        : 'No file changes were collected.'}
                    </p>
                  )}
                  {run.files?.map((file) => (
                    <details className="workspace-file-change" key={file.path}>
                      <summary>
                        {file.path} · {file.status}
                      </summary>
                      {run.resultCommit && file.status !== 'deleted' && (
                        <div className="workspace-result-file-actions">
                          {run.published && (
                            <Button
                              onClick={() =>
                                void team.navigate({
                                  app: 'library',
                                  resourceId: run.resourceId,
                                  path: file.path,
                                  commit: run.resultCommit,
                                  contextRoomId: run.roomId,
                                })
                              }
                            >
                              Open in Library
                            </Button>
                          )}
                          <Button
                            onClick={() =>
                              onReference({
                                kind: 'file',
                                resourceId: run.resourceId,
                                commit: run.resultCommit,
                                path: file.path,
                                title: file.path,
                                sourceRunId: run.id,
                                sourceStatus: run.status,
                                published: run.published,
                              })
                            }
                          >
                            Use as reference
                          </Button>
                          <a
                            href={workspaceApi.fileURL(
                              run.resourceId,
                              file.path,
                              run.resultCommit,
                            )}
                            download={file.path.split('/').at(-1)}
                          >
                            Download File
                          </a>
                        </div>
                      )}
                      <div className="workspace-file-comparison">
                        <div>
                          <strong>Before</strong>
                          <pre tabIndex={0}>
                            {file.status === 'added'
                              ? '(File did not exist)'
                              : file.before || '(Empty file)'}
                          </pre>
                        </div>
                        <div>
                          <strong>After</strong>
                          <pre tabIndex={0}>
                            {file.status === 'deleted'
                              ? '(File deleted)'
                              : file.after || '(Empty file)'}
                          </pre>
                        </div>
                      </div>
                    </details>
                  ))}
                </>
              )}
              {tab === 'checks' && (
                <>
                  <p>
                    Checks: <strong>{runChecksLabel(run)}</strong>
                  </p>
                  {run.checks?.map((check, index) => (
                    <details
                      className="workspace-run-check"
                      key={index}
                      open={['Failed', 'Interrupted'].includes(
                        checkResultLabel(check, run),
                      )}
                    >
                      <summary>
                        {check.name} · {checkResultLabel(check, run)}
                        {check.exitCode !== null && check.exitCode !== 0
                          ? ` (exit ${check.exitCode})`
                          : ''}
                      </summary>
                      <code>{check.command}</code>
                      <pre>{check.output || 'No output recorded.'}</pre>
                    </details>
                  ))}
                </>
              )}
            </Tabs>
            <RunDiagnostics
              run={run}
              events={events}
              complete={eventsLoaded && !isActiveRun(run)}
            />
          </>
        ) : (
          <p role="status">Loading work details…</p>
        )}
      </div>
    </section>
  );
}
