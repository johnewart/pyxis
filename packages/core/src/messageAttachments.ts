/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Generic attachment envelope over llmd `Message.metadata`, plus a plugin
 * surface for host-specific attachment kinds (a host's build-target
 * suggestions, agent handoffs, embedded build-run cards, etc.; Owlbert's own
 * kinds). Each host may have entirely different attachment/tracked-document
 * kinds, so kind-specific behavior (own transcript row, inline rendering,
 * anchor resolution) is supplied by the host as an `AttachmentKindPlugin[]`
 * rather than hardcoded here — this module only knows the generic envelope
 * and the mechanics of folding it into a `Message[]` transcript.
 */

import type { Message } from '@pyxis-llm/client';
import { formatAttachmentTag, stripAgentControlTagsFromAssistantContent, stripAttachmentTags } from './messageContentTags.js';

/** llmd_chat_messages.kind for persisted message attachments. */
export const LLMD_CHAT_MSG_KIND_ATTACHMENT = 'attachment';

export type LlmdMessageAttachment = {
  id: string;
  kind: string;
  payloadJson: string;
  afterToolCallId?: string;
  /**
   * The user message that opened the turn this attachment was made in (llmd
   * records it; the live driver fills it in until a reload). Places a card
   * whose `afterToolCallId` is not in the transcript, which is always the case
   * for tools a CLI provider calls over MCP: such a card goes at the end of
   * this turn instead of the end of the whole transcript.
   */
  turnMessageId?: string;
  /**
   * Provider-facing text (mirrors llmv1.MessageAttachment.content). llmd
   * splices this into the outbound message at send-time when non-empty; it
   * is NEVER part of the persisted message content the client renders.
   * Empty/absent = display-only (payloadJson only), the historical behavior.
   */
  content?: string;
};

const ATTACHMENTS_METADATA_KEY = 'llmdAttachments';

/**
 * Host-supplied behavior for one attachment `kind`. A host registers one of
 * these per kind it defines; this module never special-cases a kind by name.
 */
export type AttachmentKindPlugin = {
  kind: string;
  /** False for kinds that never render at an inline `<attachment:id/>` tag (e.g. a toast). Default true. */
  rendersInline?: boolean;
  /** For kinds that get their own transcript row instead of/in addition to an inline tag (e.g. an embedded run card). */
  ownRowFromAttachment?: (attachment: LlmdMessageAttachment) => Message | undefined;
  /**
   * A record, not a prompt (e.g. an image or chart a tool showed): always
   * visible, never gated on a pending status. When no message references it
   * with an inline tag, {@link appendAnchorlessCardMessages} gives it a row of
   * its own right after the tool result named by `afterToolCallId` (or after
   * the assistant turn that made that call), or at the end of the transcript
   * when that tool call is not in it (e.g. mid-turn, before history reloads).
   */
  persistent?: boolean;
  /**
   * Renders exactly once, in a row of its own at the end of the turn it was
   * made in (just before the next user message, or at the end of the
   * transcript while that turn is still running), never at an inline
   * `<attachment:id/>` tag. The turn is found from `turnMessageId`, else from
   * the assistant message whose `toolCalls` include `afterToolCallId`. Unlike
   * {@link persistent}, the card is placed even when a message references its
   * tag; the inline tag renders nothing.
   */
  endOfTurn?: boolean;
  /** True for a message this plugin synthesized (own row or anchorless row), so generic passes can exclude/dedupe it. */
  isOwnRow?: (message: Message) => boolean;
  /** Kind-specific anchor lookup, tried after the generic afterToolCallId resolution fails. */
  resolveAnchor?: (
    messages: Message[],
    attachment: LlmdMessageAttachment
  ) => string | undefined;
};

function pluginForKind(
  plugins: readonly AttachmentKindPlugin[],
  kind: string
): AttachmentKindPlugin | undefined {
  return plugins.find((p) => p.kind === kind);
}

function isAnyPluginOwnRow(
  message: Message,
  plugins: readonly AttachmentKindPlugin[]
): boolean {
  return plugins.some((p) => p.isOwnRow?.(message));
}

