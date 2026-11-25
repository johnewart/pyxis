/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @pyxis-llm/client — ONE client for llmd's LlmService, over either transport.
 *
 * Everything exported here is transport-agnostic. To actually connect, import a
 * transport binding:
 *
 *   import { createLlmdClient } from '@pyxis-llm/client/node';  // native gRPC, HTTP/2
 *   import { createLlmdWebClient } from '@pyxis-llm/client/web'; // grpc-web, fetch
 *
 * There is one codegen, one wrapper per RPC, and the transport is a
 * constructor argument — so a capability cannot exist on one transport and be
 * missing on the other.
 *
 * The single known exception is bidi streaming: gRPC-Web has no wire encoding
 * for it, so `runProviderAuthCommand` requires a '@pyxis-llm/client/node'
 * transport. See ./providerAuthCommand.ts.
 */

export {
  AssistantMessage,
  SystemMessage,
  ToolMessage,
  UserMessage,
  type Message,
  type MessageRole,
  type AssistantToolCall,
  type UserTextPart,
  type UserMessageAttachment,
} from "./messages.js";

export {
  CONVERSATION_EVENT_SCHEMA_VERSION,
  ConversationEventType,
  conversationStreamFrameToEvent,
  safeArgsJson,
  safeTodosJson,
  type AssistantTurnCapabilities,
  type ConversationEvent,
  type SessionTodoItem,
} from "./events.js";
export type { SessionTurnTiming } from "./conversationEvents.js";

export {
  toolInvocationLabel,
  type ToolInvocationLabelInput,
} from "./toolInvocationLabel.js";

export {
  libraryMessageToProto,
  assistantReplyToAssistantMessage,
  lastAssistantFromProtoMessages,
  messageUsageFromProto,
  MESSAGE_USAGE_METADATA_KEY,
  protoMessageToAssistantMessage,
  protoMessageToLibraryMessage,
  safeJsonRecord,
  type MessageUsage,
} from "./convert.js";

export { grpcSafeString, stripInvalidUtf8 } from "./utf8.js";

export type {
  ClaimResult,
  QueuedMessage,
  QueuedMessageAttachment,
  QueuedMessageState,
  SessionQueue,
} from "./sessionQueue.js";

export {
  LLMD_TENANT_METADATA_KEY,
  LLMD_SCOPE_METADATA_KEY,
  SYSTEM_PROMPT_SECTIONS_METADATA_KEY,
  scopeHeaders,
  type ScopeIdentity,
  emptyHeaders,
  callErrorMessage,
  getLlmError,
  llmErrorToInfo,
  type LlmErrorInfo,
  callUnary,
  UnaryTimeoutError,
  type CallOptions,
  type Transport,
  type UnaryCallOptions,
} from "./transport.js";

export {
  resolveLlmServiceClient,
  type LlmdCallTarget,
} from "./callTarget.js";

export {
  systemPromptSectionsWireJson,
  systemPromptSectionsWireMetadata,
  systemPromptSectionsToProto,
  type SystemPromptSectionWire,
} from "./systemPromptSectionsWire.js";

export {
  LlmdClient,
  createLlmdClientFromTransport,
  clientToolToRegisterProto,
  DefaultLLMOptions,
  type LLM,
  type LLMOptions,
  type LlmdTrackedDocument,
  type ClientTool,
  type ClientToolResult,
  type LlmdConnectionOptions,
  type LlmServiceClient,
  type SessionEventsSource,
  type LlmProviderSummary,
  type CatalogModelSummary,
  type RegisterProviderConfig,
  type ProviderCredentialSource,
  type SupportedProviderType,
  type CursorCliModeName,
  type OpenRouterCallOptionsConfig,
  type OpenRouterSortName,
  type ClaudeCodeLlmOptions,
} from "./llm.js";

export {
  ProviderAuthCommandSession,
  startProviderAuthCommand,
  // Session-based auth: same surface, but built from unary + server-streaming
  // calls so it also works over gRPC-Web (browsers). Prefer this when one code
  // path has to serve both web and Electron renderers.
  ProviderAuthSession,
  startProviderAuthSession,
  type ProviderAuthCommandEvent,
  type ProviderAuthCommandStartOptions,
} from "./providerAuthCommand.js";

export {
  defineClientTool,
  convertClientToolToOpenAiFormat,
  type ClientDefinedTool,
  type DefineClientToolConfig,
  type OpenAiFunctionTool,
} from "./client-defined-tool.js";

export {
  replaceLlmdSessionHistory,
  type ReplaceLlmdSessionHistoryOptions,
} from "./sessionHistory.js";

export {
  listLlmdSessions,
  iterateLlmdSessions,
  listAllLlmdSessions,
  sessionSummaryFromProto,
  DEFAULT_SESSION_PAGE_SIZE,
  MAX_SESSION_PAGE_SIZE,
  type LlmdSessionSummary,
  type LlmdSessionPage,
  type ListLlmdSessionsOptions,
  type SessionSortKey,
  type SessionSortDirection,
} from "./sessionList.js";

