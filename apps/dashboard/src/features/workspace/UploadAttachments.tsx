import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import type { DraftUpload } from './uploadState';
import { attachmentSize, uploadDrafts } from './uploadState';

export function useDraftUploads(key: string) {
  const entries = useSyncExternalStore(
    useCallback((listener) => uploadDrafts.subscribe(key, listener), [key]),
    () => uploadDrafts.snapshot(key),
  );
  useEffect(() => uploadDrafts.recover(key), [key]);
  return entries;
}

function UploadRow({
  entry,
  draftKey,
  onError,
}: {
  entry: DraftUpload;
  draftKey: string;
  onError: (error: string) => void;
}) {
  const picker = useRef<HTMLInputElement>(null);
  return (
    <div className="workspace-upload-row">
      <PixelIcon name="document" size={16} />
      <div className="workspace-upload-description">
        <strong>{entry.name}</strong>
        <small>{attachmentSize(entry.size)}</small>
        <span role={entry.state === 'error' ? 'alert' : 'status'}>
          {entry.state === 'uploading'
            ? 'Uploading…'
            : entry.state === 'checking'
              ? 'Checking saved upload…'
              : entry.error}
        </span>
      </div>
      <div className="workspace-upload-actions">
        {entry.state === 'error' && (
          <>
            <Button
              type="button"
              aria-label={`Retry upload ${entry.name}`}
              onClick={() => uploadDrafts.retry(draftKey, entry.requestId)}
            >
              Retry
            </Button>
            {entry.needsFile && (
              <>
                <Button
                  type="button"
                  aria-label={`Choose file again for ${entry.name}`}
                  onClick={() => picker.current?.click()}
                >
                  Choose file again
                </Button>
                <input
                  ref={picker}
                  type="file"
                  className="workspace-upload-input"
                  aria-label={`Reselect ${entry.name}`}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (!file) return;
                    try {
                      uploadDrafts.retry(draftKey, entry.requestId, file);
                      onError('');
                    } catch (error) {
                      onError(
                        error instanceof Error
                          ? error.message
                          : 'Could not select this file.',
                      );
                    }
                  }}
                />
              </>
            )}
          </>
        )}
        <Button
          type="button"
          aria-label={`Remove upload ${entry.name}`}
          onClick={() => uploadDrafts.remove(draftKey, entry.requestId)}
        >
          ×
        </Button>
      </div>
    </div>
  );
}

export function UploadAttachments({
  entries,
  draftKey,
  onError,
}: {
  entries: DraftUpload[];
  draftKey: string;
  onError: (error: string) => void;
}) {
  if (!entries.length) return null;
  return (
    <section
      className="workspace-upload-list"
      aria-label="Uploading attachments"
    >
      {entries.map((entry) => (
        <UploadRow
          key={entry.requestId}
          entry={entry}
          draftKey={draftKey}
          onError={onError}
        />
      ))}
    </section>
  );
}
