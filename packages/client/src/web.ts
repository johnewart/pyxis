/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @pyxis-llm/client/web — the fetch half of this package: talks to llmd's dedicated
 * Connect/grpc-web HTTP listener from any fetch-capable environment (browser
 * tab, Electron renderer, Worker, Node 18+).
 *
 * This is a transport binding, nothing more — see ./node. Every RPC wrapper and
 * LlmdClient method is shared, so the two transports cannot drift.
 *
 * Two things here are NOT interchangeable with ./node and are load-bearing:
 *
 *  1. createGrpcWebTransport, NOT createConnectTransport. llmd wraps its
 *     grpc.Server with classic grpc-web framing (github.com/improbable-eng/
 *     grpc-web — see internal/serve/connect_listener.go), which is a different
 *     wire format from the Connect protocol despite the shared client library.
 *  2. A SEPARATE port from native gRPC, started only when llmd is spawned with
 *     LLMD_CONNECT_TOKEN (+ optionally LLMD_CONNECT_HTTP_ADDR) — see
 *     cmd/llmd/main.go. It is loopback-only and token-guarded by design: this
 *     is the renderer IPC surface, not a public API. Hence `baseUrl` + `token`
 *     here rather than ./node's `address`.
 */

import { createGrpcWebTransport } from "@connectrpc/connect-web";
import type { Interceptor, Transport } from "@connectrpc/connect";
import { fromJsonString } from "@bufbuild/protobuf";
import {
  LlmdClient,
  type LlmdConnectionOptions,
  type SessionEventsSource,
} from "./llm.js";
import { conversationStreamFrameToEvent } from "./events.js";
import type { ConversationEvent } from "./conversationEvents.js";
import { ConversationStreamFrameSchema } from "./gen/llmv1/llm_pb.js";

/** Header carrying the per-spawn shared secret (LLMD_CONNECT_TOKEN). */
export const LLMD_CONNECT_TOKEN_HEADER = "x-llmd-token";

export type LlmdWebTransportOptions = {
  /** Base URL of llmd's Connect/grpc-web listener, e.g. "http://127.0.0.1:50052" — NOT the native gRPC port. */
  baseUrl: string;
  /**
   * The per-spawn shared secret llmd was started with (LLMD_CONNECT_TOKEN),
   * sent as `x-llmd-token` on every request. Omit only when the listener was
   * started with auth disabled (in-process tests only — see
   * internal/serve/connect_listener.go).
   */
  token?: string;
  /** Defaults to global fetch; override for environments without one (older Node, tests). */
  fetch?: typeof globalThis.fetch;
  /** Applied to every call on this transport, after the token interceptor. */
  interceptors?: Interceptor[];
};

/** Attaches the shared secret to every request. */
function tokenInterceptor(token: string): Interceptor {
  return (next) => async (req) => {
    req.header.set(LLMD_CONNECT_TOKEN_HEADER, token);
    return next(req);
  };
}

/** A Connect Transport speaking grpc-web over fetch to llmd's Connect listener. */
export function createLlmdWebTransport(options: LlmdWebTransportOptions): Transport {
  const token = options.token?.trim();
  const interceptors: Interceptor[] = [
    ...(token ? [tokenInterceptor(token)] : []),
    ...(options.interceptors ?? []),
  ];
  return createGrpcWebTransport({
    baseUrl: options.baseUrl,
    ...(options.fetch ? { fetch: options.fetch } : {}),
    ...(interceptors.length ? { interceptors } : {}),
  });
}

/**
 * Live session events over a plain WebSocket (llmd's `/ws/session-events`, on
 * the same Connect/grpc-web port — see internal/serve/session_events_ws.go),
 * deliberately NOT a grpc-web streaming call.
 *
 * Not collapsed into the shared SubscribeSessionEvents RPC, because the choice
 * is load-bearing: a browser's native `new WebSocket(url)` cannot set the
 * `x-llmd-token` header the rest of this client uses (hence the token query
 * param), and this is a simpler, more broadly-compatible transport for a
 * supplementary server-push feed than holding a second grpc-web streaming call
 * open.
 *
 * The Node transport uses the RPC. Both yield the same ConversationEvents.
 */
