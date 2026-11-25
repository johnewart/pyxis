import { callUnary, scopeHeaders } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";

type CommonOptions = LlmdCallTarget & {
  tenantId: string;
  scopeId?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
};

export type LlmdStorageTableStat = { name: string; bytes: number };

export type LlmdStorageSessionStat = {
  sessionId: string;
  title: string;
  chatSurface: string;
  messageCount: number;
  bytes: number;
  /** null when the session has no messages. */
  lastMessageAt: Date | null;
};

export type LlmdStorageStats = {
  databaseBytes: number;
  /** Free pages, reclaimable by VACUUM. */
  freeBytes: number;
  tables: LlmdStorageTableStat[];
  sessionCount: number;
  largestSessions: LlmdStorageSessionStat[];
};

export type GetLlmdStorageStatsOptions = CommonOptions & {
  /** Max largest sessions to return; 0/omitted lets llmd pick its default. */
  largestSessionsLimit?: number;
};

/** Database size, per-table sizes and the largest sessions by stored bytes. */
export async function getLlmdStorageStats(
  opts: GetLlmdStorageStatsOptions
): Promise<LlmdStorageStats> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.getStorageStats({ largestSessionsLimit: opts.largestSessionsLimit ?? 0 }, o),
    scopeHeaders(opts),
    opts.signal,
    { timeoutMs: opts.timeoutMs, label: "getStorageStats" }
  );
  return {
    databaseBytes: Number(res.databaseBytes),
    freeBytes: Number(res.freeBytes),
    tables: res.tables.map((t) => ({ name: t.name, bytes: Number(t.bytes) })),
    sessionCount: Number(res.sessionCount),
    largestSessions: res.largestSessions.map((s) => ({
      sessionId: s.sessionId,
      title: s.title,
      chatSurface: s.chatSurface,
      messageCount: Number(s.messageCount),
      bytes: Number(s.bytes),
      lastMessageAt: s.lastMessageUnixMs > 0n ? new Date(Number(s.lastMessageUnixMs)) : null,
    })),
  };
}

export type DeleteLlmdSessionsOptions = CommonOptions & { sessionIds: string[] };

/** Delete several sessions with the same purge as a single delete; returns the count deleted. */
export async function deleteLlmdSessions(opts: DeleteLlmdSessionsOptions): Promise<number> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.deleteSessions({ sessionIds: opts.sessionIds }, o),
    scopeHeaders(opts),
    opts.signal,
    { timeoutMs: opts.timeoutMs, label: "deleteSessions" }
  );
  return res.deleted;
}

export type LlmdCompactResult = {
  runId: string;
  bytesBefore: number;
  bytesAfter: number;
  eventsRemoved: number;
};

export type CompactLlmdOptions = CommonOptions;

/**
 * Trim leftover events and VACUUM the database. Can take minutes on a large
 * database; llmd finishes the run even if this call is aborted.
 */
export async function compactLlmd(opts: CompactLlmdOptions): Promise<LlmdCompactResult> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.compact({}, o),
    scopeHeaders(opts),
    opts.signal,
    { timeoutMs: opts.timeoutMs, label: "compact" }
  );
  return {
    runId: res.runId,
    bytesBefore: Number(res.bytesBefore),
    bytesAfter: Number(res.bytesAfter),
    eventsRemoved: Number(res.eventsRemoved),
  };
}
