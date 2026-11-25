/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

const MIN_REDUNDANT_STREAM_RUN = 12;
const MIN_PHRASE_RESTART = 16;
const MAX_PHRASE_RESTART_GAP = 120;

export function trimTrailingPunctuation(text: string): string {
  return text.replace(/[.,!?;:]+$/u, '');
}

function deltaAlreadyInFold(fold: string, delta: string): boolean {
  if (fold.includes(delta)) return true;
  const trimmed = trimTrailingPunctuation(delta);
  return (
    trimmed.length >= MIN_REDUNDANT_STREAM_RUN && fold.includes(trimmed)
  );
}

function suffixAlreadyInPrefix(prefix: string, suffix: string): boolean {
  if (prefix.includes(suffix)) return true;
  const trimmed = trimTrailingPunctuation(suffix);
  return (
    trimmed.length >= MIN_REDUNDANT_STREAM_RUN && prefix.includes(trimmed)
  );
}

function maxSuffixPrefixOverlap(a: string, b: string): number {
  const max = Math.min(a.length, b.length, 240);
  for (let k = max; k > 0; k--) {
    if (a.slice(-k) === b.slice(0, k)) return k;
  }
  return 0;
}

/**
 * When a delta re-opens the same leading phrase as fold (after a bad partial chunk),
 * prefer the delta alone instead of gluing fold + delta.
 */
function replaceFoldWhenDeltaRestartsOpeningPhrase(
  fold: string,
  delta: string
): string | null {
  if (!fold || !delta) return null;
  const maxLen = Math.min(64, fold.length, delta.length);
  for (let len = maxLen; len >= MIN_PHRASE_RESTART; len--) {
    const opening = fold.slice(0, len);
    if (delta.startsWith(opening)) {
      return delta;
    }
  }
  return null;
}

/** Collapse mid-stream re-emits like `through` + `I'll run` → `run`. */
export function repairReEmittedPhraseRestarts(text: string): string {
  if (!text) return text;
  const positions: number[] = [];
  const re = /I'll\b/gi;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    positions.push(m.index);
    if (positions.length >= 4) break;
  }
  if (positions.length < 2) return text;
  const [first, second] = positions;
  if (second <= first) return text;
  const between = text.slice(first, second);
  if (between.length > 32 || between.includes('\n\n')) return text;
  return text.slice(0, first) + text.slice(second);
}

/**
 * Collapse duplicated opening phrases when a stream restarts mid-sentence, e.g.
 * `The error still looks paths … The error still looks like …` → `The error still looks like …`.
 */
export function repairPhraseRestarts(text: string): string {
  if (text.length < MIN_PHRASE_RESTART * 2) return text;

  for (let len = Math.min(64, text.length); len >= MIN_PHRASE_RESTART; len--) {
    const opening = text.slice(0, len);
    const restartAt = text.indexOf(opening, opening.length);
    if (restartAt < 0) continue;
    const gap = restartAt - opening.length;
    if (gap < 0 || gap > MAX_PHRASE_RESTART_GAP) continue;
    if (opening.includes('\n\n') || text.slice(opening.length, restartAt).includes('\n\n')) {
      continue;
    }
    return text.slice(restartAt);
  }

  return text;
}

function repairStreamFoldText(text: string): string {
  let repaired = repairPhraseRestarts(text);
  for (let i = 0; i < 3; i++) {
    const next = repairReEmittedPhraseRestarts(repaired);
    if (next === repaired) break;
    repaired = next;
  }
  return repaired;
}

/**
 * Size of the trailing window (in chars) that the de-dup / overlap / phrase-restart
 * repairs actually need. Re-emitted chunks and suffix/prefix overlaps only ever occur
 * at the streaming *seam*, and phrase-restart gaps are bounded by MAX_PHRASE_RESTART_GAP
 * (120) with openings <= 64 chars — all comfortably inside this window.
 *
 * Why this exists: previously every delta ran `includes` / `indexOf` / regex / `slice`
 * over the ENTIRE accumulated message. That flattens and re-scans the whole string on
 * every token, making a single streamed turn O(n^2) in time AND transient allocation
 * (~1GB of throwaway strings for a ~50KB message) — in BOTH the renderer fold and the
 * main-process SessionTranscriptService fold. Bounding the repairs to a fixed tail makes
 * append O(1) amortized (O(n) per turn), which removes the RSS blow-up on send.
 */
const STREAM_FOLD_REPAIR_WINDOW = 512;

/** Append a stream delta unless it is already present in the fold (mirrors llmd suffixwire). */
export function appendStreamFoldDelta(fold: string, delta: string): string {
  if (!delta) return fold;

  // Small fold: run the original whole-string repairs (cost is negligible here).
  if (fold.length <= STREAM_FOLD_REPAIR_WINDOW) {
    // Opening-phrase restart repair (delta re-opens the fold's first <=64 chars →
    // prefer the delta alone). Only meaningful near stream start, and it MUST NOT
    // run on a large fold: the fold is rebuilt as a rope (cons string) on every
    // append, and slicing it forces V8 to flatten — copy — the ENTIRE string.
    // Doing that per delta made a single streamed turn O(n^2) in transient
    // allocation (gigabytes of throwaway strings on long turns), which was the
    // main-process RSS blow-up during streaming. Past the window, discarding a
    // large fold because a delta matched its opening would be wrong anyway.
    const restart = replaceFoldWhenDeltaRestartsOpeningPhrase(fold, delta);
    if (restart != null) return restart;

    if (deltaAlreadyInFold(fold, delta)) return fold;
    const overlap = maxSuffixPrefixOverlap(fold, delta);
    const merged = overlap > 0 ? fold + delta.slice(overlap) : fold + delta;
    return repairStreamFoldText(merged);
  }

  // Large fold: the remaining de-dup / overlap / phrase-restart repairs only matter at the
  // seam, so operate on a bounded tail window and stitch the untouched (already-repaired)
  // prefix back on. This is what makes streaming append O(1) amortized instead of O(n).
  const splitAt = fold.length - STREAM_FOLD_REPAIR_WINDOW;
  const head = fold.slice(0, splitAt);
  const tail = fold.slice(splitAt);

  if (deltaAlreadyInFold(tail, delta)) return fold;
  const overlap = maxSuffixPrefixOverlap(tail, delta);
  const mergedTail = overlap > 0 ? tail + delta.slice(overlap) : tail + delta;
  return head + repairStreamFoldText(mergedTail);
}

/**
 * Remove a trailing duplicate when the fold was built as emitted + d but d was already in emitted.
 * Defense-in-depth for empty-transcript streaming before session persistence catches up.
 */
export function dedupeRedundantStreamFold(fold: string): string {
  if (!fold) return fold;
  const minDelta = MIN_REDUNDANT_STREAM_RUN;
  // Longest suffix first: a short trailing fragment can appear inside a longer prefix and
  // would be matched incorrectly if we scanned split points from the end.
  for (let len = fold.length - minDelta; len >= minDelta; len--) {
    const prefix = fold.slice(0, fold.length - len);
    const suffix = fold.slice(fold.length - len);
    if (suffixAlreadyInPrefix(prefix, suffix)) {
      return repairStreamFoldText(prefix);
    }
  }
  return repairStreamFoldText(fold);
}
