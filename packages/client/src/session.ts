import { callUnary, scopeHeaders } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";
import { clientToolToRegisterProto, type ClientTool } from "./llm.js";
import {
  systemPromptSectionsToProto,
  type SystemPromptSectionWire,
} from "./systemPromptSectionsWire.js";
import { grpcSafeString } from "./utf8.js";

/**
 * Common scope for the session helpers.
 *
 * These previously took `(address, scope, sessionId, ..., credentials)`
 * positionally and built a gRPC channel each time. They now take one options
 * object carrying a Transport (or a client to reuse) — both transports, one
 * implementation, and no more guessing which positional slot `credentials` was.
 */
export type LlmdSessionScope = LlmdCallTarget & {
  /** Tenant id — carried as the `llmd-tenant-id` header. */
  tenantId: string;
  /** Optional scope id — carried as `llmd-scope-id` when non-empty. */
  scopeId?: string;
  principalId?: string;
  sessionId: string;
};

export type EnsureLlmdSessionOptions = LlmdSessionScope & {
  displayName?: string;
  initialTools?: ClientTool[];
  llmProviderId?: string;
  defaultModel?: string;
  /**
   * Session-level default system prompt sections, set once here instead of on
   * every ChatStream call. A ChatStream call's own systemPromptSections (see
   * LLMOptions) still overrides this default for that turn only; omitting this
   * field on a later ensureLlmdSession call (e.g. one only updating the LLM
   * binding) leaves the previously-set default untouched — only a non-empty
   * list here overwrites it.
   */
  systemPromptSections?: SystemPromptSectionWire[];
  /**
   * Session surface / agent type (build, ask, debug, doc, pair).
   *
   * This is the only place a surface can be set at session creation. Omitting it
   * on a later ensure call leaves the stored surface untouched rather than
   * clearing it, so it is safe to pass only on the first call.
   */
  chatSurface?: string;
  /**
   * Server-wide tool names hidden from this session by default. A tool named
   * here stays hidden until an explicit session registration (initialTools or
   * registerLlmdSessionTool) makes it visible again — explicit registration
   * wins. Core session tools can never be hidden. Only a non-empty list
   * overwrites the persisted set on a later ensure call; omitting it leaves
   * the stored set untouched.
   */
  disabledTools?: string[];
};

/** Idempotent session-scoped chat runtime provision. */
export async function ensureLlmdSession(
  opts: EnsureLlmdSessionOptions
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.ensureSession(
        {
          sessionId: grpcSafeString(opts.sessionId),
          displayName: opts.displayName
            ? grpcSafeString(opts.displayName)
            : undefined,
          initialTools: (opts.initialTools ?? []).map(clientToolToRegisterProto),
          llmProviderId: opts.llmProviderId
            ? grpcSafeString(opts.llmProviderId)
            : undefined,
          defaultModel: opts.defaultModel
            ? grpcSafeString(opts.defaultModel)
            : undefined,
          systemPromptSections: opts.systemPromptSections?.length
            ? systemPromptSectionsToProto(opts.systemPromptSections)
            : [],
          chatSurface: opts.chatSurface?.trim() || "",
          disabledTools: (opts.disabledTools ?? []).map(grpcSafeString),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "ensureSession" }
  );
}

/** A session's stored metadata, as returned by {@link getLlmdSessionMeta}. */
export type LlmdSessionMeta = {
  sessionId: string;
  title: string;
  /** Session surface / agent type: build, ask, debug, doc, pair. */
  chatSurface: string;
  /** Arbitrary k/v labels; `{}` when untagged. */
  tags: Record<string, string>;
  todosJson: string;
  attributesJson: string;
  cursorPos: number;
  providerId: string;
  defaultModel: string;
  /** Session this one was forked from; "" when not a fork. Informational only. */
  forkedFromSessionId: string;
  /** Inclusive cutoff message of the fork; "" when not a fork or a full clone. */
  forkedFromMessageId: string;
};

/**
 * Read a session's stored metadata. Returns zero values (not an error) for a
 * session llmd has never seen, mirroring the server's GetSession behaviour.
 */
export async function getLlmdSessionMeta(
  opts: LlmdSessionScope
): Promise<LlmdSessionMeta> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.getSession({ sessionId: grpcSafeString(opts.sessionId) }, o),
    scopeHeaders(opts),
    undefined,
    { label: "getSession" }
  );
  const m = res.meta;
  return {
    sessionId: m?.sessionId ?? "",
    title: m?.title ?? "",
    chatSurface: m?.chatSurface ?? "",
    tags: { ...(m?.tags ?? {}) },
    todosJson: m?.todosJson ?? "",
    attributesJson: m?.attributesJson ?? "",
    cursorPos: Number(m?.cursorPos ?? 0),
    providerId: m?.providerId ?? "",
    defaultModel: m?.defaultModel ?? "",
    forkedFromSessionId: m?.forkedFromSessionId ?? "",
    forkedFromMessageId: m?.forkedFromMessageId ?? "",
  };
}

