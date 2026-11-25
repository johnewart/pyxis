import { create } from "@bufbuild/protobuf";
import {
  AssistantMessage,
  SystemMessage,
  ToolMessage,
  UserMessage,
  type Message,
} from "./messages.js";
import {
  MessageSchema,
  type AssistantReply,
  type Message as ProtoMessage,
  type TokenUsage,
  type ToolCall as ProtoToolCall,
} from "./gen/llmv1/llm_pb.js";
import { grpcSafeString } from "./utf8.js";

/**
 * Token usage and model time for one assistant message. Carried on the
 * message as `metadata[MESSAGE_USAGE_METADATA_KEY]` when llmd recorded it.
 */
export type MessageUsage = {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  /** Reasoning/thinking tokens; absent when the provider did not report them. */
  reasoningTokens?: number;
  /** Wall time of the model call that produced the message, in ms; absent when not recorded. */
  modelDurationMs?: number;
};

/** The `metadata` key an assistant message's {@link MessageUsage} is stored under. */
export const MESSAGE_USAGE_METADATA_KEY = "usage";

/**
 * Builds a {@link MessageUsage} from wire token usage and model time; undefined
 * when neither was recorded.
 */
export function messageUsageFromProto(
  usage: TokenUsage | undefined,
  modelDurationMs: bigint | undefined
): MessageUsage | undefined {
  if (!usage && modelDurationMs === undefined) return undefined;
  const n = (v: bigint | undefined) => (v === undefined ? 0 : Number(v));
  return {
    inputTokens: n(usage?.inputTokens),
    outputTokens: n(usage?.outputTokens),
    cacheReadTokens: n(usage?.cacheReadTokens),
    cacheWriteTokens: n(usage?.cacheWriteTokens),
    ...(usage?.reasoningTokens !== undefined ? { reasoningTokens: Number(usage.reasoningTokens) } : {}),
    ...(modelDurationMs !== undefined ? { modelDurationMs: Number(modelDurationMs) } : {}),
  };
}

