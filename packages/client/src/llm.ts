import { create, enumToJson, type MessageInitShape } from "@bufbuild/protobuf";
import { createClient, type Client } from "@connectrpc/connect";
import {
  AssistantMessage,
  type Message,
} from "./messages.js";
import {
  assistantReplyToAssistantMessage,
  lastAssistantFromProtoMessages,
  libraryMessageToProto,
  protoMessageToLibraryMessage,
} from "./convert.js";
import { conversationStreamFrameToEvent } from "./events.js";
import type { ConversationEvent } from "./events.js";
import {
  safeTodosJson,
  type SessionTodoItem,
  type SessionTurnTiming,
} from "./conversationEvents.js";
import {
  callUnary,
  llmErrorToInfo,
  scopeHeaders,
  type LlmErrorInfo,
  type Transport,
} from "./transport.js";
import {
  systemPromptSectionsToProto,
  type SystemPromptSectionWire,
} from "./systemPromptSectionsWire.js";
import {
  CacheOptions_PromptCacheMode,
  CallOptionsSchema,
  ChatStreamRequestSchema,
  CompleteRequestSchema,
  type CompleteResponse,
  type CompleteStreamResponse,
  CursorCliMode,
  LlmProviderType,
  LlmProviderTypeSchema,
  LlmService,
  OpenRouterCallOptions_Sort,
  type OpenRouterCallOptions,
  ResponseFormat_Kind,
  ResponseFormatSchema,
  ToolClassification,
  ToolDefinitionSchema,
  type AssistantReply,
  type ConversationStreamFrame,
  ProviderConnectionStatus,
  CredentialSource,
  type GetLlmProviderConfigResponse,
  type LlmProviderInfo,
  type SupportedLlmProviderType,
  type ToolDefinition,
} from "./gen/llmv1/llm_pb.js";
import { grpcSafeString } from "./utf8.js";
// sessionList type-imports LlmServiceClient from this module; that import is
// type-only and erased, so this is not a runtime cycle.
import {
  listLlmdSessions,
  type ListLlmdSessionsOptions,
  type LlmdSessionPage,
} from "./sessionList.js";
import {
  startProviderAuthCommand,
  type ProviderAuthCommandSession,
  type ProviderAuthCommandStartOptions,
} from "./providerAuthCommand.js";
import type {
  ClaimResult,
  QueuedMessage,
  QueuedMessageAttachment,
  QueuedMessageState,
  SessionQueue,
} from "./sessionQueue.js";

/** GetSessionHistory's turn-timing fields; 0 on the wire means unknown. */
function sessionTurnTiming(res: {
  turnStartedAtMs?: bigint;
  lastTurnStartedAtMs?: bigint;
  lastTurnEndedAtMs?: bigint;
}): SessionTurnTiming {
  const started = Number(res.turnStartedAtMs ?? 0n);
  const lastStart = Number(res.lastTurnStartedAtMs ?? 0n);
  const lastEnd = Number(res.lastTurnEndedAtMs ?? 0n);
  return {
    ...(started > 0 ? { turnStartedAtMs: started } : {}),
    ...(lastStart > 0 && lastEnd >= lastStart
      ? { lastTurn: { startedAtMs: lastStart, endedAtMs: lastEnd } }
      : {}),
  };
}

/** Tail window when re-reading the session after ChatStream (canonical persisted text). */
const SESSION_HISTORY_TAIL = 64;

/**
 * Maps a wire QueuedMessage onto the client shape.
 *
 * The wire carries unix SECONDS (it is a proto int64 timestamp); every other
 * timestamp this client exposes is in milliseconds, so convert here rather than
 * leaving two units loose in the same API.
 */
function wireQueuedMessage(entry: {
  id?: string;
  content?: string;
  state?: string;
  claimOwner?: string;
  claimExpiresAtUnix?: bigint | number;
  createdAtUnix?: bigint | number;
  attachments?: Array<{ id?: string; kind?: string; payloadJson?: string; afterToolCallId?: string; content?: string }>;
} | undefined): QueuedMessage {
  const secondsToMs = (v: bigint | number | undefined): number | undefined =>
    v === undefined ? undefined : Number(v) * 1000;
  return {
    id: entry?.id ?? "",
    content: entry?.content ?? "",
    state: (entry?.state as QueuedMessageState) ?? "pending",
    claimOwner: entry?.claimOwner ?? "",
    claimExpiresAtMs: secondsToMs(entry?.claimExpiresAtUnix),
    createdAtMs: secondsToMs(entry?.createdAtUnix) ?? 0,
    attachments: (entry?.attachments ?? []).map((a) => ({
      id: a.id ?? "",
      kind: a.kind ?? "",
      payloadJson: a.payloadJson ?? "",
      ...(a.afterToolCallId ? { afterToolCallId: a.afterToolCallId } : {}),
      ...(a.content ? { content: a.content } : {}),
    })),
  };
}

/** The generated LlmService client, shared by both transports. */
export type LlmServiceClient = Client<typeof LlmService>;

/**
 * Structured result a {@link ClientTool.execute} may return instead of a plain string.
 * `endsTurn: true` asks llmd to end the turn after this round's tool calls finish
 * instead of querying the model again (e.g. a tool that proposes a change and waits
 * for user approval). Ignored when execute throws: failed calls never end the turn.
 */
export type ClientToolResult = {
  content: string;
  endsTurn?: boolean;
};

/** Tool shape aligned with ai-library-style registration (schema + optional local execute). */
export type ClientTool = {
  name: string;
  description: string;
  /** JSON Schema object for parameters (type object + properties). */
  parameters: Record<string, unknown>;
  /** When set, {@link LlmdClient.chat} runs this locally after each model tool call (client-side tool loop). */
  execute?: (
    args: Record<string, unknown>
  ) => Promise<string | ClientToolResult> | string | ClientToolResult;
  /** Query vs mutation for llmd read-only enforcement. Defaults to mutation when unset. */
  classification?: ToolClassification;
};

