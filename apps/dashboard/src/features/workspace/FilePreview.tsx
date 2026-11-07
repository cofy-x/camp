import { useEffect, useLayoutEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { Reference } from './api';
import { workspaceApi } from './api';
import { AssistantContent } from './AssistantContent';
import { readFilePreview, type FilePreviewRead } from './readFilePreview';

// Always load the selected immutable version, never the current resource pointer.
export function FilePreview({
  reference,
  onReference,
  onReady,
  onEditableChange,
}: {
  reference: Reference;
  onReference?: (reference: Reference) => void;
  onReady?: () => void;
  onEditableChange?: (editable: boolean) => void;
}) {
  const [snapshot, setSnapshot] = useState<{
    url: string;
    attempt: number;
    result?: FilePreviewRead;
    error: string;
  }>();
  const [source, setSource] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const url = workspaceApi.fileURL(
    reference.resourceId!,
    reference.path!,
    reference.commit!,
  );
  const markdown = /\.(md|markdown)$/i.test(reference.path ?? '');
  const filename = reference.path?.split('/').at(-1);
  const current =
    snapshot?.url === url && snapshot.attempt === attempt
      ? snapshot
      : undefined;
  const content = current?.result?.binary
    ? null
    : (current?.result?.content ?? null);
  const error = current?.error ?? '';
  const binary = current?.result?.binary ?? false;
  const editable = current?.result?.editable ?? false;
  useEffect(() => {
    const controller = new AbortController();
    void fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error('This file could not be loaded.');
        const result = await readFilePreview(response, controller.signal);
        if (controller.signal.aborted) return;
        setSnapshot({ url, attempt, result, error: '' });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setSnapshot({
            url,
            attempt,
            error:
              error instanceof Error ? error.message : 'Could not load file.',
          });
      });
    return () => controller.abort();
  }, [url, attempt]);
  useLayoutEffect(() => {
    if (content !== null || binary || error) onReady?.();
  }, [content, binary, error, onReady]);
  useLayoutEffect(() => {
    onEditableChange?.(editable);
  }, [editable, onEditableChange]);
  return (
    <section
      className="workspace-file-preview"
      aria-label={`Preview ${reference.path}`}
    >
      <div className="workspace-preview-actions">
        {markdown && content !== null && (
          <Button type="button" onClick={() => setSource(!source)}>
            {source ? 'Show preview' : 'Show source'}
          </Button>
        )}
        {onReference && (
          <Button type="button" onClick={() => onReference(reference)}>
            Use as reference
          </Button>
        )}
        <a
          className="p9-button"
          href={url}
          download={filename}
          aria-label={filename ? `Download ${filename}` : 'Download'}
        >
          Download
        </a>
      </div>
      {error ? (
        <p role="alert" className="app-error">
          {error}{' '}
          <Button
            type="button"
            onClick={() => setAttempt((value) => value + 1)}
          >
            Retry preview
          </Button>
        </p>
      ) : binary ? (
        <p>This file has no text preview. Download the original file.</p>
      ) : content === null ? (
        <p role="status">Loading file…</p>
      ) : (
        <>
          <div className="workspace-preview-content" tabIndex={0}>
            {markdown && !source ? (
              <AssistantContent content={content || '(Empty file)'} />
            ) : (
              <pre>{content || '(Empty file)'}</pre>
            )}
          </div>
          {current?.result?.shortened && (
            <p>Preview shortened. Download the complete file.</p>
          )}
        </>
      )}
    </section>
  );
}
