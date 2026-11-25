/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { useMemo } from 'react';
import ReactMarkdown from 'react-markdown';
import { useMarkdownPlugins } from './markdownPlugins.js';
import type { Components } from 'react-markdown';
import { pastePillLabel } from './PastePill.js';
import {
  messageHasPasteFences,
  splitMessageByPasteFences,
} from './pasteFences.js';
import { markdownCodeComponents } from './MarkdownCodeComponents.js';

/** Inline-friendly markdown (paragraphs as spans) for composer preview and paste messages. */
export const inlineMarkdownComponents: Components = {
  ...markdownCodeComponents,
  p: ({ children }) => (
    <span className="message-content-with-pastes__md">{children}</span>
  ),
};

function MessagePasteBlock({
  number,
  lineCount,
  content,
}: {
  number: number;
  lineCount: number;
  content: string;
}) {
  if (lineCount <= 1) {
    return (
      <span className="message-paste-inline">
        <span className="message-paste-inline__caption">
          Pasted content · {pastePillLabel(number, lineCount)}
        </span>{' '}
        <code className="message-paste-inline__content">{content}</code>
      </span>
    );
  }

  return (
    <figure className="message-paste-block">
      <figcaption className="message-paste-block__caption">
        Pasted content · {pastePillLabel(number, lineCount)}
      </figcaption>
      <blockquote className="message-paste-block__quote">
        <pre className="message-paste-block__content">{content}</pre>
      </blockquote>
    </figure>
  );
}

/**
 * Renders message content that may contain paste fences as captioned blocks
 * (multi-line) or inline chips (single-line), with the surrounding prose
 * rendered as normal markdown. Falls back to plain markdown when there are
 * no paste fences at all.
 */
export function MessageContentWithPastes({
  content,
  markdownComponents = markdownCodeComponents,
}: {
  content: string;
  markdownComponents?: Components;
}) {
  const segments = useMemo(
    () => splitMessageByPasteFences(content),
    [content]
  );
  const hasPastes = messageHasPasteFences(content);
  const { remarkPlugins, rehypePlugins } = useMarkdownPlugins();

  if (!hasPastes) {
    return (
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        components={markdownComponents}
      >
        {content}
      </ReactMarkdown>
    );
  }

  return (
    <div className="message-content-with-pastes">
      {segments.map((seg, i) => {
        if (seg.kind === 'paste') {
          return (
            <MessagePasteBlock
              key={`paste-${seg.number}-${i}`}
              number={seg.number}
              lineCount={seg.lineCount}
              content={seg.content}
            />
          );
        }
        if (!seg.text.trim()) return null;
        return (
          <ReactMarkdown
            key={`text-${i}`}
            remarkPlugins={remarkPlugins}
            rehypePlugins={rehypePlugins}
            components={inlineMarkdownComponents}
          >
            {seg.text}
          </ReactMarkdown>
        );
      })}
    </div>
  );
}