function toolArgsFromJson(s: string | undefined): Record<string, unknown> {
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
 * Proto tool definition for RegisterTool / EnsureSessionRequest.initial_tools.
 * Tools with a local `execute` handler are registered as delegate_to_client so ChatStream can block
 * until SubmitToolResults delivers the result.
 */
export function clientToolToRegisterProto(t: ClientTool): ToolDefinition {
  const parametersSchemaJson = JSON.stringify(
    t.parameters && Object.keys(t.parameters).length > 0
      ? t.parameters
      : { type: "object", properties: {} }
  );
  const name = t.name.trim();
  const classification = t.classification ?? ToolClassification.MUTATION;
  return create(ToolDefinitionSchema, {
    name,
    description: grpcSafeString(t.description ?? ""),
    parametersSchemaJson: grpcSafeString(parametersSchemaJson),
    classification,
    // protobuf-es models oneofs as a tagged union rather than ts-proto's flat
    // optional fields, so the execution variant is selected by `case`.
    ...(t.execute
      ? { execution: { case: "delegateToClient" as const, value: {} } }
      : {}),
  });
}

/** Per-turn Claude Code profile overrides (effort, budget, base model, thinking). */
export type ClaudeCodeLlmOptions = {
  effort?: string;
  maxBudgetUsd?: number;
  baseModel?: string;
  thinkingType?: string;
  thinkingBudgetTokens?: number;
};

const VALID_CLAUDE_THINKING_TYPES = new Set(["enabled", "disabled", "adaptive"]);

/** Map the public prompt-cache hint to the proto enum; undefined = leave cache options unset. */
function promptCacheModeToProto(
  mode?: "short" | "long" | "disabled"
): CacheOptions_PromptCacheMode | undefined {
  switch (mode) {
    case "short":
      return CacheOptions_PromptCacheMode.SHORT_TTL;
    case "long":
      return CacheOptions_PromptCacheMode.LONG_TTL;
    case "disabled":
      return CacheOptions_PromptCacheMode.DISABLED;
    default:
      return undefined;
  }
}

/**
 * Map the public response-format hint to the proto message; undefined leaves
 * the field unset so ordinary turns are byte-identical on the wire.
 */
function responseFormatToProto(
  rf?: LLMOptions["responseFormat"]
): MessageInitShape<typeof ResponseFormatSchema> | undefined {
  if (!rf) return undefined;
  if (rf.kind === "json_schema") {
    return {
      // ResponseFormat.Kind keeps its full member names: protobuf-es only strips
      // a prefix matching the enum's OWN name, and this enum is named `Kind`
      // while its values are RESPONSE_FORMAT_KIND_*. Sibling enums like
      // SessionSortField do get stripped.
      kind: ResponseFormat_Kind.RESPONSE_FORMAT_KIND_JSON_SCHEMA,
      jsonSchema: grpcSafeString(rf.jsonSchema ?? ""),
      schemaName: grpcSafeString(rf.schemaName ?? ""),
      strict: rf.strict ?? false,
    };
  }
  return { kind: ResponseFormat_Kind.RESPONSE_FORMAT_KIND_JSON_OBJECT };
}

function normalizeClaudeThinkingType(raw?: string): string | undefined {
  const t = (raw ?? "").trim().toLowerCase();
  if (!t || t === "default") return undefined;
  return VALID_CLAUDE_THINKING_TYPES.has(t) ? t : undefined;
}

/**
 * A document in scope for a turn. A client sends the full CURRENT body
 * (frontmatter included) each turn; llmd hash-dedups into immutable version
 * snapshots and injects them into the outbound model messages at send time. The
 * persisted user message stays exactly what the user typed. documentId is the
 * document's stable identity across turns (docUuid, or a review id).
 */
export type LlmdTrackedDocument = {
  documentId: string;
  name?: string;
  content: string;
};

/** Options passed per call; mirrors ai-library LLMOptions subset. */
export type LLMOptions = {
  model?: string;
  maxTokens?: number;
  temperature?: number;
  topP?: number;
  topK?: number;
  signal?: AbortSignal;
  tools?: ClientTool[];
  maxToolRounds?: number;
  /** When true, this turn must not invoke mutation tools (llmd enforces server-side). */
  readOnly?: boolean;
  /** Per-attempt provider call timeout in milliseconds (unset = server default). */
  attemptTimeoutMs?: number;
  /** Max provider call attempts including the first (unset = server default). */
  maxAttempts?: number;
  /**
   * Ask the provider for machine-readable output instead of prose.
   *
   * Best-effort per backend (OpenAI/Gemini/Ollama map natively; Anthropic and
   * Bedrock force a single tool call; CLI providers cannot express it at all),
   * so callers must still tolerate non-conforming output.
   */
  responseFormat?: {
    /** "json" = any JSON object; "json_schema" = conform to `jsonSchema`. */
    kind: "json" | "json_schema";
    /** JSON Schema as a JSON string. Required for "json_schema". */
    jsonSchema?: string;
    /** Name required by some providers; defaults server-side. */
    schemaName?: string;
    /** Ask the provider to reject non-conforming output where supported. */
    strict?: boolean;
  };
  /** Structured system prompt sections; llmd renders provider-specific prompt + MDC. */
  systemPromptSections?: readonly SystemPromptSectionWire[];
  /** Documents in scope for this turn; llmd hash-dedups + injects them at send time. */
  trackedDocuments?: readonly LlmdTrackedDocument[];
  /**
   * First-turn-only session context block (graph seed / neighborhood overview).
   * llmd persists it once on the session and injects it — frozen — as the lead
   * context on the first outbound model message. Ignored on later turns.
   */
  initialSeed?: string;
  /**
   * Let llmd decide whether this message runs now or waits in the session
   * queue: it runs only if the session is idle and its queue is empty and not
   * paused. When queued, `chatStreamEvents` yields a single `TurnQueued` event
   * and ends (no `complete`). Older llmd ignores this and always runs.
   */
  queueIfBusy?: boolean;
  claudeCode?: ClaudeCodeLlmOptions;
  /**
   * Provider prompt-cache hint. Providers that support prompt caching (e.g. Anthropic)
   * place a cache breakpoint at the end of the static system prefix (system + tools).
   * 'long' ≈ 1h TTL, 'short' ≈ 5m TTL, 'disabled' emits no cache markers. Omitted = provider default.
   */
  promptCacheMode?: "short" | "long" | "disabled";
};

export const DefaultLLMOptions: Readonly<Partial<LLMOptions>> = {};

export type LlmdConnectionOptions = {
  /**
   * The Connect Transport to use. Build one with `createLlmdTransport` from
   * '@pyxis-llm/client/node' (native gRPC over HTTP/2) or `createLlmdWebTransport`
   * from '@pyxis-llm/client/web' (grpc-web over fetch). This is the ONLY
   * transport-shaped thing in the client's API — everything else here is
   * identical on both.
   */
  transport: Transport;
  /** Sent as the `llmd-tenant-id` header on every RPC. */
  tenantId: string;
  /** Optional scope within the tenant; sent as `llmd-scope-id` when non-empty. */
  scopeId?: string;
  principalId?: string;
  /** Session identifier; scopes session-scoped RPCs (chat, stream, generate). */
  sessionId?: string;
  /**
   * Overrides how {@link LlmdClient.subscribeSessionEvents} gets its feed.
   *
   * Defaults to the SubscribeSessionEvents RPC. '@pyxis-llm/client/web' supplies a
   * WebSocket implementation instead (llmd's /ws/session-events) — a
   * deliberate choice, documented in ./web.ts.
   *
   * This is the ONE place the two transports intentionally differ in mechanism
   * rather than just in wire format. The method signature and the events it
   * yields are identical either way.
   */
  sessionEventsSource?: SessionEventsSource;
};

/** A live session-event feed; see {@link LlmdConnectionOptions.sessionEventsSource}. */
export type SessionEventsSource = (args: {
  sessionId: string;
  afterSeq?: number;
  signal?: AbortSignal;
}) => AsyncIterable<ConversationEvent>;

/**
 * ai-library-compatible LLM surface backed by llmd `LlmService`.
 *
 * Tools: registers JSON schemas (and delegate_to_client when `execute` is set) on the agent.
 * Unary {@link LlmdClient.chat} runs `execute` locally in a client-side tool loop. Streaming
 * {@link LlmdClient.chatStreamEvents} waits for tool frames and calls `SubmitToolResults` after each
 * local `execute`.
 *
 * Transport-agnostic: construct it with either transport and every method
 * below behaves identically.
 */
export class LlmdClient {
  readonly client: LlmServiceClient;
  private readonly headers: Headers;
  private readonly tenantId: string;
  private readonly scopeId: string | undefined;
  private readonly sessionId: string;
  private readonly sessionEventsSource: SessionEventsSource | undefined;
  private registeredToolNames = new Set<string>();

  constructor(conn: LlmdConnectionOptions) {
    this.client = createClient(LlmService, conn.transport);
    this.headers = scopeHeaders(conn);
    this.tenantId = conn.tenantId;
    this.scopeId = conn.scopeId;
    this.sessionEventsSource = conn.sessionEventsSource;
    // sessionId is optional for callers (stateless generateOnce needs none) but is
    // stored as a string ('' when absent). Session-scoped methods (chat, stream,
    // generate) read this.sessionId and fail server-side when it is empty.
    this.sessionId = conn.sessionId?.trim() || '';
  }

  toString(): string {
    return `LlmdClient(sessionId=${this.sessionId})`;
  }

  /**
   * Stable identity of the session this client talks to (tenant, scope,
   * session). Two clients with the same key address the same session, so a UI
   * can tell "the host rebuilt its client object" apart from "the user switched
   * to a different session" (useLlmdChatDriver resets on the latter only).
   */
  sessionKey(): string {
    return JSON.stringify([this.tenantId, this.scopeId ?? "", this.sessionId]);
  }

  supportsStructuredTools(): boolean {
    return true;
  }

  /**
   * After ChatStream, use persisted session history for the final assistant body (matches DB),
   * with stream_complete only as a fallback when there is no session_id or history read fails.
   */
  private async assistantMessageAfterStreamComplete(
    wire: AssistantReply,
    signal?: AbortSignal
  ): Promise<AssistantMessage> {
    const sid = this.sessionId.trim();
    if (!sid) {
      return assistantReplyToAssistantMessage(wire);
    }
    try {
      const res = await callUnary(
        (o) =>
          this.client.getSessionHistory(
            { sessionId: sid, maxMessages: SESSION_HISTORY_TAIL },
            o
          ),
        this.headers,
        signal,
        { label: "getSessionHistory" }
      );
      const fromDb = lastAssistantFromProtoMessages(res.messages);
      if (fromDb) {
        if (wire.finishReason?.trim()) {
          fromDb.finishReason = wire.finishReason;
        }
        return fromDb;
      }
    } catch {
      // ignore; fall back to wire
    }
    return assistantReplyToAssistantMessage(wire);
  }

  private toCallOptions(
    options?: LLMOptions
  ): MessageInitShape<typeof CallOptionsSchema> | undefined {
    const o = options;
    const thinkingType = normalizeClaudeThinkingType(o?.claudeCode?.thinkingType);
    const thinkingBudget =
      thinkingType === "disabled"
        ? undefined
        : o?.claudeCode?.thinkingBudgetTokens;
    const hasClaude =
      o?.claudeCode &&
      ((o.claudeCode.effort ?? "").trim() ||
        (o.claudeCode.baseModel ?? "").trim() ||
        (o.claudeCode.maxBudgetUsd != null && o.claudeCode.maxBudgetUsd > 0) ||
        thinkingType ||
        (thinkingBudget != null && thinkingBudget > 0));
    const promptCacheMode = promptCacheModeToProto(o?.promptCacheMode);
    const responseFormat = responseFormatToProto(o?.responseFormat);
    if (
      !o?.model &&
      o?.maxTokens === undefined &&
      o?.temperature === undefined &&
      o?.topP === undefined &&
      o?.topK === undefined &&
      o?.maxToolRounds === undefined &&
      o?.readOnly === undefined &&
      o?.attemptTimeoutMs === undefined &&
      o?.maxAttempts === undefined &&
      promptCacheMode === undefined &&
      responseFormat === undefined &&
      !hasClaude
    ) {
      return undefined;
    }
    return {
      model: grpcSafeString(o?.model ?? ""),
      maxTokens: o?.maxTokens ?? 0,
      temperature: o?.temperature,
      topP: o?.topP,
      // top_k is int64 on the wire; protobuf-es represents that as bigint.
      topK: o?.topK !== undefined ? BigInt(o.topK) : undefined,
      maxToolRounds: o?.maxToolRounds,
      readOnly: o?.readOnly,
      attemptTimeoutMs: o?.attemptTimeoutMs,
      maxAttempts: o?.maxAttempts,
      ...(promptCacheMode !== undefined
        ? { cacheOptions: { promptCacheMode } }
        : {}),
      ...(responseFormat !== undefined ? { responseFormat } : {}),
      ...(hasClaude
        ? {
            claudeCode: {
              effort: (o!.claudeCode!.effort ?? "").trim() || undefined,
              maxBudgetUsd: o!.claudeCode!.maxBudgetUsd,
              baseModel: (o!.claudeCode!.baseModel ?? "").trim() || undefined,
              thinkingType,
              thinkingBudgetTokens: thinkingBudget,
            },
          }
        : {}),
    };
  }

  /** Per-stream headers (base user/workspace). Structured sections use ChatStreamRequest.system_prompt_sections. */
  private streamCallHeaders(_options?: LLMOptions): Headers {
    return new Headers(this.headers);
  }

  private chatStreamRequestFromMessages(
    messages: Message[],
    options?: LLMOptions
  ): MessageInitShape<typeof ChatStreamRequestSchema> {
    const sections = options?.systemPromptSections;
    const sid = this.sessionId.trim();
    return {
      messages: messages.map(libraryMessageToProto),
      options: this.toCallOptions(options),
      sessionId: grpcSafeString(sid || this.sessionId),
      persistSession: true,
      ...(sections?.length
        ? { systemPromptSections: systemPromptSectionsToProto(sections) }
        : {}),
      ...(options?.trackedDocuments?.length
        ? {
            trackedDocuments: options.trackedDocuments.map((d) => ({
              documentId: grpcSafeString(d.documentId),
              name: grpcSafeString(d.name ?? ""),
              content: d.content,
            })),
          }
        : {}),
      ...((options?.initialSeed ?? "").trim()
        ? { initialSeed: grpcSafeString(options!.initialSeed!.trim()) }
        : {}),
      ...(options?.queueIfBusy ? { turnOptions: { queueIfBusy: true } } : {}),
    };
  }

  /**
   * Reads persisted session history (all roles), mapped to library
   * {@link Message} instances — the shape {@link LlmdClient.chatStreamEvents}
   * expects. Used to hydrate a fresh client-side transcript (e.g. after a
   * page reload) without replaying the whole conversation through the model.
   */
  async getHistory(maxMessages?: number): Promise<Message[]> {
    const sid = this.sessionId.trim();
    if (!sid) return [];
    const res = await callUnary(
      (o) =>
        this.client.getSessionHistory(
          { sessionId: sid, maxMessages: maxMessages ?? 0 },
          o
        ),
      this.headers,
      undefined,
      { label: "getSessionHistory" }
    );
    return (res.messages ?? []).map(protoMessageToLibraryMessage);
  }

  /**
   * Persisted history plus whether a turn is still running server-side, the
   * turn timing (see {@link SessionTurnTiming}), and the
   * event-log cursors needed to follow it. A turn keeps running when the client
   * goes away (navigation, reload), so a returning client uses this to rejoin:
   *
   * - `turnInProgress` → subscribe with `afterSeq: turnStartSeq` to rebuild the
   *   running turn's reply live, then re-read history once it finishes.
   * - otherwise → subscribe with `afterSeq: eventsSeq` to receive only what
   *   happens after this snapshot (no replay of older events).
   */
  async getSessionState(maxMessages?: number): Promise<{
    messages: Message[];
    turnInProgress: boolean;
    eventsSeq: number;
    turnStartSeq: number;
  } & SessionTurnTiming> {
    const sid = this.sessionId.trim();
    if (!sid) return { messages: [], turnInProgress: false, eventsSeq: 0, turnStartSeq: 0 };
    const res = await callUnary(
      (o) =>
        this.client.getSessionHistory(
          { sessionId: sid, maxMessages: maxMessages ?? 0 },
          o
        ),
      this.headers,
      undefined,
      { label: "getSessionHistory" }
    );
    return {
      messages: (res.messages ?? []).map(protoMessageToLibraryMessage),
      turnInProgress: !!res.turnInProgress,
      eventsSeq: Number(res.eventsSeq ?? 0n),
      turnStartSeq: Number(res.turnStartSeq ?? 0n),
      ...sessionTurnTiming(res),
    };
  }

  /**
   * The rejoin status of {@link LlmdClient.getSessionState} WITHOUT the
   * transcript: whether a turn is running server-side and the event-log
   * cursors to subscribe from. Server-side this skips loading messages,
   * attachments and tracked documents, so it is cheap enough to poll — use it
   * whenever you only need the cursor, and fetch history only when you actually
   * need to show it.
   */
  async getSessionStatus(): Promise<{
    turnInProgress: boolean;
    eventsSeq: number;
    turnStartSeq: number;
  } & SessionTurnTiming> {
    const sid = this.sessionId.trim();
    if (!sid) return { turnInProgress: false, eventsSeq: 0, turnStartSeq: 0 };
    const res = await callUnary(
      (o) =>
        this.client.getSessionHistory({ sessionId: sid, statusOnly: true }, o),
      this.headers,
      undefined,
      { label: "getSessionHistory(status_only)" }
    );
    return {
      turnInProgress: !!res.turnInProgress,
      eventsSeq: Number(res.eventsSeq ?? 0n),
      turnStartSeq: Number(res.turnStartSeq ?? 0n),
      ...sessionTurnTiming(res),
    };
  }

  /**
   * This session's persisted, session-scoped display state: its title and todo
   * list (both stored on the session row by set_session_title / add_todo /
   * update_todo). The session-events channel only carries CHANGES to these, so a
   * client opening (or switching to) a session reads the current values here
   * instead of showing nothing until the agent next touches them.
   *
   * `status` is the current turn's set_status headline ("" when none): llmd
   * clears it when a turn starts and ends, so it is only ever the running (or
   * just-started) turn's.
   */
  async getSessionInfo(): Promise<{ title: string; todos: SessionTodoItem[]; status: string }> {
    const sid = this.sessionId.trim();
    if (!sid) return { title: "", todos: [], status: "" };
    const res = await callUnary(
      (o) => this.client.getSession({ sessionId: sid }, o),
      this.headers,
      undefined,
      { label: "getSession" }
    );
    return {
      title: res.meta?.title ?? "",
      todos: safeTodosJson(res.meta?.todosJson ?? ""),
      status: res.meta?.status ?? "",
    };
  }

  /**
   * This session's provider + model selection, as persisted by
   * {@link LlmdClient.setSessionModel} (SessionMeta.provider_id/default_model).
   * Empty strings when none is set — e.g. a session created from an agent
   * profile that pins no provider, which cannot chat until one is chosen.
   */
  async getSessionModel(): Promise<{ providerId: string; modelId: string }> {
    const sid = this.sessionId.trim();
    if (!sid) return { providerId: "", modelId: "" };
    const res = await callUnary(
      (o) => this.client.getSession({ sessionId: sid }, o),
      this.headers,
      undefined,
      { label: "getSession" }
    );
    return {
      providerId: res.meta?.providerId ?? "",
      modelId: res.meta?.defaultModel ?? "",
    };
  }

  /**
   * Sets this session's provider and default model (UpdateSessionLlm). A chat
   * turn carries at most a model NAME (LLMOptions.model); the provider is
   * session state, so this — not a per-send option — is how a model picker
   * switches what the session talks to, including across providers.
   */
  async setSessionModel(providerId: string, modelId: string): Promise<void> {
    const sid = this.sessionId.trim();
    if (!sid) throw new Error("setSessionModel: client has no sessionId");
    await callUnary(
      (o) =>
        this.client.updateSessionLlm(
          {
            sessionId: sid,
            llmProviderId: grpcSafeString(providerId),
            defaultModel: grpcSafeString(modelId),
          },
          o
        ),
      this.headers,
      undefined,
      { label: "updateSessionLlm" }
    );
  }

  /**
   * This session's persisted UI attachments (ask cards, their answers, host
   * kinds), as raw rows. `content` is the JSON row encoding — decode it with
   * `@pyxis-llm/core`'s `llmdMessageAttachmentFromRowContent` (this package can't,
   * since `@pyxis-llm/core` depends on it). Attachments are loaded independently of
   * the message window, so the transcript is not fetched along with them.
   */
  async getSessionAttachmentRows(): Promise<Array<{ content: string; toolCallId: string }>> {
    const sid = this.sessionId.trim();
    if (!sid) return [];
    const res = await callUnary(
      (o) => this.client.getSessionHistory({ sessionId: sid, maxMessages: 1 }, o),
      this.headers,
      undefined,
      { label: "getSessionHistory(attachments)" }
    );
    return (res.attachmentRows ?? []).map((r) => ({ content: r.content, toolCallId: r.toolCallId }));
  }

  /**
   * Persist or replace one UI attachment on this session (e.g. an ask-card
   * answer). llmd broadcasts the change as an `attachment_upserted` event, so
   * every window on the session — including this one — sees it live.
   * `anchorMessageId` may be a message id or a tool call id; llmd falls back to
   * the latest assistant turn when it resolves to neither.
   */
  async upsertSessionAttachment(
    anchorMessageId: string,
    attachment: { id: string; kind: string; payloadJson: string; afterToolCallId?: string; content?: string }
  ): Promise<void> {
    const sid = this.requireSessionId("upsertSessionAttachment");
    await callUnary(
      (o) =>
        this.client.upsertSessionAttachment(
          {
            sessionId: sid,
            anchorMessageId: anchorMessageId.trim(),
            attachment: {
              id: attachment.id,
              kind: attachment.kind,
              payloadJson: attachment.payloadJson,
              ...(attachment.afterToolCallId ? { afterToolCallId: attachment.afterToolCallId } : {}),
              ...(attachment.content ? { content: attachment.content } : {}),
            },
          },
          o
        ),
      this.headers,
      undefined,
      { label: "upsertSessionAttachment" }
    );
  }

  /**
   * Delete one attachment from this session (e.g. dismissing an ask card).
   * llmd removes the row and broadcasts `attachment_deleted`, so every window
   * on the session drops it live. Nothing is queued and no turn starts.
   */
  async deleteSessionAttachment(attachmentId: string): Promise<void> {
    const sid = this.requireSessionId("deleteSessionAttachment");
    await callUnary(
      (o) => this.client.deleteSessionAttachment({ sessionId: sid, attachmentId: attachmentId.trim() }, o),
      this.headers,
      undefined,
      { label: "deleteSessionAttachment" }
    );
  }

  /** Database file size, free pages, per-table sizes and the largest sessions (tenant-scoped). */
  async getStorageStats(opts: { largestSessionsLimit?: number } = {}) {
    return callUnary(
      (o) => this.client.getStorageStats({ largestSessionsLimit: opts.largestSessionsLimit ?? 0 }, o),
      this.headers,
      undefined,
      { label: "getStorageStats" }
    );
  }

  /** Delete several sessions through the same purge as a single delete. */
  async deleteSessions(sessionIds: string[]) {
    return callUnary(
      (o) => this.client.deleteSessions({ sessionIds }, o),
      this.headers,
      undefined,
      { label: "deleteSessions" }
    );
  }

  /** Trim leftover events, VACUUM, and report bytes before and after. */
  async compact() {
    return callUnary(
      (o) => this.client.compact({}, o),
      this.headers,
      undefined,
      { label: "compact" }
    );
  }

  /**
   * Explicit Stop for this session's running turn. Aborting a
   * {@link LlmdClient.chatStreamEvents} call only detaches this client from the
   * stream — the turn keeps running and is persisted. This is the only way to
   * cut a turn short; llmd salvages what streamed so far with
   * `finish_reason = "cancelled"` (or `reason`). Resolves `true` if a running
   * turn was found.
   */
  async cancelTurn(reason?: string, signal?: AbortSignal): Promise<boolean> {
    const sid = this.requireSessionId("cancelTurn");
    const res = await callUnary(
      (o) =>
        this.client.cancelTurn(
          { sessionId: sid, ...(reason ? { reason: grpcSafeString(reason) } : {}) },
          o
        ),
      this.headers,
      signal,
      { label: "cancelTurn" }
    );
    return !!res.cancelled;
  }

  // --- Per-session composer draft ------------------------------------------
  //
  // The single unsent message typed into this session's composer. Exists so a
  // reload, a crash or a closed laptop does not lose it. Never history; the
  // model never sees it.

  /**
   * Saves this session's draft, replacing any previous one. Blank content clears
   * it — "the composer is empty" and "there is no draft" are the same state.
   *
   * Cheap by design (one row, one upsert), but callers should still debounce:
   * this is meant to be called as someone types.
   */
  async saveDraft(content: string): Promise<void> {
    const sid = this.requireSessionId("saveDraft");
    await callUnary(
      (o) => this.client.upsertSessionDraft({ sessionId: sid, content }, o),
      this.headers,
      undefined,
      { label: "upsertSessionDraft" }
    );
  }

  /** This session's draft, or "" when there is none. */
  async getDraft(): Promise<string> {
    const sid = this.requireSessionId("getDraft");
    const res = await callUnary(
      (o) => this.client.getSessionDraft({ sessionId: sid }, o),
      this.headers,
      undefined,
      { label: "getSessionDraft" }
    );
    return res.content ?? "";
  }

  /** Discards this session's draft — call when the message is actually sent. */
  async clearDraft(): Promise<void> {
    const sid = this.requireSessionId("clearDraft");
    await callUnary(
      (o) => this.client.clearSessionDraft({ sessionId: sid }, o),
      this.headers,
      undefined,
      { label: "clearSessionDraft" }
    );
  }

  // --- Per-session message queue -------------------------------------------
  //
  // See ./sessionQueue.ts for the drainer contract. llmd never drains the queue
  // itself; these are the primitives a drainer and a UI share.

  /**
   * Appends a message to the back of this session's queue. `attachments` ride
   * along and are persisted on the user message the entry becomes when sent.
   * `turnOptions.disableTools` runs the resulting turn with no tools (for
   * informational messages that should get a reply, not more work).
   */
  async enqueueMessage(
    content: string,
    attachments: QueuedMessageAttachment[] = [],
    turnOptions?: { disableTools?: boolean }
  ): Promise<QueuedMessage> {
    const sid = this.requireSessionId("enqueueMessage");
    const wireAttachments = attachments.map((a) => ({
      id: a.id,
      kind: a.kind,
      payloadJson: a.payloadJson,
      ...(a.afterToolCallId ? { afterToolCallId: a.afterToolCallId } : {}),
      ...(a.content ? { content: a.content } : {}),
    }));
    const res = await callUnary(
      (o) => this.client.enqueueMessage(
          {
            sessionId: sid,
            content,
            attachments: wireAttachments,
            ...(turnOptions?.disableTools ? { turnOptions: { disableTools: true } } : {}),
          },
          o
        ),
      this.headers,
      undefined,
      { label: "enqueueMessage" }
    );
    return wireQueuedMessage(res.entry);
  }

  /**
   * Answers an agent ask card (one created by the ask_user tool).
   *
   * `responseJson` is an encoded AskCardResponse (`encodeAskCardResponse` in
   * @pyxis-llm/core). llmd builds the model-facing summary itself. First answer:
   * queued at the FRONT of the queue as "Answer to: <title>" (returned as
   * `entry`), so the agent resumes on it. Re-answer: replaces the queued
   * answer, or — once sent — updates it in place without a new turn
   * (`updatedExisting`, no `entry`).
   *
   * `continueWithTools`: the answer's turn runs with tools enabled ("answer and
   * continue"). Default false ("answer and stop"): the agent replies without
   * running tools. Only the first answer's choice applies.
   */
  async answerAskCard(
    cardId: string,
    responseJson: string,
    opts?: { continueWithTools?: boolean }
  ): Promise<{ entry?: QueuedMessage; updatedExisting: boolean }> {
    const sid = this.requireSessionId("answerAskCard");
    const continueWithTools = opts?.continueWithTools === true;
    const res = await callUnary(
      (o) => this.client.answerAskCard({ sessionId: sid, cardId, responseJson, continueWithTools }, o),
      this.headers,
      undefined,
      { label: "answerAskCard" }
    );
    return {
      ...(res.entry ? { entry: wireQueuedMessage(res.entry) } : {}),
      updatedExisting: res.updatedExisting,
    };
  }

  /** This session's queue in run order, plus whether draining is paused. */
  async listQueue(): Promise<SessionQueue> {
    const sid = this.requireSessionId("listQueue");
    const res = await callUnary(
      (o) => this.client.listQueue({ sessionId: sid }, o),
      this.headers,
      undefined,
      { label: "listQueue" }
    );
    return {
      queue: (res.queue ?? []).map(wireQueuedMessage),
      paused: res.paused ?? false,
    };
  }

  /**
   * Drops a queued entry. Returns false when it was already gone — it ran, or
   * another window removed it — which is not an error.
   */
  async removeQueuedMessage(entryId: string): Promise<boolean> {
    const sid = this.requireSessionId("removeQueuedMessage");
    const res = await callUnary(
      (o) => this.client.removeQueuedMessage({ sessionId: sid, entryId }, o),
      this.headers,
      undefined,
      { label: "removeQueuedMessage" }
    );
    return res.removed ?? false;
  }

  /**
   * Rewrites queue order to `entryIds`. Pass the full order you want, as read
   * from {@link listQueue}: ids that no longer exist are ignored, and entries
   * you do not mention keep their relative order after the ones you do, so a
   * stale list can never silently drop queued work.
   */
  async reorderQueue(entryIds: string[]): Promise<QueuedMessage[]> {
    const sid = this.requireSessionId("reorderQueue");
    const res = await callUnary(
      (o) => this.client.reorderQueue({ sessionId: sid, entryIds }, o),
      this.headers,
      undefined,
      { label: "reorderQueue" }
    );
    return (res.queue ?? []).map(wireQueuedMessage);
  }

  /**
   * Pauses or resumes draining. Enforced server-side, so it holds for every
   * drainer — including ones this client knows nothing about.
   */
  async setQueuePaused(paused: boolean): Promise<void> {
    const sid = this.requireSessionId("setQueuePaused");
    await callUnary(
      (o) => this.client.setQueuePaused({ sessionId: sid, paused }, o),
      this.headers,
      undefined,
      { label: "setQueuePaused" }
    );
  }

  /**
   * Claims the next entry to run. `owner` identifies this drainer and must be
   * stable for its lifetime (e.g. a per-process uuid).
   *
   * See {@link ClaimResult}: an entry, a paused queue and an empty queue are
   * three different answers, and a drain loop has to treat them differently.
   */
  async claimNextQueuedMessage(owner: string): Promise<ClaimResult> {
    const sid = this.requireSessionId("claimNextQueuedMessage");
    const res = await callUnary(
      (o) => this.client.claimNextQueuedMessage({ sessionId: sid, owner }, o),
      this.headers,
      undefined,
      { label: "claimNextQueuedMessage" }
    );
    return {
      entry: res.entry ? wireQueuedMessage(res.entry) : undefined,
      paused: res.paused ?? false,
    };
  }

  /**
   * Extends a claim this drainer still owns. Call while the turn is genuinely
   * streaming; stopping lets the claim lapse so the entry can be reclaimed.
   * Rejects with FailedPrecondition once the lease has been lost.
   */
  async heartbeatQueuedMessage(entryId: string, owner: string): Promise<void> {
    const sid = this.requireSessionId("heartbeatQueuedMessage");
    await callUnary(
      (o) => this.client.heartbeatQueuedMessage({ sessionId: sid, entryId, owner }, o),
      this.headers,
      undefined,
      { label: "heartbeatQueuedMessage" }
    );
  }

  /**
   * Records the turn a claimed entry produced. Call as soon as the turn id is
   * known and BEFORE the turn finishes: this is what makes delivery
   * at-most-once if this drainer then dies.
   */
  async markQueuedMessageStarted(entryId: string, owner: string, turnId: string): Promise<void> {
    const sid = this.requireSessionId("markQueuedMessageStarted");
    await callUnary(
      (o) => this.client.markQueuedMessageStarted({ sessionId: sid, entryId, owner, turnId }, o),
      this.headers,
      undefined,
      { label: "markQueuedMessageStarted" }
    );
  }

  /** Removes a finished entry from the queue. */
  async retireQueuedMessage(entryId: string, owner: string): Promise<void> {
    const sid = this.requireSessionId("retireQueuedMessage");
    await callUnary(
      (o) => this.client.retireQueuedMessage({ sessionId: sid, entryId, owner }, o),
      this.headers,
      undefined,
      { label: "retireQueuedMessage" }
    );
  }

  /**
   * Hands an unstarted claim back at its original position — for a drainer
   * shutting down cleanly, rather than stalling the queue until its lease
   * expires.
   */
  async releaseQueuedMessage(entryId: string, owner: string): Promise<void> {
    const sid = this.requireSessionId("releaseQueuedMessage");
    await callUnary(
      (o) => this.client.releaseQueuedMessage({ sessionId: sid, entryId, owner }, o),
      this.headers,
      undefined,
      { label: "releaseQueuedMessage" }
    );
  }

  /**
   * Drafts and the queue are per session, so every one of those calls needs a
   * session id. Failing loudly beats sending "" and silently operating on
   * nothing.
   */
  private requireSessionId(op: string): string {
    const sid = this.sessionId.trim();
    if (!sid) {
      throw new Error(`${op}: this client has no sessionId (drafts and the queue are per session)`);
    }
    return sid;
  }

  /** List registered LLM providers for the connection's workspace. */
  async listProviders(): Promise<LlmProviderSummary[]> {
    const res = await callUnary(
      (o) => this.client.listLlmProviders({}, o),
      this.headers,
      undefined,
      { label: "listLlmProviders" }
    );
    return (res.providers ?? []).map(llmProviderInfoToSummary);
  }

  /**
   * Static catalog of provider kinds llmd can register (short-code, display name, credential
   * requirements) — independent of any already-configured provider instance. Drives an
   * "add provider" UI's type picker; use {@link LlmdClient.listProviders} for instances.
   */
  async listSupportedProviderTypes(): Promise<SupportedProviderType[]> {
    const res = await callUnary(
      (o) => this.client.listSupportedLlmProviderTypes({}, o),
      this.headers,
      undefined,
      { label: "listSupportedLlmProviderTypes" }
    );
    return (res.types ?? []).map(supportedLlmProviderTypeToSummary);
  }

  /**
   * Re-scans a provider's model catalog and returns how many models were
   * discovered. The result is persisted by llmd, so this only needs calling
   * when a provider's models may have actually changed — not on every app
   * start, and not after an llmd restart. Scanning is slow (a network
   * round-trip or CLI spawn per provider).
   */
  async refreshProviderModels(providerId: string): Promise<number> {
    const res = await callUnary(
      (o) => this.client.refreshLlmProviderModels({ providerId }, o),
      this.headers,
      undefined,
      { label: "refreshLlmProviderModels" }
    );
    return res.modelsDiscovered;
  }

  /**
   * List catalog models, optionally scoped to one provider. Never scans: it
   * returns llmd's persisted catalog, which survives llmd restarts. An empty
   * result for a configured provider means it has never been scanned
   * successfully (see `LlmProviderSummary.modelsRefreshedUnixMs`), so the fix
   * is `refreshProviderModels(id)` once — not a retry.
   */
  async listCatalogModels(providerId?: string): Promise<CatalogModelSummary[]> {
    const res = await callUnary(
      (o) =>
        this.client.listLlmCatalogModels(
          providerId?.trim() ? { providerId: providerId.trim() } : {},
          o
        ),
      this.headers,
      undefined,
      { label: "listLlmCatalogModels" }
    );
    return (res.models ?? []).map((m) => ({
      providerId: m.providerId,
      modelId: m.modelId,
      displayName: m.displayName,
    }));
  }

  /**
   * Starts an interactive CLI provider auth command (e.g. `claude login`) in
   * a server-side PTY, streamed over the wire. Only CLI-backed provider types
   * with an interactive login flow are supported server-side today
   * (LLM_PROVIDER_TYPE_CLAUDE_CODE / _V2). Not workspace-scoped: llmd itself
   * spawns and owns the process.
   */
  runProviderAuthCommand(
    providerType: LlmProviderType,
    options?: ProviderAuthCommandStartOptions
  ): ProviderAuthCommandSession {
    return startProviderAuthCommand(this.client, providerType, options);
  }

  /**
   * Registers (or updates) an LLM provider (global, not per-workspace). `config` covers the
   * API-key-shaped provider types (Anthropic/OpenAI/Gemini/Ollama/Bedrock);
   * CLI-backed types (Claude Code, Cursor CLI) have their own richer config
   * message server-side and aren't wrapped here yet.
   */
  async registerProvider(providerId: string, config: RegisterProviderConfig): Promise<void> {
    const cfg = config;
    await callUnary(
      (o) =>
        this.client.registerLlmProvider(
          {
            providerId,
            type: registerProviderConfigTypeToProto(cfg.type),
            config: registerProviderConfigToOneof(cfg),
          },
          o
        ),
      this.headers,
      undefined,
      { label: "registerLlmProvider" }
    );
  }

  /** Removes an LLM provider registered via {@link LlmdClient.registerProvider}. */
  async unregisterProvider(providerId: string): Promise<void> {
    await callUnary(
      (o) => this.client.unregisterLlmProvider({ providerId }, o),
      this.headers,
      undefined,
      { label: "unregisterLlmProvider" }
    );
  }

  /**
   * Scans the machine **llmd** is running on for installations of a CLI-backed provider's
   * binary (Claude Code, Cursor Agent).
   *
   * The scan runs server-side by necessity: llmd may be in a container, on a remote host, or
   * under a launcher whose `PATH` looks nothing like the caller's, so looking locally would
   * answer the wrong question.
   *
   * {@link LlmdClient.registerProvider} already does this implicitly when a CLI provider is
   * registered without a `binary`. Call this directly to show the user what's available (and at
   * which versions) before committing to one.
   *
   * Pass `binary` to check one specific path instead of scanning — the result then describes
   * just that path. Finding nothing is not an error: `binaries` comes back empty and `searched`
   * lists where llmd looked, which is what you want to show the user.
   */
  async discoverProviderBinaries(
    type: RegisterProviderConfig["type"],
    binary?: string
  ): Promise<{
    binaries: Array<{
      path: string;
      version: string;
      source: string;
      origin: string;
      usable: boolean;
      error: string;
    }>;
    searched: string[];
  }> {
    const res = await callUnary(
      (o) =>
        this.client.discoverProviderBinaries(
          { providerType: registerProviderConfigTypeToProto(type), binary },
          o
        ),
      this.headers,
      undefined,
      { label: "discoverProviderBinaries" }
    );
    return {
      binaries: res.binaries.map((b) => ({
        path: b.path,
        version: b.version,
        source: b.source,
        origin: b.origin,
        usable: b.usable,
        error: b.error,
      })),
      searched: [...res.searched],
    };
  }

  /**
   * Checks whether a CLI-backed provider config can actually be used — **without registering
   * it**. This is the "Connect" / "Test connection" action behind a provider form.
   *
   * Answers four questions in one call: is the binary there, is it new enough, is it signed in,
   * and can it resume sessions. Unlike {@link LlmdClient.registerProvider} (which runs the same
   * checks but only tells you pass/fail, and persists on success), this is safe to run before
   * the user commits, and reports *why* it failed in a form you can branch on.
   *
   * Switch on `status`; show `message`; never parse `message`. In particular
   * `status === "not_authenticated"` (mirrored as `needsAuth`) is the one the user can fix —
   * drive an interactive login with {@link startProviderAuthCommand}, then check again.
   *
   * Pass the same `configDir` the provider uses: Claude credentials are stored per config
   * directory, so omitting it checks a different account than the one the user signed into.
   *
   * COST: for Claude Code this makes two real (cheap) model calls, because headless resume
   * cannot be verified any other way. Wire it to an explicit button, not to form re-renders.
   */
  async checkProviderConnection(
    type: RegisterProviderConfig["type"],
    options: {
      binary?: string;
      cwd?: string;
      configDir?: string;
      oauthToken?: string;
      timeoutMs?: number;
      /**
       * Unsaved provider config to test (region, credentials, base URL, ...). Its `type` should
       * match the positional `type`. Secrets in it are sent to llmd but never echoed back.
       */
      config?: RegisterProviderConfig;
    } = {}
  ): Promise<{
    status:
      | "ok"
      | "binary_not_found"
      | "version_too_old"
      | "not_authenticated"
      | "resume_unsupported"
      | "timed_out"
      | "failed";
    binary: string;
    version: string;
    message: string;
    details: string;
    needsAuth: boolean;
    /** Structured provider error; undefined on success. */
    error: LlmErrorInfo | undefined;
    credentialSource: ProviderCredentialSource;
    warnings: string[];
    assumedRoleArn: string;
    callerIdentity: { account: string; arn: string; userId: string } | undefined;
  }> {
    const res = await callUnary(
      (o) =>
        this.client.checkProviderConnection(
          {
            providerType: registerProviderConfigTypeToProto(type),
            binary: options.binary,
            cwd: options.cwd,
            configDir: options.configDir,
            oauthToken: options.oauthToken,
            timeoutMs: options.timeoutMs,
            config: options.config
              ? registerProviderConfigToOneof(options.config)
              : { case: undefined, value: undefined },
          },
          o
        ),
      this.headers,
      undefined,
      { label: "checkProviderConnection" }
    );
    return {
      status: providerConnectionStatusFromProto(res.status),
      binary: res.binary,
      version: res.version,
      message: res.message,
      details: res.details,
      needsAuth: res.needsAuth,
      error: res.error ? llmErrorToInfo(res.error) : undefined,
      credentialSource: credentialSourceFromProto(res.credentialSource),
      warnings: [...res.warnings],
      assumedRoleArn: res.assumedRoleArn,
      callerIdentity: res.callerIdentity
        ? {
            account: res.callerIdentity.account,
            arn: res.callerIdentity.arn,
            userId: res.callerIdentity.userId,
          }
        : undefined,
    };
  }

  /**
   * Reads back the full stored config of an already-registered provider, for a "view/edit
   * provider" UI. NOTE: this echoes secret fields (e.g. apiKey) verbatim from storage — treat
   * the result as sensitive.
   */
  async getProviderConfig(providerId: string): Promise<RegisterProviderConfig> {
    const res = await callUnary(
      (o) => this.client.getLlmProviderConfig({ providerId }, o),
      this.headers,
      undefined,
      { label: "getLlmProviderConfig" }
    );
    return getLlmProviderConfigResponseToConfig(res);
  }

  /**
   * Replaces the full stored config of an already-registered provider (same `config` shape as
   * {@link LlmdClient.registerProvider}). Fails if `providerId` isn't already registered — use
   * {@link LlmdClient.registerProvider} to create a new one.
   */
  async updateProviderConfig(providerId: string, config: RegisterProviderConfig): Promise<void> {
    const cfg = config;
    await callUnary(
      (o) =>
        this.client.updateLlmProviderConfig(
          {
            providerId,
            type: registerProviderConfigTypeToProto(cfg.type),
            config: registerProviderConfigToOneof(cfg),
          },
          o
        ),
      this.headers,
      undefined,
      { label: "updateLlmProviderConfig" }
    );
  }

  /**
   * One page of this workspace's sessions, newest message first by default.
   *
   * Session listing was previously reachable only as the free function
   * `listLlmdSessions`, so a caller holding an LlmdClient could not list or
   * filter sessions without separately threading a transport and workspace id.
   * This delegates to that same function (one implementation, no drift) with the
   * client's own workspace and transport already applied.
   *
   * ```ts
   * const page = await client.listSessions({ chatSurface: 'build' });
   * const tagged = await client.listSessions({ tags: { team: 'infra' } });
   * ```
   */
  async listSessions(
    opts?: Omit<ListLlmdSessionsOptions, "tenantId" | "scopeId" | "transport" | "client">
  ): Promise<LlmdSessionPage> {
    return listLlmdSessions({
      ...(opts ?? {}),
      client: this.client,
      tenantId: this.tenantId,
      scopeId: this.scopeId,
    });
  }

  /**
   * Merge k/v tags into this client's session; returns the tags as stored.
   * A key whose value is "" is deleted. Pass `replace` for wholesale semantics.
   */
  async updateSessionTags(
    tags: Record<string, string>,
    opts?: { replace?: boolean }
  ): Promise<Record<string, string>> {
    const res = await callUnary(
      (o) =>
        this.client.updateSessionTags(
          { sessionId: this.sessionId, tags, replace: opts?.replace ?? false },
          o
        ),
      this.headers,
      undefined,
      { label: "updateSessionTags" }
    );
    return { ...(res.tags ?? {}) };
  }

  /** List tool names currently registered on this llmd agent. */
  async listRegisteredToolNames(): Promise<string[]> {
    const res = await callUnary(
      (o) => this.client.listSessionTools({ sessionId: this.sessionId }, o),
      this.headers,
      undefined,
      { label: "listSessionTools" }
    );
    const names: string[] = [];
    for (const t of res.tools ?? []) {
      const n = t.name?.trim();
      if (n) names.push(n);
    }
    return names;
  }

  /** Registers tool schemas on llmd; unregisters tools no longer in the desired set (reconciles agent snapshot). */
  async registerTools(tools: ClientTool[] | undefined): Promise<void> {
    const next = new Set(
      (tools ?? []).map((t) => t.name.trim()).filter((n) => n.length > 0)
    );
    // Only tidy up after ourselves: tools THIS client registered and no longer wants.
    // Never reconcile against the server's listing — tools installed server-side
    // (agent profile, builtins) are not ours to remove, and callers that pass no
    // tools at all would otherwise strip the session bare on every turn.
    const toRemove = new Set<string>(
      [...this.registeredToolNames].filter((n) => !next.has(n))
    );
    for (const name of toRemove) {
      await callUnary(
        (o) =>
          this.client.unregisterSessionTool(
            { name, sessionId: this.sessionId },
            o
          ),
        this.headers,
        undefined,
        { label: "unregisterSessionTool" }
      );
      this.registeredToolNames.delete(name);
    }
    if (!tools?.length) return;
    for (const t of tools) {
      const name = t.name.trim();
      if (!name) continue;
      const tool = clientToolToRegisterProto(t);
      await callUnary(
        (o) =>
          this.client.registerSessionTool(
            { tool, sessionId: this.sessionId },
            o
          ),
        this.headers,
        undefined,
        { label: "registerSessionTool" }
      );
      this.registeredToolNames.add(name);
    }
  }

  /**
   * Streams assistant text via `onChunk`. When the RPC ends, resolves with the **persisted**
   * assistant message from `GetSessionHistory` (same source as the DB) if `sessionId` was set
   * on the connection; otherwise uses `stream_complete` from the wire.
   *
   * Prefer {@link LlmdClient.chatStreamEvents} for host apps and agents: it surfaces thinking, activity,
   * tool streaming, and delegate tool execution; this method only forwards visible text deltas.
   *
   * @deprecated Use `chatStreamEvents` for full-fidelity ChatStream unless you only need plain text chunks.
   */
  async stream(
    messages: Message[],
    onChunk: (chunk: string) => void,
    options?: LLMOptions
  ): Promise<AssistantMessage> {
    await this.registerTools(options?.tools);
    const req = this.chatStreamRequestFromMessages(messages, options);
    let last: AssistantReply | undefined;
    // Connect server-streams are plain async iterables and honour the signal
    // directly, so the old event-emitter plumbing (data/error/end + manual
    // cancel) collapses into a for-await.
    for await (const frame of this.client.chatStream(req, {
      headers: this.streamCallHeaders(options),
      signal: options?.signal,
    })) {
      const f = frame.frame;
      if (f.case === "assistantTextDelta") {
        const d = f.value.delta;
        if (d) onChunk(d);
      } else if (f.case === "streamComplete") {
        last = f.value;
      }
    }
    if (!last) {
      throw new Error("LlmdClient.stream: stream ended without stream_complete");
    }
    return this.assistantMessageAfterStreamComplete(last, options?.signal);
  }

  /**
   * Stateless one-shot completion: no agent, no session, no persistence, no
   * tools. The caller supplies the provider (options.providerId) and model
   * (options.model). Used by utility one-shots (provider setup probe,
   * plan/design summaries) that have an explicit provider/model but no session.
   */
  async generateOnce(
    systemPrompt: string,
    prompt: string,
    options?: LLMOptions & {
      providerId?: string;
      /** Optional multi-part user message; mutually exclusive with a non-empty prompt. */
      promptParts?: { text: string; cacheBreakpoint?: boolean }[];
    }
  ): Promise<AssistantMessage> {
    const reply = await callUnary(
      (o) =>
        this.client.generateOnce(
          {
            systemPrompt: grpcSafeString(systemPrompt),
            prompt: grpcSafeString(prompt),
            options: this.toCallOptions(options),
            ...(options?.promptParts?.length
              ? {
                  promptParts: options.promptParts.map((p) => ({
                    text: grpcSafeString(p.text),
                    cacheBreakpoint: p.cacheBreakpoint ?? false,
                  })),
                }
              : {}),
            ...(options?.providerId
              ? { providerId: grpcSafeString(options.providerId) }
              : {}),
          },
          o
        ),
      this.headers,
      options?.signal,
      { label: "generateOnce" }
    );
    return assistantReplyToAssistantMessage(reply);
  }

  /**
   * Stateless completion over an explicit message list (no agent, no session).
   * Tool calls are returned in the reply, never executed by llmd.
   */
  async complete(
    request: MessageInitShape<typeof CompleteRequestSchema>,
    options?: { signal?: AbortSignal }
  ): Promise<CompleteResponse> {
    return callUnary(
      (o) => this.client.complete(request, o),
      this.headers,
      options?.signal,
      { label: "complete" }
    );
  }

  /**
   * Streaming variant of complete(): yields content/thinking deltas and tool
   * calls, then a final frame carrying the full reply.
   */
  completeStream(
    request: MessageInitShape<typeof CompleteRequestSchema>,
    options?: { signal?: AbortSignal }
  ): AsyncIterable<CompleteStreamResponse> {
    return this.client.completeStream(request, {
      headers: this.streamCallHeaders(),
      signal: options?.signal,
    });
  }

  /**
   * Low-level streaming: yields conversation events, then a `complete` item whose message is
   * loaded from `GetSessionHistory` when `sessionId` is set (canonical persisted turn), else from `stream_complete`.
   */
  async *chatStreamEvents(
    messages: Message[],
    options?: LLMOptions
  ): AsyncGenerator<
    | { kind: "event"; event: ConversationEvent }
    | { kind: "complete"; message: AssistantMessage }
  > {
    await this.registerTools(options?.tools);
    const req = this.chatStreamRequestFromMessages(messages, options);
    let last: AssistantReply | undefined;
    const byName = new Map(
      (options?.tools ?? []).map((t) => [t.name.trim(), t])
    );
    const argsByToolCallId = new Map<string, Record<string, unknown>>();

    for await (const frame of this.client.chatStream(req, {
      headers: this.streamCallHeaders(options),
      signal: options?.signal,
    })) {
      const f: ConversationStreamFrame["frame"] = frame.frame;
      if (f.case === "toolCallStreamFinished" && f.value.id) {
        argsByToolCallId.set(f.value.id, toolArgsFromJson(f.value.argumentsJson));
      }
      const ev = conversationStreamFrameToEvent(frame);
      if (ev) yield { kind: "event", event: ev };
      // queueIfBusy and llmd queued it: there is no turn on this stream.
      if (f.case === "turnQueued") return;
      if (f.case === "toolExecutionStarted") {
        const tes = f.value;
        const nonce = tes.clientWaitNonce?.trim();
        if (nonce && tes.toolCallId && tes.name?.trim()) {
          const toolName = tes.name.trim();
          const handler = byName.get(toolName);
          let content: string;
          let isError = false;
          let endsTurn = false;
          if (handler?.execute) {
            try {
              const args = argsByToolCallId.get(tes.toolCallId) ?? {};
              const out = await Promise.resolve(handler.execute(args));
              if (out !== null && typeof out === "object") {
                content = String(out.content ?? "");
                endsTurn = out.endsTurn === true;
              } else {
                content = String(out);
              }
            } catch (e) {
              content = e instanceof Error ? e.message : String(e);
              isError = true;
            }
          } else {
            content = `Error: no execute handler registered locally for tool "${toolName}"`;
            isError = true;
          }
          await callUnary(
            (o) =>
              this.client.submitToolResults(
                {
                  sessionId: this.sessionId,
                  clientWaitNonce: nonce,
                  toolCallId: tes.toolCallId,
                  content: grpcSafeString(content),
                  isError,
                  endsTurn: endsTurn && !isError,
                },
                o
              ),
            this.headers,
            options?.signal,
            { label: "submitToolResults" }
          );
        }
      }
      if (f.case === "streamComplete") last = f.value;
    }
    if (!last) throw new Error("chatStreamEvents: stream ended without stream_complete");
    const message = await this.assistantMessageAfterStreamComplete(last, options?.signal);
    yield { kind: "complete", message };
  }

  /**
   * Live side channel for everything happening in a session, independent of
   * any particular {@link LlmdClient.chatStreamEvents} call: a replay
   * (snapshot when `afterSeq` is omitted/0, delta since `afterSeq`
   * otherwise) followed by a genuine live tail for as long as the caller
   * keeps iterating.
   *
   * This exists because `chatStreamEvents`' own stream only carries frames
   * sourced from the specific turn it's driving — for CLI-backed providers
   * (Claude Code, Cursor CLI), MCP-dispatched tool calls run in a separate
   * request concurrent with that turn and never reach it (see
   * internal/mcpserver/server.go's `handleToolsCall` /
   * `publishToolEventFrame` and `SubscribeSessionEvents`' own doc comment
   * server-side). A host wanting live visibility into tool execution
   * progress, session status/title/todos, etc. for a CLI-backed session —
   * not just whatever its own `chatStreamEvents` call happens to be
   * streaming — should open this concurrently alongside it, e.g.:
   *
   *   const events = useMemo(() => client?.chatStreamEvents === undefined ? undefined :
   *     (async () => {
   *       for await (const event of client.subscribeSessionEvents()) onToolEvent(event);
   *     })(), [client]);
   *
   * Delivery is exactly-once and in order with no gap between replay and
   * live: llmd serves every frame from its seq-ordered session event log.
   * Pass `afterSeq` from {@link LlmdClient.getSessionState} — `turnStartSeq`
   * to rejoin a running turn, `eventsSeq` to receive only what is new — rather
   * than 0, which replays the session's entire event history.
   */
  async *subscribeSessionEvents(
    options?: { afterSeq?: number; signal?: AbortSignal }
  ): AsyncGenerator<ConversationEvent> {
    if (this.sessionEventsSource) {
      yield* this.sessionEventsSource({
        sessionId: this.sessionId,
        afterSeq: options?.afterSeq,
        signal: options?.signal,
      });
      return;
    }
    for await (const frame of this.client.subscribeSessionEvents(
      {
        sessionId: this.sessionId,
        // after_seq is int64 on the wire.
        afterSeq: BigInt(options?.afterSeq ?? 0),
      },
      { headers: this.headers, signal: options?.signal }
    )) {
      const ev = conversationStreamFrameToEvent(frame);
      if (ev) yield ev;
    }
  }
}

/** Registered LLM provider (config + catalog metadata). */
export type LlmProviderSummary = {
  id: string;
  /** e.g. "LLM_PROVIDER_TYPE_OPENAI"; generic string so callers don't depend on the proto enum. */
  type: string;
  defaultModel: string;
  catalogModelCount: number;
  /**
   * When this provider's model catalog was last discovered, in unix ms, or 0 if
   * it has never been scanned successfully. llmd persists the catalog, so this
   * is a real discovery time that survives llmd restarts — it is NOT restamped
   * when llmd reloads the provider.
   *
   * This is the ONLY way to tell "never scanned" (0, so call
   * {@link LlmdClient.refreshProviderModels} once) from "scanned and this
   * provider genuinely offers no models". Both look like an empty
   * {@link LlmdClient.listCatalogModels} result.
   */
  modelsRefreshedUnixMs: number;
  userRegistered: boolean;
  stateful: boolean;
};

/** A model in a provider's catalog. */
export type CatalogModelSummary = {
  providerId: string;
  modelId: string;
  displayName?: string;
};

function llmProviderInfoToSummary(p: LlmProviderInfo): LlmProviderSummary {
  return {
    id: p.id,
    // protobuf-es strips the enum's redundant prefix from member NAMES, but
    // enumToJson still yields the full proto JSON name, so this string stays
    // "LLM_PROVIDER_TYPE_OPENAI" exactly as ts-proto's llmProviderTypeToJSON did.
    type: String(enumToJson(LlmProviderTypeSchema, p.type)),
    defaultModel: p.defaultModel,
    catalogModelCount: p.catalogModelCount,
    // int64 on the wire, so protobuf-es hands back a bigint. Unix ms is far
    // inside Number's exact-integer range, and leaving it a bigint would make
    // every consumer do arithmetic against a type they can't mix with Date.now().
    modelsRefreshedUnixMs: Number(p.modelsRefreshedUnixMs),
    userRegistered: p.userRegistered,
    stateful: p.stateful,
  };
}

/** Provider kind llmd can register, as advertised by {@link LlmdClient.listSupportedProviderTypes}. */
export type SupportedProviderType = {
  /** Stable lowercase identifier, e.g. "openai", "claude_code_v2" — matches {@link RegisterProviderConfig}'s `type`. */
  shortCode: string;
  displayName: string;
  requiresApiKey: boolean;
  isCliBacked: boolean;
};

function supportedLlmProviderTypeToSummary(t: SupportedLlmProviderType): SupportedProviderType {
  return {
    shortCode: t.shortCode,
    displayName: t.displayName,
    requiresApiKey: t.requiresApiKey,
    isCliBacked: t.isCliBacked,
  };
}

/** Cursor CLI --mode, as a plain string (mirrors llmd's internal Kind string convention). */
export type CursorCliModeName = "default" | "ask" | "plan";

function cursorCliModeNameToProto(m: CursorCliModeName | undefined): CursorCliMode {
  switch (m) {
    case "default":
      return CursorCliMode.DEFAULT;
    case "ask":
      return CursorCliMode.ASK;
    case "plan":
      return CursorCliMode.PLAN;
    default:
      return CursorCliMode.UNSPECIFIED;
  }
}

function cursorCliModeProtoToName(m: CursorCliMode | undefined): CursorCliModeName | undefined {
  switch (m) {
    case CursorCliMode.DEFAULT:
      return "default";
    case CursorCliMode.ASK:
      return "ask";
    case CursorCliMode.PLAN:
      return "plan";
    default:
      return undefined;
  }
}

/** Mirrors `llm.v1.OpenRouterCallOptions.Sort` (without UNSPECIFIED). */
export type OpenRouterSortName = "price" | "throughput" | "latency";

/** Typed mirror of `llm.v1.OpenRouterCallOptions` (per-provider default routing overrides). */
export type OpenRouterCallOptionsConfig = {
  sort?: OpenRouterSortName;
  /** Preferred upstream provider names, in order. */
  order?: string[];
  /** Restrict routing to only these upstream providers. */
  only?: string[];
  /** Upstream providers to skip. */
  ignore?: string[];
  /** Whether OpenRouter may fall back to other upstream providers. */
  allowFallbacks?: boolean;
};

function openRouterSortNameToProto(s: OpenRouterSortName | undefined): OpenRouterCallOptions_Sort {
  switch (s) {
    case "price":
      return OpenRouterCallOptions_Sort.PRICE;
    case "throughput":
      return OpenRouterCallOptions_Sort.THROUGHPUT;
    case "latency":
      return OpenRouterCallOptions_Sort.LATENCY;
    default:
      return OpenRouterCallOptions_Sort.UNSPECIFIED;
  }
}

function openRouterSortProtoToName(s: OpenRouterCallOptions_Sort): OpenRouterSortName | undefined {
  switch (s) {
    case OpenRouterCallOptions_Sort.PRICE:
      return "price";
    case OpenRouterCallOptions_Sort.THROUGHPUT:
      return "throughput";
    case OpenRouterCallOptions_Sort.LATENCY:
      return "latency";
    default:
      return undefined;
  }
}

function openRouterCallOptionsFromProto(
  o: OpenRouterCallOptions | undefined
): OpenRouterCallOptionsConfig | undefined {
  if (!o) return undefined;
  return {
    sort: openRouterSortProtoToName(o.sort),
    order: o.order,
    only: o.only,
    ignore: o.ignore,
    allowFallbacks: o.allowFallbacks,
  };
}

/**
 * API-key- or CLI-shaped provider config for {@link LlmdClient.registerProvider} /
 * {@link LlmdClient.updateProviderConfig} / {@link LlmdClient.getProviderConfig}. `type` is the
 * short_code from {@link LlmdClient.listSupportedProviderTypes}. Every field here is already a
 * field on the matching `llm.v1.*LlmConfig` proto message (see proto/llmv1/llm.proto) — this type
 * just exposes the full set to TS callers instead of the small subset previously wrapped.
 *
 * `type` is EXACTLY one canonical name per provider kind — llmd's `Kind.String()`, which is what
 * `ListSupportedLlmProviderTypes.short_code` advertises. There is deliberately no second spelling:
 * this used to accept `claude_v2`/`cursor` as well, because `@pyxis-llm/core`'s `ProviderKind` union had
 * invented its own names, and the two halves of the SDK disagreed. `ProviderKind` now emits the
 * short_code, so a provider kind has one id everywhere — proto enum, Go `Kind`, both TS packages.
 * Keep it that way: an alias union here silently breaks discriminated-union narrowing for every
 * consumer (`Extract<RegisterProviderConfig, { type: 'cursor_cli' }>` collapses to `never`).
 */
export type RegisterProviderConfig =
  | {
      type: "anthropic";
      apiKey: string;
      baseUrl?: string;
      defaultModel?: string;
      maxOutputTokens?: number;
    }
  | {
      type: "openai";
      apiKey: string;
      baseUrl?: string;
      defaultModel?: string;
      maxTokens?: number;
    }
  | {
      type: "gemini";
      apiKey: string;
      baseUrl?: string;
      defaultModel?: string;
      maxOutputTokens?: number;
    }
  | {
      type: "openrouter";
      /** Returned redacted by the server; send only when changed. */
      apiKey: string;
      baseUrl?: string;
      defaultModel?: string;
      defaultCallOptions?: OpenRouterCallOptionsConfig;
      /** Avoid upstream endpoints reported unhealthy. Unset means enabled. */
      avoidUnhealthyEndpoints?: boolean;
      /** Endpoint-health cache TTL in seconds. Unset means 60. */
      endpointHealthTtlSeconds?: number;
    }
  | { type: "ollama"; baseUrl?: string; defaultModel?: string; maxTokens?: number }
  | {
      type: "bedrock";
      region: string;
      profile?: string;
      defaultModel?: string;
      maxOutputTokens?: number;
      endpointUrl?: string;
      assumeRoleArn?: string;
      roleSessionName?: string;
      /** Write-only: sent on save, never returned on read. */
      externalId?: string;
    }
  | {
      type: "cursor_cli";
      binary?: string;
      cwd?: string;
      workspace?: string;
      apiKey?: string;
      defaultModel?: string;
      cliMode?: CursorCliModeName;
      trust?: boolean;
      approveMcps?: boolean;
      force?: boolean;
      sessionId?: string;
      maxOutputTokens?: number;
    }
  | {
      type: "claude_code_v2";
      binary?: string;
      cwd?: string;
      agent?: string;
      defaultModel?: string;
      systemPrompt?: string;
      appendSystemPrompt?: string;
      /** "low" | "medium" | "high" | "xhigh" | "max" */
      effort?: string;
      /** e.g. default, acceptEdits, bypassPermissions, dontAsk, plan */
      permissionMode?: string;
      maxBudgetUsd?: number;
      addDir?: string[];
      allowedTools?: string[];
      disallowedTools?: string[];
      mcpConfigPath?: string;
      timeoutMs?: number;
      maxTurns?: number;
      /** CLI --setting-sources values: user, project, local */
      settingSources?: string[];
      /** enabled, disabled, adaptive */
      thinkingType?: string;
      thinkingBudgetTokens?: number;
      /** JSON object merged into --settings hooks key. */
      hooksJson?: string;
      strictMcpConfig?: boolean;
      includeHookEvents?: boolean;
      configDir?: string;
      useMyClaudeConfig?: boolean;
      deferToolSchemas?: boolean;
      useClaudeCodePreset?: boolean;
      /** Long-lived token from `claude setup-token`; sent to the CLI as CLAUDE_CODE_OAUTH_TOKEN. */
      oauthToken?: string;
    };

/**
 * The provider id → proto enum mapping, and the ONLY place the ids appear in
 * this SDK.
 *
 * There is deliberately no separate list of supported ids. TypeScript cannot
 * enumerate a union at runtime, so any such list would be a second closed set
 * that compiles fine while drifting from `RegisterProviderConfig["type"]` — the
 * same duplication that let `@pyxis-llm/core` publish `claude_v2`/`cursor` while llmd
 * said `claude_code_v2`/`cursor_cli`, which is how a name llmd rejected outright
 * ever reached it. This switch is the mapping AND the set: the case labels sit
 * next to the generated `LlmProviderType` member each one maps to, and TS
 * requires every union member to be handled (drop one and the function stops
 * returning on all paths).
 *
 * The `default` exists for callers that are not type-checked (plain JS, an `as`
 * cast, a value off the wire). Without it an unknown id fell out of the switch
 * as `undefined`, protobuf-es serialised that as the enum zero value, and llmd
 * answered "provider type LLM_PROVIDER_TYPE_UNSPECIFIED is not backed by a CLI,
 * so there is no connection to check" — describing the zero value instead of the
 * garbage two layers away. Throw here, naming the culprit.
 */
function registerProviderConfigTypeToProto(t: RegisterProviderConfig["type"]): LlmProviderType {
  switch (t) {
    case "anthropic":
      return LlmProviderType.ANTHROPIC;
    case "openai":
      return LlmProviderType.OPENAI;
    case "gemini":
      return LlmProviderType.GEMINI;
    case "openrouter":
      return LlmProviderType.OPENROUTER;
    case "ollama":
      return LlmProviderType.OLLAMA;
    case "bedrock":
      return LlmProviderType.BEDROCK;
    case "cursor_cli":
      return LlmProviderType.CURSOR_CLI;
    case "claude_code_v2":
      return LlmProviderType.CLAUDE_CODE_V2;
    default:
      throw new Error(`unsupported provider type ${JSON.stringify(t)}`);
  }
}

/**
 * The `config` oneof for Register/UpdateLlmProviderConfig.
 *
 * protobuf-es models a oneof as a single `{ case, value }` tagged union rather
 * than ts-proto's flat set of optional sibling fields, so this returns the
 * tagged variant instead of `{ anthropic: {...} }`. int64 config fields
 * (max_output_tokens, max_tokens) become bigint on the wire.
 */
function registerProviderConfigToOneof(config: RegisterProviderConfig) {
  const big = (n: number | undefined) => (n !== undefined ? BigInt(n) : undefined);
  switch (config.type) {
    case "anthropic":
      return {
        case: "anthropic" as const,
        value: {
          apiKey: config.apiKey,
          baseUrl: config.baseUrl,
          defaultModel: config.defaultModel,
          maxOutputTokens: big(config.maxOutputTokens),
        },
      };
    case "openai":
      return {
        case: "openai" as const,
        value: {
          apiKey: config.apiKey,
          baseUrl: config.baseUrl,
          defaultModel: config.defaultModel,
          maxTokens: config.maxTokens,
        },
      };
    case "gemini":
      return {
        case: "gemini" as const,
        value: {
          apiKey: config.apiKey,
          baseUrl: config.baseUrl,
          defaultModel: config.defaultModel,
          maxOutputTokens: big(config.maxOutputTokens),
        },
      };
    case "openrouter":
      return {
        case: "openrouter" as const,
        value: {
          apiKey: config.apiKey,
          baseUrl: config.baseUrl,
          defaultModel: config.defaultModel,
          defaultCallOptions: config.defaultCallOptions
            ? {
                sort: openRouterSortNameToProto(config.defaultCallOptions.sort),
                order: config.defaultCallOptions.order ?? [],
                only: config.defaultCallOptions.only ?? [],
                ignore: config.defaultCallOptions.ignore ?? [],
                allowFallbacks: config.defaultCallOptions.allowFallbacks,
              }
            : undefined,
          avoidUnhealthyEndpoints: config.avoidUnhealthyEndpoints,
          endpointHealthTtlSeconds: config.endpointHealthTtlSeconds,
        },
      };
    case "ollama":
      return {
        case: "ollama" as const,
        value: {
          baseUrl: config.baseUrl,
          defaultModel: config.defaultModel,
          maxTokens: config.maxTokens,
        },
      };
    case "bedrock":
      return {
        case: "bedrock" as const,
        value: {
          region: config.region,
          profile: config.profile,
          defaultModel: config.defaultModel,
          maxOutputTokens: big(config.maxOutputTokens),
          endpointUrl: config.endpointUrl,
          assumeRoleArn: config.assumeRoleArn,
          roleSessionName: config.roleSessionName,
          externalId: config.externalId,
        },
      };
    case "cursor_cli":
      return {
        case: "cursorCli" as const,
        value: {
          binary: config.binary,
          cwd: config.cwd,
          workspace: config.workspace,
          apiKey: config.apiKey,
          defaultModel: config.defaultModel,
          cliMode: cursorCliModeNameToProto(config.cliMode),
          trust: config.trust,
          approveMcps: config.approveMcps,
          force: config.force,
          sessionId: config.sessionId,
          maxOutputTokens: config.maxOutputTokens,
        },
      };
    case "claude_code_v2":
      return {
        case: "claudeCode" as const,
        value: {
          binary: config.binary,
          cwd: config.cwd,
          agent: config.agent,
          defaultModel: config.defaultModel,
          systemPrompt: config.systemPrompt,
          appendSystemPrompt: config.appendSystemPrompt,
          effort: config.effort,
          permissionMode: config.permissionMode,
          maxBudgetUsd: config.maxBudgetUsd,
          addDir: config.addDir ?? [],
          allowedTools: config.allowedTools ?? [],
          disallowedTools: config.disallowedTools ?? [],
          mcpConfigPath: config.mcpConfigPath,
          timeoutMs: config.timeoutMs,
          maxTurns: config.maxTurns,
          settingSources: config.settingSources ?? [],
          thinkingType: config.thinkingType,
          thinkingBudgetTokens: config.thinkingBudgetTokens,
          hooksJson: config.hooksJson,
          strictMcpConfig: config.strictMcpConfig,
          includeHookEvents: config.includeHookEvents,
          configDir: config.configDir,
          useMyClaudeConfig: config.useMyClaudeConfig,
          deferToolSchemas: config.deferToolSchemas,
          useClaudeCodePreset: config.useClaudeCodePreset,
          oauthToken: config.oauthToken,
        },
      };
  }
}

/** Where the credentials used by a connection check came from. */
export type ProviderCredentialSource =
  | "unspecified"
  | "default_chain"
  | "assumed_role"
  | "web_identity"
  | "api_key"
  | "profile"
  | "bearer_token"
  | "none";

function credentialSourceFromProto(s: CredentialSource): ProviderCredentialSource {
  switch (s) {
    case CredentialSource.DEFAULT_CHAIN:
      return "default_chain";
    case CredentialSource.ASSUMED_ROLE:
      return "assumed_role";
    case CredentialSource.WEB_IDENTITY:
      return "web_identity";
    case CredentialSource.API_KEY:
      return "api_key";
    case CredentialSource.PROFILE:
      return "profile";
    case CredentialSource.BEARER_TOKEN:
      return "bearer_token";
    case CredentialSource.NONE:
      return "none";
    default:
      return "unspecified";
  }
}

/**
 * Maps the wire enum to a readable string union.
 *
 * Callers get `"not_authenticated"` rather than `4`: the numeric enum is meaningless at a call
 * site and a `switch` over it is unreadable in review. An unrecognised value degrades to
 * `"failed"` so a client built against an older schema still takes a sane branch rather than
 * falling through every case.
 */
function providerConnectionStatusFromProto(
  s: ProviderConnectionStatus
):
  | "ok"
  | "binary_not_found"
  | "version_too_old"
  | "not_authenticated"
  | "resume_unsupported"
  | "timed_out"
  | "failed" {
  switch (s) {
    case ProviderConnectionStatus.OK:
      return "ok";
    case ProviderConnectionStatus.BINARY_NOT_FOUND:
      return "binary_not_found";
    case ProviderConnectionStatus.VERSION_TOO_OLD:
      return "version_too_old";
    case ProviderConnectionStatus.NOT_AUTHENTICATED:
      return "not_authenticated";
    case ProviderConnectionStatus.RESUME_UNSUPPORTED:
      return "resume_unsupported";
    case ProviderConnectionStatus.TIMED_OUT:
      return "timed_out";
    default:
      return "failed";
  }
}

/** Reverse of {@link registerProviderConfigToOneof}: maps a GetLlmProviderConfig response back to {@link RegisterProviderConfig}. */
function getLlmProviderConfigResponseToConfig(
  res: GetLlmProviderConfigResponse
): RegisterProviderConfig {
  const num = (v: bigint | number | undefined) =>
    v === undefined ? undefined : Number(v);
  const c = res.config;
  switch (c.case) {
    case "anthropic":
      return {
        type: "anthropic",
        apiKey: c.value.apiKey,
        baseUrl: c.value.baseUrl,
        defaultModel: c.value.defaultModel,
        maxOutputTokens: num(c.value.maxOutputTokens),
      };
    case "openai":
      return {
        type: "openai",
        apiKey: c.value.apiKey,
        baseUrl: c.value.baseUrl,
        defaultModel: c.value.defaultModel,
        maxTokens: c.value.maxTokens,
      };
    case "gemini":
      return {
        type: "gemini",
        apiKey: c.value.apiKey,
        baseUrl: c.value.baseUrl,
        defaultModel: c.value.defaultModel,
        maxOutputTokens: num(c.value.maxOutputTokens),
      };
    case "openrouter":
      return {
        type: "openrouter",
        apiKey: c.value.apiKey,
        baseUrl: c.value.baseUrl,
        defaultModel: c.value.defaultModel,
        defaultCallOptions: openRouterCallOptionsFromProto(c.value.defaultCallOptions),
        avoidUnhealthyEndpoints: c.value.avoidUnhealthyEndpoints,
        endpointHealthTtlSeconds: c.value.endpointHealthTtlSeconds,
      };
    case "ollama":
      return {
        type: "ollama",
        baseUrl: c.value.baseUrl,
        defaultModel: c.value.defaultModel,
        maxTokens: c.value.maxTokens,
      };
    case "bedrock":
      return {
        type: "bedrock",
        region: c.value.region,
        profile: c.value.profile,
        defaultModel: c.value.defaultModel,
        maxOutputTokens: num(c.value.maxOutputTokens),
        endpointUrl: c.value.endpointUrl,
        assumeRoleArn: c.value.assumeRoleArn,
        roleSessionName: c.value.roleSessionName,
        // externalId is a secret: intentionally not read back.
      };
    case "cursorCli":
      return {
        type: "cursor_cli",
        binary: c.value.binary,
        cwd: c.value.cwd,
        workspace: c.value.workspace,
        apiKey: c.value.apiKey,
        defaultModel: c.value.defaultModel,
        cliMode: cursorCliModeProtoToName(c.value.cliMode),
        trust: c.value.trust,
        approveMcps: c.value.approveMcps,
        force: c.value.force,
        sessionId: c.value.sessionId,
        maxOutputTokens: c.value.maxOutputTokens,
      };
    case "claudeCode":
      return {
        type: "claude_code_v2",
        binary: c.value.binary,
        cwd: c.value.cwd,
        agent: c.value.agent,
        defaultModel: c.value.defaultModel,
        systemPrompt: c.value.systemPrompt,
        appendSystemPrompt: c.value.appendSystemPrompt,
        effort: c.value.effort,
        permissionMode: c.value.permissionMode,
        maxBudgetUsd: c.value.maxBudgetUsd,
        addDir: c.value.addDir,
        allowedTools: c.value.allowedTools,
        disallowedTools: c.value.disallowedTools,
        mcpConfigPath: c.value.mcpConfigPath,
        timeoutMs: c.value.timeoutMs,
        maxTurns: c.value.maxTurns,
        settingSources: c.value.settingSources,
        thinkingType: c.value.thinkingType,
        thinkingBudgetTokens: c.value.thinkingBudgetTokens,
        hooksJson: c.value.hooksJson,
        strictMcpConfig: c.value.strictMcpConfig,
        includeHookEvents: c.value.includeHookEvents,
        configDir: c.value.configDir,
        useMyClaudeConfig: c.value.useMyClaudeConfig,
        deferToolSchemas: c.value.deferToolSchemas,
        useClaudeCodePreset: c.value.useClaudeCodePreset,
        oauthToken: c.value.oauthToken,
      };
    default:
      throw new Error("getProviderConfig: response has no recognized config oneof set");
  }
}

export interface LLM {
  stream(
    messages: Message[],
    onChunk: (chunk: string) => void,
    options?: LLMOptions
  ): Promise<AssistantMessage>;
  supportsStructuredTools(): boolean;
}

/**
 * An {@link LlmdClient} over an already-built Transport.
 *
 * Prefer `createLlmdClient` from '@pyxis-llm/client/node' or `createLlmdWebClient`
 * from '@pyxis-llm/client/web', which build the Transport for you. This is the
 * transport-agnostic escape hatch (custom interceptors, a test transport).
 */
export function createLlmdClientFromTransport(conn: LlmdConnectionOptions): LlmdClient {
  return new LlmdClient(conn);
}
