/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The ONE ConversationStreamFrame -> ConversationEvent mapper.
 *
 * There used to be two of these — one per transport package — because the two
 * codegens produced different frame shapes (ts-proto put each oneof member on
 * the message as its own optional field; protoc-gen-es models the oneof as a
 * `{ case, value }` tagged union). With a single codegen there is one frame
 * type and therefore one mapper, which is the point: a new frame variant can no
 * longer be handled on one transport and silently dropped on the other.
 */

import type {
  ConversationStreamFrame,
  QueuedMessage as QueuedMessageFrame,
} from "./gen/llmv1/llm_pb.js";
import type { QueuedMessage } from "./sessionQueue.js";
import {
  CONVERSATION_EVENT_SCHEMA_VERSION,
  ConversationEventType,
  safeArgsJson,
  safeTodosJson,
  type ConversationEvent,
} from "./conversationEvents.js";

export {
  CONVERSATION_EVENT_SCHEMA_VERSION,
  ConversationEventType,
  safeArgsJson,
  safeTodosJson,
  type AssistantTurnCapabilities,
  type ConversationEvent,
  type SessionTodoItem,
} from "./conversationEvents.js";

/** int64 fields arrive as bigint; the public event shape uses plain numbers. */
function num(v: bigint | number | undefined): number | undefined {
  return v === undefined ? undefined : Number(v);
}

/**
 * A wire QueuedMessage as the client shape. Wire timestamps are unix SECONDS;
 * the rest of this client speaks milliseconds, so convert at the boundary
 * rather than leaking two units.
 */
function queuedMessageFromFrame(q: QueuedMessageFrame): QueuedMessage {
  return {
    id: q.id,
    content: q.content,
    state: q.state === "running" ? ("running" as const) : ("pending" as const),
    claimOwner: q.claimOwner,
    claimExpiresAtMs:
      q.claimExpiresAtUnix === undefined ? undefined : (num(q.claimExpiresAtUnix) ?? 0) * 1000,
    createdAtMs: (num(q.createdAtUnix) ?? 0) * 1000,
    attachments: (q.attachments ?? []).map((a) => ({
      id: a.id,
      kind: a.kind,
      payloadJson: a.payloadJson,
      ...(a.afterToolCallId ? { afterToolCallId: a.afterToolCallId } : {}),
      ...(a.content ? { content: a.content } : {}),
    })),
  };
}

/**
 * Maps one stream frame to a ConversationEvent, or undefined if unmapped.
 */
