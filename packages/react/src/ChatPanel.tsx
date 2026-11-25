/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * A complete, minimal chat surface: scrollable message list (via
 * ChatMessage) + Composer, wired together by props/callbacks only.
 *
 * Deliberately transport-agnostic: it does NOT import `@pyxis-llm/client` or
 * drive `ChatStream` itself. `@pyxis-llm/client`'s gRPC transport
 * (`@grpc/grpc-js`) is Node-only — most hosts (Electron renderers, browser
 * apps) can't call it directly from the same process this component
 * renders in, and even hosts that could will have very different session/
 * attachment/model-catalog state management. So `ChatPanel` takes the
 * current `messages` array and an `onSend` callback and lets the host own
 * everything upstream of that (a hook wrapping `chatStreamEvents` over
 * IPC, a REST proxy, a mock for tests — whatever fits).
 *
 * Extension points (the "slots and callbacks" needed to slim this down to a
 * shared core while hosts add their own chrome):
 *   - `header` / `composerBefore` / `composerAfter`: arbitrary chrome
 *     (status bar, model switcher, attachments strip).
 *   - `renderCustomMessage` / `renderDecorations` / `transformVisibleContent`:
 *     forwarded to every ChatMessage — see its own docs for what each does.
 *   - Cards, code renderers and attachment placement come from llmd plugins
 *     (plugins.tsx: registerLlmdPlugin / LlmdPluginProvider), not props.
 *   - `usageByMessageId` / `decorationsByMessageId` / `trackedDocumentsByMessage`:
 *     per-message data a host looks up by id; ChatPanel does no bookkeeping
 *     of its own beyond the lookup.
 *
 * A host that supplies none of the optional props gets a fully working
 * "vanilla" chat panel — text in, text out, attachments degrade to a
 * placeholder — which is the Owlbert-style baseline. A host app
 * adds graph badges, a model switcher, and its own attachment kinds purely
 * by passing props; it never needs to fork this component.
 *
 * Addressing a single message: every `ChatMessage` root carries
 * `data-message-id` (its `Message.id`) and `data-message-role`, so a host can
 * map a DOM node or text selection back to a message and anchor its own
 * overlays — selection menus, comment pins, highlight ranges — with no extra
 * props. `scrollRequest` and the `ChatPanelHandle` ref bring a given message
 * into view; see `ChatPanelScrollRequest` for why it carries a nonce and
 * `autoScroll` for how the two interact.
 *
 * Note that message ids are the only *durable* address llmd has: turn ids and
 * turn sequence numbers exist on the live conversation event stream but are
 * not persisted, so anything a host wants to survive a reload should be keyed
 * off `Message.id`.
 *
 * Ask cards (llmd's first-class AskCard/AskCardResponse primitive, including
 * ones created server-side by the `ask_user` tool) need NO dedicated prop
 * here: they're plain attachments (kind `ask_card`), rendered inline by
 * `ChatMessage` via the existing `attachmentIndex`/`askCardResponses`/
 * `onAskCardSubmit` props — see packages/react/e2e/askCard.e2e.test.tsx for
 * the full round trip (load attachments, render, answer via
 * UpsertSessionAttachment).
 */

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { ActionIcon, Group, ScrollArea, Text } from '@mantine/core';
import { IconArrowDown } from '@tabler/icons-react';
import { TurnTimerView, useTurnTimer, type TurnTiming, type TurnTimerState } from './TurnTimer.js';
import {
  appendAnchorlessCardMessages,
  indexAskCardResponses,
  withLlmdAttachmentKindPlugins,
  type TurnDecorationView,
  type LlmdMessageAttachment,
  type TrackedDocumentRefView,
  type AskCardResponse,
} from '@pyxis-llm/core';
import { ChatMessage, type ChatMessageData, type ChatMessageProps, type ChatMessageUsage } from './ChatMessage.js';
import type { AskCardSubmitOptions } from './AskCardView.js';
import { Composer, type ComposerProps } from './Composer.js';
import { useLlmdPlugins } from './plugins.js';
import { useSendMode } from './sendMode.js';
import { QueueStrip } from './QueueStrip.js';
import { useDraft, type DraftStore } from './draftStore.js';
import type { QueuedMessage } from './vendor/llmd-types/index.js';
import {
  centerMessageInViewport,
  findMessageElement,
  isPinnedToBottom,
} from './chatMessageScroll.js';

