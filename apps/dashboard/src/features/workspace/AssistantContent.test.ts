import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { AssistantContent } from './AssistantContent';

describe('assistant Markdown', () => {
  it('renders document tables with semantic headers, alignment and inline content', () => {
    const html = renderToStaticMarkup(
      createElement(AssistantContent, {
        content: [
          '## Roles',
          '',
          '| Role | Responsibility | Due |',
          '| --- | :---: | ---: |',
          '| **Lead** | Owns the plan | D+5 |',
          '| Engineer | Run `node --test` | D+10 |',
        ].join('\n'),
      }),
    );
    expect(html).toContain(
      'role="region" aria-label="Document table" tabindex="0"',
    );
    expect(html).toContain('<table><thead><tr><th scope="col">Role</th>');
    expect(html).toContain(
      '<th scope="col" style="text-align:center">Responsibility</th>',
    );
    expect(html).toContain('<td><strong>Lead</strong></td>');
    expect(html).toContain('<code>node --test</code>');
    expect(html).toContain('<td style="text-align:right">D+10</td>');
    expect(html).not.toContain('| --- |');
  });

  it('renders task lists as read-only checked states without changing code examples', () => {
    const html = renderToStaticMarkup(
      createElement(AssistantContent, {
        content:
          '- [ ] Prepare account\n- [x] Import data\n\n```md\n- [ ] Example\n| A | B |\n```',
      }),
    );
    expect(html).toContain('class="contains-task-list"');
    expect(html).toContain(
      '<input type="checkbox" disabled=""/> Prepare account',
    );
    expect(html).toContain(
      '<input type="checkbox" disabled="" checked=""/> Import data',
    );
    expect(html.match(/type="checkbox"/g)).toHaveLength(2);
    expect(html).toContain('- [ ] Example\n| A | B |');
  });

  it('keeps footnotes within their own rendered document', () => {
    const content = 'A claim[^1].\n\n[^1]: Supporting evidence.';
    const html = renderToStaticMarkup(
      createElement(
        'div',
        null,
        createElement(AssistantContent, { content }),
        createElement(AssistantContent, { content }),
      ),
    );
    const ids = [...html.matchAll(/ id="([^"]+)"/g)].map((match) => match[1]);
    expect(new Set(ids).size).toBe(ids.length);
    const links = [...html.matchAll(/<a href="#([^"]+)"([^>]*)>/g)];
    expect(links).toHaveLength(4);
    for (const [, target, attributes] of links) {
      expect(ids).toContain(target);
      expect(attributes).not.toContain('target="_blank"');
    }
    for (const [, label] of html.matchAll(/aria-describedby="([^"]+)"/g)) {
      expect(ids).toContain(label);
    }
  });

  it('keeps GFM links safe and never loads embedded content from tables', () => {
    const html = renderToStaticMarkup(
      createElement(AssistantContent, {
        content: [
          '| Source | Status |',
          '| --- | --- |',
          '| www.example.com | ~~Pending~~ Complete |',
          '| ![Remote](https://example.com/tracking.png) | [Unsafe](javascript:alert%281%29) |',
          '| <img src="https://example.com/raw.png"> | [Data](data:text/html;base64,PHNjcmlwdD4=) |',
          '',
          '<iframe src="https://example.com/frame"></iframe>',
        ].join('\n'),
      }),
    );
    expect(html).toContain('href="http://www.example.com"');
    expect(html).toContain('<del>Pending</del> Complete');
    expect(html).toContain('Image: Remote');
    expect(html).not.toMatch(/<(img|iframe|script)\b/);
    expect(html).not.toMatch(/href="(?:javascript|data):/);
    expect(html).not.toContain('example.com/raw.png');
  });

  it('renders headings, lists and code while disabling raw HTML, unsafe links and image loading', () => {
    const html = renderToStaticMarkup(
      createElement(AssistantContent, {
        content:
          '## Changes\n\n- Trim `name`\n\n```js\nconst safe = "<script>";\n```\n\n[Docs](https://example.com/docs)\n\n[Unsafe](javascript:alert%281%29)\n\n![Remote screenshot](https://example.com/pixel.png)\n\n<script>window.compromised = true</script>\n\n<img src="https://example.com/raw.png" onerror="alert(1)">',
      }),
    );
    expect(html).toContain('<h2>Changes</h2>');
    expect(html).toContain('<li>Trim <code>name</code></li>');
    expect(html).toContain('language-js');
    expect(html).toContain('&lt;script&gt;');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).not.toContain('href="javascript:');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('<img');
    expect(html).not.toContain('onerror=');
    expect(html).toContain('Image: Remote screenshot');
  });
});
