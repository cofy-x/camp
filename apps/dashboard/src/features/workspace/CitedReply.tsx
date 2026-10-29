import { useCallback, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import type { Reference } from './api';
import type { SourceCitation } from './citations';
import { AssistantContent } from './AssistantContent';
import { FilePreview } from './FilePreview';

export function CitedReply({
  content,
  references,
  onReference,
}: {
  content: string;
  references: Reference[];
  onReference: (reference: Reference) => void;
}) {
  const [selection, setSelection] = useState<SourceCitation | null>(null);
  const preview = useRef<HTMLDivElement>(null);
  const reveal = useCallback(() => {
    const content = preview.current?.querySelector(
      '.workspace-preview-content',
    );
    (content ?? preview.current)?.scrollIntoView({ block: 'nearest' });
  }, []);
  return (
    <>
      <AssistantContent
        content={content}
        references={references}
        onCitation={(citation) => {
          setSelection(citation);
          requestAnimationFrame(reveal);
        }}
      />
      {selection && (
        <div
          className="workspace-cited-source"
          ref={preview}
          role="region"
          aria-label="Cited source"
        >
          <div className="workspace-cited-heading">
            <strong>{selection.label}</strong>
            <Button type="button" onClick={() => setSelection(null)}>
              Close source
            </Button>
          </div>
          <FilePreview
            key={`${selection.reference.document?.id}:${selection.section}`}
            reference={selection.reference}
            initialSection={selection.section}
            onReference={onReference}
            onReady={reveal}
          />
        </div>
      )}
    </>
  );
}
