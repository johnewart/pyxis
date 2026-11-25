/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Driver for llmd's RunProviderAuthCommand bidi RPC: runs an interactive CLI
 * auth command (e.g. `claude login`) in a server-side PTY and pipes its I/O
 * over the wire. Useful when the CLI is only reachable on the machine running
 * llmd, not on the operator's own machine (a remote/server llmd deployment) —
 * the caller renders the PTY bytes as a terminal (e.g. xterm.js) and forwards
 * keystrokes back via `write()`.
 *
 * TRANSPORT LIMIT — this is the one capability that genuinely cannot be at
 * parity across both transports. RunProviderAuthCommand is bidirectional
 * streaming, and gRPC-Web has no wire encoding for client-streaming or bidi
 * calls at all (it can only do unary and server-streaming). That is a
 * limitation of the protocol, not of this package, so it cannot be papered
 * over in the client: on a '@pyxis-llm/client/web' transport this call fails.
 * A renderer that needs interactive provider auth has to proxy it through a
 * Node host that holds a '@pyxis-llm/client/node' transport.
 */

import { create } from "@bufbuild/protobuf";
import {
  LlmProviderType,
  ProviderAuthCommandInputSchema,
  type ProviderAuthCommandInput,
  type ProviderAuthCommandOutput,
} from "./gen/llmv1/llm_pb.js";
import type { LlmServiceClient } from "./llm.js";

export type ProviderAuthCommandStartOptions = {
  /** Optional CLI binary override (falls back to the provider's default/PATH lookup). */
  binary?: string;
  /** Optional CLAUDE_CONFIG_DIR-style config directory override. */
  configDir?: string;
  cols?: number;
  rows?: number;
};

export type ProviderAuthCommandEvent =
  | { kind: "started" }
  | { kind: "output"; data: Uint8Array }
  | { kind: "exit"; code: number };

/**
 * A push-driven AsyncIterable of request messages.
 *
 * Connect models the client side of a bidi call as an AsyncIterable it pulls
 * from, whereas @grpc/grpc-js exposed a duplex stream you called `.write()` on.
 * This bridges the two so {@link ProviderAuthCommandSession} can keep its
 * imperative write()/resize()/end() surface — the PTY caller is inherently
 * push-shaped (keystrokes arrive when the user types, not when the transport
 * asks).
 */
class RequestQueue<T> implements AsyncIterable<T> {
  private readonly queue: T[] = [];
  private notify: (() => void) | undefined;
  private closed = false;

  push(value: T): void {
    if (this.closed) return;
    this.queue.push(value);
    this.notify?.();
    this.notify = undefined;
  }

  close(): void {
    this.closed = true;
    this.notify?.();
    this.notify = undefined;
  }

  async *[Symbol.asyncIterator](): AsyncIterator<T> {
    for (;;) {
      while (this.queue.length > 0) {
        yield this.queue.shift()!;
      }
      if (this.closed) return;
      await new Promise<void>((r) => {
        this.notify = r;
      });
    }
  }
}

/**
 * A running (or just-finished) interactive provider auth command. Iterate
 * with `for await` to receive `started`/`output`/`exit` events; call
 * `write()` to send terminal keystrokes and `resize()` on terminal resize.
 */
export class ProviderAuthCommandSession implements AsyncIterable<ProviderAuthCommandEvent> {
  private readonly requests: RequestQueue<ProviderAuthCommandInput>;
  private readonly responses: AsyncIterable<ProviderAuthCommandOutput>;
  private readonly abort: AbortController;

  constructor(
    requests: RequestQueue<ProviderAuthCommandInput>,
    responses: AsyncIterable<ProviderAuthCommandOutput>,
    abort: AbortController
  ) {
    this.requests = requests;
    this.responses = responses;
    this.abort = abort;
  }

  /** Writes bytes (typically keystrokes) to the command's stdin. */
  write(data: Uint8Array | string): void {
    const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data;
    this.requests.push(
      create(ProviderAuthCommandInputSchema, {
        frame: { case: "stdin", value: bytes },
      })
    );
  }

  /** Notifies the PTY of a terminal size change. */
  resize(cols: number, rows: number): void {
    this.requests.push(
      create(ProviderAuthCommandInputSchema, {
        frame: { case: "resize", value: { cols, rows } },
      })
    );
  }

  /** Closes the client's send side (does not kill the process; it keeps streaming output until it exits). */
  end(): void {
    this.requests.close();
  }

  /** Aborts immediately: cancels the underlying call. */
  cancel(): void {
    this.requests.close();
    this.abort.abort();
  }

  async *[Symbol.asyncIterator](): AsyncIterator<ProviderAuthCommandEvent> {
    for await (const out of this.responses) {
      const f = out.frame;
      if (f.case === "started") {
        yield { kind: "started" };
      } else if (f.case === "output" && f.value.length > 0) {
        yield { kind: "output", data: f.value };
      } else if (f.case === "exitCode") {
        yield { kind: "exit", code: f.value };
      }
    }
  }
}

/**
 * Starts an interactive provider auth command over llmd's RunProviderAuthCommand
 * RPC. Only CLI-backed provider types with an interactive login flow are
 * supported server-side today (LLM_PROVIDER_TYPE_CLAUDE_CODE / _V2).
 *
 * Requires a gRPC (HTTP/2) transport — see this module's header on why gRPC-Web
 * cannot carry a bidi call.
 */