/**
 * A request to bring one message into view.
 *
 * `token` is a nonce, and it is the whole point of this shape: a bare
 * `scrollToMessageId` prop can only fire once, so clicking the same pin twice
 * (or re-opening the same deep link) would be a no-op because the prop value
 * never changed. Change `token` to re-fire against the same `messageId` —
 * `Date.now()`, a counter, or the click event's id all work.
 *
 * A request whose `messageId` is not mounted yet (history still loading) is
 * retained and retried as `messages` changes, until it resolves.
 */
export type ChatPanelScrollRequest = {
  messageId: string;
  token: string | number;
};

/** Imperative escape hatch for hosts that would rather call than pass a prop. */
export type ChatPanelHandle = {
  /**
   * Scrolls `messageId` into view, centered. Returns false when that message
   * is not currently rendered — unlike `scrollRequest`, this does not retry.
   */
  scrollToMessage: (messageId: string) => boolean;
  /** Jumps to the newest message and re-arms auto-scroll. */
  scrollToBottom: () => void;
  /** The scrolling viewport element, for hosts positioning their own overlays. */
  getViewport: () => HTMLDivElement | null;
};

export type ChatPanelProps = {
  messages: ChatMessageData[];
  /**
   * Called on submit with what the user asked for while a turn may be running:
   * plain Enter / the send button give one behavior, holding ⌥/Ctrl/⌘ the
   * other (see `sendMode`). Signature-compatible with `useLlmdChatDriver`'s
   * `send`.
   */
  onSend: (text: string, behavior: 'queue' | 'interrupt') => void;
  /**
   * Pin what plain Enter does. Omitted (recommended) = follow the queue:
   * queue normally, send now while `queuePaused`. The modifier always gives
   * the other one.
   */
  defaultSendBehavior?: 'queue' | 'interrupt';

  /**
   * The session queue (driver's `queue`). When provided, a `QueueStrip` is
   * rendered above the composer; the `on*Queue*` callbacks enable its controls.
   */
  queue?: QueuedMessage[];
  queuePaused?: boolean;
  onRemoveQueued?: (entryId: string) => void | Promise<void>;
  onReorderQueue?: (entryIds: string[]) => void | Promise<void>;
  onSetQueuePaused?: (paused: boolean) => void | Promise<void>;
  /** Pull an entry out of the queue and send it now as an interrupt (driver's `interruptWithQueued`). */
  onInterruptWithQueued?: (entryId: string) => void | Promise<void>;
  streaming?: boolean;
  onStop?: () => void;
  onDeleteMessage?: (message: ChatMessageData) => void;
  /** Id of the last message still being generated — disables its delete button, matching the chip-in convention. */
  streamingMessageId?: string;

  /**
   * Every attachment on the session (the driver's `attachmentIndex`). A
   * still-pending ask card that no message references — e.g. one the agent
   * asked via `ask_user` before any assistant text was saved — gets a row of
   * its own at the end of the transcript, so it can never silently vanish.
   * Image and chart cards that no message references get a row right after
   * the tool result that produced them.
   */
  attachmentIndex?: Map<string, LlmdMessageAttachment>;
  usageByMessageId?: Map<string, ChatMessageUsage>;
  decorationsByMessageId?: Map<string, TurnDecorationView[]>;
  trackedDocumentsByMessageId?: Map<string, TrackedDocumentRefView[]>;
  /** First-turn session seed, shown as a badge on the first user message that has one. */
  initialSeed?: string;

  /**
   * Agent-authored session title (from the `set_session_title` tool — see
   * `useLlmdChatDriver`'s `sessionTitle`). Rendered as
   * a small title bar above the message list when set, so a session the
   * agent names live re-titles the panel without any host-supplied chrome.
   * A host that wants its own title UI (e.g. in a tab strip instead of
   * inline) can ignore this and read `sessionTitle` off the driver itself —
   * this prop is a convenience default, not the only way to consume it.
   */
  sessionTitle?: string;
  /**
   * Agent-authored status headline (from the `set_status` tool — see the
   * driver hooks' `sessionStatus`). Rendered in the turn bar just above the
   * composer, left of the turn timer. Turn-scoped: llmd clears it when a turn
   * starts and when it ends, so it only ever shows the running turn's headline.
   */
  sessionStatus?: string;
  /**
   * When the running turn started (ms since epoch), for the turn timer. Optional:
   * by default the timer starts when `streaming` turns true, which is exact for
   * turns this panel sends and approximate (mount time) for a turn it rejoined.
   */
  turnStartedAt?: number;
  /**
   * Per-session turn timing from useLlmdChatDriver (`driver.turnTiming`).
   * Preferred over `turnStartedAt`: it uses llmd's own turn-start time and
   * resets with the session, so the timer is right across reloads, rejoins
   * and session switches.
   */
  turnTiming?: TurnTiming;
  /**
   * Show the turn timer in the turn bar (right of the status headline). Default
   * true. Set false to place it yourself: `<TurnTimer streaming={driver.streaming} />`
   * or `useTurnTimer(...)` anywhere in your own chrome.
   */
  showTurnTimer?: boolean;
  /**
   * Replace what is drawn in the turn timer's default spot. Receives the timer
   * state (`elapsedMs`, `running`, `label`); return null to draw nothing for a
   * given state. Ignored when `showTurnTimer` is false.
   */
  renderTurnTimer?: (state: TurnTimerState) => ReactNode;

  renderCustomMessage?: (message: ChatMessageData) => ReactNode | undefined;
  /** Chips for host attachments sent on user messages (see ChatMessage). */
  renderUserAttachment?: ChatMessageProps['renderUserAttachment'];
  renderDecorations?: (decorations: TurnDecorationView[]) => ReactNode;
  /** Ask-card responses, keyed by the AskCard.id they answer. Defaults to the answers found in `attachmentIndex`. */
  askCardResponses?: Map<string, AskCardResponse>;
  /**
   * Called when the user submits an ask card anywhere in the transcript.
   * `options.continueWithTools` is true for "Answer & continue", false for
   * "Answer & stop" (pass both straight to the driver's `submitAskCard`).
   */
  onAskCardSubmit?: (response: AskCardResponse, options: AskCardSubmitOptions) => void;
  /**
   * Called with a card's id when the user dismisses an ask card (the ✕).
   * Pass the driver's `dismissAskCard`. Omit to hide the ✕.
   */
  onAskCardDismiss?: (cardId: string) => void;
  transformVisibleContent?: (content: string, message: ChatMessageData) => string;

  /** Chrome above the message list (e.g. a status bar). */
  header?: ReactNode;
  /** Chrome below the message list, above the composer (e.g. a scroll-to-bottom affordance). */
  footer?: ReactNode;
  /** Composer row above the textarea (attachments strip, paste pills). */
  composerBefore?: ReactNode;
  /** Composer row below the textarea (model switcher, token counter). */
  composerAfter?: ReactNode;
  composerPlaceholder?: string;
  composerDisabled?: boolean;
  composerValue?: ComposerProps['value'];
  /**
   * The driver's draft store (`driver.draft`). Preferred over `composerValue`:
   * only the composer subscribes, so typing does not re-render the panel or
   * the page that owns the driver. Ignored when `composerValue` is set.
   */
  composerDraft?: DraftStore;
  onComposerValueChange?: ComposerProps['onValueChange'];

  /**
   * Auto-scroll to the newest message when `messages` grows. Default true.
   *
   * Only chases while the viewport is parked at the bottom. Scroll away —
   * by hand or via `scrollRequest` — and new messages stop yanking the
   * transcript out from under you; scroll back to the bottom and it resumes.
   */
  autoScroll?: boolean;
  /**
   * Bring one message into view. See `ChatPanelScrollRequest` — change its
   * `token` to re-fire for the same `messageId`.
   *
   * This counts as scrolling by hand: landing somewhere other than the bottom
   * suspends `autoScroll`, so a reply streaming in mid-read will not drag the
   * view back to the newest message. Returning to the bottom re-arms it.
   */
  scrollRequest?: ChatPanelScrollRequest;
  /**
   * How many of the newest messages render as markdown at mount. Older ones
   * mount as plain text (still in the DOM, so find-in-page works) and upgrade
   * to markdown as they near the viewport. Default 10.
   */
  fullRenderTail?: number;
  /**
   * Render only the bottom of the panel (queue strip, turn bar, composer): no title bar,
   * header, transcript or footer, and the panel sizes to its content instead of filling
   * its parent. Toggling it keeps the composer the same element with the same styling,
   * so a host can collapse the chat to "just the input" (e.g. under a document on a
   * phone) without the input moving, remounting or losing focus.
   */
  hideTranscript?: boolean;
  /**
   * Show a round "jump to newest" arrow over the transcript's bottom-right corner while the
   * reader is scrolled up (more than SCROLL_BUTTON_THRESHOLD_PX from the bottom). Default true.
   */
  showScrollToBottom?: boolean;
  className?: string;
};

