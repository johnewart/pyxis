/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Lifecycle management for a spawned `llmd` process: find a port, start,
 * wait until it reports it's actually listening, and stop it cleanly. Any
 * Node host (host apps, this package's own e2e tests) can use this
 * identically — it is the same "managed llmd" contract llmd's own daemon
 * describes (see cmd/llmd/main.go's `LLMD_EMIT_MANAGED_LISTEN_JSON`), not a
 * test-only convenience.
 *
 * Deliberately does NOT scan for a free port itself: llmd is asked to bind
 * `127.0.0.1:0` (OS-assigned ephemeral port) and prints the address it
 * actually bound once ready. That removes the whole class of "port picked
 * client-side raced another process for the same port" bugs a manual scan
 * has.
 */

import { spawn, type ChildProcessByStdio } from 'node:child_process';
import { createInterface } from 'node:readline';
import type { Readable } from 'node:stream';

export type LlmdStartOptions = {
  /** Path to the built llmd binary. */
  binaryPath: string;
  /** Extra env vars merged over process.env (e.g. LLMD_DATABASE_URL for an isolated DB). */
  env?: Record<string, string>;
  cwd?: string;
  /** Requested gRPC bind address. Default '127.0.0.1:0' (OS-assigned ephemeral port). */
  grpcAddr?: string;
  /** Optional MCP HTTP bind address (e.g. '127.0.0.1:0'); when set, llmd's MCP HTTP server starts too. */
  mcpHttpAddr?: string;
  /**
   * Optional Connect/grpc-web bind address (e.g. '127.0.0.1:0'). Requires
   * `connectToken` to also be set (llmd refuses to start this listener
   * without a token) — this is a SEPARATE port from `grpcAddress`, not the
   * same port under protocol multiplexing; native gRPC and Connect/grpc-web
   * are two distinct listeners.
   */
  connectAddr?: string;
  /**
   * Per-spawn shared secret for the Connect/grpc-web listener (see
   * `connectAddr`). Required to start that listener at all; sent back as
   * `connectToken` on the handle so callers can set the `x-llmd-token`
   * header (or `Authorization: Bearer <token>`) on Connect requests.
   */
  connectToken?: string;
  /** Milliseconds to wait for the managed-listen banner before failing. Default 15000. */
  startupTimeoutMs?: number;
  /** Called with each stdout line as it arrives, for test/host diagnostics. */
  onStdoutLine?: (line: string) => void;
  /** Called with each stderr line as it arrives, for test/host diagnostics. */
  onStderrLine?: (line: string) => void;
};

export type LlmdExitInfo = {
  code: number | null;
  signal: NodeJS.Signals | null;
};

export type LlmdHandle = {
  readonly pid: number;
  /** `host:port` llmd's LlmService gRPC is actually listening on. */
  readonly grpcAddress: string;
  /** `host:port` llmd's MCP HTTP server is actually listening on, when `mcpHttpAddr` was requested. */
  readonly mcpHttpAddress?: string;
  /**
   * `host:port` llmd's Connect/grpc-web HTTP server is actually listening
   * on, when `connectAddr` + `connectToken` were requested. A SEPARATE port
   * from `grpcAddress` — see `LlmdStartOptions.connectAddr`.
   */
  readonly connectAddress?: string;
  /** The shared secret to send as `x-llmd-token` when calling `connectAddress`, echoed back from `connectToken`. */
  readonly connectToken?: string;
  /** Resolves when the process exits for any reason (crash, stop(), external kill). */
  waitForExit(): Promise<LlmdExitInfo>;
  /** Sends SIGTERM, waits up to `gracefulTimeoutMs`, then SIGKILL. Resolves once the process has exited. */
  stop(gracefulTimeoutMs?: number): Promise<void>;
};

/** Thrown when llmd fails to report its listen addresses within the startup window. */
export class LlmdStartupError extends Error {}

const MANAGED_LISTEN_PREFIX = 'LLMD_MANAGED_LISTEN_JSON=';
const STDERR_TAIL_LINES = 50;

/**
 * Spawn llmd, wait for it to report its bound addresses, and return a handle
 * for connecting to it and shutting it down.
 */
export function startLlmd(options: LlmdStartOptions): Promise<LlmdHandle> {
  const {
    binaryPath,
    env = {},
    cwd,
    grpcAddr = '127.0.0.1:0',
    mcpHttpAddr,
    connectAddr,
    connectToken,
    startupTimeoutMs = 15_000,
    onStdoutLine,
    onStderrLine,
  } = options;

  return new Promise((resolve, reject) => {
    // Start from process.env but explicitly clear LLMD_MCP_HTTP_ADDR when the
    // caller didn't ask for the MCP HTTP server: a value inherited from the
    // parent shell/process (e.g. a previous llmd instance's exported address)
    // otherwise makes this child try to bind an address another process may
    // already own, failing startup with a confusing "address already in use".
    const childEnv: NodeJS.ProcessEnv = { ...process.env, ...env };
    delete childEnv.LLMD_MCP_HTTP_ADDR;
    childEnv.LLMD_GRPC_ADDR = grpcAddr;
    if (mcpHttpAddr) childEnv.LLMD_MCP_HTTP_ADDR = mcpHttpAddr;
    if (connectToken) {
      childEnv.LLMD_CONNECT_TOKEN = connectToken;
      if (connectAddr) childEnv.LLMD_CONNECT_HTTP_ADDR = connectAddr;
    }
    childEnv.LLMD_EMIT_MANAGED_LISTEN_JSON = '1';

    const child: ChildProcessByStdio<null, Readable, Readable> = spawn(binaryPath, [], {
      cwd,
      env: childEnv,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let settled = false;
    const stderrTail: string[] = [];

    const fail = (err: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(err);
    };

    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      fail(
        new LlmdStartupError(
          `llmd did not report its listen addresses within ${startupTimeoutMs}ms. stderr tail:\n${stderrTail.join('\n')}`
        )
      );
    }, startupTimeoutMs);

    child.once('error', fail);

    child.once('exit', (code, signal) => {
      fail(
        new LlmdStartupError(
          `llmd exited before reporting listen addresses (code=${code} signal=${signal}). stderr tail:\n${stderrTail.join('\n')}`
        )
      );
    });

    const stdoutRl = createInterface({ input: child.stdout });
    stdoutRl.on('line', (line) => {
      onStdoutLine?.(line);
      if (settled) return;
      const idx = line.indexOf(MANAGED_LISTEN_PREFIX);
      if (idx < 0) return;
      const jsonText = line.slice(idx + MANAGED_LISTEN_PREFIX.length).trim();
      let parsed: { grpc?: string; mcp?: string; connect?: string };
      try {
        parsed = JSON.parse(jsonText) as { grpc?: string; mcp?: string; connect?: string };
      } catch {
        fail(new LlmdStartupError(`could not parse LLMD_MANAGED_LISTEN_JSON: ${jsonText}`));
        return;
      }
      if (!parsed.grpc) {
        fail(new LlmdStartupError(`LLMD_MANAGED_LISTEN_JSON had no grpc address: ${jsonText}`));
        return;
      }

      settled = true;
      clearTimeout(timer);

      const exitPromise = new Promise<LlmdExitInfo>((res) => {
        child.once('exit', (code, signal) => res({ code, signal }));
      });

      resolve({
        pid: child.pid!,
        grpcAddress: parsed.grpc,
        mcpHttpAddress: parsed.mcp,
        connectAddress: parsed.connect,
        connectToken,
        waitForExit: () => exitPromise,
        stop: (gracefulTimeoutMs = 5000) =>
          new Promise<void>((res) => {
            if (child.exitCode !== null || child.signalCode !== null) {
              res();
              return;
            }
            const killTimer = setTimeout(() => {
              child.kill('SIGKILL');
            }, gracefulTimeoutMs);
            child.once('exit', () => {
              clearTimeout(killTimer);
              res();
            });
            child.kill('SIGTERM');
          }),
      });
    });

    const stderrRl = createInterface({ input: child.stderr });
    stderrRl.on('line', (line) => {
      onStderrLine?.(line);
      stderrTail.push(line);
      if (stderrTail.length > STDERR_TAIL_LINES) stderrTail.shift();
    });
  });
}
