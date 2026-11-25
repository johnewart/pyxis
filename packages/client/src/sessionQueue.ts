/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Per-session composer drafts and the message queue.
 *
 * llmd owns this state but never drains the queue. Drainers are ordinary
 * clients — a background daemon that keeps working while nobody is watching, a
 * UI while it is open — and several can be alive at once, so llmd's job is the
 * state plus the guarantees that make competing drainers safe: claims are
 * atomic, they expire, pause is enforced server-side, and an entry that already
 * started a turn is never handed out twice.
 *
 * The drain loop itself is deliberately NOT here: it belongs to whoever is
 * draining (see `DrainStep` for the contract it has to honour).
 */

/** Lifecycle of one queued message. */
export type QueuedMessageState = "pending" | "running";

/** One user message waiting its turn on a session. */
export interface QueuedMessage {
  /**
   * Queue entry id — what remove/reorder address. NOT a message id: the message
   * id is minted only when the entry is finally sent as a turn.
   */
  id: string;
  content: string;
  state: QueuedMessageState;
  /** The drainer running this entry; empty while pending. */
  claimOwner: string;
  /**
   * When this claim stops being believed, in ms since the epoch. Lets a UI show
   * a stalled entry rather than one eternally in progress. Undefined while
   * pending.
   */
  claimExpiresAtMs?: number;
  createdAtMs: number;
  /** Attachments the message will carry when it is sent (empty when none). */
  attachments: QueuedMessageAttachment[];
}

/**
 * An attachment carried by a queued message (mirrors the wire
 * MessageAttachment). `content` is provider-facing: llmd splices it into the
 * user message the entry becomes; `payloadJson` is client-only.
 */
export interface QueuedMessageAttachment {
  id: string;
  kind: string;
  payloadJson: string;
  afterToolCallId?: string;
  content?: string;
}

/** A session's queue plus whether draining is paused. */
export interface SessionQueue {
  queue: QueuedMessage[];
  paused: boolean;
}

/**
 * Outcome of asking for the next entry to run.
 *
 * The three cases are deliberately distinct. `entry` set means work to do.
 * `paused` means a deliberate stop — a loop must keep polling rather than
 * conclude it is finished. Neither set means nothing claimable right now: the
 * queue is empty, or its head is already running under someone else's live
 * claim.
 */
export interface ClaimResult {
  entry?: QueuedMessage;
  paused: boolean;
}

/*
 * THE DRAINER CONTRACT
 *
 * What a drainer must do for each claimed entry, in order. Written down because
 * getting the order wrong is what turns a queue into a duplicate-message
 * generator:
 *
 * 1. `claimNextQueuedMessage(owner)` — exactly one drainer wins an entry.
 * 2. `markQueuedMessageStarted(entryId, owner, turnId)` as soon as the turn id
 *    is known and BEFORE the turn finishes. If the drainer dies after this,
 *    whoever reclaims can see the message was already delivered and retires it
 *    instead of sending it again.
 * 3. `heartbeatQueuedMessage(entryId, owner)` while the turn is genuinely still
 *    streaming — the only honest evidence the drainer is alive. Stop
 *    heartbeating and the claim lapses, and the entry becomes reclaimable.
 * 4. `retireQueuedMessage(entryId, owner)` once the turn has committed.
 *
 * On clean shutdown, `releaseQueuedMessage` hands an unstarted entry back at its
 * original position instead of stalling the queue until the lease expires.
 *
 * A `FailedPrecondition` from steps 2–4 means the lease was lost and another
 * drainer now owns the entry: abandon it rather than retrying.
 */
