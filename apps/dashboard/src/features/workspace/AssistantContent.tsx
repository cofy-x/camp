import { createContext, useContext, useId } from 'react';
import type { ComponentProps } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
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
  footnoteLabelId: string;
}>({ footnoteLabelId: '' });

function MarkdownLink({
  href,
  children,
  id,
  'aria-label': ariaLabel,
  'aria-describedby': ariaDescribedBy,
}: ComponentProps<'a'>) {
  const { footnoteLabelId } = useContext(MarkdownContext);
  return href ? (
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

export function AssistantContent({ content }: { content: string }) {
  const markdownId = useId();
  const footnotePrefix = `camp-markdown-${markdownId}-`;
  const footnoteLabelId = `${footnotePrefix}footnotes`;
  return (
    <MarkdownContext.Provider value={{ footnoteLabelId }}>
      <div className="workspace-markdown">
        <ReactMarkdown
          skipHtml
          remarkPlugins={[remarkGfm]}
          remarkRehypeOptions={{ clobberPrefix: footnotePrefix }}
          urlTransform={safeURL}
          components={markdownComponents}
        >
          {content}
        </ReactMarkdown>
      </div>
    </MarkdownContext.Provider>
  );
}
