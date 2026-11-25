/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Generic `<attachment:id/>` inline placement-tag mechanism for llmd assistant
 * content, plus assistant-stream control-tag stripping (`<activity>`,
 * `<thinking>`). Host-specific tag conventions (e.g. a host's handoff-id
 * pattern) are NOT here — only the mechanism every host needs.
 */

const ATTACHMENT_TAG_RE = /<attachment:([^/>]+)\/>/g;
const ACTIVITY_TAG_RE = /<activity>[\s\S]*?<\/activity>/gi;
const THINKING_TAG_RE = /<thinking>[\s\S]*?<\/thinking>/gi;

/** Remove agent control XML (activity/thinking headlines) from user-visible assistant prose. */
export function stripAgentControlTagsFromAssistantContent(content: string): string {
  return content
    .replace(ACTIVITY_TAG_RE, '')
    .replace(THINKING_TAG_RE, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

export type ContentSegment =
  | { type: 'markdown'; text: string }
  | { type: 'attachment'; attachmentId: string };

/** Split assistant/user prose into ordered markdown and inline attachment tag segments. */
export function parseMessageContent(content: string): ContentSegment[] {
  const segments: ContentSegment[] = [];
  let lastIndex = 0;
  ATTACHMENT_TAG_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = ATTACHMENT_TAG_RE.exec(content)) !== null) {
    const before = content.slice(lastIndex, match.index).trim();
    if (before) {
      segments.push({ type: 'markdown', text: before });
    }
    const attachmentId = match[1]?.trim();
    if (attachmentId) {
      segments.push({ type: 'attachment', attachmentId });
    }
    lastIndex = match.index + match[0].length;
  }
  const tail = content.slice(lastIndex).trim();
  if (tail) {
    segments.push({ type: 'markdown', text: tail });
  }
  return segments;
}

/** Strip inline attachment placement tags before sending content to the LLM provider. */
export function stripAttachmentTags(content: string): string {
  return content.replace(ATTACHMENT_TAG_RE, '').trim();
}

/** Self-closing tag spliced into assistant stream buffer when a card is registered. */
export function formatAttachmentTag(attachmentId: string): string {
  return `\n<attachment:${attachmentId}/>\n`;
}

export function hasInlineAttachmentTags(content: string): boolean {
  ATTACHMENT_TAG_RE.lastIndex = 0;
  return ATTACHMENT_TAG_RE.test(content);
}

/**
 * Merge llmd wire assistant prose with a host stream buffer that carries inline
 * `<attachment:…/>` placement tags (spliced during tool execution).
 */
export function mergeAssistantContentWithInlineTags(
  wireContent: string,
  streamedContent: string
): string {
  const wire = wireContent.trim();
  const streamed = streamedContent.trim();
  if (!streamed || !hasInlineAttachmentTags(streamed)) {
    return wire || streamed;
  }

  if (!wire) return streamed;

  const segments = parseMessageContent(streamed);
  if (!segments.some((s) => s.type === 'attachment')) {
    return wire || streamed;
  }

  let wireCursor = 0;
  let out = '';
  for (const seg of segments) {
    if (seg.type === 'attachment') {
      out += formatAttachmentTag(seg.attachmentId);
      continue;
    }
    const md = seg.text.trim();
    if (!md) continue;
    const idx = wire.indexOf(md, wireCursor);
    if (idx >= 0) {
      out += wire.slice(wireCursor, idx + md.length);
      wireCursor = idx + md.length;
    } else {
      if (out && !out.endsWith('\n')) out += '\n\n';
      out += md;
    }
  }
  const tail = wire.slice(wireCursor).trim();
  if (tail) {
    if (out && !out.endsWith('\n')) out += '\n\n';
    out += tail;
  }
  return out.trim() || streamed;
}
