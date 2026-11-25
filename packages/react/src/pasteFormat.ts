/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** Number of lines in pasted text (0 for empty/whitespace-only). */
export function countPasteLines(text: string): number {
  if (!text) return 0;
  const normalized = text.replace(/\r\n/g, '\n');
  if (!normalized.trim()) return 0;
  return normalized.split('\n').length;
}

/** Composer placeholder token for a captured multi-line paste, e.g. `[Paste #2 · 14 lines]`. */
export function formatPastePlaceholder(number: number, lineCount: number): string {
  const lines = lineCount === 1 ? '1 line' : `${lineCount} lines`;
  return `[Paste #${number} · ${lines}]`;
}
