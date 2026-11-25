/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Conversation-event types: the event discriminant enum, the ConversationEvent
 * union, and the JSON parsing helpers the frame mapper uses.
 *
 * One codegen serves both transports, so the frame->event mapper is shared
 * too and lives in ./events.ts next to these types.
 */

import type { QueuedMessage } from "./sessionQueue.js";

/**
 * Wire schema version for conversation stream frames (matches llmd / ai-library).
 */
export const CONVERSATION_EVENT_SCHEMA_VERSION = 2 as const;

/** Discriminant string values match the conversation_events wire contract. */
export enum ConversationEventType {
  UserTurnCommitted = "user_turn_committed",
  AssistantTurnStarted = "assistant_turn_started",
  AssistantThinkingDelta = "assistant_thinking_delta",
  AssistantActivitySnapshot = "assistant_activity_snapshot",
  AssistantTextDelta = "assistant_text_delta",
  ToolCallStreamStarted = "tool_call_stream_started",
  ToolCallArgsDelta = "tool_call_args_delta",
  ToolCallStreamFinished = "tool_call_stream_finished",
  ToolExecutionStarted = "tool_execution_started",
  ToolExecutionFinished = "tool_execution_finished",
  AssistantTurnFinished = "assistant_turn_finished",
  AssistantTurnFailed = "assistant_turn_failed",
  ConversationAborted = "conversation_aborted",
  /** Agent-authored status headline (set_status tool); session-scoped, not turn-scoped. */
  SessionStatus = "session_status",
  /** Session title set via the set_session_title tool. */
  SessionTitleChanged = "session_title_changed",
  /** Session todo list changed via add_todo/update_todo. */
  TodosUpdated = "todos_updated",
  /**
   * The session's message queue changed — enqueued, removed, reordered, claimed,
   * retired, paused or resumed. Carries the whole queue rather than a delta, so a
   * window that missed an event still converges.
   */
  QueueChanged = "queue_changed",
  /**
   * A session attachment (e.g. an ask_card from the ask_user tool) was created
   * or updated. Carries the whole attachment so it can render without a refetch.
   */
  AttachmentUpserted = "attachment_upserted",
  /** A session attachment was deleted. */
  AttachmentDeleted = "attachment_deleted",
  /**
   * Only on a ChatStream sent with `queueIfBusy`: llmd found the session busy
   * and put the message in the session queue instead of running it. The
   * stream ends after this event (no turn, no `complete`).
   */
  TurnQueued = "turn_queued",
}

/** One entry in the JSON array carried by a TodosUpdated event's todosJson (see store.TodoItem). */
export type SessionTodoItem = {
  id: string;
  description: string;
  status: "pending" | "in_progress" | "completed";
  createdTurnSeq?: number;
  startedTurnSeq?: number;
  completedTurnSeq?: number;
};

/** Parses a TodosUpdated frame's `todosJson` payload. */
export function safeTodosJson(s: string): SessionTodoItem[] {
  if (!s?.trim()) return [];
  try {
    const v = JSON.parse(s) as unknown;
    if (!Array.isArray(v)) return [];
    return v.map((raw) => {
      const r = raw as Record<string, unknown>;
      return {
        id: String(r.id ?? ""),
        description: String(r.description ?? ""),
        status: (r.status as SessionTodoItem["status"]) ?? "pending",
        createdTurnSeq: r.created_turn_seq !== undefined ? Number(r.created_turn_seq) : undefined,
        startedTurnSeq: r.started_turn_seq !== undefined ? Number(r.started_turn_seq) : undefined,
        completedTurnSeq:
          r.completed_turn_seq !== undefined ? Number(r.completed_turn_seq) : undefined,
      };
    });
  } catch {
    return [];
  }
}

/**
 * Parses an `argumentsJson` payload (ToolCallStreamFinished or
 * ToolExecutionStarted). Yields `{}` for absent, unparseable or non-object
 * input rather than throwing — these arrive from a model.
 */
