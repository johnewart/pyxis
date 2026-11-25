/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * DOM helpers for addressing a single message inside a scrolling transcript.
 *
 * `ChatMessage` stamps `data-message-id` on its root element, which makes
 * `Message.id` the one stable address a host can point at — from a scroll
 * request, a comment pin, a deep link. These helpers do the small amount of
 * geometry that turns such an id into a scroll position, kept out of
 * `ChatPanel` so they can be tested without rendering React.
 *
 * The centering arithmetic deliberately mirrors
 * `scrollChatTranscriptMatchIntoView` (chatTranscriptFindHighlights.ts): assign
 * `scrollTop` directly rather than call `scrollIntoView`, which is
 * unimplemented in some test/embedded DOMs and, in a nested scroll container,
 * tends to scroll ancestors too.
 */

/**
 * How close to the bottom still counts as "parked at the bottom". Sub-pixel
 * layout and fractional device pixel ratios mean an exact equality check
 * reports "not at bottom" for a viewport the user considers fully scrolled.
 */
export const CHAT_PANEL_BOTTOM_THRESHOLD_PX = 32;

/**
 * Finds the rendered root element for `messageId` within `viewport`.
 *
 * Scans the `data-message-id` attributes rather than using a
 * `[data-message-id="…"]` selector: message ids come from the server and are
 * not guaranteed to be selector-safe, and `CSS.escape` is absent in some
 * embedded DOMs. Transcripts are small enough that the scan is irrelevant
 * next to the layout work a scroll triggers anyway.
 */
export function findMessageElement(
  viewport: HTMLElement,
  messageId: string
): HTMLElement | null {
  if (!messageId) return null;
  const candidates = Array.from(
    viewport.querySelectorAll<HTMLElement>('[data-message-id]')
  );
  for (const el of candidates) {
    if (el.getAttribute('data-message-id') === messageId) return el;
  }
  return null;
}

/** Whether `viewport` is scrolled to (or within a hair of) its bottom. */
export function isPinnedToBottom(
  viewport: HTMLElement,
  thresholdPx: number = CHAT_PANEL_BOTTOM_THRESHOLD_PX
): boolean {
  const distance =
    viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight;
  return distance <= thresholdPx;
}

/**
 * Scrolls `viewport` so `el` sits vertically centered, clamped to the
 * scrollable range. A message taller than the viewport ends up with its top
 * region visible, which is the useful part.
 */
export function centerMessageInViewport(
  viewport: HTMLElement,
  el: HTMLElement
): void {
  const elRect = el.getBoundingClientRect();
  const vpRect = viewport.getBoundingClientRect();
  const relativeTop = elRect.top - vpRect.top + viewport.scrollTop;
  const target = relativeTop - vpRect.height / 2 + elRect.height / 2;
  const maxScroll = Math.max(0, viewport.scrollHeight - viewport.clientHeight);
  viewport.scrollTop = Math.max(0, Math.min(target, maxScroll));
}