export function attachmentsFromMessageMetadata(
  metadata: unknown
): LlmdMessageAttachment[] | undefined {
  if (metadata == null || typeof metadata !== 'object') return undefined;
  const raw = (metadata as Record<string, unknown>)[ATTACHMENTS_METADATA_KEY];
  if (!Array.isArray(raw) || raw.length === 0) return undefined;
  const out: LlmdMessageAttachment[] = [];
  for (const item of raw) {
    if (item == null || typeof item !== 'object') continue;
    const rec = item as Record<string, unknown>;
    const id = typeof rec.id === 'string' ? rec.id.trim() : '';
    const kind = typeof rec.kind === 'string' ? rec.kind.trim() : '';
    const payloadJson =
      typeof rec.payloadJson === 'string'
        ? rec.payloadJson
        : typeof rec.payload_json === 'string'
          ? rec.payload_json
          : '';
    if (!id || !kind || !payloadJson) continue;
    const afterToolCallId =
      typeof rec.afterToolCallId === 'string'
        ? rec.afterToolCallId
        : typeof rec.after_tool_call_id === 'string'
          ? rec.after_tool_call_id
          : undefined;
    const content = typeof rec.content === 'string' ? rec.content : undefined;
    out.push({
      id,
      kind,
      payloadJson,
      ...(afterToolCallId?.trim() ? { afterToolCallId: afterToolCallId.trim() } : {}),
      ...(content ? { content } : {}),
    });
  }
  return out.length > 0 ? out : undefined;
}

export function messageWithAttachmentsMetadata(
  message: Message,
  attachments: LlmdMessageAttachment[] | undefined
): Message {
  if (!attachments?.length) return message;
  return {
    ...message,
    metadata: {
      ...(message.metadata ?? {}),
      [ATTACHMENTS_METADATA_KEY]: attachments,
    },
  };
}

export function stripAttachmentsFromMessage(message: Message): Message {
  const attachments = attachmentsFromMessageMetadata(message.metadata);
  if (!attachments?.length) return message;
  const meta = { ...(message.metadata ?? {}) } as Record<string, unknown>;
  delete meta[ATTACHMENTS_METADATA_KEY];
  const cleaned = Object.keys(meta).length > 0 ? meta : undefined;
  return { ...message, metadata: cleaned };
}

export function sessionHasAttachmentKind(
  messages: Message[],
  kind: string
): boolean {
  return messages.some((m) =>
    (attachmentsFromMessageMetadata(m.metadata) ?? []).some((a) => a.kind === kind)
  );
}

/**
 * Expand attachments into placement tags / own rows in transcript order.
 * Inline cards render ONLY at an `<attachment:id/>` tag inside assistant
 * content; kinds with `ownRowFromAttachment` get a dedicated row instead.
 */
