// @pyxis-llm/react barrel — populated incrementally as components are extracted
// from app-specific UI into a reusable, Mantine-based surface.
export {
  markdownCodeComponents,
  markdownThinkingStreamCodeComponents,
  MarkdownLinkRendererContext,
  type MarkdownLinkRendererProps,
} from './MarkdownCodeComponents.js';
export type {
  AttachmentKindRenderer,
  AttachmentRenderProps,
  DisplayCardRenderProps,
  DisplayCardRenderer,
  HighlightedCodeProps,
  LanguageBlockRenderer,
  LlmdPlugin,
  ResolvedLlmdPlugins,
} from './plugins.js';
export {
  LlmdPluginProvider,
  registerLlmdPlugin,
  registeredLlmdPlugins,
  resolveLlmdPlugins,
  useLlmdPlugins,
} from './plugins.js';
export type { MarkdownPluginList, MarkdownPlugins } from './markdownPlugins.js';
export { MarkdownPluginProvider, useMarkdownPlugins } from './markdownPlugins.js';
// Mermaid, syntax highlighting and charts live in `@pyxis-llm/react/mermaid`,
// `@pyxis-llm/react/highlight` and `@pyxis-llm/react/charts` so their (optional peer)
// deps stay out of this entry.
export { splitStreamingMarkdown, StreamingMarkdown } from './StreamingMarkdown.js';

export type { ChatMessageData, ChatMessageSegment, ChatMessageProps, ChatMessageUsage } from './ChatMessage.js';
export { ChatMessage, fmtModelDuration, usageFromMessageMetadata } from './ChatMessage.js';

export type { AskCardViewProps, AskCardSubmitOptions } from './AskCardView.js';
export { AskCardView } from './AskCardView.js';

export { MessageContentWithPastes, inlineMarkdownComponents } from './MessageContentWithPastes.js';

export type { PasteFenceSegment } from './pasteFences.js';
export {
  PASTE_FENCE_TICKS,
  formatPasteFence,
  messageContainsPasteFenceForNumber,
  messageHasPasteFences,
  normalizeOutgoingPasteSpacing,
  splitMessageByPasteFences,
} from './pasteFences.js';

export { countPasteLines, formatPastePlaceholder } from './pasteFormat.js';

export { PastePill, pastePillLabel } from './PastePill.js';

export type { MarkdownCopyHandlerOptions } from './markdownClipboard.js';
export {
  bindMarkdownRenderedCopy,
  copyEventShiftKey,
  handleMarkdownRenderedCopy,
  htmlFragmentToMarkdown,
  plainTextFromSelection,
} from './markdownClipboard.js';

export type { ComposerProps } from './Composer.js';
export { Composer } from './Composer.js';

export type {
  ChatPanelProps,
  ChatPanelHandle,
  ChatPanelScrollRequest,
} from './ChatPanel.js';
export { ChatPanel } from './ChatPanel.js';

export type { DraftStore } from './draftStore.js';
export { useDraft } from './draftStore.js';

export {
  CHAT_PANEL_BOTTOM_THRESHOLD_PX,
  centerMessageInViewport,
  findMessageElement,
  isPinnedToBottom,
} from './chatMessageScroll.js';

export type { QueueStripProps } from './QueueStrip.js';
export { QueueStrip } from './QueueStrip.js';

export type { TurnTiming, TurnTimerProps, TurnTimerState } from './TurnTimer.js';
export { TurnTimer, TurnTimerView, formatTurnDuration, useTurnTimer } from './TurnTimer.js';

export type { SendBehavior, SendMode, SendModeContext, SendModeLabel } from './sendMode.js';
export { isAlternateSend, sendBehaviorFor, sendHintFor, sendLabelFor, useSendMode } from './sendMode.js';

export { DisplayCardAttachment, PluginAttachment } from './DisplayCards.js';
export { ImageCardView, imageCardPlugin } from './ImageDisplayCard.js';

export type { AskCardAnswerChipProps } from './AskCardAnswerChip.js';
export { AskCardAnswerChip } from './AskCardAnswerChip.js';

export type {
  ComposerAttachment,
  PlanDesignListItemForAttachment,
  ComposerAttachmentUnifiedPanelProps,
  ComposerOutgoingAttachment,
} from './ComposerAttachments.js';
export {
  AddAttachmentModal,
  COMPOSER_ATTACHMENT_KIND,
  appendComposerAttachmentsToMessage,
  composerAttachmentsToMessageAttachments,
  composerAttachmentSignature,
  useComposerAttachmentsSessionState,
} from './ComposerAttachments.js';

