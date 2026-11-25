/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * What a send does right now, and what to call it on the button.
 *
 * Two behaviors (the `behavior` argument of useLlmdChatDriver's `send`):
 *   - `'queue'`: run after whatever is running or already queued. llmd decides
 *     whether that means "now" (idle session) or "later".
 *   - `'interrupt'`: run now, stopping a running turn first.
 *
 * Which one plain Enter / the button gives depends on the queue:
 *   - queue running (the normal case): Enter QUEUES; holding ⌥/Ctrl/⌘ interrupts.
 *   - queue paused: Enter SENDS NOW (the paused entries are being held on
 *     purpose; a fresh message means "do this now"); holding a modifier adds it
 *     to the paused queue instead.
 * A host can pin plain Enter with `defaultSendBehavior`; the modifier then
 * always gives the other one.
 *
 * Labels say what will actually happen: "Send" when it runs immediately,
 * "Queue" when it will wait, "Interrupt" when it stops a running turn.
 */

import { useCallback, useMemo, useState, type FocusEvent, type KeyboardEvent } from 'react';

export type SendBehavior = 'queue' | 'interrupt';

export type SendModeLabel = 'Send' | 'Queue' | 'Interrupt';

export type SendModeContext = {
  /** A turn is running (the driver's `streaming`). */
  streaming: boolean;
  /** The session queue is paused (the driver's `queuePaused`). */
  queuePaused?: boolean;
  /** Entries waiting in the session queue (the driver's `queue.length`). */
  queueLength?: number;
  /** Pin what plain Enter does; omitted = follow the queue (see module doc). */
  defaultSendBehavior?: SendBehavior;
};

/** True for the keys that flip a send to its alternate: ⌥ (Alt), Ctrl, ⌘ (Meta). */
export function isAlternateSend(e: { altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean }): boolean {
  return !!(e.altKey || e.ctrlKey || e.metaKey);
}

/** The behavior a send gets, plain (`alternate` false) or with a modifier held. */
export function sendBehaviorFor(ctx: SendModeContext, alternate: boolean): SendBehavior {
  const plain: SendBehavior = ctx.defaultSendBehavior ?? (ctx.queuePaused ? 'interrupt' : 'queue');
  if (!alternate) return plain;
  return plain === 'queue' ? 'interrupt' : 'queue';
}

/** What a send with `behavior` will do right now, as a button label. */
export function sendLabelFor(behavior: SendBehavior, ctx: SendModeContext): SendModeLabel {
  if (behavior === 'interrupt') return ctx.streaming ? 'Interrupt' : 'Send';
  const waits = ctx.streaming || !!ctx.queuePaused || (ctx.queueLength ?? 0) > 0;
  return waits ? 'Queue' : 'Send';
}

/** Placeholder hint for the input: what Enter and modifier+Enter do. */
export function sendHintFor(ctx: SendModeContext): string {
  const verb = (b: SendBehavior) => sendLabelFor(b, ctx).toLowerCase();
  return `Enter to ${verb(sendBehaviorFor(ctx, false))} · ⌥/Ctrl+Enter to ${verb(sendBehaviorFor(ctx, true))}`;
}

export type SendMode = {
  /** True while ⌥/Ctrl/⌘ is held in the element the key handlers are attached to. */
  alternateHeld: boolean;
  /** The behavior the button / Enter gives right now (follows `alternateHeld`). */
  behavior: SendBehavior;
  /** Label for the send button right now (follows `alternateHeld`). */
  label: SendModeLabel;
  /** The behavior for an explicit alternate flag (e.g. from a keydown event). */
  behaviorFor: (alternate: boolean) => SendBehavior;
  /** Placeholder hint describing both keys. */
  hint: string;
  /**
   * Attach to the text input (or any element containing it) so holding a
   * modifier flips the button live. Blur resets it, so a key released
   * elsewhere never leaves the button stuck.
   */
  keyHandlers: {
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => void;
    onKeyUp: (e: KeyboardEvent<HTMLElement>) => void;
    onBlur: (e: FocusEvent<HTMLElement>) => void;
  };
};

/** Live send mode for a composer: behavior + label, flipping while a modifier is held. */
export function useSendMode(ctx: SendModeContext): SendMode {
  const [alternateHeld, setAlternateHeld] = useState(false);
  const track = useCallback((e: KeyboardEvent<HTMLElement>) => setAlternateHeld(isAlternateSend(e)), []);
  const reset = useCallback(() => setAlternateHeld(false), []);
  const { streaming, queuePaused, queueLength, defaultSendBehavior } = ctx;
  return useMemo(() => {
    const c: SendModeContext = { streaming, queuePaused, queueLength, defaultSendBehavior };
    const behavior = sendBehaviorFor(c, alternateHeld);
    return {
      alternateHeld,
      behavior,
      label: sendLabelFor(behavior, c),
      behaviorFor: (alternate: boolean) => sendBehaviorFor(c, alternate),
      hint: sendHintFor(c),
      keyHandlers: { onKeyDown: track, onKeyUp: track, onBlur: reset },
    };
  }, [alternateHeld, streaming, queuePaused, queueLength, defaultSendBehavior, track, reset]);
}
