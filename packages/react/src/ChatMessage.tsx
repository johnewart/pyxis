/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Renders one transcript message: role-tagged bubble, markdown/paste-aware
 * content, tracked-context badges, and an optional footer row (decoration
 * chips + token usage). This is intentionally NOT a 1:1 port of any host's
 * chat bubble — it owns only what every llmd-backed chat surface needs
 * (markdown, attachment placement tags, paste fences, raw/clean toggle,
 * token usage) and exposes slots for everything host-specific:
 *
 *   - `renderCustomMessage`: a message that should replace the whole bubble
 *     (e.g. a host's embedded-run card keyed off message.metadata).
 *   - cards at inline `<attachment:id/>` tags: ask cards are built in;
 *     everything else (display cards, a host's own kinds) renders through the
 *     plugin registered for it (see plugins.tsx). This component only owns
 *     placement (see @pyxis-llm/core's messageContentTags/messageAttachments).
 *   - `renderDecorations`: how per-turn TurnDecorationView[] (e.g. a host's
 *     graph-provenance badges) render in the footer, next to token usage.
 *   - `transformVisibleContent`: last-mile cleanup of assistant content
 *     beyond the generic control-tag stripping this component always does
 *     (e.g. a host-specific tag-pinning convention).
 *
 * A host that supplies none of these gets a plain, fully-functional bubble
 * with attachments rendered as an inert placeholder — which is the "vanilla
 * + attachments" baseline a minimal host can ship as-is.
 */

import React, { useEffect, useRef, useState, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import {
  ActionIcon,
  Badge,
  Code,
  Group,
  Popover,
  ScrollArea,
  Text,
  Tooltip as MantineTooltip,
} from '@mantine/core';
import { IconTrash, IconEye, IconEyeOff, IconFileText } from '@tabler/icons-react';
import {
  parseMessageContent,
  stripAgentControlTagsFromAssistantContent,
  decodeAskCard,
  decodeAskCardResponse,
  messageIsAnchorlessCardRow,
  ASK_CARD_KIND,
  ASK_CARD_RESPONSE_KIND,
  type AskCardResponse,
  type LlmdMessageAttachment,
  type TrackedDocumentRefView,
  type TurnDecorationView,
} from '@pyxis-llm/core';
import { markdownCodeComponents } from './MarkdownCodeComponents.js';
import { useLlmdPlugins } from './plugins.js';
import { useMarkdownPlugins } from './markdownPlugins.js';
import { MessageContentWithPastes } from './MessageContentWithPastes.js';
import { messageHasPasteFences } from './pasteFences.js';
import { bindMarkdownRenderedCopy } from './markdownClipboard.js';
import {
  AskCardErrorBoundary,
  AskCardUnavailable,
  AskCardView,
  type AskCardSubmitOptions,
} from './AskCardView.js';
import { AskCardAnswerChip } from './AskCardAnswerChip.js';
import { PluginAttachment } from './DisplayCards.js';

/** Library (SerializedMessage) or legacy shape — the minimal fields this component reads. */
/** One text segment of an assistant turn, split at tool calls (opt-in `segmentedTurns`). */
export type ChatMessageSegment = {
  text: string;
  /** The tool calls that followed this segment. */
  toolCalls?: Array<{ id?: string; name?: string }>;
};

export type ChatMessageData = {
  /** Assistant rows, only with the driver's `segmentedTurns` option: the turn's text segments. */
  segments?: ChatMessageSegment[];
  role: string;
  content: string;
  id?: string;
  metadata?: Record<string, unknown>;
  /** On `tool` rows: the call this result answers. */
  toolCallId?: string;
  /**
   * On assistant rows: the tool calls the turn made (ids/names only). Not
   * rendered; it lets the transcript place a card a tool produced right
   * after that tool's result (see @pyxis-llm/core's appendAnchorlessCardMessages).
   */
  toolCalls?: Array<{ id?: string; name?: string }>;
  providerId?: string;
};

export type ChatMessageUsage = {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  /** Reasoning/thinking tokens (TokenUsage.reasoning_tokens); absent when unreported. */
  reasoningTokens?: number;
  /** Upstream provider that served the turn (AssistantReply.served_by); absent when unknown. */
  servedBy?: string;
  /** Pre-compression token count; 0 if no compression this turn. */
  originalTokens: number;
  /** Post-compression wire count; 0 if no compression this turn. */
  compressedTokens: number;
  /** Wall time of the model call(s) that produced the message, in ms; absent when unknown. */
  modelDurationMs?: number;
};

/**
 * Reads the usage llmd recorded for a message from `metadata.usage` (where
 * @pyxis-llm/client's history mapping and useLlmdChatDriver put it; the key is
 * @pyxis-llm/client's MESSAGE_USAGE_METADATA_KEY). Undefined when there is none.
 */
export function usageFromMessageMetadata(
  metadata: Record<string, unknown> | undefined
): ChatMessageUsage | undefined {
  const raw = metadata?.usage;
  if (!raw || typeof raw !== 'object') return undefined;
  const u = raw as Record<string, unknown>;
  const opt = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
  return {
    inputTokens: opt(u.inputTokens) ?? 0,
    outputTokens: opt(u.outputTokens) ?? 0,
    cacheReadTokens: opt(u.cacheReadTokens) ?? 0,
    cacheWriteTokens: opt(u.cacheWriteTokens) ?? 0,
    reasoningTokens: opt(u.reasoningTokens),
    modelDurationMs: opt(u.modelDurationMs),
    originalTokens: 0,
    compressedTokens: 0,
  };
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

/** 14.2s under a minute, 1m 05s above. */
export function fmtModelDuration(ms: number): string {
  const seconds = ms / 1000;
  if (seconds < 59.95) return `${seconds.toFixed(1)}s`;
  const total = Math.round(seconds);
  return `${Math.floor(total / 60)}m ${String(total % 60).padStart(2, '0')}s`;
}

function TokenUsageBadge({ usage }: { usage: ChatMessageUsage }) {
  const effectiveIn = usage.inputTokens + usage.cacheReadTokens + usage.cacheWriteTokens;
  const hasTokens = effectiveIn > 0 || usage.outputTokens > 0;
  const durationMs = usage.modelDurationMs ?? 0;
  const hasTime = durationMs > 0;
  if (!hasTokens && !hasTime) return null;

  const hasCompression = usage.originalTokens > 0 && usage.compressedTokens > 0;
  const savedTokens = hasCompression ? usage.originalTokens - usage.compressedTokens : 0;

  return (
    <div className="chat-message__usage-badge">
      <span className="chat-message__usage-badge-tokens">
        {hasTokens && (
          <>
            <span className="chat-message__usage-badge-in" title={`${effectiveIn.toLocaleString()} tokens in (input + cache)`}>
              ↑&thinsp;{fmtTokens(effectiveIn)}
            </span>
            <span className="chat-message__usage-badge-sep"> · </span>
            <span className="chat-message__usage-badge-out" title={`${usage.outputTokens.toLocaleString()} tokens out`}>
              ↓&thinsp;{fmtTokens(usage.outputTokens)}
            </span>
          </>
        )}
        {hasTime && (
          <>
            {hasTokens && <span className="chat-message__usage-badge-sep"> · </span>}
            <span
              className="chat-message__usage-badge-time"
              title={`The model took ${(durationMs / 1000).toFixed(1)} seconds for this message`}
            >
              {fmtModelDuration(durationMs)}
            </span>
          </>
        )}
        {(usage.cacheReadTokens > 0 || usage.cacheWriteTokens > 0) && (
          <span
            className="chat-message__usage-badge-cache"
            title={`Cache: ${usage.cacheReadTokens.toLocaleString()} read · ${usage.cacheWriteTokens.toLocaleString()} written`}
          >
            {' '}(cache: {fmtTokens(usage.cacheReadTokens)} read / {fmtTokens(usage.cacheWriteTokens)} written)
          </span>
        )}
      </span>
      {hasCompression && (
        <span
          className="chat-message__usage-badge-compression"
          title={`Compressed tool results saved ${savedTokens.toLocaleString()} tokens (${usage.originalTokens.toLocaleString()} → ${usage.compressedTokens.toLocaleString()})`}
        >
          🗜&thinsp;{fmtTokens(savedTokens)} saved
        </span>
      )}
    </div>
  );
}

/**
 * A clickable badge that reveals, in a popover, the exact content that llmd
 * injected into the model for this turn — the session seed or a tracked-document
 * version. These live only in the model view (never in the persisted user text),
 * so this is the UI's window into what the assistant actually saw.
 */
function TrackedContextPopover({
  label,
  color,
  title,
  content,
}: {
  label: string;
  color: string;
  title: string;
  content: string;
}) {
  return (
    <Popover width={540} position="bottom-start" withArrow shadow="md">
      <Popover.Target>
        <Badge
          variant="light"
          color={color}
          size="sm"
          leftSection={<IconFileText size={11} stroke={1.6} />}
          style={{ cursor: 'pointer', textTransform: 'none' }}
        >
          {label}
        </Badge>
      </Popover.Target>
      <Popover.Dropdown>
        <Text size="xs" fw={600} mb={6}>
          {title}
        </Text>
        <ScrollArea.Autosize mah={360}>
          <Code block style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
            {content}
          </Code>
        </ScrollArea.Autosize>
      </Popover.Dropdown>
    </Popover>
  );
}

function TrackedContextBadges({
  documents,
  initialSeed,
}: {
  documents?: TrackedDocumentRefView[];
  initialSeed?: string;
}) {
  const seed = (initialSeed ?? '').trim();
  const docs = documents ?? [];
  if (!seed && docs.length === 0) return null;
  return (
    <Group gap={6} mt={6} className="chat-message__tracked-context">
      {seed && (
        <TrackedContextPopover
          label="Session context"
          color="grape"
          title="Initial session context (injected into the model on the first turn)"
          content={seed}
        />
      )}
      {docs.map((d) => (
        <TrackedContextPopover
          key={`${d.documentId}:${d.version}`}
          label={d.change === 'updated' ? `${d.name} · updated` : d.name}
          color={d.change === 'updated' ? 'yellow' : 'blue'}
          title={`${d.name} (v${d.version}, ${d.change}) — attached to the model this turn`}
          content={d.content}
        />
      ))}
    </Group>
  );
}

export type ChatMessageProps = {
  message: ChatMessageData;
  compact?: boolean;
  /** While the assistant tail is streaming, skip markdown/GFM and Prism for lower CPU. */
  streamPlainContent?: boolean;
  onDelete?: () => void;
  deleteDisabled?: boolean;
  deleteTooltip?: string;
  attachmentIndex?: Map<string, LlmdMessageAttachment>;
  /**
   * Per-turn token usage to show as a badge at the bottom of the bubble.
   * Omitted (undefined): read from the message's `metadata.usage`, which
   * useLlmdChatDriver and @pyxis-llm/client's history mapping fill in. `null`:
   * show no usage.
   */
  usage?: ChatMessageUsage | null;
  /**
   * Decorations attached to this message (e.g. graph provenance, a host's
   * pin or review comment) — rendering is entirely up to `renderDecorations`.
   * Shown for every role, including user messages; `kind`/`uri` are opaque to
   * llmd, so what a decoration *means* is the emitter's business.
   */
  decorations?: TurnDecorationView[] | null;
  /** Tracked-document versions attached to THIS user message (badges). */
  trackedDocuments?: TrackedDocumentRefView[];
  /** First-turn session seed, passed only for the first user message. */
  initialSeed?: string;
  /**
   * Replace the entire bubble for this message (e.g. a host's embedded-run
   * card). Return `undefined`/nothing to fall through to normal rendering.
   */
  renderCustomMessage?: (message: ChatMessageData) => ReactNode | undefined;
  /**
   * Chip for a host attachment SENT ON a user message (anchored to it), drawn
   * beside the ask-card answer chips. Return null/undefined for kinds the host
   * does not show this way. Unlike inline `<attachment:id/>` tags this needs no
   * marker in the message text, so nothing tag-like reaches the model.
   */
  renderUserAttachment?: (attachment: LlmdMessageAttachment, ctx: { message: ChatMessageData }) => ReactNode;
  /** Render this message's decorations in the footer (left of the token badge). */
  renderDecorations?: (decorations: TurnDecorationView[]) => ReactNode;
  /**
   * Ask-card responses, keyed by the AskCard.id they answer (build with
   * `indexAskCardResponses` from @pyxis-llm/core). Present ⇒ that card renders
   * as answered/read-only; absent ⇒ interactive.
   */
  askCardResponses?: Map<string, AskCardResponse>;
  /**
   * Called when the user submits an ask card's interactive form. The host
   * owns persisting the response (UpsertSessionAttachment); omit to render
   * ask cards read-only/disabled. `options.continueWithTools` says which
   * button was used ("Answer & continue" vs "Answer & stop").
   */
  onAskCardSubmit?: (response: AskCardResponse, options: AskCardSubmitOptions) => void;
  /**
   * Called with a card's id when the user dismisses it (the ✕ on an
   * unanswered card, and on a card that cannot be shown). The host deletes the
   * card; omit to hide the ✕. Also handed to plugin attachment renderers as
   * `dismiss()`, whatever their kind.
   */
  onAskCardDismiss?: (cardId: string) => void;
  /**
   * Last-mile cleanup of assistant content beyond the generic `<activity>`/
   * `<thinking>` stripping this component always applies (e.g. a
   * host-specific tag-pinning convention). Identity by default.
   */
  transformVisibleContent?: (content: string, message: ChatMessageData) => string;
  /**
   * Let the browser skip layout/paint for this row while it is off screen
   * (`content-visibility: auto`). Without it a long transcript is laid out in
   * full whenever anything forces layout (composer autosize, scroll-to-bottom
   * reading scrollHeight), so typing lags in proportion to conversation length.
   * ChatPanel leaves the newest rows uncontained: until a row has been laid out
   * once it reports the intrinsic-size placeholder, so scroll-to-bottom would
   * stop short. `contain-intrinsic-size: auto` remembers measured heights.
   */
  containOffscreen?: boolean;
  /**
   * Render prose as plain pre-wrap text (no markdown parse) until the row
   * comes near the viewport, then upgrade to markdown for good. Keeps opening
   * a long transcript cheap while every message stays in the DOM, so the
   * browser's find-in-page still searches the whole conversation. Only read at
   * mount: a row that already rendered markdown never downgrades when newer
   * messages push it out of the tail.
   */
  deferMarkdown?: boolean;
};

/**
 * Chips for the ask-card answers carried by this (user) message: the
 * `ask_card_response` attachments anchored to it.
 */
function AnsweredCardChips({
  message,
  attachmentIndex,
  renderUserAttachment,
}: {
  message: ChatMessageData;
  attachmentIndex?: Map<string, LlmdMessageAttachment>;
  renderUserAttachment?: ChatMessageProps['renderUserAttachment'];
}) {
  const id = message.id?.trim();
  if (!id || !attachmentIndex?.size) return null;
  const chips: ReactNode[] = [];
  for (const a of attachmentIndex.values()) {
    if ((a as { anchorMessageId?: string }).anchorMessageId?.trim() !== id) continue;
    if (a.kind !== ASK_CARD_RESPONSE_KIND) {
      const chip = renderUserAttachment?.(a, { message });
      if (chip != null && chip !== false) chips.push(<React.Fragment key={a.id}>{chip}</React.Fragment>);
      continue;
    }
    const response = decodeAskCardResponse(a.payloadJson);
    if (!response) continue;
    const cardAtt = attachmentIndex.get(response.cardId);
    const card = cardAtt ? decodeAskCard(cardAtt.payloadJson) : undefined;
    chips.push(<AskCardAnswerChip key={a.id} card={card} response={response} />);
  }
  if (!chips.length) return null;
  return (
    <Group gap={4} mt={4} className="chat-message__answered-cards">
      {chips}
    </Group>
  );
}

/** Shared (stable identity) style for `containOffscreen` rows. */
const OFFSCREEN_CONTAINMENT_STYLE = {
  contentVisibility: 'auto',
  containIntrinsicSize: 'auto 160px',
} as React.CSSProperties;

function SegmentSteps({ steps }: { steps: ChatMessageSegment[] }) {
  const [open, setOpen] = useState(false);
  const [openStep, setOpenStep] = useState<number | null>(null);
  return (
    <div className="chat-message__steps">
      <button
        type="button"
        className="chat-message__steps-toggle"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', color: 'inherit', opacity: 0.7 }}
      >
        {open ? '▾' : '▸'} {steps.length} {steps.length === 1 ? 'step' : 'steps'}
      </button>
      {open && (
        <ul className="chat-message__steps-list">
          {steps.map((s, i) => (
            <li key={i}>
              <div>{stripAgentControlTagsFromAssistantContent(s.text)}</div>
              {s.toolCalls?.length ? (
                <>
                  <button
                    type="button"
                    aria-expanded={openStep === i}
                    onClick={() => setOpenStep(openStep === i ? null : i)}
                    style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', color: 'inherit', opacity: 0.6 }}
                  >
                    {s.toolCalls.length} tool {s.toolCalls.length === 1 ? 'call' : 'calls'}
                  </button>
                  {openStep === i && (
                    <div className="chat-message__step-tools">
                      {s.toolCalls.map((t, j) => (
                        <div key={t.id ?? j}>{t.name ?? 'tool'}</div>
                      ))}
                    </div>
                  )}
                </>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export const ChatMessage = React.memo(function ChatMessage({
  message,
  streamPlainContent = false,
  onDelete,
  deleteDisabled = false,
  deleteTooltip = 'Delete this message and everything after it',
  attachmentIndex,
  usage,
  decorations,
  trackedDocuments,
  initialSeed,
  renderCustomMessage,
  renderUserAttachment,
  renderDecorations,
  askCardResponses,
  onAskCardSubmit,
  onAskCardDismiss,
  transformVisibleContent,
  containOffscreen = false,
  deferMarkdown = false,
}: ChatMessageProps) {
  // Every hook below must run on every render, so the early returns (custom
  // render, tool messages) come after them — a message whose role or custom
  // render changes between renders must not change the hook call order.
  const isUser = message.role === 'user';
  const isAssistant = message.role === 'assistant';
  // The persisted user content is exactly what the user typed — tracked docs and
  // the session seed are injected by llmd only into the model view, never stored
  // here — so it renders directly with no stripping.
  const visibleContent = isUser
    ? message.content
    : (transformVisibleContent ?? ((s: string) => s))(
        stripAgentControlTagsFromAssistantContent(message.content),
        message
      );

  const liveStreamMeta = message.metadata as
    | { liveActivityPreview?: boolean; streamingPlaceholder?: boolean }
    | undefined;
  const isLiveActivityPreview = liveStreamMeta?.liveActivityPreview === true;
  const isStreamingPlaceholder = liveStreamMeta?.streamingPlaceholder === true;

  const contentRef = useRef<HTMLDivElement>(null);
  const [showRaw, setShowRaw] = useState(false);
  // Initial value only: see `deferMarkdown`. Once true, never goes back.
  const [renderMarkdown, setRenderMarkdown] = useState(() => !deferMarkdown);
  useEffect(() => {
    if (renderMarkdown) return;
    const el = contentRef.current;
    if (!el || typeof IntersectionObserver === 'undefined') {
      setRenderMarkdown(true);
      return;
    }
    // Generous margin so a row is already formatted by the time it scrolls in.
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) setRenderMarkdown(true);
      },
      { rootMargin: '1000px 0px' }
    );
    io.observe(el);
    return () => io.disconnect();
  }, [renderMarkdown]);
  // Raw = exactly what was sent to the model (initial-context/seed blocks,
  // tracked-doc prefixes, attachment fences, agent control tags). The bubble
  // normally shows the cleaned view; the eye toggle reveals the hidden parts.
  const rawContent = message.content;
  const hasHiddenContent = rawContent.trim() !== visibleContent.trim();
  const usesRenderedMarkdown =
    renderMarkdown &&
    !(isAssistant && streamPlainContent) &&
    !(isUser && messageHasPasteFences(visibleContent));

  useEffect(() => {
    if (!usesRenderedMarkdown) return;
    return bindMarkdownRenderedCopy(contentRef.current, message.content);
  }, [usesRenderedMarkdown, visibleContent, message.content]);

  const { remarkPlugins, rehypePlugins } = useMarkdownPlugins();
  const { attachmentKindPlugins } = useLlmdPlugins();

  const custom = renderCustomMessage?.(message);
  if (custom !== undefined) return <>{custom}</>;

  if (message.role === 'tool') return null;

  // A host-supplied usage wins; otherwise what llmd recorded on the message.
  const shownUsage =
    usage !== undefined ? usage : isAssistant ? usageFromMessageMetadata(message.metadata) : undefined;

  const turnSegments =
    isAssistant && message.segments && message.segments.length >= 2 ? message.segments : undefined;
  const finalSegmentText = turnSegments
    ? (transformVisibleContent ?? ((s: string) => s))(
        stripAgentControlTagsFromAssistantContent(turnSegments[turnSegments.length - 1].text),
        message
      )
    : undefined;

  const renderProseSegments = () => {
    const segments = parseMessageContent(finalSegmentText ?? visibleContent);
    return segments.map((segment, idx) => {
      if (segment.type === 'markdown') {
        const text = segment.text;
        if (!text.trim()) return null;
        if (isAssistant && streamPlainContent) {
          return (
            // div (not <pre>) so `.chat-message__content pre` code-block
            // styling can never claim the streaming tail; pre-wrap comes
            // from the CSS class.
            <div key={`md-${idx}`} className="chat-message__stream-plain">
              {text}
            </div>
          );
        }
        if (!renderMarkdown) {
          // Deferred: raw text, cheap to mount and still searchable.
          return (
            <div
              key={`md-${idx}`}
              className="chat-message__deferred-plain"
              style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
            >
              {text}
            </div>
          );
        }
        if (isUser && messageHasPasteFences(text)) {
          return <MessageContentWithPastes key={`md-${idx}`} content={text} />;
        }
        return (
          <ReactMarkdown
            key={`md-${idx}`}
            remarkPlugins={remarkPlugins}
            rehypePlugins={rehypePlugins}
            components={markdownCodeComponents}
          >
            {text}
          </ReactMarkdown>
        );
      }
      // An attachment tag that resolves to no card must degrade to something
      // VISIBLE, never to nothing. Returning null here silently deletes
      // content: a message whose only body was an unresolvable tag would
      // render as an empty bubble, and the user would see "no response" with
      // no way to tell whether the agent had replied at all. What it
      // degrades to used to be the raw `<attachment:id/>` tag, which is
      // machinery leaking into the transcript; a muted placeholder keeps the
      // "something was here" signal without pretending the id is meaningful.
      const attachment = attachmentIndex?.get(segment.attachmentId);
      // End-of-turn kinds render once, in their own row at the end of the turn;
      // an inline tag (mid-turn) renders nothing, not a placeholder.
      if (
        attachment &&
        attachmentKindPlugins.some((p) => p.kind === attachment.kind && p.endOfTurn) &&
        !messageIsAnchorlessCardRow(message as unknown as Parameters<typeof messageIsAnchorlessCardRow>[0])
      ) {
        return null;
      }
      if (attachment?.kind === ASK_CARD_KIND) {
        const card = decodeAskCard(attachment.payloadJson);
        const dismiss = onAskCardDismiss
          ? () => onAskCardDismiss(card?.id ?? attachment.id)
          : undefined;
        if (card) {
          const response = askCardResponses?.get(card.id) ?? null;
          return (
            <div key={`att-${segment.attachmentId}`} className="chat-message__attachments">
              <AskCardErrorBoundary cardId={card.id} onDismiss={dismiss}>
                <AskCardView
                  card={card}
                  response={response}
                  onSubmit={onAskCardSubmit}
                  onDismiss={response ? undefined : dismiss}
                  disabled={!onAskCardSubmit}
                />
              </AskCardErrorBoundary>
            </div>
          );
        }
        // Undecodable card: still clearable, so a broken one is not stuck.
        return (
          <div key={`att-${segment.attachmentId}`} className="chat-message__attachments">
            <AskCardUnavailable cardId={attachment.id} label="[card unavailable]" onDismiss={dismiss} />
          </div>
        );
      }
      if (!attachment) {
        return (
          <span
            key={`att-text-${idx}`}
            title={`Unresolved attachment: ${segment.attachmentId}`}
            style={{ opacity: 0.6, fontStyle: 'italic' }}
          >
            [card unavailable]
          </span>
        );
      }
      return (
        // Display cards and host kinds: whatever plugin is registered for them.
        <div key={`att-${segment.attachmentId}`} className="chat-message__attachments">
          <PluginAttachment
            attachment={attachment}
            attachmentId={segment.attachmentId}
            messageId={message.id}
            dismiss={onAskCardDismiss ? () => onAskCardDismiss(segment.attachmentId) : undefined}
          />
        </div>
      );
    });
  };

  return (
    <div
      className={`chat-message chat-message--${message.role}`}
      style={containOffscreen ? OFFSCREEN_CONTAINMENT_STYLE : undefined}
      // Stable DOM address for this message. A host that wants to anchor its own
      // UI to the transcript — a selection menu, a comment pin, a highlight
      // range — has to map a DOM node back to the message it belongs to, and
      // `Message.id` is the only durable address llmd has (turn ids live on the
      // live event stream and do not survive a reload). Emitted unconditionally
      // rather than behind a prop: it costs one attribute, and the alternative
      // is hosts guessing from class names and DOM position.
      data-message-id={message.id}
      data-message-role={message.role}
    >
      <div className="chat-message__header">
        <div className="chat-message__role">
          {/* user/assistant are conveyed visually (accent bar / plain prose),
              so the shouty "USER"/"ASSISTANT" label is suppressed. Other roles
              (e.g. system) still get a label. */}
          {!isUser && !isAssistant && message.role}
          {message.providerId && (
            <span className="chat-message__provider-badge">
              {message.providerId}
            </span>
          )}
          {(message.metadata as { queued?: boolean } | undefined)?.queued && (
            <span
              className="chat-message__queued-badge"
              style={{
                marginLeft: 6,
                fontSize: '0.62rem',
                fontWeight: 700,
                letterSpacing: '0.05em',
                textTransform: 'uppercase',
                color: 'var(--mantine-color-yellow-7)',
              }}
            >
              Queued
            </span>
          )}
          {/* A reply that was cut short — the user pressed stop, or sent a
              correction that interrupted it. Without a visible marker the
              truncated text reads as the model's complete answer, which is
              exactly the wrong impression: it stopped mid-sentence because it
              was interrupted, not because it was finished. Set live by
              useLlmdChatDriver on interrupt, and re-derived from the persisted
              finish_reason when a transcript is rehydrated after a reload. */}
          {(message.metadata as { interrupted?: boolean } | undefined)?.interrupted && (
            <span
              className="chat-message__interrupted-badge"
              title="This reply was interrupted before the model finished"
              style={{
                marginLeft: 6,
                fontSize: '0.62rem',
                fontWeight: 700,
                letterSpacing: '0.05em',
                textTransform: 'uppercase',
                color: 'var(--mantine-color-orange-7)',
              }}
            >
              Interrupted
            </span>
          )}
        </div>
        {hasHiddenContent && (
          <MantineTooltip
            label={
              showRaw
                ? 'Show cleaned message'
                : 'Show raw message (includes hidden context stripped from view)'
            }
            withArrow
            position="left"
          >
            <ActionIcon
              type="button"
              variant="subtle"
              color="gray"
              size="xs"
              className="chat-message__raw-toggle"
              aria-label={showRaw ? 'Show cleaned message' : 'Show raw message'}
              aria-pressed={showRaw}
              onClick={(e) => {
                e.stopPropagation();
                setShowRaw((v) => !v);
              }}
            >
              {showRaw ? (
                <IconEyeOff size={14} stroke={1.5} />
              ) : (
                <IconEye size={14} stroke={1.5} />
              )}
            </ActionIcon>
          </MantineTooltip>
        )}
        {onDelete && (
          <MantineTooltip label={deleteTooltip} withArrow position="left">
            <ActionIcon
              type="button"
              variant="subtle"
              color="gray"
              size="xs"
              className="chat-message__delete"
              aria-label={deleteTooltip}
              disabled={deleteDisabled}
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
            >
              <IconTrash size={14} stroke={1.5} />
            </ActionIcon>
          </MantineTooltip>
        )}
      </div>
      <div ref={contentRef} className="chat-message__content">
        {showRaw ? (
          <pre
            className="chat-message__raw"
            style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
          >
            {rawContent}
          </pre>
        ) : isAssistant && isStreamingPlaceholder && !visibleContent.trim() ? (
          <div className="chat-message__stream-placeholder" aria-live="polite">
            <span className="chat-message__stream-placeholder-dot" />
            <span className="chat-message__stream-placeholder-dot" />
            <span className="chat-message__stream-placeholder-dot" />
          </div>
        ) : isAssistant && isLiveActivityPreview ? (
          <p className="chat-message__activity-preview">{visibleContent}</p>
        ) : turnSegments ? (
          <>
            <SegmentSteps steps={turnSegments.slice(0, -1)} />
            {renderProseSegments()}
          </>
        ) : (
          renderProseSegments()
        )}
      </div>
      {isUser && (
        <AnsweredCardChips
          message={message}
          attachmentIndex={attachmentIndex}
          renderUserAttachment={renderUserAttachment}
        />
      )}
      {isUser && (
        <TrackedContextBadges
          documents={trackedDocuments}
          initialSeed={initialSeed}
        />
      )}
      {/* Footer badge row: decorations chip anchored left, token in/out
          anchored right. An empty left slot keeps the token badge
          right-aligned when there's nothing to decorate with.

          Decorations render for ANY role, not just assistant turns: a host
          pinning a follow-up, attaching a review comment or flagging a prompt
          wants that marker on the user's message as often as the reply's. The
          token badge stays assistant-only because `usage` is only ever
          supplied for assistant turns. */}
      {((decorations && decorations.length > 0) || shownUsage) && (
        <div
          className="chat-message__footer-badges"
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 8,
          }}
        >
          <div>
            {decorations && decorations.length > 0 && renderDecorations?.(decorations)}
          </div>
          {shownUsage && <TokenUsageBadge usage={shownUsage} />}
        </div>
      )}
    </div>
  );
});
