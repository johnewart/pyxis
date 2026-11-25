/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

export const CHAT_FIND_HIGHLIGHT_ALL = 'chat-transcript-find';
export const CHAT_FIND_HIGHLIGHT_CURRENT = 'chat-transcript-find-current';

function nodeInFindScope(node: Node, root: HTMLElement): boolean {
  const parent =
    node.nodeType === Node.TEXT_NODE ? node.parentElement : (node as Element);
  if (!parent || !root.contains(parent)) return false;
  if (parent.closest('[data-chat-find-ignore]')) return false;
  if (parent.closest('.chat-transcript-find-bar')) return false;
  const tag = parent.tagName;
  if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'TEXTAREA' || tag === 'INPUT') {
    return false;
  }
  return true;
}

/** Collect non-overlapping text match ranges under `root` (case-insensitive). */
export function collectChatTranscriptMatchRanges(
  root: HTMLElement,
  query: string
): Range[] {
  const q = query.trim();
  if (!q) return [];

  const lowerQ = q.toLowerCase();
  const ranges: Range[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      return nodeInFindScope(node, root)
        ? NodeFilter.FILTER_ACCEPT
        : NodeFilter.FILTER_REJECT;
    },
  });

  let node: Node | null;
  while ((node = walker.nextNode())) {
    const text = node.textContent ?? '';
    const lowerText = text.toLowerCase();
    let start = 0;
    while (start < lowerText.length) {
      const idx = lowerText.indexOf(lowerQ, start);
      if (idx === -1) break;
      const range = document.createRange();
      range.setStart(node, idx);
      range.setEnd(node, idx + q.length);
      ranges.push(range);
      start = idx + Math.max(1, q.length);
    }
  }
  return ranges;
}

export function supportsCssCustomHighlight(): boolean {
  return typeof CSS !== 'undefined' && 'highlights' in CSS;
}

export function applyChatTranscriptFindHighlights(
  ranges: Range[],
  currentIndex: number,
  maxPainted = 300
): void {
  if (!supportsCssCustomHighlight()) return;
  CSS.highlights.delete(CHAT_FIND_HIGHLIGHT_ALL);
  CSS.highlights.delete(CHAT_FIND_HIGHLIGHT_CURRENT);
  if (ranges.length === 0) return;
  const paintRanges =
    ranges.length > maxPainted ? ranges.slice(0, maxPainted) : ranges;
  CSS.highlights.set(
    CHAT_FIND_HIGHLIGHT_ALL,
    new Highlight(...paintRanges)
  );
  if (currentIndex >= 0 && currentIndex < ranges.length) {
    CSS.highlights.set(
      CHAT_FIND_HIGHLIGHT_CURRENT,
      new Highlight(ranges[currentIndex]!)
    );
  }
}

export function clearChatTranscriptFindHighlights(): void {
  if (!supportsCssCustomHighlight()) return;
  CSS.highlights.delete(CHAT_FIND_HIGHLIGHT_ALL);
  CSS.highlights.delete(CHAT_FIND_HIGHLIGHT_CURRENT);
}

export function scrollChatTranscriptMatchIntoView(
  range: Range,
  viewport: HTMLElement
): void {
  const rangeRect = range.getBoundingClientRect();
  const vpRect = viewport.getBoundingClientRect();
  if (rangeRect.height === 0 && rangeRect.width === 0) {
    const el =
      range.startContainer.nodeType === Node.TEXT_NODE
        ? range.startContainer.parentElement
        : (range.startContainer as Element);
    el?.scrollIntoView({ block: 'center', behavior: 'auto' });
    return;
  }
  const relativeTop = rangeRect.top - vpRect.top + viewport.scrollTop;
  const targetScroll =
    relativeTop - vpRect.height / 2 + rangeRect.height / 2;
  viewport.scrollTop = Math.max(
    0,
    Math.min(targetScroll, viewport.scrollHeight - viewport.clientHeight)
  );
}
