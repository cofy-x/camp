import { useCallback, useRef, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import type { DocumentSnapshot, Reference, Resource, RunStatus } from './api';
import { statusLabel } from './api';
import { FilePreview } from './FilePreview';
import { attachmentSize } from './uploadState';
import { referenceKey } from './conversationState';
import {
  documentFormat,
  documentSnapshot,
  documentStatusLabel,
} from './documents';
import { useDocumentPreview } from './useDocumentPreview';

function ReferenceCard({
  reference,
  current,
  onSource,
  onRemove,
  onDocumentChange,
}: {
  reference: Reference;
  current?: string;
  onSource: (messageId: string) => void;
  onRemove?: () => void;
  onDocumentChange?: (reference: Reference, document: DocumentSnapshot) => void;
}) {
  const [open, setOpen] = useState(false);
  const evidence = useRef<HTMLDivElement>(null);
  const reveal = useCallback(() => {
    evidence.current
      ?.querySelector('.workspace-preview-content')
      ?.scrollIntoView({ block: 'nearest' });
  }, []);
  const file = reference.kind === 'file';
  const prepareDocument = Boolean(
    onDocumentChange && file && documentFormat(reference.path),
  );
  const documentState = useDocumentPreview(
    reference,
    (document) => onDocumentChange?.(reference, document),
    prepareDocument,
  );
  const document = documentState.document
    ? documentSnapshot(documentState.document)
    : reference.document;
  const status = reference.sourceStatus
    ? (statusLabel[reference.sourceStatus as RunStatus] ??
      reference.sourceStatus)
    : '';
  return (
    <div className="workspace-reference-card">
      <details
        open={open}
        onToggle={(event) => setOpen(event.currentTarget.open)}
      >
        <summary>
          <PixelIcon name={file ? 'document' : 'conversation'} size={16} />
          <span>{reference.title || reference.path || 'Message'}</span>
          {reference.author && <small>{reference.author}</small>}
          {reference.origin === 'upload' && (
            <small>
              Attached
              {reference.size !== undefined
                ? ` · ${attachmentSize(reference.size)}`
                : ''}
            </small>
          )}
          {status && <small>{status}</small>}
          {document ? (
            <small>{documentStatusLabel(document)}</small>
          ) : prepareDocument ? (
            <small>
              {documentState.error ? 'Preview unavailable' : 'Preparing text'}
            </small>
          ) : null}
        </summary>
        {open && (
          <div className="workspace-reference-evidence" ref={evidence}>
            {file ? (
              <>
                <p>
                  {reference.origin === 'upload'
                    ? 'Uploaded attachment'
                    : reference.sourceRunId
                      ? reference.published
                        ? 'Saved result file'
                        : 'Retained result file'
                      : 'Shared resource file'}{' '}
                  · pinned version
                  {current && current !== reference.commit && (
                    <strong> · Shared files use a different version</strong>
                  )}
                </p>
                <FilePreview
                  reference={reference}
                  onReady={reveal}
                  documentState={prepareDocument ? documentState : undefined}
                  onDocumentChange={
                    onDocumentChange
                      ? (document) => onDocumentChange(reference, document)
                      : undefined
                  }
                />
                <details>
                  <summary>Version details</summary>
                  <code>{reference.commit}</code>
                </details>
              </>
            ) : (
              <>
                {reference.content && (
                  <blockquote>{reference.content}</blockquote>
                )}
                {reference.truncated && (
                  <p>
                    Excerpt shortened. Open the source for the complete message.
                  </p>
                )}
                {reference.sourceStatus && (
                  <p>
                    {status}
                    {reference.published
                      ? ' · Saved to room files'
                      : reference.sourceRunId
                        ? ' · No shared files published'
                        : ''}
                    {reference.checks ? ` · ${reference.checks}` : ''}
                  </p>
                )}
                <Button
                  type="button"
                  onClick={() => onSource(reference.messageId!)}
                >
                  Open source
                </Button>
              </>
            )}
          </div>
        )}
      </details>
      {onRemove && (
        <Button
          type="button"
          aria-label={`Remove reference ${reference.title}`}
          onClick={onRemove}
        >
          ×
        </Button>
      )}
    </div>
  );
}

export function References({
  references,
  resources,
  onSource,
  onRemove,
  onDocumentChange,
}: {
  references: Reference[];
  resources: Resource[];
  onSource: (messageId: string) => void;
  onRemove?: (index: number) => void;
  onDocumentChange?: (reference: Reference, document: DocumentSnapshot) => void;
}) {
  if (!references.length) return null;
  return (
    <section
      className={`workspace-references ${onRemove ? 'is-draft' : ''}`}
      aria-label={onRemove ? 'Draft references' : 'Message references'}
    >
      <div className="workspace-reference-label">
        {onRemove
          ? `References · ${references.length} / 8`
          : `Referenced sources · ${references.length}`}
      </div>
      {references.map((reference, index) => (
        <ReferenceCard
          key={referenceKey(reference)}
          reference={reference}
          current={
            resources.find((resource) => resource.id === reference.resourceId)
              ?.currentCommit
          }
          onSource={onSource}
          onRemove={onRemove ? () => onRemove(index) : undefined}
          onDocumentChange={onDocumentChange}
        />
      ))}
    </section>
  );
}