export type { ChatTranscriptFindProps } from './ChatTranscriptFind.js';
export { ChatTranscriptFind } from './ChatTranscriptFind.js';
export type { ChatTranscriptFindBarProps } from './ChatTranscriptFindBar.js';
export { ChatTranscriptFindBar } from './ChatTranscriptFindBar.js';
export {
  applyChatTranscriptFindHighlights,
  clearChatTranscriptFindHighlights,
  collectChatTranscriptMatchRanges,
  scrollChatTranscriptMatchIntoView,
  supportsCssCustomHighlight,
} from './chatTranscriptFindHighlights.js';

export { isModKey, isTypingTarget } from './keyboardShortcuts.js';

export { ErrorBoundary } from './ErrorBoundary.js';

export type {
  CatalogTree,
  CatalogTreeModelInput,
  CatalogTreeNode,
  CatalogTreeNodeKind,
} from './modelCatalogTree.js';
export {
  buildCatalogTree,
  catalogTreeBreadcrumb,
  catalogTreeIsFlat,
  flattenCatalogTreeLeaves,
  formatCatalogTreeBreadcrumb,
} from './modelCatalogTree.js';

export {
  filterMultiProviderRoots,
  filterSelectableTreeNodes,
  makeResolveOption,
  optionLookupKey,
} from './catalogColumnBrowserTree.js';

export type { ChatModelOption } from './chatModelOptions.js';
export {
  buildConfiguredModelOptions,
  decodeChatModelOptionValue,
  encodeChatModelOptionValue,
} from './chatModelOptions.js';

export type { StoredModelPair } from './modelCatalogHelpers.js';
export {
  formatEffectiveModelLabel,
  modelOptionExists,
  resolveCanonicalModelOption,
} from './modelCatalogHelpers.js';

export * from './catalogPickerMenuStyles.js';

export type { CatalogPickerMenuItemProps } from './CatalogPickerMenuItem.js';
export { CatalogPickerMenuItem } from './CatalogPickerMenuItem.js';

export type {
  CatalogColumnBrowserHandle,
  CatalogColumnBrowserProps,
} from './CatalogColumnBrowser.js';
export { CatalogColumnBrowser } from './CatalogColumnBrowser.js';

export type { CatalogTreeBuilder, CatalogTreePickerOptionsProps } from './CatalogTreePickerOptions.js';
export {
  CatalogTreePickerOptions,
  getFilteredFlatOptions,
} from './CatalogTreePickerOptions.js';

export type { CatalogModelPickerMenuContentProps } from './CatalogModelPickerMenuContent.js';
export { CatalogModelPickerMenuContent } from './CatalogModelPickerMenuContent.js';

export type { CatalogModelPickerProps } from './CatalogModelPicker.js';
export {
  CatalogModelPicker,
  formatCatalogModelPickerLabel,
} from './CatalogModelPicker.js';

export type {
  CatalogModelSummaryLike,
  LlmProviderSummaryLike,
} from './modelCatalogSource.js';
export { chatModelOptionsFromCatalog } from './modelCatalogSource.js';

export type {
  ProviderConnectConfig,
  ProviderConnectConfigType,
  ProviderConnectFormProps,
  ProviderConnectionTestResult,
} from './ProviderConnectForm.js';
export { ProviderConnectForm } from './ProviderConnectForm.js';

export type { ProviderListProps } from './ProviderList.js';
export { ProviderList } from './ProviderList.js';

export type {
  AgentProfileEditorProps,
  AgentProfileToolOption,
} from './AgentProfileEditor.js';
export {
  AgentProfileEditor,
  AGENT_TOOL_POLICY,
  INSTRUCTIONS_SECTION_ID,
  IDENTITY_SECTION_ID,
  buildProfileSections,
} from './AgentProfileEditor.js';

export type { AgentProfileListProps } from './AgentProfileList.js';
export { AgentProfileList } from './AgentProfileList.js';

export type {
  AgentProfileOverlayEditorProps,
  AgentProfileOverlayDraft,
  OverlayFormState,
} from './AgentProfileOverlayEditor.js';
export {
  AgentProfileOverlayEditor,
  buildAgentProfileOverlay,
  overlayFormState,
} from './AgentProfileOverlayEditor.js';

export type { ProviderAuthTerminalProps } from './ProviderAuthTerminal.js';
export { ProviderAuthTerminal } from './ProviderAuthTerminal.js';