export function conversationStreamFrameToEvent(
  frame: ConversationStreamFrame
): ConversationEvent | undefined {
  const sv = CONVERSATION_EVENT_SCHEMA_VERSION;
  // A real protobuf-es message always carries `frame` (as { case: undefined }
  // when unset), but tolerate a hand-built object rather than throwing.
  const f = frame.frame ?? { case: undefined };
  switch (f.case) {
    case "userTurnCommitted":
      return {
        type: ConversationEventType.UserTurnCommitted,
        schemaVersion: sv,
        userMessageId: f.value.userMessageId,
        textPreview: f.value.textPreview,
        committedAtMs: num(f.value.committedAtMs),
      };
    case "assistantTurnStarted":
      return {
        type: ConversationEventType.AssistantTurnStarted,
        schemaVersion: sv,
        turnId: f.value.turnId,
        turnSeq: num(f.value.turnSeq),
        capabilities: {
          thinking: f.value.thinkingEnabled,
          streamingToolCalls: f.value.streamingToolCalls,
        },
        parentTurnGroupId: f.value.parentTurnGroupId,
      };
    case "assistantThinkingDelta":
      return {
        type: ConversationEventType.AssistantThinkingDelta,
        schemaVersion: sv,
        turnId: f.value.turnId,
        delta: f.value.delta,
      };
    case "assistantActivitySnapshot":
      return {
        type: ConversationEventType.AssistantActivitySnapshot,
        schemaVersion: sv,
        turnId: f.value.turnId,
        headline: f.value.headline,
      };
    case "assistantTextDelta":
      return {
        type: ConversationEventType.AssistantTextDelta,
        schemaVersion: sv,
        turnId: f.value.turnId,
        delta: f.value.delta,
      };
    case "toolCallStreamStarted":
      return {
        type: ConversationEventType.ToolCallStreamStarted,
        schemaVersion: sv,
        turnId: f.value.turnId,
        id: f.value.id,
        name: f.value.name,
      };
    case "toolCallArgsDelta":
      return {
        type: ConversationEventType.ToolCallArgsDelta,
        schemaVersion: sv,
        turnId: f.value.turnId,
        id: f.value.id,
        argsDelta: f.value.argsDelta,
      };
    case "toolCallStreamFinished":
      return {
        type: ConversationEventType.ToolCallStreamFinished,
        schemaVersion: sv,
        turnId: f.value.turnId,
        id: f.value.id,
        name: f.value.name,
        arguments: safeArgsJson(f.value.argumentsJson),
      };
    case "toolExecutionStarted":
      return {
        type: ConversationEventType.ToolExecutionStarted,
        schemaVersion: sv,
        turnId: f.value.turnId,
        toolCallId: f.value.toolCallId,
        name: f.value.name,
        clientWaitNonce: f.value.clientWaitNonce,
        // safeArgsJson yields {} for a truncated payload (not valid JSON) as
        // well as for an absent one, so argumentsText is carried alongside for
        // a display fallback.
        arguments: safeArgsJson(f.value.argumentsJson ?? ""),
        argumentsTruncated: f.value.argumentsTruncated,
        argumentsText: f.value.argumentsJson,
      };
    case "toolExecutionFinished":
      return {
        type: ConversationEventType.ToolExecutionFinished,
        schemaVersion: sv,
        turnId: f.value.turnId,
        toolCallId: f.value.toolCallId,
        name: f.value.name,
        success: f.value.success,
        errorMessage: f.value.errorMessage,
      };
    case "assistantTurnFinished":
      return {
        type: ConversationEventType.AssistantTurnFinished,
        schemaVersion: sv,
        turnId: f.value.turnId,
        assistantMessageId: f.value.assistantMessageId,
        finishReason: f.value.finishReason,
        usage:
          f.value.usage !== undefined
            ? {
                inputTokens: num(f.value.usage.inputTokens),
                outputTokens: num(f.value.usage.outputTokens),
                cacheReadTokens: num(f.value.usage.cacheReadTokens),
                cacheWriteTokens: num(f.value.usage.cacheWriteTokens),
                reasoningTokens: num(f.value.usage.reasoningTokens),
              }
            : undefined,
        modelDurationMs: num(f.value.modelDurationMs),
      };
    case "assistantTurnFailed":
      return {
        type: ConversationEventType.AssistantTurnFailed,
        schemaVersion: sv,
        turnId: f.value.turnId,
        error: f.value.error,
      };
    case "conversationAborted":
      return {
        type: ConversationEventType.ConversationAborted,
        schemaVersion: sv,
        reason: f.value.reason,
      };
    case "sessionStatus":
      return {
        type: ConversationEventType.SessionStatus,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        headline: f.value.headline,
      };
    case "sessionTitleChanged":
      return {
        type: ConversationEventType.SessionTitleChanged,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        title: f.value.title,
      };
    case "todosUpdated":
      return {
        type: ConversationEventType.TodosUpdated,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        todos: safeTodosJson(f.value.todosJson),
      };
    case "queueChanged":
      return {
        type: ConversationEventType.QueueChanged,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        queue: (f.value.queue ?? []).map(queuedMessageFromFrame),
        paused: f.value.paused,
      };
    case "turnQueued":
      if (!f.value.entry) return undefined;
      return {
        type: ConversationEventType.TurnQueued,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        entry: queuedMessageFromFrame(f.value.entry),
      };
    case "attachmentUpserted":
      return {
        type: ConversationEventType.AttachmentUpserted,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        messageId: f.value.messageId || undefined,
        attachment: {
          id: f.value.attachmentId,
          kind: f.value.kind,
          payloadJson: f.value.payloadJson,
          ...(f.value.afterToolCallId ? { afterToolCallId: f.value.afterToolCallId } : {}),
        },
      };
    case "attachmentDeleted":
      return {
        type: ConversationEventType.AttachmentDeleted,
        schemaVersion: sv,
        sessionId: f.value.sessionId,
        attachmentId: f.value.attachmentId,
      };
    default:
      // streamComplete is consumed by the callers directly, not mapped.
      return undefined;
  }
}