export function hydrateMessageAttachments(
  messages: Message[],
  plugins: readonly AttachmentKindPlugin[] = []
): Message[] {
  const out: Message[] = [];
  const ownRowIds = new Set<string>();
  let i = 0;
  while (i < messages.length) {
    const m = messages[i]!;
    if (m.role !== 'assistant') {
      out.push(m);
      i += 1;
      continue;
    }

    const attachments = attachmentsFromMessageMetadata(m.metadata);
    let hydrated = m;
    if (attachments?.length) {
      const content = typeof m.content === 'string' ? m.content : '';
      const missing: string[] = [];
      for (const attachment of attachments) {
        const plugin = pluginForKind(plugins, attachment.kind);
        if (plugin?.ownRowFromAttachment) continue; // own row below
        if (plugin?.rendersInline === false) continue;
        if (plugin?.endOfTurn) continue; // its own row at the end of the turn
        const tag = formatAttachmentTag(attachment.id).trim();
        if (content.includes(tag)) continue;
        missing.push(formatAttachmentTag(attachment.id));
      }
      if (missing.length > 0) {
        hydrated = { ...m, content: `${content}${missing.join('')}` } as Message;
      }
    }
    out.push(hydrated);
    i += 1;

    while (i < messages.length && messages[i]?.role === 'tool') {
      out.push(messages[i]!);
      i += 1;
    }

    if (!attachments?.length) continue;
    for (const attachment of attachments) {
      const plugin = pluginForKind(plugins, attachment.kind);
      if (!plugin?.ownRowFromAttachment) continue;
      const cardId = attachment.id.trim();
      if (out.some((row) => row.id?.trim() === cardId)) continue;
      const card = plugin.ownRowFromAttachment(attachment);
      if (card) {
        out.push(card);
        ownRowIds.add(cardId);
      }
    }
  }

  if (ownRowIds.size === 0) return out;
  const seen = new Set<string>();
  return out.filter((m) => {
    const id = m.id?.trim() ?? '';
    if (!id || !ownRowIds.has(id)) return true;
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

/**
 * Message shape including the assistant tool-call fields llmd carries at
 * runtime but which are not on the base `Message` type.
 */
export type MessageWithToolCalls = Message & {
  toolCalls?: Array<{
    id?: string;
    name?: string;
    arguments?: Record<string, unknown>;
  }>;
  toolCallId?: string;
};

/**
 * Resolve anchor assistant message id when llmd re-ids rows or the stored
 * anchor is stale. Generic `afterToolCallId` resolution first; kind-specific
 * fallback delegated to the matching plugin's `resolveAnchor`.
 */
export function resolveAttachmentAnchorMessageId(
  messages: MessageWithToolCalls[],
  attachment: LlmdMessageAttachment,
  plugins: readonly AttachmentKindPlugin[] = [],
  storedAnchorMessageId?: string
): string | undefined {
  const stored = storedAnchorMessageId?.trim();
  if (stored && messages.some((m) => m.id?.trim() === stored)) return stored;

  const afterToolCallId = attachment.afterToolCallId?.trim();
  if (afterToolCallId) {
    for (const m of messages) {
      if (m.role !== 'assistant') continue;
      const matched = (m.toolCalls ?? []).some(
        (tc) => tc.id?.trim() === afterToolCallId
      );
      if (matched) return m.id?.trim();
    }
    for (const m of messages) {
      if (m.role === 'tool' && m.toolCallId?.trim() === afterToolCallId) {
        for (let i = messages.indexOf(m) - 1; i >= 0; i--) {
          if (messages[i]?.role === 'assistant') {
            return messages[i]?.id?.trim();
          }
        }
      }
    }
  }

  return pluginForKind(plugins, attachment.kind)?.resolveAnchor?.(
    messages,
    attachment
  );
}

/** Find the assistant turn that invoked a named tool (latest match). */
export function findAssistantAnchorForToolCall(
  messages: MessageWithToolCalls[],
  toolName: string,
  matchArgs?: { repo?: string; name?: string; target?: string }
): { anchorMessageId: string; toolCallId: string } | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]!;
    if (m.role !== 'assistant') continue;
    const toolCalls = m.toolCalls ?? [];
    for (const tc of toolCalls) {
      if (tc.name !== toolName) continue;
      const args = tc.arguments ?? {};
      if (
        matchArgs?.repo != null &&
        'repo' in args &&
        args.repo !== matchArgs.repo
      ) {
        continue;
      }
      if (
        matchArgs?.name != null &&
        'name' in args &&
        args.name !== matchArgs.name
      ) {
        continue;
      }
      if (
        matchArgs?.target != null &&
        'target' in args &&
        args.target !== matchArgs.target
      ) {
        continue;
      }
      const anchorMessageId = m.id?.trim();
      const toolCallId = tc.id?.trim();
      if (anchorMessageId && toolCallId) {
        return { anchorMessageId, toolCallId };
      }
    }
  }
  return undefined;
}

function stripOwnRowForAttachment(
  messages: Message[],
  attachmentId: string,
  plugins: readonly AttachmentKindPlugin[]
): Message[] {
  const id = attachmentId.trim();
  if (!id) return messages;
  return messages.filter(
    (m) => !(isAnyPluginOwnRow(m, plugins) && m.id?.trim() === id)
  );
}

export function upsertAttachmentOnAnchorMessage(
  messages: Message[],
  anchorMessageId: string,
  attachment: LlmdMessageAttachment,
  plugins: readonly AttachmentKindPlugin[] = []
): Message[] {
  const plugin = pluginForKind(plugins, attachment.kind);
  const stripped = plugin?.ownRowFromAttachment
    ? stripOwnRowForAttachment(messages, attachment.id, plugins)
    : messages;
  const updated = stripped.map((message) => {
    if (message.id?.trim() !== anchorMessageId) return message;
    const existing = attachmentsFromMessageMetadata(message.metadata) ?? [];
    const merged = [...existing];
    const idx = merged.findIndex((a) => a.id === attachment.id);
    if (idx >= 0) merged[idx] = attachment;
    else merged.push(attachment);
    return messageWithAttachmentsMetadata(message, merged);
  });
  return hydrateMessageAttachments(updated, plugins);
}