export function createWebSocketSessionEvents(opts: {
  baseUrl: string;
  tenantId: string;
  scopeId?: string;
  token?: string;
}): SessionEventsSource {
  return async function* ({ sessionId, afterSeq, signal }) {
    const wsUrl = new URL(opts.baseUrl.replace(/^http/, "ws"));
    wsUrl.pathname = wsUrl.pathname.replace(/\/$/, "") + "/ws/session-events";
    wsUrl.searchParams.set("session_id", sessionId);
    wsUrl.searchParams.set("tenant_id", opts.tenantId.trim());
    const scope = (opts.scopeId ?? "").trim();
    if (scope) wsUrl.searchParams.set("scope_id", scope);
    if (afterSeq) wsUrl.searchParams.set("after_seq", String(afterSeq));
    if (opts.token) wsUrl.searchParams.set("token", opts.token);

    const ws = new WebSocket(wsUrl.toString());
    const queue: MessageEvent<unknown>[] = [];
    let resolveNext: (() => void) | undefined;
    let closed: { code?: number; reason?: string } | undefined;
    let failure: Error | undefined;

    const wake = () => {
      const r = resolveNext;
      resolveNext = undefined;
      r?.();
    };
    ws.addEventListener("message", (ev) => {
      queue.push(ev as MessageEvent<unknown>);
      wake();
    });
    ws.addEventListener("close", (ev) => {
      closed = { code: (ev as CloseEvent).code, reason: (ev as CloseEvent).reason };
      wake();
    });
    ws.addEventListener("error", () => {
      failure = new Error("subscribeSessionEvents: WebSocket error");
      wake();
    });

    const onAbort = () => ws.close();
    if (signal) {
      if (signal.aborted) onAbort();
      else signal.addEventListener("abort", onAbort, { once: true });
    }

    try {
      for (;;) {
        if (queue.length === 0 && !closed && !failure) {
          await new Promise<void>((resolve) => {
            resolveNext = resolve;
          });
        }
        while (queue.length > 0) {
          const ev = queue.shift()!;
          const raw = (ev as MessageEvent).data;
          const text = typeof raw === "string" ? raw : await (raw as Blob).text();
          let event: ConversationEvent | undefined;
          try {
            // protobuf-es v2 parses JSON via a free function against the schema
            // rather than ts-proto/v1's Message.fromJsonString static.
            event = conversationStreamFrameToEvent(
              fromJsonString(ConversationStreamFrameSchema, text)
            );
          } catch {
            continue; // skip unparsable frames rather than killing the subscription
          }
          if (event) yield event;
        }
        if (failure) throw failure;
        if (closed) return;
      }
    } finally {
      if (signal) signal.removeEventListener("abort", onAbort);
      ws.close();
    }
  };
}

/**
 * One session's live summary from llmd's per-scope session-states feed
 * (internal/sessionstate). Empty strings are omitted on the wire.
 */
export type LlmdSessionState = {
  sessionId: string;
  running: boolean;
  title?: string;
  statusHeadline?: string;
  activeTool?: string;
};

/**
 * A message on the session-states feed. A `snapshot` REPLACES the caller's view
 * of the scope (sent first, and again after the server dropped updates); a
 * `state` is one session's whole new summary.
 */
export type LlmdSessionStatesMessage =
  | { type: "snapshot"; sessions: LlmdSessionState[] }
  | { type: "state"; session: LlmdSessionState };

/**
 * Subscribe to the summary of EVERY session in a scope over one WebSocket
 * (llmd's `/ws/session-states`, same port and auth model as
 * `/ws/session-events`). This is the feed for a nav spinner or tab strip: it
 * replays no transcripts and costs the server nothing while idle. Ends when
 * the socket closes or `signal` aborts; throws on a socket error.
 */
export async function* subscribeLlmdSessionStates(opts: {
  baseUrl: string;
  tenantId: string;
  scopeId?: string;
  token?: string;
  signal?: AbortSignal;
}): AsyncGenerator<LlmdSessionStatesMessage> {
  const wsUrl = new URL(opts.baseUrl.replace(/^http/, "ws"));
  wsUrl.pathname = wsUrl.pathname.replace(/\/$/, "") + "/ws/session-states";
  wsUrl.searchParams.set("tenant_id", opts.tenantId.trim());
  const scope = (opts.scopeId ?? "").trim();
  if (scope) wsUrl.searchParams.set("scope_id", scope);
  if (opts.token) wsUrl.searchParams.set("token", opts.token);

  const ws = new WebSocket(wsUrl.toString());
  const queue: unknown[] = [];
  let resolveNext: (() => void) | undefined;
  let closed = false;
  let failure: Error | undefined;
  const wake = () => {
    const r = resolveNext;
    resolveNext = undefined;
    r?.();
  };
  ws.addEventListener("message", (ev) => {
    queue.push((ev as MessageEvent).data);
    wake();
  });
  ws.addEventListener("close", () => {
    closed = true;
    wake();
  });
  ws.addEventListener("error", () => {
    failure = new Error("subscribeLlmdSessionStates: WebSocket error");
    wake();
  });
  const signal = opts.signal;
  const onAbort = () => ws.close();
  if (signal) {
    if (signal.aborted) onAbort();
    else signal.addEventListener("abort", onAbort, { once: true });
  }

  try {
    for (;;) {
      if (queue.length === 0 && !closed && !failure) {
        await new Promise<void>((resolve) => {
          resolveNext = resolve;
        });
      }
      while (queue.length > 0) {
        const raw = queue.shift();
        const text = typeof raw === "string" ? raw : await (raw as Blob).text();
        let msg: LlmdSessionStatesMessage;
        try {
          msg = JSON.parse(text) as LlmdSessionStatesMessage;
        } catch {
          continue;
        }
        if (msg.type === "snapshot" || msg.type === "state") yield msg;
      }
      if (failure) throw failure;
      if (closed) return;
    }
  } finally {
    if (signal) signal.removeEventListener("abort", onAbort);
    ws.close();
  }
}

export type CreateLlmdWebClientOptions = Omit<LlmdConnectionOptions, "transport"> &
  LlmdWebTransportOptions;

/** An {@link LlmdClient} bound to llmd's Connect/grpc-web listener. */
export function createLlmdWebClient(options: CreateLlmdWebClientOptions): LlmdClient {
  const { baseUrl, token, fetch: fetchImpl, interceptors, ...conn } = options;
  return new LlmdClient({
    ...conn,
    transport: createLlmdWebTransport({ baseUrl, token, fetch: fetchImpl, interceptors }),
    // Keep the WebSocket feed unless the caller explicitly supplies its own.
    sessionEventsSource:
      conn.sessionEventsSource ??
      createWebSocketSessionEvents({
        baseUrl,
        tenantId: conn.tenantId,
        scopeId: conn.scopeId,
        token,
      }),
  });
}
