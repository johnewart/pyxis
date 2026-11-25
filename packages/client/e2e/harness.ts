/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Shared e2e test harness: spawns a real llmd (via @pyxis-llm/core's startLlmd —
 * the same lifecycle contract host apps use), registers a fake
 * OpenAI-compatible provider pointed at a local scriptable server, and hands
 * back a real `@pyxis-llm/client` LlmdClient plus the raw LlmServiceClient for
 * setup/assertions.
 */

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import os from 'node:os';
import fs from 'node:fs';
import { randomUUID } from 'node:crypto';

import { startLlmd, type LlmdHandle } from '@pyxis-llm/core/node';
import {
  ensureLlmdSession,
  scopeHeaders,
  type LlmdClient,
  type LlmServiceClient,
} from '@pyxis-llm/client';
import { createLlmdNodeConnection } from '@pyxis-llm/client/node';
import type { GetSessionHistoryResponse } from '@pyxis-llm/client/gen/llmv1/llm_pb.js';

import { startFakeOpenAiServer, type FakeOpenAiServer } from './fakeOpenAiServer.js';

const e2eRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)));
const llmdRoot = path.resolve(e2eRoot, '..', '..', '..'); // llmd/packages/client/e2e -> llmd
export const LLMD_BINARY_PATH = path.join(llmdRoot, 'llmd-e2e-bin');

/** Where a running e2e llmd listens. All a test needs to know about the process. */
export type E2eLlmdAddresses = Pick<LlmdHandle, 'grpcAddress' | 'connectAddress'>;

/**
 * Env var a vitest globalSetup sets (see `startSharedE2eLlmd`) so every test
 * file talks to ONE llmd instead of spawning its own. JSON: `SharedE2eLlmd`.
 */
export const SHARED_LLMD_ENV = 'LLMD_E2E_SHARED_LLMD';

type SharedE2eLlmd = { grpcAddress: string; connectAddress?: string; connectToken: string };

/** Spawns an isolated llmd (own temp data dir, Connect listener on). */
async function spawnE2eLlmd(): Promise<{
  handle: LlmdHandle;
  connectToken: string;
  stop(): Promise<void>;
}> {
  if (!fs.existsSync(LLMD_BINARY_PATH)) {
    throw new Error(
      `llmd binary not found at ${LLMD_BINARY_PATH} — run \`npm run build:llmd\` first (the \`test\` script does this automatically).`
    );
  }
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'llmd-e2e-'));
  const connectToken = `e2e-connect-token-${randomUUID()}`;
  // llmd's recent stderr, printed if it dies mid-run: otherwise a crash shows
  // up only as a wall of connection errors in whichever tests run next.
  const stderrTail: string[] = [];
  let stopping = false;
  const handle = await startLlmd({
    onStderrLine: (line) => {
      stderrTail.push(line);
      if (stderrTail.length > 50) stderrTail.shift();
    },
    binaryPath: LLMD_BINARY_PATH,
    env: {
      LLMD_DATABASE_URL: path.join(dataDir, 'agent.sqlite'),
      LLMD_KNOWLEDGE_SQLITE_PATH: path.join(dataDir, 'knowledge.sqlite'),
      // The React suites run under jsdom, whose WebSocket sends the page
      // Origin (vitest's jsdom default, http://localhost:3000). Without it on
      // the allowlist llmd rejects every /ws/session-events handshake as
      // cross-origin — correctly — and the web session-events feed never
      // connects, so nothing on that path was actually being exercised.
      LLMD_CONNECT_ALLOWED_ORIGINS: 'http://localhost:3000',
      // The queue suites claim and retire entries by hand as a client drainer
      // would; llmd's own server drainer would race them for every entry.
      LLMD_DISABLE_SERVER_QUEUE_DRAIN: '1',
    },
    connectAddr: '127.0.0.1:0',
    connectToken,
  });
  void handle.waitForExit().then((info) => {
    if (stopping) return;
    // eslint-disable-next-line no-console
    console.error(
      `[e2e] llmd exited unexpectedly (code=${info.code} signal=${info.signal}). stderr tail:\n${stderrTail.join('\n')}`
    );
  });
  return {
    handle,
    connectToken,
    async stop() {
      stopping = true;
      await handle.stop();
      fs.rmSync(dataDir, { recursive: true, force: true });
    },
  };
}

/**
 * For a vitest `globalSetup`: starts one llmd for the whole run and publishes
 * it via {@link SHARED_LLMD_ENV}, so `startE2eEnv` in every file reuses it.
 * Files stay isolated anyway — each gets its own random tenant/scope, its own
 * fake provider registration, and its own fake OpenAI server. Returns the
 * teardown.
 */
export async function startSharedE2eLlmd(): Promise<() => Promise<void>> {
  const llmd = await spawnE2eLlmd();
  const shared: SharedE2eLlmd = {
    grpcAddress: llmd.handle.grpcAddress,
    connectAddress: llmd.handle.connectAddress,
    connectToken: llmd.connectToken,
  };
  process.env[SHARED_LLMD_ENV] = JSON.stringify(shared);
  return async () => {
    delete process.env[SHARED_LLMD_ENV];
    await llmd.stop();
  };
}

