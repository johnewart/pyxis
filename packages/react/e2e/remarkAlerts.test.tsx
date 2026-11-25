/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** Unit-speed: GitHub alert callouts rendered through ReactMarkdown with the built-in plugins. */

import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ReactMarkdown from 'react-markdown';
import { describe, expect, it } from 'vitest';
import remarkGfm from 'remark-gfm';
import { remarkAlerts } from '../src/remarkAlerts';
import { StreamingMarkdown } from '../src/StreamingMarkdown';

const PLUGINS = [remarkGfm, remarkAlerts];

function render(md: string): string {
  return renderToStaticMarkup(<ReactMarkdown remarkPlugins={PLUGINS}>{md}</ReactMarkdown>);
}

describe('StreamingMarkdown callouts (built-in plugins)', () => {
  const text = '> [!WARNING]\n> Could not reach X';
  const stream = (t: string, complete = false) =>
    renderToStaticMarkup(<StreamingMarkdown text={t} complete={complete} />);

  it('shows the plain-text tail while the block is incomplete', () => {
    const html = stream(text);
    expect(html).not.toContain('llmd-callout');
    expect(html).toContain('streaming-markdown-tail');
  });

  it('renders the callout once the block is frozen', () => {
    const html = stream(`${text}\n\nmore`);
    expect(html).toContain('class="llmd-callout llmd-callout--warning"');
    expect(html).not.toContain('[!');
  });

  it('renders the callout when complete', () => {
    expect(stream(text, true)).toContain('class="llmd-callout llmd-callout--warning"');
  });
});

describe('remarkAlerts', () => {
  for (const kind of ['NOTE', 'TIP', 'IMPORTANT', 'WARNING', 'CAUTION']) {
    it(`renders ${kind} as a callout and removes the marker`, () => {
      const html = render(`> [!${kind}]\n> Body text`);
      expect(html).toContain(`class="llmd-callout llmd-callout--${kind.toLowerCase()}"`);
      const title = kind[0] + kind.slice(1).toLowerCase();
      expect(html).toContain(`<div class="llmd-callout__title">${title}</div>`);
      expect(html).not.toContain('[!');
      expect(html).toContain('Body text');
    });
  }

  it('is case-insensitive on the marker', () => {
    expect(render('> [!warning]\n> x')).toContain('llmd-callout--warning');
  });

  it('keeps markdown in the body', () => {
    const html = render('> [!TIP]\n> use **bold** and `code` and [docs](https://example.com)\n>\n> - item');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toContain('<code>code</code>');
    expect(html).toContain('<a href="https://example.com">docs</a>');
    expect(html).toContain('<li>item</li>');
  });

  it('leaves unknown kinds unchanged', () => {
    const html = render('> [!BOGUS]\n> text');
    expect(html).not.toContain('llmd-callout');
    expect(html).toContain('[!BOGUS]');
  });

  it('leaves plain blockquotes unchanged', () => {
    const html = render('> just a quote');
    expect(html).not.toContain('llmd-callout');
    expect(html).toContain('<blockquote>');
  });
});