/** How far above the bottom the reader must be before the jump-to-newest arrow appears. */
const SCROLL_BUTTON_THRESHOLD_PX = 120;

function distanceFromBottom(viewport: HTMLElement): number {
  return viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight;
}

export const ChatPanel = forwardRef<ChatPanelHandle, ChatPanelProps>(function ChatPanel({
  messages: messagesProp,
  onSend,
  defaultSendBehavior,
  queue,
  queuePaused = false,
  onRemoveQueued,
  onReorderQueue,
  onSetQueuePaused,
  onInterruptWithQueued,
  streaming = false,
  onStop,
  onDeleteMessage,
  streamingMessageId,
  attachmentIndex,
  usageByMessageId,
  decorationsByMessageId,
  trackedDocumentsByMessageId,
  initialSeed,
  sessionTitle,
  sessionStatus,
  turnStartedAt,
  turnTiming,
  showTurnTimer = true,
  renderTurnTimer,
  renderCustomMessage,
  renderUserAttachment,
  renderDecorations,
  askCardResponses: askCardResponsesProp,
  onAskCardSubmit,
  onAskCardDismiss,
  transformVisibleContent,
  header,
  footer,
  composerBefore,
  composerAfter,
  composerPlaceholder,
  composerDisabled,
  composerValue,
  composerDraft,
  onComposerValueChange,
  autoScroll = true,
  scrollRequest,
  fullRenderTail = 10,
  hideTranscript = false,
  showScrollToBottom = true,
  className,
}, ref) {
  // Placement rules: llmd's own kinds plus every plugin's (plugins.tsx).
  const { attachmentKindPlugins } = useLlmdPlugins();
  const effectivePlugins = useMemo(() => withLlmdAttachmentKindPlugins(attachmentKindPlugins), [attachmentKindPlugins]);
  // Idempotent (rows are keyed by card id), so a host that still appends
  // anchorless rows itself gets no duplicates.
  const messages = useMemo(
    () =>
      appendAnchorlessCardMessages(
        messagesProp as unknown as Parameters<typeof appendAnchorlessCardMessages>[0],
        attachmentIndex,
        effectivePlugins
      ) as unknown as ChatMessageData[],
    [messagesProp, attachmentIndex, effectivePlugins]
  );
  const derivedAskCardResponses = useMemo(
    () => (attachmentIndex ? indexAskCardResponses(attachmentIndex.values()) : undefined),
    [attachmentIndex]
  );
  const askCardResponses = askCardResponsesProp ?? derivedAskCardResponses;
  const viewportRef = useRef<HTMLDivElement>(null);

  // Whether the viewport is parked at the bottom. Starts true so a freshly
  // mounted panel lands on the newest message, then simply tracks where the
  // viewport actually is.
  //
  // This single flag is what defines precedence between an explicit scroll
  // request and autoScroll, without a separate "suspended" mode to get out of
  // sync: a scroll-to-message leaves the viewport somewhere that is not the
  // bottom, so autoScroll stops chasing for exactly as long as the user is
  // reading up there — indistinguishable from having scrolled by hand. Coming
  // back to the bottom (by scrolling, or via the handle) re-arms it.
  const pinnedToBottomRef = useRef(true);
  // Drives the jump-to-newest arrow. State (not the ref) so it re-renders; only set when
  // it flips, so scrolling doesn't re-render the panel on every frame.
  const [scrolledUp, setScrolledUp] = useState(false);
  const syncScrolledUp = useCallback((viewport: HTMLElement) => {
    setScrolledUp(distanceFromBottom(viewport) > SCROLL_BUTTON_THRESHOLD_PX);
  }, []);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    const onScroll = () => {
      pinnedToBottomRef.current = isPinnedToBottom(viewport);
      syncScrolledUp(viewport);
    };
    viewport.addEventListener('scroll', onScroll, { passive: true });
    return () => viewport.removeEventListener('scroll', onScroll);
    // hideTranscript: the viewport only exists while the transcript is shown.
  }, [hideTranscript]);

  const scrollToBottom = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    // Plain scrollTop assignment rather than viewport.scrollTo({...}): no
    // smooth-scroll behavior is needed for "jump to newest message", and
    // scrollTo is unimplemented in some test/embedded DOM environments
    // (e.g. jsdom) even though scrollTop always works.
    viewport.scrollTop = viewport.scrollHeight;
    pinnedToBottomRef.current = true;
    setScrolledUp(false);
  }, []);

  // Keep the newest message visible when the viewport or its content changes
  // size without `messages` changing — e.g. footer chrome (a tool-activity
  // strip, an error banner) appearing below the ScrollArea shrinks the
  // viewport, which would otherwise hide the tail until the next message
  // update. A resize fires no scroll event, so the pinned flag still reflects
  // where the user was. Skipped where ResizeObserver is absent (jsdom).
  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => {
      if (autoScroll && pinnedToBottomRef.current) scrollToBottom();
      // New content while reading higher up moves the bottom away without a scroll event.
      else syncScrolledUp(viewport);
    });
    ro.observe(viewport);
    const content = viewport.firstElementChild;
    if (content) ro.observe(content);
    // Shown again: land on the newest message, as on first mount.
    if (autoScroll && pinnedToBottomRef.current) scrollToBottom();
    return () => ro.disconnect();
  }, [autoScroll, scrollToBottom, syncScrolledUp, hideTranscript]);

  const scrollToMessage = useCallback((messageId: string): boolean => {
    const viewport = viewportRef.current;
    if (!viewport) return false;
    const el = findMessageElement(viewport, messageId);
    if (!el) return false;
    centerMessageInViewport(viewport, el);
    // Recomputed here rather than left to the scroll event: assigning the same
    // scrollTop fires no event, and jsdom dispatches none at all, so the flag
    // would keep a stale value in exactly the case that matters.
    pinnedToBottomRef.current = isPinnedToBottom(viewport);
    syncScrolledUp(viewport);
    return true;
  }, [syncScrolledUp]);

  useImperativeHandle(
    ref,
    () => ({
      scrollToMessage,
      scrollToBottom,
      getViewport: () => viewportRef.current,
    }),
    [scrollToMessage, scrollToBottom]
  );

  // The last scroll request we actually landed. Tracking it lets the effect
  // below re-run harmlessly on every `messages` change (needed to retry a
  // request whose target has not been fetched yet) without re-scrolling on
  // every appended message once the request has been satisfied.
  const landedScrollTokenRef = useRef<string | number | undefined>(undefined);

  // Set when a scroll request lands, cleared by the autoScroll effect below.
  //
  // Needed because both effects can fire in the same commit — a request that
  // arrives together with new messages — and "am I at the bottom?" is not a
  // sufficient tiebreaker on its own: centering one of the last messages leaves
  // the viewport legitimately within the bottom threshold, so autoScroll would
  // see "pinned" and yank the view to the newest message in the very commit
  // that was supposed to honor the request. An explicit request wins its own
  // commit outright; from the next one on, position decides as usual.
  const scrollRequestLandedRef = useRef(false);

  useEffect(() => {
    if (!scrollRequest?.messageId) return;
    if (landedScrollTokenRef.current === scrollRequest.token) return;
    if (scrollToMessage(scrollRequest.messageId)) {
      landedScrollTokenRef.current = scrollRequest.token;
      scrollRequestLandedRef.current = true;
    }
  }, [scrollRequest, messages, scrollToMessage]);

  useEffect(() => {
    // Consumed unconditionally so it cannot leak into a later commit.
    const yieldToScrollRequest = scrollRequestLandedRef.current;
    scrollRequestLandedRef.current = false;
    if (yieldToScrollRequest) return;
    if (!autoScroll) return;
    if (!pinnedToBottomRef.current) return;
    scrollToBottom();
  }, [autoScroll, messages, scrollToBottom]);

  const turnTimer = useTurnTimer(streaming, turnTiming ?? turnStartedAt);
  const sendMode = useSendMode({
    streaming,
    queuePaused,
    queueLength: queue?.length ?? 0,
    defaultSendBehavior,
  });

  let firstUserMessageId: string | undefined;
  for (const m of messages) {
    if (m.role === 'user') {
      firstUserMessageId = m.id;
      break;
    }
  }

  return (
    <div
      className={['llmd-chat-panel', className].filter(Boolean).join(' ')}
      style={{ display: 'flex', flexDirection: 'column', height: hideTranscript ? 'auto' : '100%', minHeight: 0 }}
    >
      {!hideTranscript && sessionTitle && (
        <Group
          className="llmd-chat-panel__session-bar"
          justify="space-between"
          wrap="nowrap"
          gap="sm"
          px="sm"
          py="xs"
        >
          <Text className="llmd-chat-panel__session-title" fw={600} truncate="end">
            {sessionTitle}
          </Text>
        </Group>
      )}
      {!hideTranscript && header}
      {!hideTranscript && (
      <div
        className="llmd-chat-panel__messages-frame"
        style={{ position: 'relative', flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}
      >
      {showScrollToBottom && scrolledUp && (
        <ActionIcon
          className="llmd-chat-panel__scroll-bottom"
          variant="default"
          radius="xl"
          size="lg"
          aria-label="Scroll to newest message"
          onClick={scrollToBottom}
          style={{ position: 'absolute', right: 16, bottom: 12, zIndex: 2, boxShadow: 'var(--mantine-shadow-md)' }}
        >
          <IconArrowDown size={18} />
        </ActionIcon>
      )}
      <ScrollArea
        viewportRef={viewportRef}
        className="llmd-chat-panel__messages"
        style={{ flex: 1, minHeight: 0 }}
      >
        {messages.map((message, idx) => {
          const id = message.id ?? String(idx);
          return (
            <ChatMessage
              key={id}
              message={message}
              streamPlainContent={streaming && message.id === streamingMessageId}
              onDelete={onDeleteMessage ? () => onDeleteMessage(message) : undefined}
              deleteDisabled={streaming && message.id === streamingMessageId}
              attachmentIndex={attachmentIndex}
              usage={message.id ? usageByMessageId?.get(message.id) : undefined}
              decorations={message.id ? decorationsByMessageId?.get(message.id) : undefined}
              trackedDocuments={
                message.id ? trackedDocumentsByMessageId?.get(message.id) : undefined
              }
              initialSeed={message.id === firstUserMessageId ? initialSeed : undefined}
              renderCustomMessage={renderCustomMessage}
                renderUserAttachment={renderUserAttachment}
              renderDecorations={renderDecorations}
              askCardResponses={askCardResponses}
              onAskCardSubmit={onAskCardSubmit}
              onAskCardDismiss={onAskCardDismiss}
              transformVisibleContent={transformVisibleContent}
              containOffscreen={idx < messages.length - UNCONTAINED_TAIL_ROWS}
              deferMarkdown={idx < messages.length - fullRenderTail}
            />
          );
        })}
      </ScrollArea>
      </div>
      )}
      {!hideTranscript && footer}
      {queue && (
        <QueueStrip
          queue={queue}
          paused={queuePaused}
          onRemove={onRemoveQueued}
          onReorder={onReorderQueue}
          onSetPaused={onSetQueuePaused}
          onInterruptWith={onInterruptWithQueued}
        />
      )}
      {(sessionStatus || (showTurnTimer && turnTimer.elapsedMs !== undefined)) && (
        <Group
          className="llmd-chat-panel__turn-bar"
          justify="space-between"
          wrap="nowrap"
          gap="sm"
          px="sm"
          py={4}
        >
          {sessionStatus ? (
            <Text
              className="llmd-chat-panel__session-status"
              size="sm"
              c="dimmed"
              truncate="end"
              style={{ minWidth: 0, flex: 1 }}
            >
              {sessionStatus}
            </Text>
          ) : (
            // Keeps the timer right-aligned when there is no headline.
            <span style={{ flex: 1 }} />
          )}
          {showTurnTimer &&
            turnTimer.elapsedMs !== undefined &&
            (renderTurnTimer ? (
              renderTurnTimer(turnTimer)
            ) : (
              <TurnTimerView state={turnTimer} className="llmd-chat-panel__turn-timer" />
            ))}
        </Group>
      )}
      <PanelComposer
        draft={composerValue === undefined ? composerDraft : undefined}
        onSend={(text, alternate) => onSend(text, sendMode.behaviorFor(alternate))}
        sendMode={sendMode}
        streaming={streaming}
        onStop={onStop}
        placeholder={
          // While a turn runs, or the queue is in play, the empty input says
          // what Enter and modifier+Enter will do.
          streaming || queuePaused || (queue?.length ?? 0) > 0 ? sendMode.hint : composerPlaceholder
        }
        disabled={composerDisabled}
        value={composerValue}
        onValueChange={onComposerValueChange}
        beforeInput={composerBefore}
        afterInput={composerAfter}
      />
    </div>
  );
});

/**
 * Newest rows never get `content-visibility: auto`, so scroll-to-bottom always
 * measures their real height (see ChatMessageProps.containOffscreen).
 */
const UNCONTAINED_TAIL_ROWS = 4;

/** Composer that, given a draft store, subscribes to it itself. */
function PanelComposer({ draft, ...props }: ComposerProps & { draft?: DraftStore }) {
  return draft ? <DraftComposer draft={draft} {...props} /> : <Composer {...props} />;
}

function DraftComposer({ draft, ...props }: ComposerProps & { draft: DraftStore }) {
  return <Composer {...props} value={useDraft(draft)} />;
}
