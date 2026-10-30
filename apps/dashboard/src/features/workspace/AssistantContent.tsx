import { createContext, useContext, useId } from 'react';
import type { ComponentProps } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Reference } from './api';
import { resolveCitation } from './citations';
import type { SourceCitation } from './citations';
import './AssistantContent.css';

function safeURL(value: string) {
  try {
    const safe = defaultUrlTransform(value);
    const url = new URL(safe, 'https://camp.invalid');
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? safe : '';
  } catch {
    return '';
  }
}

const MarkdownContext = createContext<{
  references: Reference[];
  onCitation?: (citation: SourceCitation) => void;
  footnoteLabelId: string;
}>({ references: [], footnoteLabelId: '' });

function MarkdownLink({
  href,
  children,
  id,
  'aria-label': ariaLabel,
  'aria-describedby': ariaDescribedBy,
}: ComponentProps<'a'>) {
  const { references, onCitation, footnoteLabelId } =
    useContext(MarkdownContext);
  const citation = href && resolveCitation(href, references);
  return citation && onCitation ? (
    <button
      type="button"
      className="workspace-source-link"
      aria-label={`Open source ${citation.label}`}
      onClick={() => onCitation(citation)}
    >
      {citation.label}
    </button>
  ) : href ? (
    <a
      href={href}
      id={id}
      aria-label={ariaLabel}
      aria-describedby={
        ariaDescribedBy === 'footnote-label' ? footnoteLabelId : ariaDescribedBy
      }
      target={href.startsWith('#') ? undefined : '_blank'}
      rel={href.startsWith('#') ? undefined : 'noopener noreferrer'}
    >
      {children}
    </a>
  ) : (
    <span>{children}</span>
  );
}

function MarkdownHeading({ id, children }: ComponentProps<'h2'>) {
  const { footnoteLabelId } = useContext(MarkdownContext);
  return (
    <h2 id={id === 'footnote-label' ? footnoteLabelId : id}>{children}</h2>
  );
}

// Stable component types preserve focused links, selection and pending pointer
// clicks when surrounding desktop state or a streamed reply updates.
const markdownComponents: Components = {
  a: MarkdownLink,
  h2: MarkdownHeading,
  table: ({ children }) => (
    <div
      className="workspace-markdown-table"
      role="region"
      aria-label="Document table"
      tabIndex={0}
    >
      <table>{children}</table>
    </div>
  ),
  th: ({ children, style }) => (
    <th scope="col" style={style}>
      {children}
    </th>
  ),
  img: ({ src, alt }) => (
    <span className="workspace-image-reference">
      Image: {alt || 'Untitled'}
      {typeof src === 'string' && src && (
        <>
          {' '}
          ·{' '}
          <a href={src} target="_blank" rel="noopener noreferrer">
            Open image
          </a>
        </>
      )}
    </span>
  ),
};

export function AssistantContent({
  content,
  references = [],
  onCitation,
}: {
  content: string;
  references?: Reference[];
  onCitation?: (citation: SourceCitation) => void;
}) {
  const documentId = useId();
  const footnotePrefix = `camp-markdown-${documentId}-`;
  const footnoteLabelId = `${footnotePrefix}footnotes`;
  return (
    <MarkdownContext.Provider
      value={{ references, onCitation, footnoteLabelId }}
    >
      <div className="workspace-markdown">
        <ReactMarkdown
          skipHtml
          remarkPlugins={[remarkGfm]}
          remarkRehypeOptions={{ clobberPrefix: footnotePrefix }}
          urlTransform={(value, key) =>
            key === 'href' && onCitation && resolveCitation(value, references)
              ? value
              : safeURL(value)
          }
          components={markdownComponents}
        >
          {content}
        </ReactMarkdown>
      </div>
    </MarkdownContext.Provider>
  );
}
