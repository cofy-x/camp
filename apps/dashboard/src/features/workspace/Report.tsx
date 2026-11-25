import { MarkdownContent } from '../../shared/content/MarkdownContent';
import { ReadingDisclosure } from '../../shared/content/ReadingDisclosure';
import { readStored, writeStored } from './conversationState';

// Keep the exact authored report available without parsing and mounting a long
// transcript before the person asks to read it. Saved files and state stay outside.
export function Report({
  content,
  stateKey,
  kind = 'report',
  compact = false,
}: {
  content: string;
  stateKey: string;
  kind?: 'reply' | 'report';
  compact?: boolean;
}) {
  const long = compact
    ? content.length > 240 || /\r?\n\s*\r?\n/.test(content)
    : content.length > 800 || content.split('\n', 13).length > 12;
  const report = <MarkdownContent content={content} />;
  if (!long)
    return <div className="workspace-agent-reply-inline">{report}</div>;
  const lead =
    kind === 'report'
      ? content
          .trim()
          .split(/\r?\n\s*\r?\n/, 2)
          .find(
            (paragraph) =>
              paragraph.length <= 320 && !/^\s*#{1,6}\s/.test(paragraph),
          )
      : undefined;
  const preview = lead && (
    <div
      className={`workspace-agent-reply-inline${compact ? ' workspace-agent-reply-preview' : ''}`}
    >
      <MarkdownContent content={lead} />
    </div>
  );

  return (
    <ReadingDisclosure
      className="workspace-agent-reply"
      label={`Full ${kind}`}
      preview={preview}
      defaultOpen={readStored(stateKey) === 'true'}
      onOpenChange={(open) => writeStored(stateKey, open ? 'true' : '')}
    >
      {report}
    </ReadingDisclosure>
  );
}
