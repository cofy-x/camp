import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { isActiveRun, workspaceApi } from './api';
import type { RunEvent } from './api';
import { mergeEvents } from './events';
import { buildRunDiagnostics } from './runDiagnosticRecords';

export function RunDiagnostics({ id }: { id: string }) {
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), [id]);

  async function download() {
    if (pending) return;
    const controller = new AbortController();
    request.current = controller;
    setPending(true);
    let url: string | undefined;
    const anchor = document.createElement('a');
    try {
      const { run } = await workspaceApi.run(id, controller.signal);
      let events: RunEvent[] = [];
      for (;;) {
        const after = events.at(-1)?.seq ?? 0;
        const page = await workspaceApi.events(id, after, controller.signal);
        controller.signal.throwIfAborted();
        events = mergeEvents(events, page.events ?? []);
        const next = events.at(-1)?.seq ?? after;
        if (page.events.length < 500) break;
        if (next <= after)
          throw new Error('Could not finish reading activity. Try again.');
      }
      controller.signal.throwIfAborted();
      const data = buildRunDiagnostics(run, events, !isActiveRun(run));
      url = URL.createObjectURL(
        new Blob([`${JSON.stringify(data, null, 2)}\n`], {
          type: 'application/json',
        }),
      );
      anchor.href = url;
      anchor.download = `camp-work-${run.id}-diagnostics.json`;
      document.body.append(anchor);
      anchor.click();
      setError('');
    } catch (error) {
      if (!controller.signal.aborted)
        setError(
          error instanceof Error
            ? error.message
            : 'Could not prepare diagnostics. Try again.',
        );
    } finally {
      if (!controller.signal.aborted) setPending(false);
      anchor.remove();
      // Let the browser start the download before releasing the Blob URL.
      if (url) {
        const downloadURL = url;
        setTimeout(() => URL.revokeObjectURL(downloadURL), 1000);
      }
    }
  }

  return (
    <div className="workspace-diagnostics">
      <Button onClick={() => void download()} disabled={pending}>
        {pending ? 'Preparing diagnostics…' : 'Download diagnostics'}
      </Button>
      <p>Task identifiers, limits and recorded activity (JSON).</p>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
