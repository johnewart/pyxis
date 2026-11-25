import { callUnary, scopeHeaders } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";
import type { SessionAttachmentRow } from "./gen/llmv1/llm_pb.js";

type BaseOpts = LlmdCallTarget & {
  /** Tenant id — carried as the `llmd-tenant-id` header. */
  tenantId: string;
  /** Optional scope id — carried as `llmd-scope-id` when non-empty. */
  scopeId?: string;
  principalId?: string;
  sessionId: string;
};

export type LlmdAttachmentInput = {
  id: string;
  kind: string;
  payloadJson: string;
  afterToolCallId?: string;
  /** Provider-facing text; empty = display-only (unchanged historical behavior). */
  content?: string;
};

export type UpsertLlmdSessionAttachmentOptions = BaseOpts & {
  anchorMessageId: string;
  attachment: LlmdAttachmentInput;
};

/** Persist or replace one UI attachment row anchored to a message's turn (canonical llmd store). */
export async function upsertLlmdSessionAttachment(
  opts: UpsertLlmdSessionAttachmentOptions
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.upsertSessionAttachment(
        {
          sessionId: opts.sessionId.trim(),
          anchorMessageId: opts.anchorMessageId.trim(),
          attachment: {
            id: opts.attachment.id,
            kind: opts.attachment.kind,
            payloadJson: opts.attachment.payloadJson,
            ...(opts.attachment.afterToolCallId
              ? { afterToolCallId: opts.attachment.afterToolCallId }
              : {}),
            ...(opts.attachment.content ? { content: opts.attachment.content } : {}),
          },
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "upsertSessionAttachment" }
  );
}

/** Remove one attachment row from a session's canonical llmd store. */
export async function deleteLlmdSessionAttachment(
  opts: BaseOpts & { attachmentId: string }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.deleteSessionAttachment(
        {
          sessionId: opts.sessionId.trim(),
          attachmentId: opts.attachmentId.trim(),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "deleteSessionAttachment" }
  );
}

/** Splice updated content into a persisted assistant message (e.g. inline attachment tags after flush). */
export async function patchLlmdAssistantMessageContent(
  opts: BaseOpts & { messageId: string; content: string; turnId?: string }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.patchAssistantMessageContent(
        {
          sessionId: opts.sessionId.trim(),
          messageId: opts.messageId.trim(),
          content: opts.content,
          ...(opts.turnId?.trim() ? { turnId: opts.turnId.trim() } : {}),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "patchAssistantMessageContent" }
  );
}

/**
 * Fetch the raw attachment rows for a session (mirrors llmd_chat_messages
 * kind="attachment"). The server populates these independently of the message window,
 * so the small maxMessages here only bounds the message payload, not the attachments.
 */
export async function getLlmdSessionAttachmentRows(
  opts: BaseOpts & { maxMessages?: number }
): Promise<SessionAttachmentRow[]> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.getSessionHistory(
        {
          sessionId: opts.sessionId.trim(),
          maxMessages: opts.maxMessages ?? 10_000,
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "getSessionHistory" }
  );
  return res.attachmentRows ?? [];
}
