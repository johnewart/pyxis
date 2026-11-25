/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Multi-line clipboard pastes get wrapped in a distinctive fenced block
 * (` ````Paste #N `) when sent, so a rendered message can present them as a
 * collapsible/captioned block instead of dumping raw pasted text inline.
 * This module is the parser for that wire format; the composer-side capture
 * UI (turning a live paste into one of these fences) is a host concern and
 * not part of this package (yet).
 */

import { countPasteLines } from './pasteFormat.js';

/** Outer fence length for sent pastes (4 ticks so inner ``` blocks stay inside). */
export const PASTE_FENCE_TICKS = 4;

const PASTE_FENCE_OPEN_RE = /(`{3,})Paste #(\d+)\n/g;

export type PasteFenceSegment =
  | {
      kind: 'text';
      text: string;
    }
  | {
      kind: 'paste';
      number: number;
      content: string;
      lineCount: number;
    };

function isLineStart(content: string, index: number): boolean {
  return index === 0 || content[index - 1] === '\n';
}

function parseFenceLine(
  content: string,
  lineStart: number
): { tickCount: number; info: string; lineEnd: number } | null {
  const rest = content.slice(lineStart);
  const m = rest.match(/^(`{3,})([^\n]*)(\n|$)/);
  if (!m) return null;
  return {
    tickCount: m[1].length,
    info: m[2].trim(),
    lineEnd: lineStart + m[0].length,
  };
}

function findNextFenceLineStart(content: string, from: number): number {
  for (let i = from; i < content.length; i++) {
    if (content[i] !== '`' || !isLineStart(content, i)) continue;
    const parsed = parseFenceLine(content, i);
    if (parsed && parsed.tickCount >= 3) return i;
  }
  return -1;
}

function findPasteFenceClose(
  content: string,
  bodyStart: number,
  openTickCount: number
): number {
  let insideGenericFence = false;
  let pos = bodyStart;

  while (pos < content.length) {
    const fenceStart = findNextFenceLineStart(content, pos);
    if (fenceStart === -1) return -1;

    const line = parseFenceLine(content, fenceStart);
    if (!line) {
      pos = fenceStart + 1;
      continue;
    }

    const { tickCount, info, lineEnd } = line;
    const canClosePaste = tickCount >= openTickCount && info === '';

    if (insideGenericFence) {
      insideGenericFence = false;
      pos = lineEnd;
      continue;
    }

    if (canClosePaste) {
      const afterLine = content.slice(lineEnd);
      const nextRelative = findNextFenceLineStart(afterLine, 0);
      const between =
        nextRelative === -1 ? afterLine : afterLine.slice(0, nextRelative);

      // If the next fence opens a *new* paste block, this fence is a genuine
      // close — never merge two distinct pastes (and the text between them)
      // into one blob. The generic-fence lookahead below is only for keeping
      // nested ``` code blocks inside a single paste, where the following
      // fence is a bare (non-paste) fence.
      const nextIsPasteOpen =
        nextRelative !== -1 &&
        /^`{3,}Paste #\d+/.test(afterLine.slice(nextRelative));

      if (
        nextRelative !== -1 &&
        between.trim().length > 0 &&
        !nextIsPasteOpen
      ) {
        insideGenericFence = true;
        pos = lineEnd;
      } else {
        return fenceStart;
      }
    } else if (tickCount >= 3) {
      insideGenericFence = true;
      pos = lineEnd;
    } else {
      pos = lineEnd;
    }
  }

  return -1;
}

/** Wrap `content` as a sendable paste fence, e.g. for a composer-side capture feature. */
export function formatPasteFence(number: number, content: string): string {
  const tick = '`'.repeat(PASTE_FENCE_TICKS);
  return `\n\n${tick}Paste #${number}\n${content}\n${tick}\n\n`;
}

/** Ensure blank lines surround paste fences so chat markdown renders correctly. */
export function normalizeOutgoingPasteSpacing(text: string): string {
  let out = text.replace(/\r\n/g, '\n');
  out = out.replace(/([^\n])\s*\n(`{3,}Paste #\d+\n)/g, '$1\n\n$2');
  out = out.replace(/(`{3,})\n[ \t]*(?=\S)/g, '$1\n\n');
  out = out.replace(/\n{4,}/g, '\n\n\n');
  return out.replace(/[^\S\n]+$/g, '');
}

/** Split a message's content into text runs and paste-fence blocks, in order. */
export function splitMessageByPasteFences(content: string): PasteFenceSegment[] {
  if (!content) return [{ kind: 'text', text: '' }];
  const segments: PasteFenceSegment[] = [];
  let last = 0;
  const openRe = new RegExp(PASTE_FENCE_OPEN_RE.source, 'g');
  let m: RegExpExecArray | null;

  while ((m = openRe.exec(content)) !== null) {
    if (m.index > last) {
      const t = content.slice(last, m.index);
      if (t) segments.push({ kind: 'text', text: t });
    }

    const openTickCount = m[1].length;
    const pasteNum = Number.parseInt(m[2], 10);
    const bodyStart = m.index + m[0].length;
    const closeStart = findPasteFenceClose(content, bodyStart, openTickCount);

    if (closeStart === -1) {
      const body = content.slice(bodyStart);
      segments.push({
        kind: 'paste',
        number: pasteNum,
        content: body.replace(/\n$/, ''),
        lineCount: countPasteLines(body),
      });
      last = content.length;
      break;
    }

    const body = content.slice(bodyStart, closeStart);
    const closeLine = parseFenceLine(content, closeStart)!;
    segments.push({
      kind: 'paste',
      number: pasteNum,
      content: body.replace(/\n$/, ''),
      lineCount: countPasteLines(body),
    });
    last = closeLine.lineEnd;
    openRe.lastIndex = last;
  }

  if (last < content.length) {
    segments.push({ kind: 'text', text: content.slice(last) });
  }
  if (segments.length === 0) {
    segments.push({ kind: 'text', text: content });
  }
  return segments;
}

export function messageHasPasteFences(content: string): boolean {
  return /`{3,}Paste #\d+\n/.test(content);
}

export function messageContainsPasteFenceForNumber(
  content: string,
  number: number
): boolean {
  return new RegExp(`\`{3,}Paste #${number}\\n`, 'm').test(content);
}