export function removeAttachmentFromTranscript(
  messages: Message[],
  attachmentId: string,
  plugins: readonly AttachmentKindPlugin[] = []
): Message[] {
  const updated = messages.map((message) => {
    const existing = attachmentsFromMessageMetadata(message.metadata);
    if (!existing?.some((a) => a.id === attachmentId)) return message;
    const merged = existing.filter((a) => a.id !== attachmentId);
    if (!merged.length) return stripAttachmentsFromMessage(message);
    return messageWithAttachmentsMetadata(message, merged);
  });
  return hydrateMessageAttachments(updated, plugins);
}

/**
 * Prefix for synthetic assistant rows that host a card with no anchor
 * message. Never a real llmd message id — anything that writes back to llmd
 * must ignore rows with this prefix.
 */
export const ANCHORLESS_CARD_MESSAGE_ID_PREFIX = 'llmd-anchorless-card:';

/** True for a view-only row minted by {@link appendAnchorlessCardMessages}. */
export function messageIsAnchorlessCardRow(message: Message): boolean {
  return (
    message.role === 'assistant' &&
    (message.id ?? '').startsWith(ANCHORLESS_CARD_MESSAGE_ID_PREFIX)
  );
}

/**
 * True only for a card still awaiting the user: `metadata.status === 'pending'`.
 * Gate on minting synthetic anchorless rows — a resolved card or one with no
 * pending state at all (e.g. a record of something that already happened)
 * must not get one, or they pile up at the bottom of the transcript forever.
 */
function attachmentIsPendingCard(attachment: LlmdMessageAttachment): boolean {
  try {
    const payload = JSON.parse(attachment.payloadJson ?? '{}') as {
      status?: unknown;
    };
    return payload?.status === 'pending';
  } catch {
    return false;
  }
}

/**
 * Where a persistent card's own row goes: right after the tool result for
 * `afterToolCallId`, else after the assistant turn that made that call (and
 * its tool rows), else at the end of the turn opened by `turnMessageId`
 * (just before the next user message), else at the end. Rows already placed
 * at that spot keep their order (the new one goes after them).
 */
function persistentCardRowIndex(messages: Message[], attachment: LlmdMessageAttachment): number {
  const rows = messages as MessageWithToolCalls[];
  const callId = attachment.afterToolCallId?.trim();
  let idx = callId ? rows.findIndex((m) => m.role === 'tool' && m.toolCallId?.trim() === callId) : -1;
  if (idx >= 0) {
    idx += 1;
  } else if (callId) {
    idx = rows.findIndex(
      (m) => m.role === 'assistant' && (m.toolCalls ?? []).some((tc) => tc.id?.trim() === callId)
    );
    if (idx >= 0) {
      idx += 1;
      while (idx < rows.length && rows[idx]?.role === 'tool') idx += 1;
    }
  }
  if (idx < 0) {
    const turnId = attachment.turnMessageId?.trim();
    const start = turnId ? rows.findIndex((m) => m.role === 'user' && m.id?.trim() === turnId) : -1;
    if (start < 0) return messages.length;
    idx = start + 1;
    while (idx < rows.length && rows[idx]?.role !== 'user') idx += 1;
    return idx;
  }
  while (idx < rows.length && messageIsAnchorlessCardRow(rows[idx]!)) idx += 1;
  return idx;
}

/**
 * Where an end-of-turn card's row goes: just before the user message that
 * follows its turn, or at the end when the turn is still running or cannot
 * be found. The turn is the one opened by `turnMessageId`, else the one
 * holding the assistant message whose toolCalls include `afterToolCallId`.
 * Rows already placed at that spot keep their order.
 */
function endOfTurnRowIndex(messages: Message[], attachment: LlmdMessageAttachment): number {
  const rows = messages as MessageWithToolCalls[];
  let start = -1;
  const turnId = attachment.turnMessageId?.trim();
  if (turnId) start = rows.findIndex((m) => m.role === 'user' && m.id?.trim() === turnId);
  if (start < 0) {
    const callId = attachment.afterToolCallId?.trim();
    const call = callId
      ? rows.findIndex((m) => m.role === 'assistant' && (m.toolCalls ?? []).some((tc) => tc.id?.trim() === callId))
      : -1;
    if (call >= 0) {
      start = call;
    } else {
      // No usable turn id (loaded from history, or a stale live guess): use
      // the turn of the message that carries the card's tag or metadata.
      const tag = formatAttachmentTag(attachment.id).trim();
      const tagged = rows.findIndex(
        (m) =>
          !messageIsAnchorlessCardRow(m) &&
          ((typeof m.content === 'string' && m.content.includes(tag)) ||
            (attachmentsFromMessageMetadata(m.metadata) ?? []).some((a) => a.id === attachment.id))
      );
      if (tagged < 0) return messages.length;
      start = tagged;
    }
  }
  let idx = start + 1;
  while (idx < rows.length && rows[idx]?.role !== 'user') idx += 1;
  return idx;
}

