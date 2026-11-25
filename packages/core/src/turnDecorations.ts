/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Typed decoder for llmd turn decorations (llmd_turn_decorations →
 * ListTurnDecorations RPC). Decorations are passive, out-of-band pointers
 * attached to a turn (joined by turnSeq) or a tool result (joined by
 * toolCallId). llmd stores kind + uri verbatim and never interprets either —
 * all data (confidence, labels, counts, …) lives at whatever uri resolves to,
 * owned by the emitting system. This module does no domain interpretation;
 * it's a thin, typed wire-shape wrapper so callers don't hand-roll field
 * access, and a place to normalize numeric/bigint wire quirks.
 */

export interface TurnDecorationView {
  turnSeq: number;
  turnId: string;
  target: 'turn' | 'tool_result';
  toolCallId: string;
  /** Discriminator the emitter owns (e.g. "graph_provenance", "entity_reference"). */
  kind: string;
  /** Opaque pointer to the emitter-owned data this decoration references. */
  uri: string;
  createdAtUnix: number;
}

/** Wire shape from the llmd TS client (ts-proto camelCase). */
export interface TurnDecorationWire {
  turnSeq?: number | string | bigint;
  turnId?: string;
  target?: string;
  toolCallId?: string;
  kind?: string;
  uri?: string;
  createdAtUnix?: number | string | bigint;
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function num(v: unknown): number {
  const n = Number(v ?? 0);
  return Number.isFinite(n) ? n : 0;
}

export function decodeTurnDecoration(wire: TurnDecorationWire): TurnDecorationView {
  return {
    turnSeq: num(wire.turnSeq),
    turnId: str(wire.turnId),
    target: wire.target === 'turn' ? 'turn' : 'tool_result',
    toolCallId: str(wire.toolCallId),
    kind: str(wire.kind),
    uri: str(wire.uri),
    createdAtUnix: num(wire.createdAtUnix),
  };
}

/**
 * Groups decoded decorations for transcript joins.
 *
 * Prefer keying decorations off `Message.id` where you can. `turnSeq` and
 * `turnId` exist only on the live conversation event stream — `Message` (see
 * proto/llmv1/llm.proto) persists neither — so a decoration joined by
 * `byTurnSeq` resolves until the page reloads and then silently attaches to
 * nothing. `@pyxis-llm/react`'s `ChatPanel` takes `decorationsByMessageId` for this
 * reason, and `ChatMessage` stamps `data-message-id` so a host can address a
 * message from the DOM as well.
 *
 * `byTurnSeq` is kept for consumers joining against a live stream they already
 * hold in memory, where turn identity is still valid.
 */
export function groupTurnDecorations(all: TurnDecorationView[]): {
  byToolCallId: Map<string, TurnDecorationView[]>;
  byTurnSeq: Map<number, TurnDecorationView[]>;
} {
  const byToolCallId = new Map<string, TurnDecorationView[]>();
  const byTurnSeq = new Map<number, TurnDecorationView[]>();
  for (const d of all) {
    if (d.target === 'tool_result' && d.toolCallId) {
      const list = byToolCallId.get(d.toolCallId) ?? [];
      list.push(d);
      byToolCallId.set(d.toolCallId, list);
    } else if (d.turnSeq > 0) {
      const list = byTurnSeq.get(d.turnSeq) ?? [];
      list.push(d);
      byTurnSeq.set(d.turnSeq, list);
    }
  }
  return { byToolCallId, byTurnSeq };
}
