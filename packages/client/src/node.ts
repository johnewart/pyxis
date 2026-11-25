/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @pyxis-llm/client/node — the Node half of this package: dials llmd's NATIVE gRPC
 * port over HTTP/2.
 *
 * This is a transport binding, nothing more. Every RPC wrapper, LlmdClient
 * method and convenience helper lives in the shared root entry ('@pyxis-llm/client')
 * and is identical on both transports; ./node and ./web only differ in how the
 * connection is described (address vs. token-guarded baseUrl) and which
 * Transport gets constructed. Import this from a process that can actually open
 * an HTTP/2 socket; import ./web from anything fetch-based.
 *
 * @connectrpc/connect-node's createGrpcTransport speaks real gRPC (Node's
 * node:http2), so this is wire-compatible with the same grpc.Server the old
 * @grpc/grpc-js client talked to — the server side needed no change at all.
 */

import { createGrpcTransport, Http2SessionManager } from "@connectrpc/connect-node";
import type { Transport } from "@connectrpc/connect";
import { LlmdClient, type LlmdConnectionOptions } from "./llm.js";

export type LlmdNodeTransportOptions = {
  /**
   * llmd's native gRPC address. Accepts bare `host:port` (the historical
   * @pyxis-llm/client form, e.g. "127.0.0.1:50051") or a full URL; a bare
   * host:port is normalised to http:// since Connect requires a baseUrl.
   */
  address?: string;
  /** Applied to every call on this transport (see Connect's Interceptor). */
  interceptors?: NonNullable<Parameters<typeof createGrpcTransport>[0]>["interceptors"];
};

export const DEFAULT_LLMD_ADDRESS = "127.0.0.1:50051";

/** Normalises `host:port` to a baseUrl; passes through anything with a scheme. */
export function llmdBaseUrlFromAddress(address: string): string {
  const a = address.trim() || DEFAULT_LLMD_ADDRESS;
  return /^[a-z][a-z0-9+.-]*:\/\//i.test(a) ? a : `http://${a}`;
}

/** A Connect Transport speaking native gRPC over HTTP/2 to llmd. */
export function createLlmdTransport(options?: LlmdNodeTransportOptions): Transport {
  return createGrpcTransport({
    baseUrl: llmdBaseUrlFromAddress(options?.address ?? DEFAULT_LLMD_ADDRESS),
    ...(options?.interceptors ? { interceptors: options.interceptors } : {}),
  });
}

/**
 * One HTTP/2 connection to llmd that many clients can share, and that can be
 * closed.
 *
 * Why this exists: every `createLlmdClient` builds its own transport, and
 * connect-node keeps each transport's idle HTTP/2 session open for
 * `idleConnectionTimeoutMs` (15 minutes by default) with no way to close it
 * from the client. A process that creates clients freely (one per session,
 * one per render) accumulates open sockets that keep it alive long after it is
 * done. Build one connection, hand out clients from it, and `close()` it when
 * finished.
 */
export type LlmdNodeConnection = {
  readonly transport: Transport;
  /** A client on this connection (tenant/scope/session as usual). */
  client(conn: Omit<LlmdConnectionOptions, "transport">): LlmdClient;
  /** Closes the HTTP/2 connection and any open streams on it. */
  close(): void;
};

export function createLlmdNodeConnection(
  options?: LlmdNodeTransportOptions & {
    /** Close the connection after this long with no requests (connect-node default: 15 min). */
    idleConnectionTimeoutMs?: number;
  }
): LlmdNodeConnection {
  const baseUrl = llmdBaseUrlFromAddress(options?.address ?? DEFAULT_LLMD_ADDRESS);
  const sessionManager = new Http2SessionManager(baseUrl, {
    ...(options?.idleConnectionTimeoutMs !== undefined
      ? { idleConnectionTimeoutMs: options.idleConnectionTimeoutMs }
      : {}),
  });
  const transport = createGrpcTransport({
    baseUrl,
    sessionManager,
    ...(options?.interceptors ? { interceptors: options.interceptors } : {}),
  });
  return {
    transport,
    client: (conn) => new LlmdClient({ ...conn, transport }),
    close: () => sessionManager.abort(),
  };
}

export type CreateLlmdClientOptions = Omit<LlmdConnectionOptions, "transport"> &
  LlmdNodeTransportOptions;

/**
 * An {@link LlmdClient} bound to llmd's native gRPC port.
 *
 * Replaces the old `createLlmdClient({ address, credentials, ... })`. The
 * `credentials` argument is gone: it existed only to hand this package a
 * `grpc.ChannelCredentials` from the host's own @grpc/grpc-js copy, and
 * @grpc/grpc-js is no longer a dependency. Pass `interceptors` for
 * per-call concerns such as auth headers.
 */
export function createLlmdClient(options: CreateLlmdClientOptions): LlmdClient {
  const { address, interceptors, ...conn } = options;
  return new LlmdClient({
    ...conn,
    transport: createLlmdTransport({ address, interceptors }),
  });
}
