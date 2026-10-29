import { useEffect, useLayoutEffect, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { DocumentSnapshot, Reference } from './api';
import { workspaceApi } from './api';
import { AssistantContent } from './AssistantContent';
import { DocumentPreview } from './DocumentPreview';
import { documentFormat, documentPending, documentSnapshot } from './documents';
import { useDocumentPreview } from './useDocumentPreview';
import type { DocumentPreviewState } from './useDocumentPreview';

const previewLimit = 65536;

// Always load the selected immutable version, never the current resource pointer.
export function FilePreview({
  reference,
  onReference,
  onReady,
  initialSection,
  onDocumentChange,
  documentState,
  onEditableChange,
}: {
  reference: Reference;
  onReference?: (reference: Reference) => void;
  onReady?: () => void;
  initialSection?: string;
  onDocumentChange?: (document: DocumentSnapshot) => void;
  documentState?: DocumentPreviewState;
  onEditableChange?: (editable: boolean) => void;
}) {
  const format = documentFormat(reference.path);
  return format ? (
    <DocumentFilePreview
      key={`${reference.resourceId}:${reference.commit}:${reference.path}`}
      reference={reference}
      onReference={onReference}
      onReady={onReady}
      initialSection={initialSection}
      onDocumentChange={onDocumentChange}
      documentState={documentState}
    />
  ) : (
    <TextFilePreview
      reference={reference}
      onReference={onReference}
      onReady={onReady}
      onEditableChange={onEditableChange}
    />
  );
}

function DocumentFilePreview({
  reference,
  onReference,
  onReady,
  initialSection,
  onDocumentChange,
  documentState,
}: {
  reference: Reference;
  onReference?: (reference: Reference) => void;
  onReady?: () => void;
  initialSection?: string;
  onDocumentChange?: (document: DocumentSnapshot) => void;
  documentState?: DocumentPreviewState;
}) {
  const localState = useDocumentPreview(
    reference,
    onDocumentChange,
    !documentState,
  );
  const state = documentState ?? localState;
  const document = state.document
    ? documentSnapshot(state.document)
    : undefined;
  return (
    <section
      className="workspace-file-preview"
      aria-label={`Preview ${reference.path}`}
    >
      <div className="workspace-preview-actions">
        <a
          href={workspaceApi.fileURL(
            reference.resourceId!,
            reference.path!,
            reference.commit!,
          )}
          download={reference.path?.split('/').at(-1)}
        >
          Download {reference.path?.split('/').at(-1)}
        </a>
        {onReference && (
          <Button
            type="button"
            disabled={!document || documentPending(document.status)}
            onClick={() => onReference({ ...reference, document })}
          >
            Use as reference
          </Button>
        )}
      </div>
      <DocumentPreview
        reference={reference}
        initialSection={initialSection}
        canRetry={Boolean(
          onReference || onDocumentChange || !reference.document,
        )}
        state={state}
        onReady={onReady}
      />
    </section>
  );
}

function TextFilePreview({
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
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [source, setSource] = useState(false);
  const [binary, setBinary] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const url = workspaceApi.fileURL(
    reference.resourceId!,
    reference.path!,
    reference.commit!,
  );
  const markdown = /\.(md|markdown)$/i.test(reference.path ?? '');
  useEffect(() => {
    const controller = new AbortController();
    setContent(null);
    setError('');
    setBinary(false);
    void fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error('This file could not be loaded.');
        const bytes = new Uint8Array(await response.arrayBuffer());
        if (controller.signal.aborted) return;
        try {
          const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
          if (text.includes('\0')) setBinary(true);
          else setContent(text);
        } catch {
          setBinary(true);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load file.',
          );
      });
    return () => controller.abort();
  }, [url, attempt]);
  useLayoutEffect(() => {
    if (content !== null || binary || error) onReady?.();
  }, [content, binary, error, onReady]);
  useEffect(() => {
    onEditableChange?.(
      content !== null && new TextEncoder().encode(content).length <= 1048576,
    );
  }, [content, onEditableChange]);
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
        <a href={url} download={reference.path?.split('/').at(-1)}>
          Download {reference.path?.split('/').at(-1)}
        </a>
        {onReference && (
          <Button type="button" onClick={() => onReference(reference)}>
            Use as reference
          </Button>
        )}
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
        <p>
          {/\.doc$/i.test(reference.path ?? '')
            ? 'Legacy Word files (.doc) have no text extraction. Save a DOCX copy and attach it to make the text readable.'
            : 'This file has no text preview. Download it to open in a suitable application.'}{' '}
          Agents receive the original file without extracted text.
        </p>
      ) : content === null ? (
        <p role="status">Loading file…</p>
      ) : (
        <>
          <div className="workspace-preview-content" tabIndex={0}>
            {markdown && !source ? (
              <AssistantContent
                content={content.slice(0, previewLimit) || '(Empty file)'}
              />
            ) : (
              <pre>{content.slice(0, previewLimit) || '(Empty file)'}</pre>
            )}
          </div>
          {content.length > previewLimit && (
            <p>Preview shortened. Download the complete file.</p>
          )}
        </>
      )}
    </section>
  );
}
