import { useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { Run, RunEvent } from './api';
import { buildRunDiagnostics } from './runDiagnosticRecords';

export function RunDiagnostics({
  run,
  events,
  complete,
}: {
  run: Run;
  events: RunEvent[];
  complete: boolean;
}) {
  const [error, setError] = useState('');

  function download() {
    let url: string | undefined;
    const anchor = document.createElement('a');
    try {
      const data = buildRunDiagnostics(run, events, complete);
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
    } catch {
      setError('Could not prepare diagnostics. Try again.');
    } finally {
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
      <Button onClick={download}>Download diagnostics</Button>
      <p>Task identifiers, limits and recorded activity (JSON).</p>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