export {
  getLlmdStorageStats,
  deleteLlmdSessions,
  compactLlmd,
  type LlmdStorageStats,
  type LlmdStorageTableStat,
  type LlmdStorageSessionStat,
  type GetLlmdStorageStatsOptions,
  type DeleteLlmdSessionsOptions,
  type CompactLlmdOptions,
  type LlmdCompactResult,
} from "./storage.js";

export {
  upsertLlmdSessionAttachment,
  deleteLlmdSessionAttachment,
  patchLlmdAssistantMessageContent,
  getLlmdSessionAttachmentRows,
  type LlmdAttachmentInput,
  type UpsertLlmdSessionAttachmentOptions,
} from "./sessionAttachments.js";

export {
  agentProfileFromProto,
  listLlmdTools,
  type LlmdToolInfo,
  listLlmdAgentProfiles,
  getLlmdAgentProfile,
  createLlmdAgentProfile,
  updateLlmdAgentProfile,
  deleteLlmdAgentProfile,
  createLlmdSessionFromAgentProfile,
  agentProfileOverlayFromProto,
  getLlmdAgentProfileOverlay,
  setLlmdAgentProfileOverlay,
  resetLlmdAgentProfileOverlay,
  AgentProfileOverlayField,
  type LlmdAgentProfileOverlay,
  type LlmdAgentProfile,
  type LlmdAgentProfileToolMapping,
  type LlmdAgentProfileSpec,
  type CreateLlmdAgentProfileOptions,
  type UpdateLlmdAgentProfileOptions,
  type CreateLlmdSessionFromAgentProfileOptions,
} from "./agentProfiles.js";

export {
  ensureLlmdSession,
  getLlmdSessionMeta,
  setLlmdSessionMeta,
  updateLlmdSessionTags,
  type LlmdSessionMeta,
  forkLlmdSession,
  type ForkLlmdSessionOptions,
  type ForkLlmdSessionResult,
  updateLlmdSessionLlm,
  registerLlmdSessionTool,
  unregisterLlmdSessionTool,
  listLlmdSessionToolNames,
  type EnsureLlmdSessionOptions,
  type LlmdSessionScope,
} from "./session.js";

/*
 * Generated surface.
 *
 * protobuf-es emits, per message, a TYPE (`Foo`) plus a runtime SCHEMA
 * (`FooSchema`) instead of ts-proto's single class with static fromPartial —
 * and services are descriptors consumed by Connect's createClient rather than
 * generated *ServiceClient classes. That is far too many symbols to enumerate
 * here (and enumerating them is how llm.proto and this barrel silently drifted
 * before), so each generated module is re-exported wholesale under a namespace.
 * Namespacing also resolves the name collisions that forced the old
 * `...Wire`-suffixed aliases: mcpproxy and llm both declare ListProvidersRequest.
 *
 *   import { llmv1 } from '@pyxis-llm/client';
 *   const req = create(llmv1.ListSessionsRequestSchema, { pageSize: 20 });
 */
export * as llmv1 from "./gen/llmv1/llm_pb.js";
// mcpproxy: llmd owns the upstream MCP proxy itself (spawns the servers,
// composes proxied tool ids, dispatches calls) as part of its own tool
// runtime, not as workspace/graph/kanban data.
export * as mcpproxyv1 from "./gen/mcpproxy/v1/mcpproxy_pb.js";
// KnowledgeService: llmd's knowledgebase / embeddings / semantic search
// surface (separate SQLite + sqlite-vec + ONNX embeddings store).
export * as knowledgev1 from "./gen/knowledge/v1/knowledge_pb.js";
// LlamaCppService: llmd's managed local llama.cpp runtime (part of model /
// provider management — llmd resolves the binary, downloads GGUF models,
// spawns/reaps the server, and reports lifecycle verbatim).
export * as llamacppv1 from "./gen/llamacpp/v1/llamacpp_pb.js";

/** Service descriptors, for `createClient(Service, transport)`. */
export {
  LlmService,
  SessionCacheService,
  ToolProviderService,
} from "./gen/llmv1/llm_pb.js";

/**
 * Enums used directly in this package's public API.
 *
 * NOTE: protobuf-es strips the redundant enum-name prefix from member names, so
 * these are `LlmProviderType.ANTHROPIC` / `ToolClassification.MUTATION`, not the
 * old ts-proto `LLM_PROVIDER_TYPE_ANTHROPIC` / `TOOL_CLASSIFICATION_MUTATION`.
 * Wire numbers are unchanged. Use `enumToJson` from '@bufbuild/protobuf' when
 * you need the SCREAMING_CASE proto JSON name (ts-proto's `*ToJSON` helpers are
 * gone).
 */
export {
  CacheOptions_PromptCacheMode,
  CursorCliMode,
  LlmProviderType,
  ResponseFormat_Kind,
  SessionSortField,
  SessionSortOrder,
  ToolClassification,
} from "./gen/llmv1/llm_pb.js";