/**
 * Give cards that no message references a row of their own, so they never
 * silently vanish:
 *  - still-pending cards (an ask card parked before llmd committed any
 *    assistant message) get one at the end of the transcript;
 *  - persistent kinds (`AttachmentKindPlugin.persistent`, e.g. images and
 *    charts) always get one, placed after the tool result that produced them.
 *
 * Kind behavior comes only from `plugins`. A host calling this directly (not
 * through ChatPanel) should pass `withLlmdAttachmentKindPlugins(hostPlugins)`
 * so llmd's own kinds are included.
 */
export function appendAnchorlessCardMessages(
  messages: Message[],
  attachmentIndex: Map<string, LlmdMessageAttachment> | undefined,
  plugins: readonly AttachmentKindPlugin[] = []
): Message[] {
  if (!attachmentIndex?.size) return messages;

  const referenced = new Set<string>();
  for (const m of messages) {
    for (const a of attachmentsFromMessageMetadata(m.metadata) ?? []) {
      referenced.add(a.id);
    }
    const content = typeof m.content === 'string' ? m.content : '';
    if (!content) continue;
    for (const id of attachmentIndex.keys()) {
      if (content.includes(formatAttachmentTag(id).trim())) referenced.add(id);
    }
  }

  let out = messages;
  for (const attachment of attachmentIndex.values()) {
    const plugin = pluginForKind(plugins, attachment.kind);
    if (plugin?.endOfTurn && plugin.rendersInline !== false) {
      const rowId = `${ANCHORLESS_CARD_MESSAGE_ID_PREFIX}${attachment.id}`;
      if (out.some((m) => m.id?.trim() === rowId)) continue;
      const row = messageWithAttachmentsMetadata(
        { role: 'assistant', content: formatAttachmentTag(attachment.id), id: rowId } as Message,
        [attachment]
      );
      const at = endOfTurnRowIndex(out, attachment);
      out = [...out.slice(0, at), row, ...out.slice(at)];
      continue;
    }
    if (referenced.has(attachment.id)) continue;
    if (plugin?.rendersInline === false) continue;
    if (plugin?.persistent) {
      const rowId = `${ANCHORLESS_CARD_MESSAGE_ID_PREFIX}${attachment.id}`;
      if (out.some((m) => m.id?.trim() === rowId)) continue;
      const row = messageWithAttachmentsMetadata(
        { role: 'assistant', content: formatAttachmentTag(attachment.id), id: rowId } as Message,
        [attachment]
      );
      const at = persistentCardRowIndex(out, attachment);
      out = [...out.slice(0, at), row, ...out.slice(at)];
      continue;
    }
    if (!attachmentIsPendingCard(attachment)) continue;
    // Answered (or answer queued): the card's job is done, so it gets no row.
    // Its record is the "Answer to: …" user message (and its chip), not a
    // card re-pinned under the newest message on every render. Checked here,
    // not only via the card's own status, so a card answered before llmd
    // marked cards resolved (or whose answer is still queued) also clears.
    if (attachmentIndex.has(`${attachment.id}:response`)) continue;
    const rowId = `${ANCHORLESS_CARD_MESSAGE_ID_PREFIX}${attachment.id}`;
    if (out.some((m) => m.id?.trim() === rowId)) continue;
    out = [
      ...out,
      messageWithAttachmentsMetadata(
        {
          role: 'assistant',
          content: formatAttachmentTag(attachment.id),
          id: rowId,
        } as Message,
        [attachment]
      ),
    ];
  }
  return out;
}

/**
 * Live attachment changes seen on the session-events stream since the host
 * last loaded attachments: an attachment for an upsert, `null` for a delete.
 * Produced by `useLlmdChatDriver` (`driver.liveAttachments`).
 */
