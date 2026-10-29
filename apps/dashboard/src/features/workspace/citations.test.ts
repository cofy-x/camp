import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { Reference } from './api';
import { AssistantContent } from './AssistantContent';
import { resolveCitation } from './citations';

const reference: Reference = {
  kind: 'file',
  resourceId: 'original-resource',
  commit: 'a'.repeat(40),
  path: 'customer-brief.pdf',
  title: 'Customer brief',
  document: {
    id: 'frozen-artifact',
    format: 'pdf',
    status: 'ready',
    sectionCount: 2,
    warnings: [],
  },
};

describe('source citations', () => {
  it('resolves physical pages against only this run’s exact evidence', () => {
    const sources: Reference[] = [
      { kind: 'message', messageId: 'anchor', title: 'Source reply' },
      reference,
    ];
    const citation = resolveCitation('camp-source:2:p2', sources);
    expect(citation?.reference.document?.id).toBe('frozen-artifact');
    expect(citation?.label).toBe('customer-brief.pdf · Page 2');
    expect(citation?.section).toBe('p2');
    for (const invalid of [
      'camp-source:1:p1',
      'camp-source:3:p1',
      'camp-source:2:p3',
      'camp-source:2:p0',
      'camp-source:02:p1',
      'camp-source:2:p1?path=/etc/passwd',
      'camp-source://2/p1',
      'https://example.com/camp-source:2:p1',
    ])
      expect(resolveCitation(invalid, sources)).toBeUndefined();
  });

  it('does not turn unreadable or missing extraction into a source claim', () => {
    for (const status of [
      'queued',
      'extracting',
      'unreadable',
      'failed',
      'unavailable',
    ] as const)
      expect(
        resolveCitation('camp-source:1:p1', [
          { ...reference, document: { ...reference.document!, status } },
        ]),
      ).toBeUndefined();
    expect(
      resolveCitation('camp-source:1:p1', [
        { ...reference, document: undefined },
      ]),
    ).toBeUndefined();
  });

  it('labels DOCX source positions as paragraphs rather than pages', () => {
    const citation = resolveCitation('camp-source:1:p2', [
      {
        ...reference,
        path: 'interviews.docx',
        document: { ...reference.document!, format: 'docx' },
      },
    ]);
    expect(citation?.label).toBe('interviews.docx · Paragraph 2');
  });

  it('uses canonical source labels, never opens internal URLs externally, and ignores citations in code', () => {
    const html = renderToStaticMarkup(
      createElement(AssistantContent, {
        content:
          '[Invented page 99](camp-source:1:p2) [Missing](camp-source:1:p3) `camp-source:1:p1` ![Photo](camp-source:1:p1)',
        references: [reference],
        onCitation: () => {},
      }),
    );
    expect(html).toContain('Open source customer-brief.pdf · Page 2');
    expect(html).not.toContain('Invented page 99');
    expect(html).toContain('<span>Missing</span>');
    expect(html).toContain('<code>camp-source:1:p1</code>');
    expect(html.match(/<button/g)).toHaveLength(1);
    expect(html).not.toContain('href="camp-source');
    expect(html).not.toContain('<img');
    const standalone = renderToStaticMarkup(
      createElement(AssistantContent, {
        content: '[Source](camp-source:1:p2)',
        references: [reference],
      }),
    );
    expect(standalone).not.toContain('<button');
    expect(standalone).not.toContain('href=');
  });
});
