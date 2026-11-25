/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * How long the current turn has been running — and, once it finishes, how long
 * it took (kept until the next turn starts).
 *
 * Three layers, use whichever fits:
 *   - `useTurnTimer(streaming, turnStartedAt?)` — just the state; render it
 *     however you like.
 *   - `<TurnTimer streaming={…} />` — the default look, placeable anywhere
 *     (header, toolbar, tab title) and styleable via className/style/format.
 *   - `ChatPanel` shows `<TurnTimer>` in its turn bar by default; override what
 *     it draws there with `renderTurnTimer`, or hide it with
 *     `showTurnTimer={false}` and place your own.
 *
 * Pass `driver.streaming` and `driver.turnTiming` (from useLlmdChatDriver):
 * the driver tracks timing per session from llmd's own turn-start timestamp,
 * so the clock is right across reloads, rejoins and session switches. Without
 * `turnTiming` the timer falls back to timing from when `streaming` flipped,
 * which is only correct for a single session that stays on screen.
 */

import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Text } from '@mantine/core';

/** `9s`, `1m 05s`, `1h 02m`. */
export function formatTurnDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`;
  if (m > 0) return `${m}m ${String(s).padStart(2, '0')}s`;
  return `${s}s`;
}

/** Per-session turn timing, as kept by useLlmdChatDriver (`driver.turnTiming`). */
export type TurnTiming = {
  /** The user message that started the turn, when known. */
  userMessageId?: string;
  /** Epoch ms the turn started (llmd's timestamp when available). */
  startedAt?: number;
  /** Set once the turn has ended: how long it took. */
  durationMs?: number;
};

export type TurnTimerState = {
  /** Elapsed ms of the running turn, or the last turn's final duration. Undefined before any turn. */
  elapsedMs?: number;
  /** True while the turn is running (the value is ticking). */
  running: boolean;
  /** Ready-made text: `"42s"` while running, `"took 42s"` once finished; `""` before any turn. */
  label: string;
};

/**
 * Ticks once a second while `streaming`, then freezes at the final duration
 * until the next turn starts.
 *
 * Pass a {@link TurnTiming} (`driver.turnTiming`) as the second argument and
 * the value is derived from it entirely: nothing is remembered in the
 * component, so it is always the timing of the session being shown.
 *
 * Legacy: a number (ms since epoch) overrides the start time; with neither,
 * the turn is timed from when `streaming` turned true.
 */
export function useTurnTimer(streaming: boolean, turnStartedAt?: number | TurnTiming): TurnTimerState {
  const timing = typeof turnStartedAt === 'object' ? turnStartedAt : undefined;
  const legacyStart = typeof turnStartedAt === 'number' ? turnStartedAt : undefined;
  const controlled = timing !== undefined;

  // Controlled: a 1s clock while the turn runs; everything else is derived.
  const liveStart =
    controlled && streaming && timing.durationMs === undefined ? timing.startedAt : undefined;
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    if (liveStart === undefined) return;
    setNowMs(Date.now());
    const id = setInterval(() => setNowMs(Date.now()), 1_000);
    return () => clearInterval(id);
  }, [liveStart]);

  // Legacy (uncontrolled) state.
  const startRef = useRef<number | undefined>(undefined);
  const [legacyElapsedMs, setElapsedMs] = useState<number | undefined>(undefined);

  useEffect(() => {
    if (controlled) return;
    if (!streaming) {
      if (startRef.current !== undefined) {
        setElapsedMs(Date.now() - startRef.current);
        startRef.current = undefined;
      }
      return;
    }
    startRef.current = legacyStart ?? startRef.current ?? Date.now();
    const tick = () => {
      if (startRef.current !== undefined) setElapsedMs(Date.now() - startRef.current);
    };
    tick();
    const id = setInterval(tick, 1_000);
    return () => clearInterval(id);
  }, [controlled, streaming, legacyStart]);

  let elapsedMs: number | undefined;
  if (!controlled) {
    elapsedMs = legacyElapsedMs;
  } else if (liveStart !== undefined) {
    // `nowMs` only drives the 1s re-render; read the clock here so the first
    // render after a switch is not computed from a stale tick. Clamped: the
    // start is llmd's clock, `now` is this machine's.
    elapsedMs = Math.max(0, Math.max(nowMs, Date.now()) - liveStart);
  } else if (!streaming) {
    elapsedMs = timing.durationMs;
  }
  // (Controlled + streaming with no live start: the new turn's timing has not
  // landed yet — show nothing for that one render rather than a stale value.)

  const label =
    elapsedMs === undefined ? '' : `${streaming ? '' : 'took '}${formatTurnDuration(elapsedMs)}`;
  return { elapsedMs, running: streaming, label };
}

export type TurnTimerProps = {
  /** Whether a turn is running (e.g. `driver.streaming`). */
  streaming: boolean;
  turnStartedAt?: number;
  /** Per-session timing (`driver.turnTiming`); preferred over `turnStartedAt`. */
  timing?: TurnTiming;
  /** Custom text; defaults to `state.label`. */
  format?: (state: TurnTimerState) => ReactNode;
  className?: string;
  style?: CSSProperties;
};

/** The default timer. Renders nothing before the first turn. */
export function TurnTimer({ streaming, turnStartedAt, timing, format, className, style }: TurnTimerProps) {
  const state = useTurnTimer(streaming, timing ?? turnStartedAt);
  return <TurnTimerView state={state} format={format} className={className} style={style} />;
}

/** Presentational half of {@link TurnTimer}, for when the state is computed elsewhere. */
export function TurnTimerView({
  state,
  format,
  className,
  style,
}: {
  state: TurnTimerState;
  format?: (state: TurnTimerState) => ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  if (state.elapsedMs === undefined) return null;
  return (
    <Text
      className={['llmd-turn-timer', className].filter(Boolean).join(' ')}
      data-testid="llmd-turn-timer"
      data-running={state.running ? 'true' : 'false'}
      size="xs"
      c="dimmed"
      style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', flexShrink: 0, ...style }}
      title={state.running ? 'Time this turn has been running' : 'How long the last turn took'}
    >
      {format ? format(state) : state.label}
    </Text>
  );
}