/**
 * Browser-compatible interactive provider auth.
 *
 * Exposes the same surface as {@link ProviderAuthCommandSession} — iterate it for output, call
 * `write()`/`resize()`/`end()` — but drives llmd's session-based RPCs (start / stream / send /
 * end) instead of the bidi one. That matters because gRPC-Web has no wire encoding for bidi
 * calls at all, so {@link startProviderAuthCommand} simply cannot run in a browser.
 *
 * Trade-off vs the bidi call: llmd holds the PTY between calls, so an abandoned session is a
 * live process on the llmd host until the server's idle reaper collects it. **Always call
 * `end()`** (e.g. from a dialog's teardown) rather than relying on that.
 *
 * `write()` is fire-and-forget: each keystroke is a unary RPC, and failures surface on the
 * output stream (or as the process exiting) rather than at the call site, to keep the
 * imperative terminal-style API.
 */
export class ProviderAuthSession {
  private readonly client: LlmServiceClient;
  private readonly sessionIdPromise: Promise<string>;
  private readonly abort = new AbortController();
  private ended = false;

  constructor(client: LlmServiceClient, sessionIdPromise: Promise<string>) {
    this.client = client;
    this.sessionIdPromise = sessionIdPromise;
  }

  /** Resolves once llmd has spawned the CLI and assigned an id. */
  async sessionId(): Promise<string> {
    return this.sessionIdPromise;
  }

  /** Writes bytes (typically keystrokes) to the command's stdin. */
  write(data: Uint8Array | string): void {
    const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data;
    void this.sessionIdPromise
      .then((sessionId) =>
        this.client.sendProviderAuthInput({ sessionId, frame: { case: "stdin", value: bytes } })
      )
      .catch(() => {
        /* surfaced on the output stream / process exit */
      });
  }

  /** Notifies the PTY of a terminal size change. */
  resize(cols: number, rows: number): void {
    void this.sessionIdPromise
      .then((sessionId) =>
        this.client.sendProviderAuthInput({
          sessionId,
          frame: { case: "resize", value: { cols, rows } },
        })
      )
      .catch(() => {
        /* best-effort: a failed resize is cosmetic */
      });
  }

  /**
   * Ends the session: kills the CLI if it is still running and releases it server-side.
   * Idempotent. Call this even on the success path — the process is not tied to any stream.
   */
  async end(): Promise<void> {
    if (this.ended) return;
    this.ended = true;
    this.abort.abort();
    try {
      const sessionId = await this.sessionIdPromise;
      await this.client.endProviderAuthSession({ sessionId });
    } catch {
      /* already gone */
    }
  }

  async *[Symbol.asyncIterator](): AsyncIterator<ProviderAuthCommandEvent> {
    const sessionId = await this.sessionIdPromise;
    // replay: true so output produced before this stream attached (notably the
    // login URL, printed immediately) is not lost.
    const responses = this.client.streamProviderAuthOutput(
      { sessionId, replay: true },
      { signal: this.abort.signal }
    );
    for await (const out of responses) {
      const f = out.frame;
      if (f.case === "started") {
        yield { kind: "started" };
      } else if (f.case === "output" && f.value.length > 0) {
        yield { kind: "output", data: f.value };
      } else if (f.case === "exitCode") {
        yield { kind: "exit", code: f.value };
      }
    }
  }
}

/**
 * Starts interactive provider auth over llmd's session-based RPCs — the browser-safe
 * counterpart to {@link startProviderAuthCommand}.
 *
 * Works on **both** transports, so prefer it when a single code path has to serve web and
 * Electron renderers alike. Use {@link startProviderAuthCommand} only when you specifically
 * want the single-call bidi form and know you hold a gRPC (HTTP/2) transport.
 *
 * Pass the same `configDir` used by the provider and by `checkProviderConnection`: Claude
 * credentials are written per config directory, so signing in against one and checking another
 * leaves the user permanently "signed out".
 */
export function startProviderAuthSession(
  client: LlmServiceClient,
  providerType: LlmProviderType,
  options?: ProviderAuthCommandStartOptions
): ProviderAuthSession {
  const started = client
    .startProviderAuthSession({
      providerType,
      binary: options?.binary,
      configDir: options?.configDir,
      cols: options?.cols ?? 80,
      rows: options?.rows ?? 24,
    })
    .then((r) => r.sessionId);
  return new ProviderAuthSession(client, started);
}

export function startProviderAuthCommand(
  client: LlmServiceClient,
  providerType: LlmProviderType,
  options?: ProviderAuthCommandStartOptions
): ProviderAuthCommandSession {
  const requests = new RequestQueue<ProviderAuthCommandInput>();
  requests.push(
    create(ProviderAuthCommandInputSchema, {
      frame: {
        case: "start",
        value: {
          providerType,
          binary: options?.binary,
          configDir: options?.configDir,
          cols: options?.cols ?? 80,
          rows: options?.rows ?? 24,
        },
      },
    })
  );
  const abort = new AbortController();
  const responses = client.runProviderAuthCommand(requests, {
    signal: abort.signal,
  });
  return new ProviderAuthCommandSession(requests, responses, abort);
}