export type LiveAttachmentChanges = ReadonlyMap<string, LlmdMessageAttachment | null>;

/**
 * Overlays live attachment changes on a host-loaded attachment index. Live
 * entries win (they are newer than any fetch), except for `turnMessageId`:
 * the loaded one is what llmd recorded, the live one only the driver's guess.
 * `null` removes. Returns `base` unchanged when there is nothing to apply, so
 * memoized consumers stay stable.
 */
export function mergeLiveAttachments(
  base: Map<string, LlmdMessageAttachment> | undefined,
  live: LiveAttachmentChanges | undefined
): Map<string, LlmdMessageAttachment> {
  const start = base ?? new Map<string, LlmdMessageAttachment>();
  if (!live?.size) return start;
  const out = new Map(start);
  for (const [id, attachment] of live) {
    if (!attachment) {
      out.delete(id);
      continue;
    }
    const recordedTurn = start.get(id)?.turnMessageId;
    out.set(id, recordedTurn ? { ...attachment, turnMessageId: recordedTurn } : attachment);
  }
  return out;
}

/** Messages for LLM provider history: no own-row cards, attachment payloads, or placement tags. */
export function transcriptMessagesForProvider(
  messages: Message[],
  plugins: readonly AttachmentKindPlugin[] = []
): Message[] {
  return messages
    .filter((m) => !isAnyPluginOwnRow(m, plugins))
    .filter((m) => !messageIsAnchorlessCardRow(m))
    .map((m) => {
      const stripped = stripAttachmentsFromMessage(m);
      if (stripped.role !== 'assistant' && stripped.role !== 'user') {
        return stripped;
      }
      const content = stripAgentControlTagsFromAssistantContent(
        stripAttachmentTags(String(stripped.content ?? ''))
      );
      return content === stripped.content ? stripped : { ...stripped, content };
    });
}

export function llmdMessageAttachmentFromRowContent(
  content: string
): (LlmdMessageAttachment & { anchorMessageId?: string }) | undefined {
  if (!content.trim()) return undefined;
  try {
    const parsed = JSON.parse(content) as Record<string, unknown>;
    const id = typeof parsed.id === 'string' ? parsed.id.trim() : '';
    const kind = typeof parsed.kind === 'string' ? parsed.kind.trim() : '';
    const payloadJson =
      typeof parsed.payloadJson === 'string'
        ? parsed.payloadJson
        : typeof parsed.payload_json === 'string'
          ? parsed.payload_json
          : '';
    if (!id || !kind || !payloadJson) return undefined;
    const afterToolCallId =
      typeof parsed.afterToolCallId === 'string'
        ? parsed.afterToolCallId
        : typeof parsed.after_tool_call_id === 'string'
          ? parsed.after_tool_call_id
          : undefined;
    const anchorMessageId =
      typeof parsed.anchorMessageId === 'string'
        ? parsed.anchorMessageId
        : typeof parsed.anchor_message_id === 'string'
          ? parsed.anchor_message_id
          : undefined;
    const turnMessageId =
      typeof parsed.turnMessageId === 'string'
        ? parsed.turnMessageId
        : typeof parsed.turn_message_id === 'string'
          ? parsed.turn_message_id
          : undefined;
    const attachmentContent = typeof parsed.content === 'string' ? parsed.content : undefined;
    return {
      id,
      kind,
      payloadJson,
      ...(afterToolCallId?.trim() ? { afterToolCallId: afterToolCallId.trim() } : {}),
      ...(anchorMessageId?.trim()
        ? { anchorMessageId: anchorMessageId.trim() }
        : {}),
      ...(turnMessageId?.trim() ? { turnMessageId: turnMessageId.trim() } : {}),
      ...(attachmentContent ? { content: attachmentContent } : {}),
    };
  } catch {
    return undefined;
  }
}

export function llmdMessageAttachmentRowContent(
  attachment: LlmdMessageAttachment,
  anchorMessageId: string
): string {
  return JSON.stringify({
    id: attachment.id,
    kind: attachment.kind,
    payload_json: attachment.payloadJson,
    anchor_message_id: anchorMessageId,
    ...(attachment.afterToolCallId
      ? { after_tool_call_id: attachment.afterToolCallId }
      : {}),
    ...(attachment.turnMessageId ? { turn_message_id: attachment.turnMessageId } : {}),
    ...(attachment.content ? { content: attachment.content } : {}),
  });
}