export type ForkLlmdSessionOptions = LlmdCallTarget & {
  /** Tenant id — carried as the `llmd-tenant-id` header. */
  tenantId: string;
  /** Optional scope id — carried as `llmd-scope-id` when non-empty. */
  scopeId?: string;
  principalId?: string;
  sourceSessionId: string;
  /**
   * Inclusive cutoff. The fork also takes everything up to (not including) the
   * next user/assistant message, so an assistant message's tool results come
   * with it. Omit to clone the whole history.
   */
  upToMessageId?: string;
  /** Id for the fork; llmd mints one when omitted. */
  newSessionId?: string;
  /** Title for the fork; defaults to the source's title. */
  title?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
};

export type ForkLlmdSessionResult = {
  sessionId: string;
  /** Distinct messages copied into the fork. */
  copiedMessages: number;
};

/**
 * Clone a session (optionally up to a message) into a new, fully independent
 * session. Provider-side state is not copied: the fork's first turn replays
 * its history as a fresh provider session, like a provider/model switch.
 *
 * Rejects with FAILED_PRECONDITION if the cloned range reaches into a turn
 * that is still running, NOT_FOUND for an unknown session/message, and
 * ALREADY_EXISTS if `newSessionId` is taken.
 */
export async function forkLlmdSession(
  opts: ForkLlmdSessionOptions
): Promise<ForkLlmdSessionResult> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.forkSession(
        {
          sourceSessionId: grpcSafeString(opts.sourceSessionId),
          upToMessageId: grpcSafeString(opts.upToMessageId ?? ""),
          newSessionId: grpcSafeString(opts.newSessionId ?? ""),
          title: grpcSafeString(opts.title ?? ""),
        },
        o
      ),
    scopeHeaders(opts),
    opts.signal,
    { timeoutMs: opts.timeoutMs, label: "forkSession" }
  );
  return { sessionId: res.sessionId, copiedMessages: res.copiedMessages };
}

/**
 * Write a session's title / todos / attributes / surface.
 *
 * WHOLESALE for the fields it sends: title, todosJson and attributesJson are
 * overwritten with whatever is passed (omitted = cleared), so read-modify-write
 * via {@link getLlmdSessionMeta} if you only mean to change one. `chatSurface`
 * is the exception — omitting it leaves the stored surface alone.
 *
 * Tags are deliberately NOT settable here; llmd ignores SessionMeta.tags for
 * exactly this wholesale reason. Use {@link updateLlmdSessionTags}.
 */
export async function setLlmdSessionMeta(
  opts: LlmdSessionScope & {
    title?: string;
    todosJson?: string;
    attributesJson?: string;
    chatSurface?: string;
  }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.setSessionMeta(
        {
          sessionId: grpcSafeString(opts.sessionId),
          meta: {
            title: grpcSafeString(opts.title ?? ""),
            todosJson: opts.todosJson ?? "",
            attributesJson: opts.attributesJson ?? "",
            chatSurface: opts.chatSurface?.trim() || "",
          },
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "setSessionMeta" }
  );
}

/**
 * Merge k/v tags into a session, returning the tags as stored afterwards.
 *
 * MERGE by default: keys not mentioned are left alone, and a key whose value is
 * "" is deleted. Pass `replace: true` for "these tags and no others", in which
 * case empty-valued keys are simply absent from the result rather than deletes.
 */
export async function updateLlmdSessionTags(
  opts: LlmdSessionScope & {
    tags: Record<string, string>;
    replace?: boolean;
  }
): Promise<Record<string, string>> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.updateSessionTags(
        {
          sessionId: grpcSafeString(opts.sessionId),
          tags: opts.tags,
          replace: opts.replace ?? false,
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "updateSessionTags" }
  );
  return { ...(res.tags ?? {}) };
}

export async function updateLlmdSessionLlm(
  opts: LlmdSessionScope & { llmProviderId?: string; defaultModel?: string }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.updateSessionLlm(
        {
          sessionId: grpcSafeString(opts.sessionId),
          llmProviderId: opts.llmProviderId
            ? grpcSafeString(opts.llmProviderId)
            : undefined,
          defaultModel: opts.defaultModel
            ? grpcSafeString(opts.defaultModel)
            : undefined,
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "updateSessionLlm" }
  );
}

export async function registerLlmdSessionTool(
  opts: LlmdSessionScope & { tool: ClientTool }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.registerSessionTool(
        {
          sessionId: grpcSafeString(opts.sessionId),
          tool: clientToolToRegisterProto(opts.tool),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "registerSessionTool" }
  );
}

export async function unregisterLlmdSessionTool(
  opts: LlmdSessionScope & { name: string }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.unregisterSessionTool(
        {
          sessionId: grpcSafeString(opts.sessionId),
          name: grpcSafeString(opts.name),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "unregisterSessionTool" }
  );
}

export async function listLlmdSessionToolNames(
  opts: LlmdSessionScope
): Promise<string[]> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.listSessionTools({ sessionId: grpcSafeString(opts.sessionId) }, o),
    scopeHeaders(opts),
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
