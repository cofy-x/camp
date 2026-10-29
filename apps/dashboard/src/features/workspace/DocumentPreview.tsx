import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import type { Reference } from './api';
import { documentPending } from './documents';
import type { DocumentPreviewState } from './useDocumentPreview';

export function DocumentPreview({
  reference,
  initialSection,
  canRetry,
  state,
  onReady,
}: {
  reference: Reference;
  initialSection?: string;
  canRetry: boolean;
  state: DocumentPreviewState;
  onReady?: () => void;
}) {
  const { document, error, pollingStopped, refresh, retry } = state;
  const [sectionId, setSectionId] = useState(initialSection ?? '');
  const content = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setSectionId(initialSection ?? '');
  }, [initialSection, reference.resourceId, reference.commit, reference.path]);
  const selected = document?.sections.find((item) => item.id === sectionId);
  const section = selected ?? (!sectionId ? document?.sections[0] : undefined);
  const sectionIndex = document?.sections.findIndex(
    (item) => item.id === section?.id,
  );
  useLayoutEffect(() => {
    if (content.current) {
      content.current.scrollTop = 0;
      content.current.scrollIntoView({ block: 'nearest' });
    }
  }, [section?.id]);
  useLayoutEffect(() => {
    if (error || (document && !documentPending(document.status))) onReady?.();
  }, [document, error, onReady]);

  if (error)
    return (
      <p className="app-error" role="alert">
        {error}{' '}
        <Button type="button" onClick={refresh}>
          Retry preview
        </Button>
      </p>
    );
  if (!document || documentPending(document.status))
    return (
      <p role="status" className="workspace-document-status">
        {pollingStopped
          ? 'Text preparation is taking longer than expected.'
          : 'Preparing text…'}
        {pollingStopped && (
          <Button type="button" onClick={refresh}>
            Check again
          </Button>
        )}
      </p>
    );
  return (
    <div className="workspace-document-preview">
      {document.status === 'ready' ? (
        <>
          <p className="workspace-document-note">
            Extracted text · {document.format.toUpperCase()} ·{' '}
            {document.sections.length}{' '}
            {document.format === 'pdf' ? 'page' : 'paragraph'}
            {document.sections.length === 1 ? '' : 's'}
            {document.warnings.length > 0 && (
              <strong>
                {' '}
                · {document.warnings.length} note
                {document.warnings.length === 1 ? '' : 's'}
              </strong>
            )}
          </p>
          {document.sections.length > 0 && (
            <div className="workspace-document-navigation">
              <Select
                label="Source location"
                value={section?.id ?? ''}
                onChange={(event) => setSectionId(event.target.value)}
              >
                {!section && <option value="">Choose a source location</option>}
                {document.sections.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.label}
                  </option>
                ))}
              </Select>
              <div className="workspace-document-step-actions">
                <Button
                  type="button"
                  disabled={sectionIndex === undefined || sectionIndex <= 0}
                  onClick={() =>
                    setSectionId(document.sections[sectionIndex! - 1].id)
                  }
                >
                  Previous
                </Button>
                <Button
                  type="button"
                  disabled={
                    sectionIndex === undefined ||
                    sectionIndex < 0 ||
                    sectionIndex >= document.sections.length - 1
                  }
                  onClick={() =>
                    setSectionId(document.sections[sectionIndex! + 1].id)
                  }
                >
                  Next
                </Button>
              </div>
            </div>
          )}
          {section ? (
            <div
              className="workspace-preview-content workspace-document-section"
              ref={content}
              tabIndex={0}
              role="region"
              aria-label={section.label}
            >
              <strong>{section.label}</strong>
              {section.text ? (
                <p>{section.text}</p>
              ) : (
                <p className="workspace-document-empty">
                  No readable text was found here. Images and scanned text are
                  not extracted; OCR is not available.
                </p>
              )}
            </div>
          ) : (
            <p role="status">
              {sectionId
                ? 'The cited source location is not available in this extraction.'
                : 'No readable text was found in this document.'}
            </p>
          )}
          <details className="workspace-document-about">
            <summary>About extracted text</summary>
            <p>
              {document.format === 'pdf'
                ? 'Page numbers follow the original PDF.'
                : 'Paragraph numbers follow the document body, not printed pages.'}{' '}
              Images and layout are not included. Scanned text is not read; OCR
              is not available.
            </p>
          </details>
        </>
      ) : (
        <div className="workspace-document-unavailable" role="status">
          <strong>No extracted text</strong>
          <p>
            {document.error?.message ||
              'Readable text could not be extracted from this document.'}
          </p>
          <p>
            The original is saved and can be downloaded. Agents receive the
            original and this limitation, without extracted text.
          </p>
          {canRetry && document.error?.retryable && (
            <Button
              type="button"
              onClick={() => {
                setSectionId('');
                retry();
              }}
            >
              Prepare text again
            </Button>
          )}
        </div>
      )}
      {document.warnings.length > 0 && (
        <ul
          className="workspace-document-warnings"
          aria-label="Text limitations"
        >
          {document.warnings.map((warning, index) => (
            <li key={`${index}:${warning}`}>{warning}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