export function safeArgsJson(s: string): Record<string, unknown> {
  if (!s?.trim()) return {};
  try {
    const v = JSON.parse(s) as unknown;
    return v !== null && typeof v === "object" && !Array.isArray(v)
      ? (v as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

/**
 * Turn timing a session reports with its status (llmd's server clock, epoch
 * ms), so any client can show a turn's clock and how long the last one took
 * without having watched it. Absent from older llmd.
 */
export type SessionTurnTiming = {
  /** When a turn is in progress: when it started. */
  turnStartedAtMs?: number;
  /** The session's most recently finished turn, if one finished since llmd started. */
  lastTurn?: { startedAtMs: number; endedAtMs: number };
};

export type AssistantTurnCapabilities = {
  thinking?: boolean;
  streamingToolCalls?: boolean;
};

export type ConversationEvent =
  | {
      type: ConversationEventType.UserTurnCommitted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      userMessageId: string;
      textPreview?: string;
      /** Server wall-clock ms the user turn started; time the turn from this. Absent from older llmd. */
      committedAtMs?: number;
    }
  | {
      type: ConversationEventType.AssistantTurnStarted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      /** Session-scoped, gap-free monotonic turn sequence minted by llmd's TurnRegister. */
      turnSeq?: number;
      capabilities?: AssistantTurnCapabilities;
      parentTurnGroupId?: string;
    }
  | {
      type: ConversationEventType.AssistantThinkingDelta;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      delta: string;
    }
  | {
      type: ConversationEventType.AssistantActivitySnapshot;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      headline: string;
    }
  | {
      type: ConversationEventType.AssistantTextDelta;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      delta: string;
    }
  | {
      type: ConversationEventType.ToolCallStreamStarted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      id: string;
      name?: string;
    }
  | {
      type: ConversationEventType.ToolCallArgsDelta;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      id: string;
      argsDelta: string;
    }
  | {
      type: ConversationEventType.ToolCallStreamFinished;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      id: string;
      name: string;
      arguments: Record<string, unknown>;
    }
  | {
      type: ConversationEventType.ToolExecutionStarted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      toolCallId: string;
      name: string;
      clientWaitNonce?: string;
      /**
       * Arguments the tool was invoked with, for rendering a descriptive label
       * ("Reading lines 15-200 of foo.go") instead of a bare tool name.
       *
       * Present on every transport. For MCP-dispatched tools (Claude Code /
       * Cursor CLI) this is the ONLY event carrying arguments — those calls
       * never produce a ToolCallStreamFinished, because the model-side call
       * happened inside the CLI.
       *
       * `{}` when llmd sent nothing or the payload could not be parsed —
       * never a throw.
       */
      arguments: Record<string, unknown>;
      /**
       * True when llmd shortened the arguments to bound its event log.
       * Oversized values are elided (a file body becomes a short marker) while
       * keys and small scalars survive, so `arguments` still parses and still
       * has the identifying fields — a path or line range is intact even when
       * this is true. Read session history if you need exact values.
       */
      argumentsTruncated?: boolean;
      /** Raw arguments payload as llmd sent it, for display fallback. */
      argumentsText?: string;
    }
  | {
      type: ConversationEventType.ToolExecutionFinished;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      toolCallId: string;
      name: string;
      success: boolean;
      errorMessage?: string;
    }
  | {
      type: ConversationEventType.AssistantTurnFinished;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      assistantMessageId?: string;
      finishReason?: string;
      usage?: {
        inputTokens?: number;
        outputTokens?: number;
        cacheReadTokens?: number;
        cacheWriteTokens?: number;
        reasoningTokens?: number;
      };
      /** Wall time of the model call that produced this message, in ms; absent when not measured. */
      modelDurationMs?: number;
    }
  | {
      type: ConversationEventType.AssistantTurnFailed;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      turnId: string;
      error: string;
    }
  | {
      type: ConversationEventType.ConversationAborted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      reason?: string;
    }
  | {
      type: ConversationEventType.SessionStatus;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      headline: string;
    }
  | {
      type: ConversationEventType.SessionTitleChanged;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      title: string;
    }
  | {
      type: ConversationEventType.TodosUpdated;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      todos: SessionTodoItem[];
    }
  | {
      type: ConversationEventType.QueueChanged;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      queue: QueuedMessage[];
      paused: boolean;
    }
  | {
      type: ConversationEventType.AttachmentUpserted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      /** Message the attachment hangs off; undefined for tool-created (anchorless) cards. */
      messageId?: string;
      attachment: {
        id: string;
        kind: string;
        payloadJson: string;
        afterToolCallId?: string;
      };
    }
  | {
      type: ConversationEventType.AttachmentDeleted;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      attachmentId: string;
    }
  | {
      type: ConversationEventType.TurnQueued;
      schemaVersion?: typeof CONVERSATION_EVENT_SCHEMA_VERSION;
      sessionId: string;
      /** The queue entry the message became. */
      entry: QueuedMessage;
    };
