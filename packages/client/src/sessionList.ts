import { callUnary, scopeHeaders } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";
import {
  SessionSortField,
  SessionSortOrder,
  type SessionSummary,
} from "./gen/llmv1/llm_pb.js";
import type { LlmServiceClient } from "./llm.js";

/** Server-side default page size; mirrors store.DefaultSessionPageSize. */
export const DEFAULT_SESSION_PAGE_SIZE = 50;
/** Server-side cap; a larger pageSize is clamped by llmd, not rejected. */
export const MAX_SESSION_PAGE_SIZE = 500;

/**
 * Sort keys for {@link listLlmdSessions}.
 *
 * `lastMessage` (the default) orders by the newest chat message in the session,
 * which is deliberately not `updated` — `updated` is bumped by any session
 * mutation (title edit, cursor advance, settings change), so it answers "last
 * touched" rather than "last said something".
 */
export type SessionSortKey = "lastMessage" | "created" | "updated" | "title";

export type SessionSortDirection = "asc" | "desc";

// protobuf-es strips the redundant enum-name prefix from member names, so these
// are SessionSortField.LAST_MESSAGE_AT rather than
// SessionSortField.SESSION_SORT_FIELD_LAST_MESSAGE_AT. The wire numbers are
// unchanged.
const SORT_FIELDS: Record<SessionSortKey, SessionSortField> = {
  lastMessage: SessionSortField.LAST_MESSAGE_AT,
  created: SessionSortField.CREATED_AT,
  updated: SessionSortField.UPDATED_AT,
  title: SessionSortField.TITLE,
};

/** One row of the session list, with wire millis decoded to Dates. */
export type LlmdSessionSummary = {
  sessionId: string;
  title: string;
  /** Session surface / agent type: build, ask, debug, doc, pair. */
  chatSurface: string;
  createdAt: Date;
  updatedAt: Date;
  /** null when the session has no messages yet. */
  lastMessageAt: Date | null;
  cursorPos: number;
  /** Arbitrary k/v labels; `{}` when the session is untagged. */
  tags: Record<string, string>;
  /** Session this one was forked from; "" when not a fork. Informational only. */
  forkedFromSessionId: string;
  /** Inclusive cutoff message of the fork; "" when not a fork or a full clone. */
  forkedFromMessageId: string;
};

export type LlmdSessionPage = {
  sessions: LlmdSessionSummary[];
  /** Rows matching the filter across all pages. */
  totalCount: number;
  hasMore: boolean;
  /** Pass as `offset` to fetch the next page; null on the last page. */
  nextOffset: number | null;
};

export type ListLlmdSessionsOptions = LlmdCallTarget & {
  tenantId: string;
  /** Optional scope; omitted/empty lists tenant-wide. */
  scopeId?: string;
  /** Defaults to "lastMessage". */
  sortBy?: SessionSortKey;
  /** Defaults to "desc" for the timestamp keys and "asc" for "title". */
  direction?: SessionSortDirection;
  /** Defaults to {@link DEFAULT_SESSION_PAGE_SIZE}; clamped server-side. */
  pageSize?: number;
  offset?: number;
  /** Restrict to one surface (build, ask, …). Omit for all surfaces. */
  chatSurface?: string;
  /**
   * Restrict to sessions carrying ALL of these tags (AND). A value of ""
   * matches on key presence with any value.
   *
   * Unlike `chatSurface` this is not index-backed server-side — it scans the
   * workspace's sessions. Combine it with `chatSurface` when you can so the
   * indexed predicate narrows the set first.
   */
  tags?: Record<string, string>;
  signal?: AbortSignal;
  timeoutMs?: number;
};

/**
 * Decode a wire SessionSummary; `lastMessageAt` 0 means "no messages yet".
 *
 * The int64 timestamp fields arrive as bigint (protobuf-es represents int64
 * that way on BOTH transports now), so they are narrowed to Number before
 * becoming Dates — the public shape stays Date/number exactly as before.
 */
export function sessionSummaryFromProto(s: SessionSummary): LlmdSessionSummary {
  return {
    sessionId: s.sessionId,
    title: s.title,
    chatSurface: s.chatSurface,
    createdAt: new Date(Number(s.createdAt)),
    updatedAt: new Date(Number(s.updatedAt)),
    lastMessageAt: s.lastMessageAt > 0n ? new Date(Number(s.lastMessageAt)) : null,
    cursorPos: Number(s.cursorPos),
    tags: { ...(s.tags ?? {}) },
    forkedFromSessionId: s.forkedFromSessionId,
    forkedFromMessageId: s.forkedFromMessageId,
  };
}

function sortOrder(opts: ListLlmdSessionsOptions): SessionSortOrder {
  if (opts.direction === "asc") return SessionSortOrder.ASC;
  if (opts.direction === "desc") return SessionSortOrder.DESC;
  // Leave it unspecified and let llmd apply the per-key default (newest-first
  // for timestamps, A→Z for titles) so the two sides cannot drift.
  return SessionSortOrder.UNSPECIFIED;
}

/**
 * Fetch one page of the workspace's sessions, newest message first by default.
 *
 * ```ts
 * const transport = createLlmdTransport({ address }); // '@pyxis-llm/client/node'
 * const page = await listLlmdSessions({ transport, tenantId, scopeId, pageSize: 20 });
 * const older = await listLlmdSessions({
 *   transport, tenantId, scopeId, pageSize: 20, offset: page.nextOffset ?? 0,
 * });
 * ```
 */
export async function listLlmdSessions(
  opts: ListLlmdSessionsOptions
): Promise<LlmdSessionPage> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.listSessions(
        {
          sortBy: SORT_FIELDS[opts.sortBy ?? "lastMessage"],
          order: sortOrder(opts),
          pageSize: opts.pageSize ?? 0,
          offset: opts.offset ?? 0,
          chatSurface: opts.chatSurface?.trim() || "",
          tagFilters: opts.tags ?? {},
        },
        o
      ),
    scopeHeaders(opts),
    opts.signal,
    { timeoutMs: opts.timeoutMs, label: "listSessions" }
  );
  return {
    sessions: res.sessions.map(sessionSummaryFromProto),
    totalCount: Number(res.totalCount),
    hasMore: res.hasMore,
    nextOffset: res.hasMore ? res.nextOffset : null,
  };
}

/**
 * Walk every session, following pagination for the caller.
 *
 * Prefer this over a single huge `pageSize`: llmd clamps a page at
 * {@link MAX_SESSION_PAGE_SIZE}, so asking for "all of them" in one call
 * silently truncates, while this keeps going until the server says it is done.
 */
export async function* iterateLlmdSessions(
  opts: ListLlmdSessionsOptions
): AsyncGenerator<LlmdSessionSummary> {
  let offset = opts.offset ?? 0;
  // Resolve the client once for the whole walk rather than per page.
  const client: LlmServiceClient = resolveLlmServiceClient(opts);
  for (;;) {
    const page = await listLlmdSessions({ ...opts, transport: undefined, client, offset });
    for (const session of page.sessions) yield session;
    // Guard against a server that reports has_more but returns nothing, which
    // would otherwise spin forever.
    if (!page.hasMore || page.nextOffset === null || page.sessions.length === 0)
      return;
    offset = page.nextOffset;
  }
}

/** Collect every session into one array (see {@link iterateLlmdSessions}). */
export async function listAllLlmdSessions(
  opts: ListLlmdSessionsOptions
): Promise<LlmdSessionSummary[]> {
  const out: LlmdSessionSummary[] = [];
  for await (const session of iterateLlmdSessions(opts)) out.push(session);
  return out;
}
