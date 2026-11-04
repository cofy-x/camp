import { useCallback, useRef, useState } from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import type { Reference, Resource, RunStatus } from './api';
import { statusLabel } from './api';
import { FilePreview } from './FilePreview';
import { attachmentSize } from './uploadState';
import { referenceKey } from './conversationState';

function ReferenceCard({
  reference,
  current,
  onSource,
  onRemove,
}: {
  reference: Reference;
  current?: string;
  onSource: (messageId: string) => void;
  onRemove?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const evidence = useRef<HTMLDivElement>(null);
  const reveal = useCallback(() => {
    evidence.current
      ?.querySelector('.workspace-preview-content')
      ?.scrollIntoView({ block: 'nearest' });
  }, []);
  const file = reference.kind === 'file';
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
        </summary>
        {open && (
          <div className="workspace-reference-evidence" ref={evidence}>
            {file ? (
              <>
                {reference.sourceRunId && (
                  <p>
                    {reference.published
                      ? 'Saved result file'
                      : 'Retained result file'}
                  </p>
                )}
                {current && current !== reference.commit && (
                  <p>
                    <strong>Shared files use a different version</strong>
                  </p>
                )}
                <FilePreview reference={reference} onReady={reveal} />
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
  showLabel = true,
}: {
  references: Reference[];
  resources: Resource[];
  onSource: (messageId: string) => void;
  onRemove?: (index: number) => void;
  showLabel?: boolean;
}) {
  if (!references.length) return null;
  return (
    <section
      className={`workspace-references ${onRemove ? 'is-draft' : ''}`}
      aria-label={onRemove ? 'Draft references' : 'Message references'}
    >
      {showLabel && (
        <div className="workspace-reference-label">
          {onRemove
            ? `References · ${references.length} / 8`
            : `Referenced sources · ${references.length}`}
        </div>
      )}
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
        />
      ))}
    </section>
  );
}
