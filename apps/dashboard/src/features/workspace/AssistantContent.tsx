import { useId } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
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
        components={{
          a: ({
            href,
            children,
            id,
            'aria-label': ariaLabel,
            'aria-describedby': ariaDescribedBy,
          }) => {
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
                  ariaDescribedBy === 'footnote-label'
                    ? footnoteLabelId
                    : ariaDescribedBy
                }
                target={href.startsWith('#') ? undefined : '_blank'}
                rel={href.startsWith('#') ? undefined : 'noopener noreferrer'}
              >
                {children}
              </a>
            ) : (
              <span>{children}</span>
            );
          },
          h2: ({ id, children }) => (
            <h2 id={id === 'footnote-label' ? footnoteLabelId : id}>
              {children}
            </h2>
          ),
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
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
