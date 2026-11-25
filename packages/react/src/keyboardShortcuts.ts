/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** True when the event uses Cmd (macOS) or Ctrl (Windows/Linux). */
export function isModKey(e: KeyboardEvent | React.KeyboardEvent): boolean {
  return e.metaKey || e.ctrlKey;
}

/** Skip app shortcuts while the user is typing in a form field. */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (target.isContentEditable) return true;
  return false;
}
