/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives a `ChatPanel` from any llmd client satisfying {@link ChatStreamCapableClient}
 * — the reference implementation of the `messages`/`streaming`/`send`/`stop`
 * contract `ChatPanel` expects. Transport-agnostic: it depends only on the
 * message/event value types and the minimal `chatStreamEvents` shape, which
 * `@pyxis-llm/client`'s `LlmdClient` satisfies on EITHER transport. This package's
 * `./node` and `./browser` entry points both re-export this same hook under the
 * name `useLlmdChatDriver`: it is the same hook over the same client type, and
 * the transport is decided by which binding built the client.
 *
 * Usage (Node, native gRPC):
 *
 *   import { createLlmdClient } from '@pyxis-llm/client/node';
 *   import { useLlmdChatDriver } from '@pyxis-llm/react/node';
 *   import { ChatPanel } from '@pyxis-llm/react';
 *
 *   const client = useMemo(() => createLlmdClient({ address, tenantId, scopeId, sessionId }), [...]);
 *   const driver = useLlmdChatDriver(client);
 *   <ChatPanel messages={driver.messages} onSend={driver.send} streaming={driver.streaming}
 *              onStop={driver.stop} streamingMessageId={driver.streamingMessageId} />
 *
 * Usage (browser/fetch, grpc-web against llmd's token-guarded Connect listener):
 *
 *   import { createLlmdWebClient } from '@pyxis-llm/client/web';
 *   import { useLlmdChatDriver } from '@pyxis-llm/react/browser';
 *   import { ChatPanel } from '@pyxis-llm/react';
 *
 *   const client = useMemo(() => createLlmdWebClient({ baseUrl, tenantId, scopeId, sessionId, token }), [...]);
 *   const driver = useLlmdChatDriver(client);
 *   <ChatPanel messages={driver.messages} onSend={driver.send} streaming={driver.streaming}
 *              onStop={driver.stop} streamingMessageId={driver.streamingMessageId} />
 *
 * A host that can't import either client directly in the process rendering
 * ChatPanel (typical Electron renderer with contextIsolation, a browser tab
 * talking to a backend that itself proxies llmd) does not use this hook at
 * all — it implements the same four-field shape itself, backed by
 * IPC/WebSocket/REST to wherever a concrete client actually runs. `ChatPanel`
 * never needs to know the difference.
 *
 * IMPORTANT — session history contract: llmd is the single source of truth
 * for a persisted session's history. `ChatStream` ALWAYS prepends the
 * session's persisted messages to whatever `messages` the client sends
 * (`internal/server/server.go`'s `ChatStream`: `full := histMsgs ++
 * incoming`) — every `ChatStream` call persists, unconditionally. So this
 * driver sends ONLY the new user message each turn, never a client-side
 * replay of prior turns; resending prior turns would double them up
 * server-side (llmd has no dedup). This mirrors how a host app uses
 * @pyxis-llm/client (see its `memoryChatHistoryStore`: "turn-scoped buffer,
 * never pre-loaded with prior history... LLMD is the single source of
 * truth"). `messages`/`setMessages`/`hydrate` below are purely about what
 * THIS driver instance renders locally — never about what gets sent to
 * llmd next turn.
 *
 * Turn lifetime: a turn runs on llmd independently of this client. Unmount,
 * navigation or reload only DETACH — the model keeps going and the reply is
 * persisted. On mount the driver asks llmd (`getSessionState`) whether a turn
 * is still running; if so it shows a streaming bubble rebuilt live from the
 * session-events subscription, and swaps in the persisted transcript once the
 * turn commits. Only `stop()` / an interrupting `send()` end a turn, via
 * `cancelTurn`.
 *
 * Mid-turn behavior:
 *   - `stop()` cancels the turn on llmd and detaches; the partial assistant bubble is
 *     finalized in place (whatever text streamed so far, plus
 *     `metadata.interrupted = true`) rather than left as a permanent empty
 *     placeholder or silently discarded.
 *   - `send()` while already streaming INTERRUPTS the current turn (same
 *     finalization as `stop()`) and immediately starts the new one — this is
 *     "type a correction and hit send" support, not a queue. A generation
 *     counter guards against the aborted turn's async cleanup clobbering the
 *     new turn's state once it (asynchronously) unwinds.
 *   - `<thinking>` deltas accumulate live into the streaming assistant
 *     message's `metadata.thinking` (not just after the turn completes),
 *     independent of the visible-text delta stream.
 *
 * Tool events (opt-in via `onToolEvent`):
 *   `ToolCallStreamStarted`/`ToolCallArgsDelta`/`ToolCallStreamFinished`/
 *   `ToolExecutionStarted`/`ToolExecutionFinished` are NOT accumulated into
 *   this driver's state or folded into `messages` — the intermediate
 *   tool-call turn still isn't persisted as its own message (see
 *   `toolInvocation.e2e.test.tsx`). Pass a third `onToolEvent` argument to
 *   receive them as they arrive; what to do with them (a "running
 *   tool-calls" list, a timeline, a toast) is entirely up to the host,
 *   since that rendering choice varies:
 *
 *   const driver = useLlmdChatDriver(client, options, (ev) => {
 *     if (ev.type === ConversationEventType.ToolExecutionStarted) { ... }
 *   });
 *
 * Side-channel subscription (automatic when the client supports it):
 *   This channel carries EVERY frame of every turn on the session — text and
 *   thinking deltas, tool calls, turn started/finished/aborted, status, title,
 *   todos, queue — served from llmd's seq-ordered event log. It is what
 *   rebuilds a turn this driver rejoins after a reload. It is kept up for the
 *   component's lifetime and reconnects with backoff if it errors or is
 *   closed (e.g. by a proxy); `sessionEventsError` reports while it is down.
 *   `chatStreamEvents`' own stream only carries frames sourced from the
 *   specific turn it's driving. For CLI-backed providers (Claude Code,
 *   Cursor CLI), MCP-dispatched tool calls run in a separate request
 *   concurrent with that turn and never reach it (see
 *   internal/mcpserver/server.go's handleToolsCall /
 *   internal/serve/session_events_ws.go). If `client` also implements
 *   {@link SessionEventsSubscribableClient} (`LlmdClient` does on both
 *   transports — the SubscribeSessionEvents RPC on node, a WebSocket on web;
 *   see @pyxis-llm/client's web.ts for why that difference is deliberate), this hook
 *   opens that subscription itself, for the component's whole lifetime
 *   (not scoped to a single `send()` turn), and routes its events through
 *   the exact same handling as the primary stream: tool events to
 *   `onToolEvent`, session status/title/todos into this driver's own
 *   state. No extra wiring required — a host on `@pyxis-llm/client` gets live
 *   tool-execution visibility for CLI-backed sessions for free. A client that only implements
 *   {@link ChatStreamCapableClient} (e.g. a hand-rolled IPC/REST shim) just
 *   won't get this — `onToolEvent` still fires normally for whatever the
 *   primary stream itself carries.
 */

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  ConversationEventType,
  UserMessage,
  type AssistantMessage,
  type ConversationEvent,
  type Message,
  type QueuedMessage,
  type SessionQueue,
  type SessionTodoItem,
  type SessionTurnTiming,
} from './vendor/llmd-types/index.js';
import type { ChatMessageData, ChatMessageSegment } from './ChatMessage.js';
import type { TurnTiming } from './TurnTimer.js';
import { createDraftStore, type DraftStore } from './draftStore.js';
import type { AskCardSubmitOptions } from './AskCardView.js';
import {
  ASK_CARD_RESPONSE_KIND,
  buildAskCardResponseAttachment,
  decodeAskCard,
  encodeAskCardResponse,
  indexAskCardResponses,
  llmdMessageAttachmentFromRowContent,
  mergeLiveAttachments,
  type AskCardResponse,
  type LlmdMessageAttachment,
} from '@pyxis-llm/core';

/**
 * Minimal transport-agnostic surface this driver needs. `@pyxis-llm/client`'s
 * `LlmdClient` satisfies this structurally on either transport binding
 * (`@pyxis-llm/client/node`, `@pyxis-llm/client/web`) — the driver only ever calls
 * `chatStreamEvents`.
 */
export interface ChatStreamCapableClient<TOptions = unknown> {
  chatStreamEvents(
    messages: Message[],
    options?: TOptions
  ): AsyncGenerator<
    | { kind: 'event'; event: ConversationEvent }
    | { kind: 'complete'; message: AssistantMessage }
  >;
}

/**
 * Optional capability a {@link ChatStreamCapableClient} may also implement:
 * the live side-channel described in this module's doc comment.
 * `@pyxis-llm/client`'s `LlmdClient` implements this on both transports — the
 * `SubscribeSessionEvents` RPC via `@pyxis-llm/client/node`, the WebSocket
 * `/ws/session-events` feed via `@pyxis-llm/client/web`; a client that doesn't just
 * won't get the automatic subscription.
 */
export interface SessionEventsSubscribableClient {
  subscribeSessionEvents(options?: {
    afterSeq?: number;
    signal?: AbortSignal;
  }): AsyncGenerator<ConversationEvent>;
}

function supportsSessionEventsSubscription(
  client: unknown
): client is SessionEventsSubscribableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionEventsSubscribableClient).subscribeSessionEvents === 'function'
  );
}

/**
 * Optional capability: explicit turn cancellation. A turn runs server-side
 * independently of any client stream — aborting `chatStreamEvents` only
 * detaches this client (navigation, reload and unmount must NOT stop the
 * model). `cancelTurn` is the actual Stop. Without it, `stop()` can only
 * detach and the turn runs to completion.
 */
export interface TurnControlCapableClient {
  cancelTurn(reason?: string): Promise<boolean>;
}

function supportsTurnControl(client: unknown): client is TurnControlCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as TurnControlCapableClient).cancelTurn === 'function'
  );
}

/**
 * Optional capability: session state for rejoining. Tells the driver on mount
 * whether a turn is still running server-side (the user left mid-reply and came
 * back) and the event-log cursors to subscribe from, so the running reply can be
 * rebuilt live and nothing older is replayed.
 */
export interface SessionStateCapableClient {
  getSessionState(): Promise<{
    messages: Message[];
    turnInProgress: boolean;
    eventsSeq: number;
    turnStartSeq: number;
  } & SessionTurnTiming>;
}

function supportsSessionState(client: unknown): client is SessionStateCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionStateCapableClient).getSessionState === 'function'
  );
}

/**
 * Optional capability: the rejoin status of {@link SessionStateCapableClient}
 * without the transcript. When present the driver uses it to pick its
 * subscription cursor and to poll a rejoined turn, and only falls back to the
 * (much heavier) getSessionState when it actually has to replace the
 * displayed transcript.
 */
export interface SessionStatusCapableClient {
  getSessionStatus(): Promise<{
    turnInProgress: boolean;
    eventsSeq: number;
    turnStartSeq: number;
  } & SessionTurnTiming>;
}

function supportsSessionStatus(client: unknown): client is SessionStatusCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionStatusCapableClient).getSessionStatus === 'function'
  );
}

/**
 * Optional capability: the session's persisted title and todo list. The
 * session-events channel only carries CHANGES to these, so without a snapshot a
 * freshly opened (or switched-to) session would show no title and no todos until
 * the agent next touched them.
 */
export interface SessionInfoCapableClient {
  /** `status`: the current turn's set_status headline, "" when none (optional for older clients). */
  getSessionInfo(): Promise<{ title: string; todos: SessionTodoItem[]; status?: string }>;
}

function supportsSessionInfo(client: unknown): client is SessionInfoCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionInfoCapableClient).getSessionInfo === 'function'
  );
}

/**
 * Optional capability: the session's persisted attachments (ask cards and
 * their answers) and a way to write one. With it the driver owns the whole
 * card lifecycle — load, live updates, answering — and exposes a ready-made
 * `attachmentIndex`; without it (an older client) cards only arrive live.
 */
export interface SessionAttachmentsCapableClient {
  getSessionAttachmentRows(): Promise<Array<{ content: string; toolCallId: string }>>;
  upsertSessionAttachment(
    anchorMessageId: string,
    attachment: { id: string; kind: string; payloadJson: string; afterToolCallId?: string; content?: string }
  ): Promise<void>;
}

function supportsSessionAttachments(client: unknown): client is SessionAttachmentsCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionAttachmentsCapableClient).getSessionAttachmentRows === 'function' &&
    typeof (client as SessionAttachmentsCapableClient).upsertSessionAttachment === 'function'
  );
}

/**
 * Optional capability: deleting a session attachment (DeleteSessionAttachment).
 * Needed to dismiss an ask card; without it `dismissAskCard` reports an error.
 */
export interface AttachmentDeleteCapableClient {
  deleteSessionAttachment(attachmentId: string): Promise<void>;
}

function supportsAttachmentDelete(client: unknown): client is AttachmentDeleteCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as AttachmentDeleteCapableClient).deleteSessionAttachment === 'function'
  );
}

/**
 * Optional capability: llmd's AnswerAskCard RPC. With it an answer is queued at
 * the front of the session queue (so the agent resumes on it) and re-answers
 * update in place; without it (an older client/server) the driver falls back
 * to writing the answer attachment directly, which records it but does not
 * resume the agent.
 */
export interface AskCardAnswerCapableClient {
  answerAskCard(
    cardId: string,
    responseJson: string,
    opts?: { continueWithTools?: boolean }
  ): Promise<{ entry?: QueuedMessage; updatedExisting: boolean }>;
}

function supportsAnswerAskCard(client: unknown): client is AskCardAnswerCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as AskCardAnswerCapableClient).answerAskCard === 'function'
  );
}

/** An attachment as loaded from a row: also remembers the message it hangs off. */
type LoadedAttachment = LlmdMessageAttachment & { anchorMessageId?: string };

/**
 * Optional capability: a stable key for the session the client addresses.
 * Lets the driver reset only when the SESSION changes, not whenever a host
 * rebuilds an equivalent client object (e.g. creating it during render without
 * useMemo). Without it the driver falls back to object identity.
 */
export interface SessionKeyCapableClient {
  sessionKey(): string;
}

function sessionIdentity(client: unknown): unknown {
  if (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionKeyCapableClient).sessionKey === 'function'
  ) {
    return (client as SessionKeyCapableClient).sessionKey();
  }
  return client;
}

/** How often to re-check whether a rejoined turn has committed after its last frame. */
const REJOIN_SETTLE_POLL_MS = 300;
/** Give up waiting for a rejoined turn to commit after this many checks (~30s). */
const REJOIN_SETTLE_MAX_POLLS = 100;
/** While a rejoined turn is showing, check this often whether it has finished (safety net). */
const REJOIN_WATCH_MS = 3_000;
/** Session-events reconnect backoff: first retry delay, doubling up to the max. */
const SUBSCRIBE_RETRY_BASE_MS = 500;
const SUBSCRIBE_RETRY_MAX_MS = 10_000;

/**
 * Optional capability: the per-session composer draft and message queue.
 *
 * Feature-detected rather than required, the same way the session-events side
 * channel is — an older `@pyxis-llm/client` build simply has no draft persistence and
 * no queue, and the driver degrades to purely local composer text instead of
 * failing to mount.
 */
export interface SessionQueueCapableClient {
  saveDraft(content: string): Promise<void>;
  getDraft(): Promise<string>;
  clearDraft(): Promise<void>;
  enqueueMessage(content: string, attachments?: OutgoingAttachment[]): Promise<QueuedMessage>;
  listQueue(): Promise<SessionQueue>;
  removeQueuedMessage(entryId: string): Promise<boolean>;
  reorderQueue(entryIds: string[]): Promise<QueuedMessage[]>;
  setQueuePaused(paused: boolean): Promise<void>;
}

function supportsSessionQueue(client: unknown): client is SessionQueueCapableClient {
  return (
    typeof client === 'object' &&
    client !== null &&
    typeof (client as SessionQueueCapableClient).enqueueMessage === 'function' &&
    typeof (client as SessionQueueCapableClient).saveDraft === 'function'
  );
}

/** How long to wait after the last keystroke before persisting a draft. */
const DRAFT_SAVE_DEBOUNCE_MS = 400;

/**
 * Options passed through to a `ChatStreamCapableClient`'s `chatStreamEvents`;
 * always includes `signal`. Deliberately has no index signature — concrete
 * option types like `LlmdClient`'s `LLMOptions` are plain interfaces without
 * one, and TypeScript
 * only allows a type to satisfy `extends Record<string, unknown>` if it
 * itself declares an index signature, which would break generic inference
 * for `TOptions` at every call site.
 */
export type LlmdChatDriverOptions = { signal?: AbortSignal };

/**
 * What sending does while a turn is already running: `'queue'` appends the
 * message to the session queue (it runs after the current turn), `'interrupt'`
 * stops the running turn and starts this one now. When nothing is running both
 * just send.
 */
export type SendBehavior = 'queue' | 'interrupt';

/**
 * An attachment sent ON a user message (composer attachments, ask-card
 * answers, host kinds). llmd persists it anchored to that message; `content`
 * is what the model sees (spliced into the message), `payloadJson` is
 * client-only display data.
 */
export type OutgoingAttachment = {
  id: string;
  kind: string;
  payloadJson: string;
  afterToolCallId?: string;
  content?: string;
};

/** Driver behaviour switches (the 5th `useLlmdChatDriver` argument). */
export type LlmdChatDriverConfig = {
  /**
   * Drain the session queue from this client while it is mounted: claim the
   * next entry whenever nothing is running, send it as a turn (with its
   * attachments), and retire it — following @pyxis-llm/client's drainer contract
   * (claim → markStarted → heartbeat → retire/release). Needed where llmd will
   * not drain itself: sessions with client-owned tools, or a host that runs
   * llmd with server draining disabled. Safe alongside the server drainer and
   * other windows (claims are atomic). Off by default.
   */
  drainQueue?: boolean;
  /**
   * Keep an assistant turn's text segments apart at each tool call instead of
   * concatenating them: the message carries `segments` (live, rejoined and
   * loaded from history), and ChatMessage shows the in-between ones as a
   * collapsed "N steps" list plus the final segment. Off by default.
   */
  segmentedTurns?: boolean;
};

function pushSegmentDelta(segs: ChatMessageSegment[], startNew: boolean, delta: string): ChatMessageSegment[] {
  const last = segs[segs.length - 1];
  if (startNew || !last) return [...segs, { text: delta }];
  return [...segs.slice(0, -1), { ...last, text: last.text + delta }];
}

function noteSegmentTool(segs: ChatMessageSegment[], id: string | undefined, name: string | undefined): ChatMessageSegment[] {
  const last = segs[segs.length - 1];
  if (!last) return segs;
  return [...segs.slice(0, -1), { ...last, toolCalls: [...(last.toolCalls ?? []), { id, name }] }];
}

/**
 * Merges each run of assistant/tool rows between other rows into one assistant
 * message carrying `segments` (one per assistant text). Tool rows stay, ahead
 * of the merged message. The merged row stands in for the dropped assistant
 * rows (it keeps the last one's id); its content joins the segments with
 * nothing between them, as the live path does. Runs with fewer than two text segments are unchanged.
 */
function mergeSegmentedTurns(rows: ChatMessageData[]): ChatMessageData[] {
  const out: ChatMessageData[] = [];
  let tools: ChatMessageData[] = [];
  let assistants: ChatMessageData[] = [];
  const flush = () => {
    const segs: ChatMessageSegment[] = [];
    for (const a of assistants) {
      if (a.content.trim()) segs.push({ text: a.content, ...(a.toolCalls?.length ? { toolCalls: a.toolCalls } : {}) });
      else if (a.toolCalls?.length && segs.length) {
        const last = segs[segs.length - 1];
        segs[segs.length - 1] = { ...last, toolCalls: [...(last.toolCalls ?? []), ...a.toolCalls] };
      }
    }
    if (segs.length >= 2) {
      const last = assistants[assistants.length - 1];
      out.push(...tools, { ...last, content: segs.map((s) => s.text).join(''), segments: segs });
    } else {
      // Not segmented: restore the original order.
      out.push(...tools, ...assistants);
    }
    tools = [];
    assistants = [];
  };
  for (const row of rows) {
    if (row.role === 'assistant') assistants.push(row);
    else if (row.role === 'tool') tools.push(row);
    else {
      flush();
      out.push(row);
    }
  }
  flush();
  return out;
}

/**
 * Optional capability: the client-drainer half of the queue API. Required for
 * `drainQueue`.
 */
export interface QueueDrainCapableClient {
  claimNextQueuedMessage(owner: string): Promise<{ entry?: QueuedMessage; paused: boolean }>;
  heartbeatQueuedMessage(entryId: string, owner: string): Promise<void>;
  markQueuedMessageStarted(entryId: string, owner: string, turnId: string): Promise<void>;
  retireQueuedMessage(entryId: string, owner: string): Promise<void>;
  releaseQueuedMessage(entryId: string, owner: string): Promise<void>;
}

function supportsQueueDrain(client: unknown): client is QueueDrainCapableClient {
  const c = client as Partial<QueueDrainCapableClient> | null;
  return (
    typeof c === 'object' &&
    c !== null &&
    typeof c.claimNextQueuedMessage === 'function' &&
    typeof c.heartbeatQueuedMessage === 'function' &&
    typeof c.markQueuedMessageStarted === 'function' &&
    typeof c.retireQueuedMessage === 'function' &&
    typeof c.releaseQueuedMessage === 'function'
  );
}

/** Client drainer heartbeat; well inside llmd's claim lease. */
const DRAIN_HEARTBEAT_MS = 5_000;
/** After an entry failed to start a turn, wait before claiming again (no hot loop). */
const DRAIN_RETRY_BACKOFF_MS = 3_000;

/** Hooks a caller (the client drainer) can attach to one turn started by `interrupt`. */
type TurnHooks = {
  /** User message id to send under (the drainer uses `queued_<entryId>`, matching llmd's own drainer). */
  messageId?: string;
  /** First AssistantTurnStarted of the turn. */
  onTurnStarted?: (turnId: string) => void;
  /** The turn ended for this driver (completed, failed or aborted). */
  onDone?: () => void;
  /**
   * Send with ChatStream `queueIfBusy`: llmd runs it now only if the session is
   * genuinely free, otherwise queues it. Nothing is drawn until llmd answers,
   * so a queued message never flashes up as a turn.
   */
  queueIfBusy?: boolean;
};

/** The five tool-related event types, narrowed out of `ConversationEvent` for `onToolEvent`. */
export type ToolConversationEvent = Extract<
  ConversationEvent,
  {
    type:
      | ConversationEventType.ToolCallStreamStarted
      | ConversationEventType.ToolCallArgsDelta
      | ConversationEventType.ToolCallStreamFinished
      | ConversationEventType.ToolExecutionStarted
      | ConversationEventType.ToolExecutionFinished;
  }
>;

export type LlmdChatDriver = {
  messages: ChatMessageData[];
  streaming: boolean;
  /**
   * When the current (or last) user turn on THIS session started, and once it
   * ends how long it took. Started from llmd's UserTurnCommitted timestamp, so
   * it is right after a reload, a rejoin or switching sessions; reset on every
   * session switch. Pass to `ChatPanel`'s `turnTiming` or `<TurnTimer timing>`.
   */
  turnTiming: TurnTiming;
  /** Id of the in-flight assistant message, for `ChatPanel`'s `streamingMessageId` prop. */
  streamingMessageId?: string;
  /**
   * Sends a message. While a turn is running, `behavior` decides what happens
   * (default `'queue'`): queue it behind the running turn, or interrupt. Falls
   * back to interrupting when the client has no queue support.
   */
  send: (text: string, behavior?: SendBehavior, attachments?: OutgoingAttachment[]) => void;
  /** Sends a message now, interrupting a running turn first (see module doc). */
  interrupt: (text: string) => void;
  /**
   * Pulls a not-yet-started entry out of the queue and sends it as an
   * interrupt. The entry is removed first; if the server already started it
   * (removal fails) nothing is sent, so it never runs twice.
   */
  interruptWithQueued: (entryId: string) => Promise<void>;
  /**
   * Stops the running turn: `cancelTurn` on llmd (the model stops and the
   * partial is saved as interrupted), then detaches this client's stream. Also
   * stops a turn this driver rejoined after a reload.
   */
  stop: () => void;
  /** Set the whole transcript (e.g. after loading persisted session history). Purely local/display — see module doc. */
  setMessages: (messages: ChatMessageData[]) => void;
  /** Seeds the display transcript from real `Message[]` (e.g. `client.getHistory()`) — e.g. resuming a session after a remount/reload. Purely local/display. */
  hydrate: (messages: Message[]) => void;
  error: Error | null;
  /** Latest agent-authored status headline (set_status tool), or undefined if none has been emitted this session. Turn-scoped: llmd clears it (an empty EvSessionStatus) when a turn starts and ends, and it is seeded from the session on every (re)connect, so it never shows a previous turn's headline. */
  sessionStatus?: string;
  /** Latest agent-authored session title (set_session_title tool), or undefined if never set via this driver's stream. */
  sessionTitle?: string;
  /** Current todo list (add_todo/update_todo tools), most-recently-known state. Empty until the first todos_updated event. */
  todos: SessionTodoItem[];
  /**
   * Every attachment on this session (ask cards, their answers, host kinds):
   * loaded from llmd on (re)connect and kept current from the session-events
   * channel, so a card the agent asks mid-turn — or an answer given in
   * another window — shows up without a reload. Pass straight to `ChatPanel`'s
   * `attachmentIndex`. Empty with a client lacking attachment support.
   */
  attachmentIndex: Map<string, LlmdMessageAttachment>;
  /** Ask-card answers in `attachmentIndex`, keyed by the card id they answer (`ChatPanel`'s `askCardResponses`). */
  askCardResponses: Map<string, AskCardResponse>;
  /**
   * Answers an ask card via llmd's AnswerAskCard: the first answer is queued at
   * the front of the session queue as "Answer to: <title>" (so the agent
   * resumes on it, subject to pause); a re-answer replaces the queued answer
   * or, once sent, updates it in place. The card reads as answered as soon as
   * the answer is queued. Failures are reported via `attachmentsError` and
   * rethrown. With an older client lacking `answerAskCard` it falls back to
   * writing the answer attachment directly (recorded, but the agent does not
   * resume). Wire to `ChatPanel`'s `onAskCardSubmit`.
   *
   * `options.continueWithTools` ("Answer & continue") lets the answer's turn
   * run tools; omitted or false ("Answer & stop") runs it with tools off.
   */
  submitAskCard: (response: AskCardResponse, options?: AskCardSubmitOptions) => Promise<void>;
  /**
   * Dismisses an ask card as though it was never asked: the card's attachment
   * is deleted on llmd (every window drops it via `attachment_deleted`).
   * Silent: nothing is queued and the agent is not told; it learns from
   * whatever the user says next. Removed locally at once; restored, with the
   * failure in `attachmentsError`, if the delete fails. Wire to `ChatPanel`'s
   * `onAskCardDismiss`.
   */
  dismissAskCard: (cardId: string) => Promise<void>;
  /** Set when loading attachments or saving an ask-card answer failed; cleared by the next success. */
  attachmentsError?: Error;
  /**
   * Set when the automatic session-events side-channel subscription (see
   * module doc, "Side-channel subscription") fails or disconnects —
   * distinct from `error`, which is reserved for the primary
   * `chatStreamEvents` turn. `undefined` while connected/not yet attempted.
   * A failure here means tool-execution visibility for CLI-backed sessions
   * (Claude Code, Cursor CLI) is silently degraded to nothing — worth
   * surfacing somewhere in the UI (a small "live updates unavailable"
   * indicator), not just logging, since the host has no other way to know.
   * Always also logged via `console.error` regardless of whether a host
   * reads this field.
   */
  sessionEventsError?: Error;

  /**
   * Current composer text, hydrated from llmd's persisted draft on mount.
   *
   * A store, not a string: the driver lives in the host page, and plain state
   * here would re-render that whole page on every keystroke. Read it with
   * `useDraft(driver.draft)` in the component that renders the input, or pass
   * it to `ChatPanel` as `composerDraft` (with {@link setDraft} as
   * `onComposerValueChange`) and only the composer re-renders while typing.
   * Either way an unsent message survives a reload, a crash, or closing the
   * laptop. Without that wiring the composer keeps its own local state and
   * nothing is persisted.
   */
  draft: DraftStore;
  /** Updates the composer text and persists it (debounced). */
  setDraft: (text: string) => void;

  /**
   * This session's message queue, in run order — pending and running alike.
   * Kept current from the session-events side channel, so edits made in another
   * window show up here too.
   */
  queue: QueuedMessage[];
  /** True while draining is paused. Server-enforced, so it holds for every drainer. */
  queuePaused: boolean;
  /** Appends a message to the back of the queue instead of sending it now. */
  enqueue: (text: string, attachments?: OutgoingAttachment[]) => Promise<void>;
  /** Drops a queued entry. */
  removeQueued: (entryId: string) => Promise<void>;
  /** Sets queue order to exactly these ids (pass the full order you want). */
  reorderQueue: (entryIds: string[]) => Promise<void>;
  /** Pauses or resumes draining for this session. */
  setQueuePaused: (paused: boolean) => Promise<void>;
};

function randomId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

/**
 * `finish_reason` values llmd stamps on an assistant turn that did NOT run to
 * completion (a user abort, or a turn whose context died). Anything else — and
 * the empty string — means the model finished on its own terms.
 */
const CUT_SHORT_FINISH_REASONS = new Set(['cancelled', 'timed_out']);

function wasCutShort(m: Message): boolean {
  if (m.role !== 'assistant') return false;
  // `finishReason` is declared on `AssistantMessage`, not on the base `Message`
  // interface this function is handed (role-specific fields deliberately live on
  // the concrete classes). Read it structurally rather than widening the shared
  // message shape for one consumer.
  const reason = (m as Message & { finishReason?: string }).finishReason;
  return !!reason && CUT_SHORT_FINISH_REASONS.has(reason);
}

/** Token usage and model time on a message's `metadata.usage` (see ChatMessage's usageFromMessageMetadata). */
type TurnUsageTotal = {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens?: number;
  modelDurationMs?: number;
};

/**
 * Adds one model call's usage (an AssistantTurnFinished event) to the running
 * total for a live reply bubble. That bubble shows the text of every model call
 * in the turn, so its numbers are their sum; a reloaded transcript has one
 * bubble per call, each with its own numbers.
 */
function addTurnUsage(
  total: TurnUsageTotal | undefined,
  ev: {
    usage?: {
      inputTokens?: number;
      outputTokens?: number;
      cacheReadTokens?: number;
      cacheWriteTokens?: number;
      reasoningTokens?: number;
    };
    modelDurationMs?: number;
  }
): TurnUsageTotal | undefined {
  if (!ev.usage && ev.modelDurationMs === undefined) return total;
  const t = total ?? { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 };
  const u = ev.usage ?? {};
  const sum = (a: number | undefined, b: number | undefined) =>
    a === undefined && b === undefined ? undefined : (a ?? 0) + (b ?? 0);
  const reasoningTokens = sum(t.reasoningTokens, u.reasoningTokens);
  const modelDurationMs = sum(t.modelDurationMs, ev.modelDurationMs);
  return {
    inputTokens: t.inputTokens + (u.inputTokens ?? 0),
    outputTokens: t.outputTokens + (u.outputTokens ?? 0),
    cacheReadTokens: t.cacheReadTokens + (u.cacheReadTokens ?? 0),
    cacheWriteTokens: t.cacheWriteTokens + (u.cacheWriteTokens ?? 0),
    ...(reasoningTokens !== undefined ? { reasoningTokens } : {}),
    ...(modelDurationMs !== undefined ? { modelDurationMs } : {}),
  };
}

function toChatMessageData(m: Message): ChatMessageData {
  // A reply llmd recorded as cut short has to render as interrupted after a
  // reload too, not as an answer the model completed. The live interrupt path
  // sets `metadata.interrupted` itself, but a rehydrated transcript has only
  // what was persisted, and `finish_reason` is the durable trace of it.
  const metadata = wasCutShort(m) ? { ...(m.metadata ?? {}), interrupted: true } : m.metadata;
  // Tool-call identity is kept (not rendered): it is how a card a tool
  // produced (an image, a chart) finds its place right after that tool's
  // result in a reloaded transcript. Read structurally: role-specific fields
  // live on the concrete message classes, not the base Message.
  const { toolCallId, toolCalls } = m as Message & {
    toolCallId?: string;
    toolCalls?: Array<{ id?: string; name?: string }>;
  };
  return {
    role: m.role,
    content: m.content,
    id: m.id,
    metadata,
    ...(m.role === 'tool' && toolCallId ? { toolCallId } : {}),
    ...(m.role === 'assistant' && toolCalls?.length
      ? { toolCalls: toolCalls.map((tc) => ({ id: tc.id, name: tc.name })) }
      : {}),
  };
}

/**
 * Turns any {@link ChatStreamCapableClient} into the driver shape `ChatPanel`
 * consumes. `client` may be `null`/`undefined` while a host is still setting
 * up its connection — `send` is a no-op until it's provided.
 */
export function useLlmdChatDriver<TOptions extends LlmdChatDriverOptions = LlmdChatDriverOptions>(
  client: ChatStreamCapableClient<TOptions> | null | undefined,
  options?: TOptions,
  onToolEvent?: (event: ToolConversationEvent) => void,
  /**
   * Fires once per user turn (llmd's UserTurnCommitted: one per message sent,
   * covering every model round and tool call that answers it) — for every
   * turn on the session: ones this driver sent, and ones started in another
   * window or by the queue. This is THE signal for per-turn host UI: a host
   * must clear what it accumulates from `onToolEvent` (its tool log) here.
   * (`sessionStatus` needs nothing from the host: llmd clears it itself.)
   *
   * Not AssistantTurnStarted: llmd emits that once per model call, so an
   * agent loop produces several per user turn.
   */
  onTurnStart?: (userMessageId: string) => void,
  config?: LlmdChatDriverConfig
): LlmdChatDriver {
  const [messages, setMessagesState] = useState<ChatMessageData[]>([]);
  const [streaming, setStreamingState] = useState(false);
  const [turnTiming, setTurnTiming] = useState<TurnTiming>({});
  // Every streaming change goes through here so turnTiming moves in the SAME
  // render: a provisional local start when a turn begins (the turn's
  // UserTurnCommitted replaces it with llmd's timestamp), the duration frozen
  // when it ends. Updating timing in an effect instead left one render with
  // `streaming` flipped but the previous turn's timing, which a host that
  // memoizes on `streaming` then kept indefinitely.
  const setStreaming = useCallback((next: boolean) => {
    setStreamingState(next);
    setTurnTiming((prev) => {
      const live = prev.startedAt !== undefined && prev.durationMs === undefined;
      if (next) return live ? prev : { startedAt: Date.now() };
      return live ? { ...prev, durationMs: Math.max(0, Date.now() - prev.startedAt!) } : prev;
    });
  }, []);
  // Adopt the turn timing llmd reports with the session status. This is what
  // makes a session opened (or returned to, or reloaded) show its running
  // turn's real start and how long its last turn took, rather than only what
  // this driver happened to watch. Call AFTER any setStreaming for the same
  // status, so a turn that ended is already closed off locally.
  const adoptServerTurnTiming = useCallback((st: SessionTurnTiming & { turnInProgress: boolean }) => {
    if (st.turnInProgress) {
      const at = st.turnStartedAtMs;
      if (at === undefined) return;
      setTurnTiming((prev) =>
        prev.durationMs === undefined && prev.startedAt === at
          ? prev
          : { ...(prev.durationMs === undefined ? prev : {}), startedAt: at }
      );
      return;
    }
    const last = st.lastTurn;
    if (!last) return;
    setTurnTiming((prev) => {
      // A turn this driver is running (or just started) is newer than any
      // status that says nothing is running: keep it.
      if (prev.startedAt !== undefined && prev.durationMs === undefined) return prev;
      const durationMs = Math.max(0, last.endedAtMs - last.startedAtMs);
      if (prev.startedAt === last.startedAtMs && prev.durationMs === durationMs) return prev;
      return {
        ...(prev.startedAt === last.startedAtMs && prev.userMessageId ? { userMessageId: prev.userMessageId } : {}),
        startedAt: last.startedAtMs,
        durationMs,
      };
    });
  }, []);
  const [streamingMessageId, setStreamingMessageId] = useState<string | undefined>();
  const [error, setError] = useState<Error | null>(null);
  const [sessionStatus, setSessionStatus] = useState<string | undefined>();
  const [sessionTitle, setSessionTitle] = useState<string | undefined>();
  const [todos, setTodos] = useState<SessionTodoItem[]>([]);
  // Attachments as last loaded from llmd, plus changes seen live since that
  // load (null = deleted). Kept apart so a reload can replace the snapshot and
  // drop the overlay without racing events: the load is read after the
  // subscription cursor is chosen, so anything newer is replayed on top.
  const [loadedAttachments, setLoadedAttachments] = useState<Map<string, LoadedAttachment>>(
    () => new Map()
  );
  const [liveAttachments, setLiveAttachments] = useState<ReadonlyMap<string, LlmdMessageAttachment | null>>(
    () => new Map()
  );
  const [attachmentsError, setAttachmentsError] = useState<Error | undefined>();
  const [queue, setQueue] = useState<QueuedMessage[]>([]);
  // The exported index includes ask-card answers that are still QUEUED
  // (AnswerAskCard), not only persisted ones, so a card reads as answered while
  // its answer waits (e.g. in a paused queue, or across a reload) — for hosts
  // that pass only `attachmentIndex` to ChatPanel as well as those that also
  // pass `askCardResponses`. Removing the queued entry un-answers the card, by
  // design. A persisted row with the same id wins once the answer is sent.
  const attachmentIndex = useMemo(() => {
    const persisted = mergeLiveAttachments(loadedAttachments, liveAttachments);
    let merged: Map<string, LlmdMessageAttachment> | undefined;
    for (const q of queue) {
      for (const a of q.attachments ?? []) {
        if (a.kind !== ASK_CARD_RESPONSE_KIND || persisted.has(a.id)) continue;
        merged ??= new Map(persisted);
        merged.set(a.id, a as LlmdMessageAttachment);
      }
    }
    return merged ?? persisted;
  }, [loadedAttachments, liveAttachments, queue]);
  const attachmentIndexRef = useRef(attachmentIndex);
  attachmentIndexRef.current = attachmentIndex;
  const askCardResponses = useMemo(
    () => indexAskCardResponses(attachmentIndex.values()),
    [attachmentIndex]
  );
  const [sessionEventsError, setSessionEventsError] = useState<Error | undefined>();

  // One store per driver instance; see DraftStore for why this is not state.
  const [draftStore] = useState(createDraftStore);
  const setDraftState = draftStore.set;
  const [queuePaused, setQueuePausedState] = useState(false);
  // Pending debounced draft save, so typing does not mean one RPC per keystroke.
  const draftSaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The save the pending timer will run, so a session switch can flush it to the
  // session the text was typed in rather than drop it (or send it to the new one).
  const pendingDraftSave = useRef<(() => void) | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  // Bumped on every send() call; lets a superseded (interrupted) turn's
  // async cleanup detect it's stale and avoid clobbering the new turn's
  // streaming/error state. Message-array updates targeting a specific id
  // are safe regardless (see the interrupt-finalization in the catch block
  // below) since they can't collide with a different turn's message id.
  const generationRef = useRef(0);
  // Set while this driver is showing a turn it did NOT start — one still running
  // server-side when the driver mounted (the user navigated away mid-reply and
  // came back), or one that STARTED while we watch without this driver sending
  // it (`observed`: llmd running a queued message, another window's send). Its
  // reply is rebuilt from the session-events subscription.
  //
  // An observed turn may begin before this driver has finished unwinding its
  // own previous turn (the server drainer claims the next queued message the
  // moment a turn ends), so unlike a mount-time rejoin it is not gated on
  // abortRef: everything after its UserTurnCommitted on the side channel
  // belongs to it, because llmd serializes a session's turns.
  //
  // `awaitingCommit`: the subscription was (re)opened at this turn's start, so
  // its replay begins with the turn's OWN UserTurnCommitted — which must be
  // absorbed, not mistaken for a new turn.
  const rejoinRef = useRef<{
    assistantId: string;
    text: string;
    thinking: string;
    observed?: boolean;
    awaitingCommit?: boolean;
    segments?: ChatMessageSegment[];
    segBreak?: boolean;
  } | null>(null);

  const segmentedRef = useRef(false);
  segmentedRef.current = config?.segmentedTurns === true;
  const historyToChat = useCallback((ms: Message[]): ChatMessageData[] => {
    const rows = ms.map(toChatMessageData);
    return segmentedRef.current ? mergeSegmentedTurns(rows) : rows;
  }, []);

  const rejoinPlaceholder = (r: {
    assistantId: string;
    text: string;
    thinking: string;
    segments?: ChatMessageSegment[];
  }): ChatMessageData => ({
    role: 'assistant',
    content: r.text,
    id: r.assistantId,
    ...(r.segments?.length ? { segments: r.segments } : {}),
    metadata: { streamingPlaceholder: true, ...(r.thinking ? { thinking: r.thinking } : {}) },
  });

  const setMessages = useCallback((next: ChatMessageData[]) => {
    setMessagesState(next);
  }, []);

  // This driver's own in-flight turn (started by send()), until llmd has
  // persisted it. History does not contain the turn until it commits, so a
  // hydrate that lands mid-turn must carry the local user message and the live
  // bubble over rather than wiping them.
  const ownTurnRef = useRef<{ user: ChatMessageData; assistantId: string } | null>(null);
  // Messages this driver sent with queueIfBusy and is still waiting on llmd's
  // run-or-queue answer for. If llmd runs one, the session-events channel may
  // deliver its UserTurnCommitted before our own stream does; this is how that
  // is recognised as ours rather than shown a second time as an observed turn.
  const pendingOwnIdsRef = useRef(new Set<string>());
  // Which channel owns each tool call's events. Two channels can carry the
  // same call: send()'s own stream and the session-events side channel (which
  // replays llmd's event log). Each call is owned by whichever delivers it
  // FIRST, and only the owner forwards it to onToolEvent — so every event
  // fires once, and a call's events never mix channels (args deltas have no
  // unique key; interleaving two copies would garble the arguments).
  // In practice: your own turn's in-band tools are owned by the primary stream
  // (llmd sends each frame there before logging it), so they stay in order with
  // the reply text; MCP-dispatched tools, rejoined turns and other windows'
  // turns only exist on the side channel, which owns them.
  const toolCallOwnerRef = useRef(new Map<string, 'primary' | 'side'>());
  const claimToolCall = useCallback((ev: ToolConversationEvent, channel: 'primary' | 'side'): boolean => {
    const id = 'toolCallId' in ev ? ev.toolCallId : 'id' in ev ? ev.id : '';
    if (!id) return channel === 'primary';
    const owners = toolCallOwnerRef.current;
    const owner = owners.get(id);
    if (owner) return owner === channel;
    if (owners.size > 2_000) owners.clear(); // bounded; ids are per call and never reused
    owners.set(id, channel);
    return true;
  }, []);

  // The single turn-start path, driven by UserTurnCommitted. Both channels
  // deliver it for this driver's own turns, so it is deduped by user message
  // id: each turn resets once.
  const onTurnStartRef = useRef(onTurnStart);
  onTurnStartRef.current = onTurnStart;
  const lastTurnIdRef = useRef<string | undefined>(undefined);
  const beginTurn = useCallback((userMessageId: string, committedAtMs?: number) => {
    // Timing is recorded before the dedupe: a rejoin replays a turn this driver
    // may already have begun, and its replayed commit carries the real start.
    setTurnTiming((prev) => {
      if (userMessageId && prev.userMessageId === userMessageId) {
        return committedAtMs === undefined || prev.startedAt === committedAtMs
          ? prev
          : { ...prev, startedAt: committedAtMs };
      }
      return { userMessageId, startedAt: committedAtMs ?? Date.now() };
    });
    if (userMessageId && lastTurnIdRef.current === userMessageId) return;
    lastTurnIdRef.current = userMessageId;
    onTurnStartRef.current?.(userMessageId);
  }, []);

  const hydrate = useCallback((seed: Message[]) => {
    // A host hydrates from history (typically on mount), and that can land
    // while a turn is live — one this driver started, or one it rejoined. Neither
    // is in history yet; keep both at the end instead of wiping them.
    const base = historyToChat(seed);
    setMessagesState((prev) => {
      const tail: ChatMessageData[] = [];
      const own = ownTurnRef.current;
      if (own) {
        // Between llmd persisting the turn and our stream delivering
        // `complete`, history already holds it (user + assistant at the end):
        // then history is authoritative and there is nothing to carry over.
        // llmd persists the user message at turn START, and the reply at the
        // end. So mid-turn, history already ends with our message (carry over
        // only the live bubble); once committed it ends with message + reply
        // (carry over nothing).
        const n = base.length;
        const last = base[n - 1];
        const fullyPersisted =
          n >= 2 &&
          last.role === 'assistant' &&
          base[n - 2].role === 'user' &&
          base[n - 2].content === own.user.content;
        if (!fullyPersisted) {
          const userPersisted = last?.role === 'user' && last.content === own.user.content;
          const bubble = prev.find((m) => m.id === own.assistantId) ?? {
            role: 'assistant' as const,
            content: '',
            id: own.assistantId,
            metadata: { streamingPlaceholder: true },
          };
          if (!userPersisted) tail.push(own.user);
          tail.push(bubble);
        }
      }
      const r = rejoinRef.current;
      if (r) tail.push(rejoinPlaceholder(r));
      return [...base, ...tail];
    });
  }, []);

  // A new `client` is a different session (hosts build one client per session
  // and may keep this hook mounted across a switch). Everything this driver holds
  // is per-session, so it all starts over: the old session's transcript, title,
  // status, todos, draft and queue must not show on the new one, and the old
  // session's turn must stop writing into this state. Leaving is a DETACH, as on
  // unmount — the old turn keeps running on llmd and is rejoined if the user
  // comes back. A layout effect so the reset lands before paint and before any
  // host effect hydrates the new session.
  const sessionId = sessionIdentity(client);
  // Session-scoped effects below key on `sessionId`, not `client`, and read the
  // current client from here. A host that rebuilds an equivalent client object
  // every render must not tear down and reopen the subscription (and re-fetch
  // draft/queue, whose fresh state would re-render and loop) each time.
  const clientRef = useRef(client);
  clientRef.current = client;
  const sessionIdRef = useRef(sessionId);
  useLayoutEffect(() => {
    if (sessionIdRef.current === sessionId) return;
    sessionIdRef.current = sessionId;
    generationRef.current++;
    abortRef.current?.abort();
    abortRef.current = null;
    ownTurnRef.current = null;
    rejoinRef.current = null;
    toolCallOwnerRef.current.clear();
    lastTurnIdRef.current = undefined;
    if (draftSaveTimer.current) {
      clearTimeout(draftSaveTimer.current);
      draftSaveTimer.current = null;
      pendingDraftSave.current?.();
    }
    pendingDraftSave.current = null;
    setMessagesState([]);
    setStreaming(false);
    setTurnTiming({});
    setStreamingMessageId(undefined);
    setError(null);
    setSessionStatus(undefined);
    setSessionTitle(undefined);
    setTodos([]);
    setLoadedAttachments(new Map());
    setLiveAttachments(new Map());
    setAttachmentsError(undefined);
    setSessionEventsError(undefined);
    setDraftState('');
    setQueue([]);
    setQueuePausedState(false);
  }, [sessionId]);

  // Unmount (navigation, closing the panel) DETACHES from a running turn; it
  // never stops it. Abort this driver's own stream so a gone component stops
  // consuming the turn and firing onToolEvent/setState, and bump the
  // generation so any frame already in flight is dropped. llmd keeps the turn
  // running and persists it; a remounted driver rejoins it from session state.
  useEffect(
    () => () => {
      generationRef.current++;
      abortRef.current?.abort();
      abortRef.current = null;
    },
    []
  );

  /** Fire the explicit server-side Stop; never throws (a failed cancel is logged). */
  const cancelServerTurn = useCallback(async () => {
    if (!supportsTurnControl(client)) return;
    try {
      await client.cancelTurn();
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver: cancelTurn failed — the turn may keep running server-side', err);
    }
  }, [client]);

  const stop = useCallback(() => {
    // Order matters only loosely: the cancel is what stops the model; the abort
    // just detaches this client's stream (which alone would NOT stop the turn).
    void cancelServerTurn();
    abortRef.current?.abort();
  }, [cancelServerTurn]);

  // --- draft + queue -------------------------------------------------------

  /** Reads the queue back from llmd. Used on mount and after local edits. */
  const refreshQueue = useCallback(async () => {
    if (!supportsSessionQueue(client)) return;
    try {
      const snapshot = await client.listQueue();
      setQueue(snapshot.queue);
      setQueuePausedState(snapshot.paused);
    } catch (err) {
      // Non-fatal: the queue is supplementary state, and a failure here must not
      // take down the chat surface. It is logged rather than swallowed so a
      // missing queue is diagnosable.
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver: listQueue failed', err);
    }
  }, [client]);

  const refreshQueueRef = useRef(refreshQueue);
  refreshQueueRef.current = refreshQueue;

  // Hydrate the persisted draft and queue once per session. The draft is only
  // adopted while the composer is still untouched: a slow round trip must never
  // overwrite something the user has started typing since mount.
  useEffect(() => {
    const client = clientRef.current;
    const refreshQueue = refreshQueueRef.current;
    if (!supportsSessionQueue(client)) return;
    let cancelled = false;
    void (async () => {
      try {
        const saved = await client.getDraft();
        if (!cancelled && saved) {
          setDraftState((current) => (current === '' ? saved : current));
        }
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: getDraft failed', err);
      }
      if (!cancelled) await refreshQueue();
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  const setDraft = useCallback(
    (text: string) => {
      setDraftState(text);
      if (!supportsSessionQueue(client)) return;
      if (draftSaveTimer.current) clearTimeout(draftSaveTimer.current);
      const save = () => {
        void client.saveDraft(text).catch((err) => {
          // eslint-disable-next-line no-console
          console.error('useLlmdChatDriver: saveDraft failed', err);
        });
      };
      pendingDraftSave.current = save;
      draftSaveTimer.current = setTimeout(() => {
        draftSaveTimer.current = null;
        pendingDraftSave.current = null;
        save();
      }, DRAFT_SAVE_DEBOUNCE_MS);
    },
    [client]
  );

  /**
   * Drops the draft, locally and on the server. Called when the text has become
   * a real message (sent or queued): a draft that outlived its own send would
   * reappear in the composer next to the message it turned into.
   */
  const discardDraft = useCallback(() => {
    if (draftSaveTimer.current) {
      clearTimeout(draftSaveTimer.current);
      draftSaveTimer.current = null;
    }
    pendingDraftSave.current = null;
    setDraftState('');
    if (!supportsSessionQueue(client)) return;
    void client.clearDraft().catch((err) => {
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver: clearDraft failed', err);
    });
  }, [client]);

  // On unmount (leaving the tab, closing the panel) FLUSH any in-flight debounce
  // instead of dropping it: the last keystrokes before leaving are exactly the
  // ones a remount must restore. The save is issued now, against the client it
  // was typed for, and the timer is cleared so it never fires afterwards.
  useEffect(
    () => () => {
      if (draftSaveTimer.current) {
        clearTimeout(draftSaveTimer.current);
        draftSaveTimer.current = null;
        pendingDraftSave.current?.();
      }
      pendingDraftSave.current = null;
    },
    []
  );

  const enqueue = useCallback(
    async (text: string, attachments: OutgoingAttachment[] = []) => {
      const trimmed = text.trim();
      if ((!trimmed && attachments.length === 0) || !supportsSessionQueue(client)) return;
      await client.enqueueMessage(trimmed, attachments);
      discardDraft();
      await refreshQueue();
    },
    [client, discardDraft, refreshQueue]
  );

  const removeQueued = useCallback(
    async (entryId: string) => {
      if (!supportsSessionQueue(client)) return;
      await client.removeQueuedMessage(entryId);
      await refreshQueue();
    },
    [client, refreshQueue]
  );

  const reorderQueue = useCallback(
    async (entryIds: string[]) => {
      if (!supportsSessionQueue(client)) return;
      const next = await client.reorderQueue(entryIds);
      setQueue(next);
    },
    [client]
  );

  const setQueuePaused = useCallback(
    async (paused: boolean) => {
      if (!supportsSessionQueue(client)) return;
      // Optimistic: pausing is a deliberate act and the button should respond at
      // once. The authoritative value arrives via the queue-changed event.
      setQueuePausedState(paused);
      try {
        await client.setQueuePaused(paused);
      } catch (err) {
        await refreshQueue();
        throw err;
      }
    },
    [client, refreshQueue]
  );

  const interrupt = useCallback(
    (text: string, attachments: OutgoingAttachment[] = [], hooks?: TurnHooks) => {
      if (!client) {
        hooks?.onDone?.();
        return;
      }
      // Interrupting an in-flight turn: abort it now. Its own async
      // cleanup (below) finalizes its assistant bubble once the abort
      // propagates; we don't wait for that before starting the new turn.
      // The abort only detaches our stream — the server-side Stop is the
      // cancelTurn awaited at the top of the new turn below.
      const interrupting = streaming;
      if (streaming) {
        abortRef.current?.abort();
      }
      // Interrupting a turn this driver rejoined (not one it started): finalize
      // that bubble as interrupted; there is no local stream to unwind.
      const rejoined = rejoinRef.current;
      if (rejoined) {
        rejoinRef.current = null;
        setMessagesState((prev) =>
          prev.map((m) =>
            m.id === rejoined.assistantId
              ? {
                  ...m,
                  content: rejoined.text,
                  metadata: { ...(rejoined.thinking ? { thinking: rejoined.thinking } : {}), interrupted: true },
                }
              : m
          )
        );
      }
      // The text has become a real message, so it is no longer a draft. Done
      // before the turn starts rather than after it finishes: the draft's job is
      // to survive a crash, and a crash mid-turn should not restore text that was
      // already sent.
      discardDraft();

      const generation = ++generationRef.current;

      const userMessage = new UserMessage(text, hooks?.messageId ?? randomId('u'));
      if (attachments.length > 0) userMessage.attachments = attachments;
      const assistantId = randomId('a');
      const controller = new AbortController();

      // Draw the turn: the user message, the live reply bubble, streaming on.
      // Immediately for a plain send; for queueIfBusy only once llmd has said
      // it is running this message now (its first frame that is not TurnQueued).
      const deferred = hooks?.queueIfBusy === true;
      let shown = false;
      const showTurn = () => {
        if (shown) return;
        shown = true;
        if (attachments.length > 0) {
          // llmd persists these anchored to this message and announces them to
          // OTHER windows; this one shows them now (the echo, on reload, is the
          // same rows).
          setLiveAttachments((prev) => {
            const next = new Map(prev);
            for (const a of attachments) {
              next.set(a.id, { ...a, anchorMessageId: userMessage.id } as LlmdMessageAttachment);
            }
            return next;
          });
        }
        setMessagesState((prev) => [...prev, toChatMessageData(userMessage)]);
        ownTurnRef.current = { user: toChatMessageData(userMessage), assistantId };
        setMessagesState((prev) => [
          ...prev,
          { role: 'assistant', content: '', id: assistantId, metadata: { streamingPlaceholder: true } },
        ]);
        setStreaming(true);
        setStreamingMessageId(assistantId);
        setError(null);
        abortRef.current = controller;
      };
      if (deferred) {
        pendingOwnIdsRef.current.add(userMessage.id);
      } else {
        showTurn();
      }

      void (async () => {
        let liveText = '';
        let liveSegments: ChatMessageSegment[] = [];
        let liveSegBreak = false;
        const segmented = config?.segmentedTurns === true;
        let liveThinking = '';
        // Sum of this turn's model calls (see addTurnUsage), kept on the bubble.
        let liveUsage: TurnUsageTotal | undefined;
        let turnStartedReported = false;
        // Stop the interrupted turn server-side BEFORE opening the new one. It
        // must be awaited: llmd queues the new turn behind the running one, and
        // a cancel that raced past it could land on the NEW turn instead.
        if (interrupting) {
          await cancelServerTurn();
        }
        try {
          // Only the new message — llmd prepends persisted session history
          // server-side (see module doc). Resending prior turns would
          // double them up.
          for await (const item of client.chatStreamEvents([userMessage], {
            ...options,
            ...(deferred ? { queueIfBusy: true } : {}),
            signal: controller.signal,
          } as TOptions)) {
            if (generationRef.current !== generation) return;
            if (!shown) {
              if (item.kind === 'event' && item.event.type === ConversationEventType.TurnQueued) {
                // llmd queued it: no turn here. The queue snapshot also
                // arrives over the session-events channel; read it now too so
                // the entry shows without waiting on that.
                void refreshQueueRef.current();
                return;
              }
              showTurn();
            }
            if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTurnStarted) {
              if (!turnStartedReported && item.event.turnId) {
                turnStartedReported = true;
                hooks?.onTurnStarted?.(item.event.turnId);
              }
            }
            if (
              segmented &&
              item.kind === 'event' &&
              item.event.type === ConversationEventType.ToolExecutionStarted
            ) {
              const ev = item.event as { toolCallId?: string; name?: string };
              liveSegments = noteSegmentTool(liveSegments, ev.toolCallId, ev.name);
              liveSegBreak = true;
            }
            if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTextDelta) {
              liveText += item.event.delta;
              const snapshot = liveText;
              if (segmented) {
                liveSegments = pushSegmentDelta(liveSegments, liveSegBreak, item.event.delta);
                liveSegBreak = false;
              }
              const segSnapshot = liveSegments;
              setMessagesState((prev) =>
                prev.map((m) =>
                  m.id === assistantId
                    ? {
                        ...m,
                        content: snapshot,
                        ...(segmented ? { segments: segSnapshot } : {}),
                        metadata: {
                          ...(liveThinking ? { thinking: liveThinking } : {}),
                          ...(liveUsage ? { usage: liveUsage } : {}),
                        },
                      }
                    : m
                )
              );
            } else if (
              item.kind === 'event' &&
              item.event.type === ConversationEventType.AssistantThinkingDelta
            ) {
              liveThinking += item.event.delta;
              const snapshot = liveThinking;
              setMessagesState((prev) =>
                prev.map((m) =>
                  m.id === assistantId ? { ...m, metadata: { ...m.metadata, thinking: snapshot } } : m
                )
              );
            } else if (item.kind === 'event' && item.event.type === ConversationEventType.UserTurnCommitted) {
              if (generationRef.current === generation) beginTurn(item.event.userMessageId, item.event.committedAtMs);
            } else if (item.kind === 'event' && item.event.type === ConversationEventType.SessionStatus) {
              if (generationRef.current === generation) setSessionStatus(item.event.headline || undefined);
            } else if (
              item.kind === 'event' &&
              item.event.type === ConversationEventType.SessionTitleChanged
            ) {
              if (generationRef.current === generation) setSessionTitle(item.event.title);
            } else if (item.kind === 'event' && item.event.type === ConversationEventType.TodosUpdated) {
              if (generationRef.current === generation) setTodos(item.event.todos);
            } else if (
              item.kind === 'event' &&
              (item.event.type === ConversationEventType.ToolCallStreamStarted ||
                item.event.type === ConversationEventType.ToolCallArgsDelta ||
                item.event.type === ConversationEventType.ToolCallStreamFinished ||
                item.event.type === ConversationEventType.ToolExecutionStarted ||
                item.event.type === ConversationEventType.ToolExecutionFinished)
            ) {
              // The side channel carries these same frames too; whichever
              // channel saw this tool call first owns it (see claimToolCall).
              if (
                generationRef.current === generation &&
                claimToolCall(item.event as ToolConversationEvent, 'primary')
              ) {
                onToolEvent?.(item.event as ToolConversationEvent);
              }
            } else if (
              item.kind === 'event' &&
              item.event.type === ConversationEventType.AssistantTurnFinished
            ) {
              liveUsage = addTurnUsage(liveUsage, item.event);
              const snapshot = liveUsage;
              if (snapshot) {
                setMessagesState((prev) =>
                  prev.map((m) =>
                    m.id === assistantId ? { ...m, metadata: { ...m.metadata, usage: snapshot } } : m
                  )
                );
              }
            } else if (item.kind === 'complete') {
              const finalUsage = liveUsage;
              setMessagesState((prev) =>
                prev.map((m) => {
                  if (m.id !== assistantId) return m;
                  const done = toChatMessageData(item.message);
                  return {
                    ...done,
                    id: assistantId,
                    ...(segmented && liveSegments.length >= 2 ? { segments: liveSegments } : {}),
                    ...(finalUsage ? { metadata: { ...done.metadata, usage: finalUsage } } : {}),
                  };
                })
              );
            }
          }
        } catch (err) {
          const aborted = controller.signal.aborted;
          if (generationRef.current === generation && !aborted) {
            setError(err instanceof Error ? err : new Error(String(err)));
          }
          if (aborted) {
            // Finalize THIS turn's bubble regardless of whether a newer
            // turn has since started — leaving a permanently empty
            // "streaming" placeholder after an interrupt/stop is a bug,
            // not a feature.
            setMessagesState((prev) =>
              prev.map((m) =>
                m.id === assistantId
                  ? {
                      ...m,
                      content: liveText,
                      ...(segmented && liveSegments.length ? { segments: liveSegments } : {}),
                      metadata: {
                        ...(liveThinking ? { thinking: liveThinking } : {}),
                        ...(liveUsage ? { usage: liveUsage } : {}),
                        interrupted: true,
                      },
                    }
                  : m
              )
            );
          }
        } finally {
          pendingOwnIdsRef.current.delete(userMessage.id);
          // Only a turn this call actually drew is ended here. A queued send
          // drew nothing, and by now another turn (llmd draining the queue) may
          // be on screen; it is not ours to stop.
          //
          // That includes a turn this one drew: llmd starts the next queued
          // message the moment this turn ends, and the side channel can deliver
          // its UserTurnCommitted (observed turn: streaming on, new bubble)
          // BEFORE this stream finishes closing. An observed turn does not bump
          // the generation, so the generation check alone would switch
          // streaming off under it - the queued turn then ran with no live
          // state (no tool strip, "waiting") until the chat was reopened.
          const observedSince = rejoinRef.current?.observed === true;
          if (shown && generationRef.current === generation && !observedSince) {
            setStreaming(false);
            setStreamingMessageId(undefined);
          }
          if (abortRef.current === controller) {
            abortRef.current = null;
          }
          // The turn is over for this driver (completed, failed, or detached):
          // from here history, not local state, is the record of it.
          if (ownTurnRef.current?.assistantId === assistantId) {
            ownTurnRef.current = null;
          }
          hooks?.onDone?.();
        }
      })();
    },
    [client, streaming, options, onToolEvent, discardDraft, cancelServerTurn, claimToolCall, beginTurn]
  );

  const send = useCallback(
    (text: string, requested?: SendBehavior, attachments: OutgoingAttachment[] = []) => {
      if (!client) return;
      // No explicit behavior: plain-Enter semantics (see sendMode) — queue,
      // or send now while the queue is paused.
      const behavior: SendBehavior = requested ?? (queuePaused ? 'interrupt' : 'queue');
      if (behavior === 'interrupt' || !supportsSessionQueue(client)) {
        interrupt(text, attachments);
        return;
      }
      if (!streaming) {
        // Nothing is streaming HERE, but that is not the same as llmd being
        // idle: a queued message may be between being claimed and starting,
        // another window may be running a turn, or the queue may be paused.
        // llmd decides, atomically, whether this runs now or waits its turn.
        interrupt(text, attachments, { queueIfBusy: true });
        return;
      }
      // A turn is visibly running (`streaming` covers rejoined and observed
      // turns too), so this certainly waits: queue it directly.
      enqueue(text, attachments).catch((err) => {
        const e = err instanceof Error ? err : new Error(String(err));
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: enqueue failed', e);
        setError(e);
      });
    },
    [client, streaming, queuePaused, interrupt, enqueue]
  );

  const interruptWithQueued = useCallback(
    async (entryId: string) => {
      if (!supportsSessionQueue(client)) return;
      const entry = queue.find((q) => q.id === entryId);
      if (!entry || entry.state === 'running') return;
      // Remove BEFORE sending: if the drainer picked it up in the meantime the
      // removal fails and we must not send a second copy.
      const removed = await client.removeQueuedMessage(entryId);
      await refreshQueue();
      if (!removed) return;
      interrupt(entry.content, entry.attachments ?? []);
    },
    [client, queue, refreshQueue, interrupt]
  );

  // Client-side queue drainer (opt-in, `config.drainQueue`). One entry at a
  // time, only while nothing is running and the queue is not paused; the
  // claim makes it safe next to llmd's own drainer and other windows.
  const drainQueueEnabled = config?.drainQueue === true;
  const drainOwnerRef = useRef(randomId('react-drainer'));
  const drainingRef = useRef(false);
  const [drainTick, setDrainTick] = useState(0);
  useEffect(() => {
    if (!drainQueueEnabled || !client || !supportsQueueDrain(client)) return;
    if (streaming || queuePaused || drainingRef.current) return;
    if (!queue.some((q) => q.state === 'pending')) return;
    drainingRef.current = true;
    const drainer = client;
    const owner = drainOwnerRef.current;
    const finish = (backoff: boolean) => {
      const done = () => {
        drainingRef.current = false;
        void refreshQueueRef.current();
        setDrainTick((n) => n + 1);
      };
      if (backoff) setTimeout(done, DRAIN_RETRY_BACKOFF_MS);
      else done();
    };
    void (async () => {
      let entry: QueuedMessage | undefined;
      try {
        entry = (await drainer.claimNextQueuedMessage(owner)).entry;
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: queue claim failed', err);
        finish(true);
        return;
      }
      if (!entry) {
        finish(false);
        return;
      }
      const claimed = entry;
      let started = false;
      const hb = setInterval(() => {
        drainer.heartbeatQueuedMessage(claimed.id, owner).catch((err) => {
          // eslint-disable-next-line no-console
          console.warn('useLlmdChatDriver: queue heartbeat failed', err);
        });
      }, DRAIN_HEARTBEAT_MS);
      interrupt(claimed.content, claimed.attachments ?? [], {
        messageId: `queued_${claimed.id}`,
        onTurnStarted: (turnId) => {
          started = true;
          drainer.markQueuedMessageStarted(claimed.id, owner, turnId).catch((err) => {
            // eslint-disable-next-line no-console
            console.warn('useLlmdChatDriver: queue markStarted failed', err);
          });
        },
        onDone: () => {
          clearInterval(hb);
          // Delivered → retire (never re-send). Never reached the model →
          // hand it back at its position, and back off before retrying.
          const settle = started
            ? drainer.retireQueuedMessage(claimed.id, owner)
            : drainer.releaseQueuedMessage(claimed.id, owner);
          settle
            .catch((err) => {
              // eslint-disable-next-line no-console
              console.warn('useLlmdChatDriver: queue retire/release failed', err);
            })
            .finally(() => finish(!started));
        },
      });
    })();
  }, [drainQueueEnabled, client, streaming, queuePaused, queue, interrupt, drainTick]);

  // Latest onToolEvent in a ref so the subscription effect below doesn't
  // need to restart (tearing down and reopening the live connection) just
  // because the host passed a new closure identity this render.
  const onToolEventRef = useRef(onToolEvent);
  onToolEventRef.current = onToolEvent;

  // Automatic side-channel subscription — see this module's doc comment
  // ("Side-channel subscription"). Opened once per session, for the
  // component's whole lifetime, independent of any particular send() turn's
  // generation counter: this is session-scoped, not turn-scoped.
  useEffect(() => {
    const client = clientRef.current;
    if (!client) return;
    if (!supportsSessionEventsSubscription(client)) {
      // Silent no-op here reads identically to "everything's fine, nothing
      // to report" from the outside — which is exactly the debugging trap
      // this line exists to close. If you're staring at zero network
      // requests to /ws/session-events (or its gRPC equivalent) and no
      // console output at all, THIS is why: `client` doesn't have a
      // `subscribeSessionEvents` method as far as this hook can see, so it
      // never even tries. Common causes: an older @pyxis-llm/client(-web) build
      // (check its package.json version), or `client` being a wrapped/
      // spread/proxied object that dropped the method (it lives on the
      // class prototype, so `{ ...client }` silently loses it).
      // eslint-disable-next-line no-console
      console.debug(
        'useLlmdChatDriver: client does not implement subscribeSessionEvents — skipping automatic ' +
          'session-events side channel (no live tool-execution/session-status updates for CLI-backed ' +
          'sessions). typeof client.subscribeSessionEvents ===',
        typeof (client as { subscribeSessionEvents?: unknown }).subscribeSessionEvents,
        client
      );
      return;
    }
    const controller = new AbortController();
    setSessionEventsError(undefined);

    const updateRejoinBubble = () => {
      const r = rejoinRef.current;
      if (!r) return;
      setMessagesState((prev) => prev.map((m) => (m.id === r.assistantId ? rejoinPlaceholder(r) : m)));
    };

    /** Sleep that ends early when the effect is torn down. */
    const sleep = (ms: number) =>
      new Promise<void>((resolve) => {
        const t = setTimeout(resolve, ms);
        controller.signal.addEventListener(
          'abort',
          () => {
            clearTimeout(t);
            resolve();
          },
          { once: true }
        );
      });

    /**
     * If llmd reports the rejoined turn as finished, swap its live bubble for the
     * persisted transcript. Returns true once there is nothing left to wait for.
     */
    // An observed turn is not displaced by our own (see rejoinRef); a mount-time
    // rejoin yields to a turn this driver is running.
    const rejoinActive = () => {
      const r = rejoinRef.current;
      return !!r && (r.observed || !abortRef.current);
    };

    const finishRejoinIfDone = async (): Promise<boolean> => {
      if (!rejoinActive() || !supportsSessionState(client)) return true;
      if (supportsSessionStatus(client)) {
        // Cheap check first: only pull the transcript once the turn is done.
        const status = await client.getSessionStatus();
        if (controller.signal.aborted || !rejoinRef.current) return true;
        if (status.turnInProgress) return false;
      }
      const st = await client.getSessionState();
      if (controller.signal.aborted || !rejoinRef.current) return true; // torn down or superseded
      if (st.turnInProgress) return false;
      rejoinRef.current = null;
      setMessagesState(historyToChat(st.messages));
      setStreaming(false);
      setStreamingMessageId(undefined);
      adoptServerTurnTiming(st);
      return true;
    };

    // The rejoined turn's last frame arrives just BEFORE llmd commits it to
    // history, so after a terminal frame re-read state (quickly) until the turn
    // is no longer in progress.
    let settling = false;
    const settleRejoin = async () => {
      if (settling) return;
      settling = true;
      try {
        for (let i = 0; i < REJOIN_SETTLE_MAX_POLLS && !controller.signal.aborted; i++) {
          if (await finishRejoinIfDone()) return;
          await sleep(REJOIN_SETTLE_POLL_MS);
        }
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: failed to settle rejoined turn', err);
      } finally {
        settling = false;
      }
    };

    // Safety net: a rejoined turn must end even if its terminal frame never
    // reaches us (connection dropped and the turn finished in the gap, a proxy
    // swallowing frames, …). Without this, the spinner's only exit is a frame
    // over the very channel that may be broken.
    const rejoinWatch = setInterval(() => {
      if (!rejoinActive() || settling) return;
      finishRejoinIfDone().catch((err) => {
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: rejoin watch failed to read session state', err);
      });
    }, REJOIN_WATCH_MS);

    // The subscription is kept up for the component's whole lifetime: when it
    // errors OR simply ends (server restart, a proxy dropping an idle socket),
    // it is re-established with backoff. Each (re)connect re-reads session state
    // to choose its cursor, so nothing a rejoined turn needs is skipped.
    // The idle-session transcript is loaded once per session (this effect is keyed
    // on it), on the first connect. Reconnects don't reload: the subscription
    // resumes from its cursor and llmd replays what was missed.
    let historyLoaded = false;
    void (async () => {
      let failures = 0;
      while (!controller.signal.aborted) {
      try {
        // Pick the subscription cursor from session state: rejoin a running
        // turn from where it started, otherwise receive only what is new.
        // Subscribing from 0 would replay the session's whole event history
        // and re-fire every old tool/abort event.
        let afterSeq: number | undefined;
        if (supportsSessionState(client)) {
          // Status first (no transcript); the full state is only needed when the
          // displayed transcript must be replaced: starting a rejoin, or a
          // rejoined turn having finished while we were disconnected.
          let st: {
            messages?: Message[];
            turnInProgress: boolean;
            eventsSeq: number;
            turnStartSeq: number;
          } & SessionTurnTiming = supportsSessionStatus(client)
            ? await client.getSessionStatus()
            : await client.getSessionState();
          if (controller.signal.aborted) return;
          const needsTranscript =
            (st.turnInProgress && !abortRef.current && !rejoinRef.current) ||
            (!!rejoinRef.current && !st.turnInProgress);
          if (needsTranscript && !st.messages) {
            st = await client.getSessionState();
            if (controller.signal.aborted) return;
          }
          const messages = st.messages ?? [];
          const r = rejoinRef.current;
          if (st.turnInProgress && !abortRef.current) {
            afterSeq = st.turnStartSeq;
            if (r) {
              // Reconnecting mid-rejoin: the stream replays the turn from its
              // start, so rebuild the bubble from scratch rather than append.
              r.text = '';
              r.thinking = '';
              r.segments = undefined;
              r.segBreak = false;
              r.awaitingCommit = true;
              updateRejoinBubble();
            } else {
              const nr = { assistantId: randomId('a'), text: '', thinking: '', awaitingCommit: true };
              rejoinRef.current = nr;
              // The transcript to show is exactly what llmd has right now
              // (which, mid-turn, ends with the message the user sent — it is
              // persisted at turn start) plus the live reply. Seeding it here
              // makes a session load complete on its own; a host's own later
              // hydrate() carries the live bubble over.
              setMessagesState([...historyToChat(messages), rejoinPlaceholder(nr)]);
              setStreaming(true);
              setStreamingMessageId(nr.assistantId);
            }
          } else {
            afterSeq = st.eventsSeq;
            if (r && !st.turnInProgress) {
              // The rejoined turn finished while we were disconnected.
              rejoinRef.current = null;
              setMessagesState(historyToChat(messages));
              setStreaming(false);
              setStreamingMessageId(undefined);
            } else if (!historyLoaded) {
              // Opening an idle session: show its transcript. Without this a
              // session with history rendered empty unless the host remembered
              // to call hydrate() itself (the rejoin path above already seeded
              // a busy session on its own). hydrate() keeps a turn this driver
              // started meanwhile, so a slow load cannot wipe a fresh send.
              const transcript = st.messages ?? (await client.getSessionState()).messages;
              if (controller.signal.aborted) return;
              hydrate(transcript);
            }
          }
          historyLoaded = true;
          adoptServerTurnTiming(st);
        }
        // Seed title + todos from the session row. Read AFTER the cursor was
        // chosen: any change past it is replayed by the subscription below, so
        // the two always converge on the latest value.
        if (supportsSessionInfo(client)) {
          try {
            const info = await client.getSessionInfo();
            if (controller.signal.aborted) return;
            setSessionTitle(info.title || undefined);
            setSessionStatus(info.status || undefined);
            // Keep the old array when nothing changed: a host that rebuilds its
            // client every render re-runs this effect each time, and a fresh
            // (equal) array would re-render and restart it forever.
            setTodos((prev) => (JSON.stringify(prev) === JSON.stringify(info.todos) ? prev : info.todos));
          } catch (err) {
            // Non-fatal: live updates still arrive; only the initial snapshot is missing.
            // eslint-disable-next-line no-console
            console.error('useLlmdChatDriver: getSessionInfo failed; title/todos show once next updated', err);
          }
        }
        // Seed attachments the same way (after the cursor), replacing the live
        // overlay: everything it held is in this snapshot, and anything newer
        // than the cursor is replayed by the subscription below.
        if (supportsSessionAttachments(client)) {
          try {
            const rows = await client.getSessionAttachmentRows();
            if (controller.signal.aborted) return;
            const loaded = new Map<string, LoadedAttachment>();
            for (const row of rows) {
              const attachment = llmdMessageAttachmentFromRowContent(row.content);
              if (attachment) {
                loaded.set(attachment.id, attachment);
              } else {
                // eslint-disable-next-line no-console
                console.warn('useLlmdChatDriver: skipping undecodable attachment row', row);
              }
            }
            setLoadedAttachments(loaded);
            setLiveAttachments(new Map());
            setAttachmentsError(undefined);
          } catch (err) {
            // Non-fatal: live updates still arrive; only previously asked cards are missing.
            const e = err instanceof Error ? err : new Error(String(err));
            // eslint-disable-next-line no-console
            console.error('useLlmdChatDriver: loading session attachments failed', e);
            setAttachmentsError(e);
          }
        }
        for await (const event of client.subscribeSessionEvents({ afterSeq, signal: controller.signal })) {
          failures = 0;
          setSessionEventsError(undefined); // a message got through: connection is (still) good
          // Rebuild a rejoined turn's reply. Only while rejoined and with no
          // turn of our own running: our own turn's frames also flow through
          // this channel and are already rendered by send().
          const r = rejoinRef.current;
          if (r && rejoinActive()) {
            if (segmentedRef.current && event.type === ConversationEventType.ToolExecutionStarted) {
              const ev = event as { toolCallId?: string; name?: string };
              r.segments = noteSegmentTool(r.segments ?? [], ev.toolCallId, ev.name);
              r.segBreak = true;
            }
            if (event.type === ConversationEventType.AssistantTextDelta) {
              r.text += event.delta;
              if (segmentedRef.current) {
                r.segments = pushSegmentDelta(r.segments ?? [], r.segBreak === true, event.delta);
                r.segBreak = false;
              }
              updateRejoinBubble();
              continue;
            }
            if (event.type === ConversationEventType.AssistantThinkingDelta) {
              r.thinking += event.delta;
              updateRejoinBubble();
              continue;
            }
            if (
              event.type === ConversationEventType.AssistantTurnFinished ||
              event.type === ConversationEventType.AssistantTurnFailed ||
              event.type === ConversationEventType.ConversationAborted
            ) {
              void settleRejoin();
            }
          }
          if (event.type === ConversationEventType.UserTurnCommitted) {
            // A turn this driver did not send (llmd draining the queue, another
            // window): show it like one we just sent — the user message plus a
            // live reply bubble that the deltas above fill in. Our own turns
            // are recognised by the message id send() minted (llmd echoes it),
            // checked BEFORE beginTurn records it.
            const id = event.userMessageId;
            const own =
              !!id &&
              (ownTurnRef.current?.user.id === id ||
                lastTurnIdRef.current === id ||
                pendingOwnIdsRef.current.has(id));
            const cur = rejoinRef.current;
            if (cur?.awaitingCommit) {
              // Replay of the rejoined turn's own start (see rejoinRef).
              cur.awaitingCommit = false;
            } else if (!own) {
              const prevTurn = rejoinRef.current;
              const nr = { assistantId: randomId('a'), text: '', thinking: '', observed: true };
              rejoinRef.current = nr;
              const userMsg: ChatMessageData = {
                role: 'user',
                content: event.textPreview ?? '',
                id: id || randomId('u'),
              };
              setMessagesState((prev) => {
                // A previous observed/rejoined turn that ended without settling
                // yet (the next queued message started immediately): keep what
                // it streamed as a finished reply. Settle swaps in the persisted
                // transcript later.
                const base = prevTurn
                  ? prev.map((m) =>
                      m.id === prevTurn.assistantId
                        ? {
                            ...m,
                            content: prevTurn.text,
                            metadata: prevTurn.thinking ? { thinking: prevTurn.thinking } : {},
                          }
                        : m
                    )
                  : prev;
                if (base.some((m) => m.id === userMsg.id)) return [...base, rejoinPlaceholder(nr)];
                return [...base, userMsg, rejoinPlaceholder(nr)];
              });
              setStreaming(true);
              setStreamingMessageId(nr.assistantId);
            }
            beginTurn(id, event.committedAtMs);
          } else if (
            event.type === ConversationEventType.ToolCallStreamStarted ||
            event.type === ConversationEventType.ToolCallArgsDelta ||
            event.type === ConversationEventType.ToolCallStreamFinished ||
            event.type === ConversationEventType.ToolExecutionStarted ||
            event.type === ConversationEventType.ToolExecutionFinished
          ) {
            if (claimToolCall(event as ToolConversationEvent, 'side')) {
              onToolEventRef.current?.(event as ToolConversationEvent);
            }
          } else if (event.type === ConversationEventType.SessionStatus) {
            // An empty headline is llmd clearing it (turn start/end).
            setSessionStatus(event.headline || undefined);
          } else if (event.type === ConversationEventType.SessionTitleChanged) {
            setSessionTitle(event.title);
          } else if (event.type === ConversationEventType.TodosUpdated) {
            setTodos(event.todos);
          } else if (event.type === ConversationEventType.QueueChanged) {
            // Whole-queue snapshot, so this converges even if an earlier event was
            // missed — and it is how edits made in another window (or a claim by a
            // background drainer) reach this one.
            setQueue(event.queue);
            setQueuePausedState(event.paused);
          } else if (event.type === ConversationEventType.AttachmentUpserted) {
            const att = event.attachment;
            const anchor = event.messageId;
            // The turn it was made in, until a reload brings the one llmd
            // recorded (which mergeLiveAttachments prefers): what places a card
            // whose tool call is not in the transcript (CLI tools over MCP).
            const turnNow = lastTurnIdRef.current;
            setLiveAttachments((prev) => {
              const next = new Map(prev);
              const turnMessageId = prev.get(att.id)?.turnMessageId ?? turnNow;
              // Keep the anchor: it is what ties an attachment to its message
              // (e.g. an ask-card answer to its "Answer to: …" bubble).
              next.set(att.id, {
                ...att,
                ...(anchor ? { anchorMessageId: anchor } : {}),
                ...(turnMessageId ? { turnMessageId } : {}),
              } as LlmdMessageAttachment);
              return next;
            });
          } else if (event.type === ConversationEventType.AttachmentDeleted) {
            const id = event.attachmentId;
            setLiveAttachments((prev) => {
              const next = new Map(prev);
              next.set(id, null);
              return next;
            });
          } else if (event.type === ConversationEventType.ConversationAborted) {
            // A turn on this session was cut short. This matters for observers
            // that did not do the cancelling — a second window on the same
            // session — which have no aborted AbortController to learn from and
            // would otherwise show a turn as in-flight forever. llmd emits this
            // only after the salvaged partial is committed, so the transcript is
            // already consistent by the time we clear the in-flight state.
            //
            // Only act when THIS driver has no turn of its own running. The
            // window that interrupted also receives this event, and by then its
            // replacement turn is already streaming: clearing state here would
            // clobber the new turn and flag its live bubble as interrupted —
            // the same hazard send()'s generation counter guards against. That
            // window's own abort handling has already finalized the old turn.
            if (!abortRef.current && !rejoinRef.current) {
              setStreaming(false);
              setStreamingMessageId(undefined);
              setMessagesState((prev) =>
                prev.map((m) => {
                  const meta = m.metadata as { streamingPlaceholder?: boolean } | undefined;
                  if (!meta?.streamingPlaceholder) return m;
                  const { streamingPlaceholder: _dropped, ...rest } = meta;
                  return { ...m, metadata: { ...rest, interrupted: true } };
                })
              );
            }
          }
        }
        // The stream ended without an error. Nothing on our side ended it, so
        // the server or something in between (a dev-server proxy) closed it.
        // That is not "done": the session is still live, so reconnect.
        if (controller.signal.aborted) return;
        const closed = new Error('session-events stream was closed by the server or a proxy; reconnecting');
        // eslint-disable-next-line no-console
        console.warn('useLlmdChatDriver:', closed.message);
        setSessionEventsError(closed);
      } catch (err) {
        // A subscription error must never surface as this driver's own
        // `error` (that's reserved for the primary chatStreamEvents turn's
        // failures) — but it must NOT be silent either: while it is down,
        // rejoined turns and tool/status updates from other sources go dark.
        // Log AND expose via sessionEventsError (cleared by the next frame
        // that gets through), then retry.
        if (controller.signal.aborted) return;
        const e = err instanceof Error ? err : new Error(String(err));
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: session-events subscription failed; reconnecting', e);
        setSessionEventsError(e);
      }
      failures++;
      await sleep(Math.min(SUBSCRIBE_RETRY_MAX_MS, SUBSCRIBE_RETRY_BASE_MS * 2 ** Math.min(failures - 1, 6)));
      }
    })();
    return () => {
      clearInterval(rejoinWatch);
      controller.abort();
    };
  }, [sessionId, claimToolCall, beginTurn, adoptServerTurnTiming]);

  const submitAskCard = useCallback(async (response: AskCardResponse, options?: AskCardSubmitOptions): Promise<void> => {
    const c = clientRef.current;
    const cardAttachment = attachmentIndexRef.current.get(response.cardId) as LoadedAttachment | undefined;
    const card = cardAttachment ? decodeAskCard(cardAttachment.payloadJson) : undefined;
    if (!card || !cardAttachment) {
      const e = new Error(`submitAskCard: no ask card ${JSON.stringify(response.cardId)} on this session`);
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver:', e);
      setAttachmentsError(e);
      throw e;
    }
    if (supportsAnswerAskCard(c)) {
      // llmd queues the answer at the front of the session queue (resuming the
      // agent) or updates an earlier answer in place. The card flips to
      // answered from the queue entry (see askCardResponses), then from the
      // persisted attachment once its turn runs.
      try {
        await c.answerAskCard(card.id, encodeAskCardResponse(response), {
          continueWithTools: options?.continueWithTools === true,
        });
        setAttachmentsError(undefined);
      } catch (err) {
        const e = err instanceof Error ? err : new Error(String(err));
        // eslint-disable-next-line no-console
        console.error('useLlmdChatDriver: answering ask card failed', e);
        setAttachmentsError(e);
        throw e;
      }
      await refreshQueue();
      return;
    }
    if (!supportsSessionAttachments(c)) {
      const e = new Error('submitAskCard: this llmd client cannot write session attachments');
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver:', e);
      setAttachmentsError(e);
      throw e;
    }
    const answer = buildAskCardResponseAttachment(card, response);
    const hadLive = liveAttachments.has(answer.id);
    const previous = liveAttachments.get(answer.id);
    // Optimistic: show the answer now. llmd's attachment_upserted echo later
    // overwrites this entry with the same value.
    setLiveAttachments((prev) => new Map(prev).set(answer.id, answer));
    try {
      await c.upsertSessionAttachment(
        cardAttachment.anchorMessageId || cardAttachment.afterToolCallId || card.id,
        answer
      );
      setAttachmentsError(undefined);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver: saving ask-card answer failed; rolled back', e);
      setLiveAttachments((prev) => {
        const next = new Map(prev);
        if (hadLive) next.set(answer.id, previous ?? null);
        else next.delete(answer.id);
        return next;
      });
      setAttachmentsError(e);
      throw e;
    }
  }, [liveAttachments, refreshQueue]);

  const dismissAskCard = useCallback(async (cardId: string): Promise<void> => {
    const id = cardId.trim();
    const c = clientRef.current;
    if (!supportsAttachmentDelete(c)) {
      const e = new Error('dismissAskCard: this llmd client cannot delete session attachments');
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver:', e);
      setAttachmentsError(e);
      throw e;
    }
    // Optimistic: hide it now. A `null` live entry removes it from
    // attachmentIndex (mergeLiveAttachments), which is what renders it.
    let hadLive = false;
    let previous: LlmdMessageAttachment | null | undefined;
    setLiveAttachments((prev) => {
      hadLive = prev.has(id);
      previous = prev.get(id);
      return new Map(prev).set(id, null);
    });
    try {
      await c.deleteSessionAttachment(id);
      setAttachmentsError(undefined);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      // eslint-disable-next-line no-console
      console.error('useLlmdChatDriver: dismissing ask card failed; restored', e);
      setLiveAttachments((prev) => {
        const next = new Map(prev);
        if (hadLive) next.set(id, previous ?? null);
        else next.delete(id);
        return next;
      });
      setAttachmentsError(e);
      throw e;
    }
  }, []);

  return {
    attachmentIndex,
    askCardResponses,
    submitAskCard,
    dismissAskCard,
    attachmentsError,
    draft: draftStore,
    setDraft,
    queue,
    queuePaused,
    enqueue,
    removeQueued,
    reorderQueue,
    setQueuePaused,
    messages,
    streaming,
    turnTiming,
    streamingMessageId,
    send,
    interrupt,
    interruptWithQueued,
    stop,
    setMessages,
    hydrate,
    error,
    sessionStatus,
    sessionTitle,
    todos,
    sessionEventsError,
  };
}
