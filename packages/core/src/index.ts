// @pyxis-llm/core barrel — populated incrementally as Tier-1 files are ported.
export { toCanonicalModelId, modelIdFromCanonicalId } from './canonicalModelId.js';
// Re-exported from @pyxis-llm/client so host UIs on @pyxis-llm/core (e.g. @pyxis-llm/react)
// can label tool invocations without depending on the transport package.
export {
  toolInvocationLabel,
  type ToolInvocationLabelInput,
} from '@pyxis-llm/client';
export type { ProviderKind } from './providerKind.js';
export {
  providerKindFromLlmProviderType,
  providerKindFromLlmProviderTypeJson,
} from './providerKind.js';
export type {
  TurnDecorationView,
  TurnDecorationWire,
} from './turnDecorations.js';
export {
  decodeTurnDecoration,
  groupTurnDecorations,
} from './turnDecorations.js';
export type {
  CatalogModel,
  CatalogProvider,
  GlobalModelCatalog,
} from './modelCatalogTypes.js';
export {
  trimTrailingPunctuation,
  repairReEmittedPhraseRestarts,
  repairPhraseRestarts,
  appendStreamFoldDelta,
  dedupeRedundantStreamFold,
} from './streamFoldDelta.js';
export type { EmbeddingConfigWire } from './embeddingConfigWire.js';
export type { EmbeddingModelId } from './embeddingModelCatalog.js';
export {
  EMBEDDING_MODEL_IDS,
  EMBEDDING_MODEL_OPTIONS,
  dimensionForEmbeddingModel,
  normalizeEmbeddingModel,
} from './embeddingModelCatalog.js';
export type {
  TrackedDocumentRefView,
  SessionTrackedContext,
} from './trackedDocuments.js';
export {
  EMPTY_SESSION_TRACKED_CONTEXT,
  trackedContextFromRows,
  trackedDocumentsByMessage,
} from './trackedDocuments.js';
export type { ContentSegment } from './messageContentTags.js';
export {
  stripAgentControlTagsFromAssistantContent,
  parseMessageContent,
  stripAttachmentTags,
  formatAttachmentTag,
  hasInlineAttachmentTags,
  mergeAssistantContentWithInlineTags,
} from './messageContentTags.js';
export type {
  LlmdMessageAttachment,
  LiveAttachmentChanges,
  AttachmentKindPlugin,
  MessageWithToolCalls,
} from './messageAttachments.js';
export {
  LLMD_CHAT_MSG_KIND_ATTACHMENT,
  attachmentsFromMessageMetadata,
  messageWithAttachmentsMetadata,
  stripAttachmentsFromMessage,
  sessionHasAttachmentKind,
  hydrateMessageAttachments,
  resolveAttachmentAnchorMessageId,
  findAssistantAnchorForToolCall,
  upsertAttachmentOnAnchorMessage,
  removeAttachmentFromTranscript,
  ANCHORLESS_CARD_MESSAGE_ID_PREFIX,
  messageIsAnchorlessCardRow,
  appendAnchorlessCardMessages,
  mergeLiveAttachments,
  transcriptMessagesForProvider,
  llmdMessageAttachmentFromRowContent,
  llmdMessageAttachmentRowContent,
} from './messageAttachments.js';
export type {
  AskCardType,
  AskChoiceCard,
  AskChoiceOption,
  AskFileCard,
  AskCard,
  AskChoiceResponse,
  AskFileResponse,
  AskCardResponse,
} from './askCard.js';
export {
  ASK_CARD_KIND,
  ASK_CARD_RESPONSE_KIND,
  encodeAskCard,
  decodeAskCard,
  encodeAskCardResponse,
  decodeAskCardResponse,
  synthesizeAskCardResponseContent,
  buildAskCardAttachment,
  buildAskCardResponseAttachment,
  indexAskCardResponses,
} from './askCard.js';
export type { DisplayCard, BuildDisplayCardAttachmentOptions } from './displayCard.js';
export {
  DISPLAY_CARD_KIND,
  DISPLAY_CARD_MAX_PAYLOAD_BYTES,
  encodeDisplayCard,
  decodeDisplayCard,
  synthesizeDisplayCardContent,
  buildDisplayCardAttachment,
} from './displayCard.js';
export type { ImageBody, ImageCard, ImageCardMimeType } from './imageCard.js';
export {
  IMAGE_CARD_TYPE,
  IMAGE_CARD_MIME_TYPES,
  IMAGE_CARD_MAX_BYTES,
  base64DecodedLength,
  imageCardDataUrl,
  imageCardSrcProblem,
  decodeImageBody,
  imageDisplayCard,
} from './imageCard.js';
export type { ChartBody, ChartCard, ChartCardFormat } from './chartCard.js';
export {
  CHART_CARD_TYPE,
  CHART_CARD_FORMATS,
  CHART_CARD_MAX_SPEC_BYTES,
  chartCardSpecProblem,
  decodeChartBody,
  chartSpecTitle,
  chartDisplayCard,
} from './chartCard.js';
export {
  LLMD_ATTACHMENT_KIND_PLUGINS,
  withLlmdAttachmentKindPlugins,
} from './builtinAttachmentKinds.js';
// Spawning the llmd binary is Node-only: see `@pyxis-llm/core/node`.
