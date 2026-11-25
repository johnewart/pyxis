/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The transport seam. This is the ONLY module in @pyxis-llm/client that knows a
 * transport exists; everything above it (every RPC wrapper, LlmdClient, the
 * convenience helpers) is written once against Connect's `Transport` interface
 * and runs unchanged on both.
 *
 * No @grpc/grpc-js types leak into the public API, so a browser build of this
 * package is possible. Concrete
 * transports are constructed in ./node (createGrpcTransport, HTTP/2) and ./web
 * (createConnectTransport, fetch); neither is imported from here, so a browser
 * bundle of ./web never pulls in node:http2.
 */

import { Code, ConnectError } from "@connectrpc/connect";
import type { CallOptions, Transport } from "@connectrpc/connect";

import { LlmErrorSchema } from "./gen/llmv1/llm_pb.js";
import type { LlmError } from "./gen/llmv1/llm_pb.js";

export type { Transport, CallOptions };

/** Tenant identity, sent as a header on every RPC. */
export const LLMD_TENANT_METADATA_KEY = "llmd-tenant-id";

/** Optional scope within the tenant; omitted means tenant-wide. */
export const LLMD_SCOPE_METADATA_KEY = "llmd-scope-id";

/** Optional principal id (must match internal/serverutil/scope_wire.go). */
export const LLMD_PRINCIPAL_METADATA_KEY = "llmd-principal-id";

/** Caller identity: a required tenant and an optional scope. */
export interface ScopeIdentity {
  tenantId: string;
  scopeId?: string;
  /** Optional principal within the tenant; omitted when empty. */
  principalId?: string;
}

/** Sent on ChatStream with structured system prompt sections (Phase 2). */
export const SYSTEM_PROMPT_SECTIONS_METADATA_KEY =
  "llmd-system-prompt-sections";

/**
 * Headers carrying tenant + scope identity. The scope header is sent only
 * when `scopeId` is non-empty after trimming (empty = tenant-wide).
 *
 * Returns a plain `Headers` rather than the old `grpc.Metadata`. That type was
 * the single worst offender in the old API: it forced every caller onto
 * @grpc/grpc-js, and mixing two copies of that package produced the infamous
 * "Channel credentials must be a ChannelCredentials object" failure. `Headers`
 * is a web standard available in Node 18+ and every browser, so the same call
 * sites now work on both transports.
 */
export function scopeHeaders({ tenantId, scopeId, principalId }: ScopeIdentity): Headers {
  const h = new Headers();
  const t = (tenantId ?? "").trim();
  if (t) h.set(LLMD_TENANT_METADATA_KEY, t);
  const s = (scopeId ?? "").trim();
  if (s) h.set(LLMD_SCOPE_METADATA_KEY, s);
  const p = (principalId ?? "").trim();
  if (p) h.set(LLMD_PRINCIPAL_METADATA_KEY, p);
  return h;
}

/** Empty headers for calls that carry no workspace scope. */
export function emptyHeaders(): Headers {
  return new Headers();
}

/** Best-effort human-readable message from a failed call. */
export function callErrorMessage(err: unknown): string {
  if (err instanceof ConnectError) {
    return err.rawMessage || err.message || Code[err.code];
  }
  return err instanceof Error ? err.message : String(err);
}

/**
 * Returns the structured LlmError detail attached to a failed call, or
 * undefined when `err` is not a ConnectError or carries no such detail.
 */
export function getLlmError(err: unknown): LlmErrorInfo | undefined {
  if (!(err instanceof ConnectError)) return undefined;
  const details = err.findDetails(LlmErrorSchema);
  if (details.length === 0) return undefined;
  return llmErrorToInfo(details[0]);
}

/** Maps the wire LlmError message to its client-side view. */
export function llmErrorToInfo(d: LlmError): LlmErrorInfo {
  return {
    kind: d.kind,
    providerType: d.providerType,
    providerStatus: d.providerStatus,
    retryable: d.retryable,
    retryAfterMs: d.retryAfter
      ? Number(d.retryAfter.seconds) * 1000 + d.retryAfter.nanos / 1e6
      : undefined,
  };
}

/** Client-side view of the server's LlmError detail. */
export interface LlmErrorInfo {
  kind: LlmError["kind"];
  providerType: LlmError["providerType"];
  providerStatus: number;
  retryable: boolean;
  /** Retry-after in milliseconds; undefined when the server sent none. */
  retryAfterMs: number | undefined;
}

/** Optional controls for {@link callUnary}. */
export interface UnaryCallOptions {
  /**
   * Cancel the call and reject if it does not settle within this many ms.
   * Without a deadline a wedged server (e.g. one blocked on a SQLite write
   * lock) hangs this promise forever, since the caller has no way out.
   */
  timeoutMs?: number;
  /** Human-readable RPC label used in timeout/abort error messages. */
  label?: string;
}

/** Rejection thrown when a unary call exceeds its client-side timeout. */
export class UnaryTimeoutError extends Error {
  readonly timeoutMs: number;
  constructor(label: string, timeoutMs: number) {
    super(`${label}: call timed out after ${timeoutMs}ms`);
    this.name = "UnaryTimeoutError";
    this.timeoutMs = timeoutMs;
  }
}

/**
 * Runs a unary call with AbortSignal cancellation and an optional client-side
 * timeout, preserving the old unaryPromise contract.
 *
 * Connect enforces `timeoutMs` itself and surfaces expiry as a ConnectError
 * with Code.DeadlineExceeded. We translate that back into UnaryTimeoutError so
 * the thrown type stays part of this package's API rather than becoming a
 * transport detail callers have to sniff for. Aborts are likewise normalised
 * to the signal's own reason, matching the previous behaviour.
 */
export async function callUnary<Res>(
  call: (opts: CallOptions) => Promise<Res>,
  headers: HeadersInit | undefined,
  signal?: AbortSignal,
  opts?: UnaryCallOptions
): Promise<Res> {
  const label = opts?.label ?? "unary";
  if (signal?.aborted) {
    throw signal.reason ?? new Error(`${label}: aborted`);
  }
  const timeoutMs = opts?.timeoutMs && opts.timeoutMs > 0 ? opts.timeoutMs : undefined;
  try {
    return await call({ headers, signal, timeoutMs });
  } catch (err) {
    if (signal?.aborted) {
      throw signal.reason ?? new Error(`${label}: aborted`);
    }
    if (
      timeoutMs !== undefined &&
      err instanceof ConnectError &&
      err.code === Code.DeadlineExceeded
    ) {
      throw new UnaryTimeoutError(label, timeoutMs);
    }
    throw err;
  }
}