export type E2eEnv = {
  handle: E2eLlmdAddresses;
  fakeOpenAi: FakeOpenAiServer;
  /**
   * The raw Connect client, for RPCs @pyxis-llm/client has no helper for. Connect
   * clients are promise-based and take `{ headers }` per call, so assertions
   * read as plain awaits — no callback wrapping, and no grpc.Metadata.
   */
  rawClient: LlmServiceClient;
  /** Tenant + scope headers (built with `scopeHeaders`) for raw `rawClient` calls. */
  headers: Headers;
  tenantId: string;
  scopeId: string;
  providerId: string;
  /** Shared secret for llmd's Connect/grpc-web listener (see LlmdHandle.connectAddress). */
  connectToken: string;
  /** Creates a fresh LlmdClient scoped to a new session id, registered against the fake provider. */
  newSession(override?: SessionOverride): Promise<{ client: LlmdClient; sessionId: string }>;
  /**
   * A native-gRPC LlmdClient for an existing session, on this file's ONE shared
   * HTTP/2 connection. Use this instead of `createLlmdClient` in tests: each
   * `createLlmdClient` opens its own connection, which connect-node keeps idle
   * for 15 minutes and which nothing closes — enough of those keep the test
   * worker alive after its tests pass (the e2e run then never exits).
   */
  clientFor(sessionId: string, override?: { tenantId?: string; scopeId?: string }): LlmdClient;
  /** Builds tenant/scope headers for raw calls as another tenant or scope. */
  headersFor(identity: { tenantId: string; scopeId?: string }): Headers;
  /** Persisted session history — the usual way these tests assert on what llmd stored. */
  getHistory(sessionId: string): Promise<GetSessionHistoryResponse>;
  teardown(): Promise<void>;
};

/** Optional identity override for newSession (e.g. a second tenant on the same llmd). */
export type SessionOverride = { tenantId?: string; scopeId?: string; sessionId?: string };

/**
 * A fake OpenAI provider plus an llmd to talk to; call teardown() in afterAll.
 *
 * Uses the run-wide llmd when a globalSetup published one (see
 * {@link startSharedE2eLlmd}); otherwise spawns a private one for this file.
 */
export async function startE2eEnv(): Promise<E2eEnv> {
  const sharedJson = process.env[SHARED_LLMD_ENV];
  let handle: E2eLlmdAddresses;
  let connectToken: string;
  let stopOwnLlmd: (() => Promise<void>) | undefined;
  if (sharedJson) {
    const shared = JSON.parse(sharedJson) as SharedE2eLlmd;
    handle = { grpcAddress: shared.grpcAddress, connectAddress: shared.connectAddress };
    connectToken = shared.connectToken;
  } else {
    const own = await spawnE2eLlmd();
    handle = own.handle;
    connectToken = own.connectToken;
    stopOwnLlmd = own.stop;
  }

  // Per file even with a shared llmd: tests script it in-process via enqueue().
  const fakeOpenAi = await startFakeOpenAiServer();

  const tenantId = `e2e-tenant-${randomUUID()}`;
  const scopeId = `e2e-scope-${randomUUID()}`;
  const providerId = 'fake-openai';
  const headers = scopeHeaders({ tenantId, scopeId });

  // One connection for the whole suite: `admin.client` is the raw Connect
  // client, so per-session LlmdClients and the helpers below all reuse it.
  // The provider is registered with tenant-only identity, so it is
  // tenant-wide and resolvable from any scope under this tenant.
  // ...and one HTTP/2 connection for every client this file makes, closed in
  // teardown (see clientFor).
  const connection = createLlmdNodeConnection({ address: handle.grpcAddress });
  const admin = connection.client({ tenantId });
  const rawClient = admin.client;
  const clientFor = (sessionId: string, override?: { tenantId?: string; scopeId?: string }) =>
    connection.client({
      tenantId: override?.tenantId ?? tenantId,
      scopeId: override?.scopeId ?? scopeId,
      sessionId,
    });

  await admin.registerProvider(providerId, {
    type: 'openai',
    apiKey: 'fake-key',
    baseUrl: fakeOpenAi.url,
    defaultModel: 'fake-model',
  });

  return {
    handle,
    fakeOpenAi,
    rawClient,
    headers,
    tenantId,
    scopeId,
    providerId,
    connectToken,
    headersFor: (identity) => scopeHeaders(identity),
    async newSession(override?: SessionOverride) {
      const sTenant = override?.tenantId ?? tenantId;
      const sScope = override?.scopeId ?? scopeId;
      const sessionId = override?.sessionId ?? `e2e-sess-${randomUUID()}`;
      await ensureLlmdSession({
        client: rawClient,
        tenantId: sTenant,
        scopeId: sScope,
        sessionId,
        llmProviderId: providerId,
        defaultModel: 'fake-model',
      });
      const client = clientFor(sessionId, { tenantId: sTenant, scopeId: sScope });
      return { client, sessionId };
    },
    clientFor,
    getHistory(sessionId: string) {
      return rawClient.getSessionHistory({ sessionId }, { headers });
    },
    async teardown() {
      // Close this file's HTTP/2 connection: left open it idles for 15 minutes
      // and keeps the test worker alive. A shared llmd belongs to the
      // globalSetup, which stops it after the run.
      connection.close();
      await stopOwnLlmd?.();
      await fakeOpenAi.close();
    },
  };
}