export function safeJsonRecord(s: string | undefined): Record<string, unknown> {
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

export function libraryMessageToProto(m: Message): ProtoMessage {
  const base = {
    content: grpcSafeString(m.content ?? ""),
    id: grpcSafeString(m.id ?? ""),
    thinkingContent: "",
    toolCallId: "",
  };
  if (m.role === "system") {
    return create(MessageSchema, { ...base, role: "system" });
  }
  if (m.role === "user") {
    // Structural, NOT `instanceof UserMessage`: @pyxis-llm/react constructs its
    // user messages from a vendored copy of the class (scripts/
    // vendor-shared-types.mjs), so instanceof is false there and attachments
    // would be silently dropped.
    const raw = (m as { attachments?: unknown }).attachments;
    const list = Array.isArray(raw) ? (raw as NonNullable<UserMessage["attachments"]>) : [];
    const attachments = list.map((a) => ({
      id: grpcSafeString(a.id),
      kind: grpcSafeString(a.kind),
      payloadJson: grpcSafeString(a.payloadJson ?? ""),
      ...(a.afterToolCallId ? { afterToolCallId: grpcSafeString(a.afterToolCallId) } : {}),
      content: grpcSafeString(a.content ?? ""),
    }));
    return create(MessageSchema, { ...base, role: "user", attachments });
  }
  if (m.role === "tool" && m instanceof ToolMessage) {
    return create(MessageSchema, {
      ...base,
      role: "tool",
      toolCallId: grpcSafeString(m.toolCallId),
    });
  }
  if (m.role === "assistant" && m instanceof AssistantMessage) {
    const thinking =
      m.metadata && typeof m.metadata.thinking === "string"
        ? grpcSafeString(m.metadata.thinking as string)
        : "";
    const toolCalls = (m.toolCalls ?? []).map((tc) => ({
      id: grpcSafeString(tc.id),
      name: grpcSafeString(tc.name),
      argumentsJson: grpcSafeString(JSON.stringify(tc.arguments ?? {})),
      thoughtSignature:
        tc.thoughtSignature && tc.thoughtSignature.length > 0
          ? new Uint8Array(tc.thoughtSignature)
          : new Uint8Array(0),
    }));
    return create(MessageSchema, {
      ...base,
      role: "assistant",
      thinkingContent: thinking,
      toolCalls,
    });
  }
  if (m.role === "assistant") {
    const thinking =
      m.metadata && typeof m.metadata.thinking === "string"
        ? grpcSafeString(m.metadata.thinking as string)
        : "";
    return create(MessageSchema, {
      ...base,
      role: "assistant",
      thinkingContent: thinking,
    });
  }
  if (m.role === "tool") {
    return create(MessageSchema, {
      ...base,
      role: "tool",
      toolCallId: grpcSafeString((m as ToolMessage).toolCallId ?? ""),
    });
  }
  return create(MessageSchema, { ...base, role: m.role });
}

function assistantToolCallFromProto(tc: ProtoToolCall) {
  return {
    id: tc.id,
    name: tc.name,
    arguments: safeJsonRecord(tc.argumentsJson),
    thoughtSignature:
      tc.thoughtSignature && tc.thoughtSignature.length > 0
        ? new Uint8Array(tc.thoughtSignature)
        : undefined,
  };
}

export function assistantReplyToAssistantMessage(
  reply: AssistantReply
): AssistantMessage {
  const id = reply.id?.trim() ? reply.id : `asst_${Date.now()}`;
  const msg = new AssistantMessage(reply.content ?? "", id);
  const tcs = reply.toolCalls ?? [];
  if (tcs.length > 0) {
    msg.toolCalls = tcs.map(assistantToolCallFromProto);
  }
  if (reply.finishReason?.trim()) msg.finishReason = reply.finishReason;
  const think = reply.thinkingContent?.trim();
  if (think) msg.metadata = { ...msg.metadata, thinking: think };
  return msg;
}

/** Maps a persisted `llm.v1` assistant message to {@link AssistantMessage}. */
export function protoMessageToAssistantMessage(m: ProtoMessage): AssistantMessage {
  const id = m.id?.trim() ? m.id : `asst_${Date.now()}`;
  const msg = new AssistantMessage(m.content ?? "", id);
  const tcs = m.toolCalls ?? [];
  if (tcs.length > 0) {
    msg.toolCalls = tcs.map(assistantToolCallFromProto);
  }
  // Carry the persisted completion status. Dropping it here defeated the whole
  // point of llmd storing it: a reply it recorded as `cancelled` (a turn the
  // user interrupted) came back from getHistory() looking exactly like one the
  // model finished, so a resumed transcript rendered a truncated answer as
  // complete. The live wire mapper (assistantReplyToAssistantMessage) always
  // did this; the persisted one has to agree.
  if (m.finishReason?.trim()) msg.finishReason = m.finishReason;
  const think = m.thinkingContent?.trim();
  if (think) msg.metadata = { ...msg.metadata, thinking: think };
  // Token usage and model time llmd recorded for this model call (history
  // loads them from llmd_turn_metadata); hosts show them on the bubble.
  const usage = messageUsageFromProto(m.usage, m.modelDurationMs);
  if (usage) msg.metadata = { ...msg.metadata, [MESSAGE_USAGE_METADATA_KEY]: usage };
  return msg;
}

/**
 * Maps any persisted `llm.v1` message (any role) to a library {@link Message}
 * instance. Used to hydrate a fresh client-side transcript (e.g. resuming a
 * session after a page reload / remount) from `GetSessionHistory` — unlike
 * {@link protoMessageToAssistantMessage}, this handles user/system/tool rows too.
 */
export function protoMessageToLibraryMessage(m: ProtoMessage): Message {
  const id = m.id?.trim() ? m.id : `${m.role || "msg"}_${Date.now()}`;
  if (m.role === "assistant") {
    return protoMessageToAssistantMessage(m);
  }
  if (m.role === "user") {
    return new UserMessage(m.content ?? "", id);
  }
  if (m.role === "tool") {
    return new ToolMessage(m.content ?? "", m.toolCallId ?? "", id);
  }
  return new SystemMessage(m.content ?? "", id);
}

/** Last assistant message in wire order (e.g. tail of GetSessionHistory). */
export function lastAssistantFromProtoMessages(
  messages: ProtoMessage[] | undefined
): AssistantMessage | null {
  if (!messages?.length) return null;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m?.role === "assistant") {
      return protoMessageToAssistantMessage(m);
    }
  }
  return null;
}
