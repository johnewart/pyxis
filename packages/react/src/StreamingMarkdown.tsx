/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Progressive ("block-freezing") markdown renderer for streamed agent text.
 *
 * Rendering a growing markdown document by re-parsing the whole string on every
 * token is O(n) parse per token — O(n²) over a stream — and remounts the DOM
 * each render (flicker). Instead we split the stream into:
 *
 *   - committed blocks: everything up to the last SAFE block boundary (a blank
 *     line that is not inside an open code fence). Each block is parsed to
 *     markdown ONCE and memoized, so it never re-renders as more text streams in.
 *   - a live tail: the trailing, still-growing block, rendered as cheap plain
 *     pre-wrap text (flicker-free; no half-parsed markdown flashing).
 *
 * When a new boundary is crossed the tail's completed portion freezes into the
 * committed stack. Net cost is ~O(n) total instead of O(n²), and frozen blocks
 * get `content-visibility: auto` so off-screen ones skip layout/paint.
 */

import React, { useMemo } from 'react';
import ReactMarkdown from 'react-markdown';
import { useMarkdownPlugins } from './markdownPlugins.js';
import { markdownThinkingStreamCodeComponents } from './MarkdownCodeComponents.js';

type SplitResult = { blocks: string[]; tail: string };

/**
 * Split streamed markdown into fully-committed blocks + a live trailing block.
 * Boundaries are blank lines at the top level; a blank line inside an open
 * ``` / ~~~ code fence is NOT a boundary, so fenced code (which may contain
 * blank lines) is never split mid-block.
 */
export function splitStreamingMarkdown(text: string): SplitResult {
  if (!text) return { blocks: [], tail: '' };
  const lines = text.split('\n');
  const blocks: string[] = [];
  let current: string[] = [];
  let inFence = false;
  let fenceMarker = '';

  for (const line of lines) {
    const trimmed = line.trimStart();
    const fenceHit = trimmed.startsWith('```')
      ? '```'
      : trimmed.startsWith('~~~')
        ? '~~~'
        : '';

    if (fenceHit) {
      if (!inFence) {
        inFence = true;
        fenceMarker = fenceHit;
      } else if (trimmed.startsWith(fenceMarker)) {
        inFence = false;
      }
      current.push(line);
      continue;
    }

    if (!inFence && line.trim() === '') {
      // Block boundary — freeze the current block (collapsing consecutive blanks).
      if (current.length > 0) {
        blocks.push(current.join('\n'));
        current = [];
      }
      continue;
    }

    current.push(line);
  }

  // The trailing block stays "live" until a blank line terminates it, so the
  // paragraph currently being written is always the (cheap, plain-text) tail.
  return { blocks, tail: current.join('\n') };
}

/**
 * One frozen markdown block. Memoized on its (immutable, once-committed) content
 * so it is parsed exactly once; `content-visibility: auto` lets the browser skip
 * layout/paint for blocks scrolled off screen. `contain-intrinsic-size: auto …`
 * caches the real size after first render so re-showing does not jump the scroll.
 */
const MarkdownBlock = React.memo(function MarkdownBlock({
  content,
  virtualize,
}: {
  content: string;
  /**
   * Apply `content-visibility: auto` to skip layout/paint while off screen.
   * NEVER virtualize the newest committed block: before it has been laid out
   * once, content-visibility reports the intrinsic-size placeholder instead of
   * its real height, so the viewport under-reports scrollHeight and the
   * scroll-to-bottom stops short (the just-formatted block never comes into
   * view). Older blocks have already been measured — `contain-intrinsic-size:
   * auto` remembers their real size — so collapsing them off screen is safe.
   */
  virtualize: boolean;
}) {
  const { remarkPlugins, rehypePlugins } = useMarkdownPlugins();
  return (
    <div
      className="streaming-markdown-block"
      style={
        virtualize
          ? ({
              contentVisibility: 'auto',
              containIntrinsicSize: 'auto 32px',
            } as React.CSSProperties)
          : undefined
      }
    >
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        components={markdownThinkingStreamCodeComponents}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
});

/**
 * Render streamed markdown progressively: committed blocks as memoized markdown,
 * the still-streaming trailing block as plain text.
 */
export function StreamingMarkdown({
  text,
  complete = false,
}: {
  text: string;
  /**
   * The run has finished, so nothing is still arriving: parse the WHOLE text as
   * markdown instead of leaving the trailing block as plain text. Callers that
   * re-render a finished message from the persisted transcript never need this;
   * callers that keep displaying the streamed text after the run ends (e.g. an
   * in-document ask card) would otherwise show the last block — and for a short
   * answer, the entire response — unformatted.
   */
  complete?: boolean;
}) {
  const { blocks, tail } = useMemo(() => splitStreamingMarkdown(text), [text]);
  if (complete) {
    return <MarkdownBlock content={text} virtualize={false} />;
  }
  return (
    <>
      {blocks.map((block, i) => (
        // Stable prefix: index i always maps to the same frozen content, so the
        // string-equality memo skips re-parsing already-committed blocks. The
        // newest block (last index) is never virtualized so the auto-scroll can
        // measure its real height and reach the bottom.
        <MarkdownBlock
          key={i}
          content={block}
          virtualize={i < blocks.length - 1}
        />
      ))}
      {tail ? (
        <div className="streaming-markdown-tail" style={{ whiteSpace: 'pre-wrap' }}>
          {tail}
        </div>
      ) : null}
    </>
  );
}
