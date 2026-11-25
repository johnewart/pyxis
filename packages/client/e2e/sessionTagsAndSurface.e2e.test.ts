/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * End-to-end coverage for session surface + k/v tags against a REAL llmd
 * (spawned by the shared harness, real SQLite, real gRPC).
 *
 * These exist because the surface bug was invisible to unit tests on either
 * side: the Go store test passed, the client compiled, and the field still never
 * survived a round trip because nothing in the TS path ever sent it and the
 * server's ON CONFLICT clause dropped it on re-ensure. Only a real write-then-
 * read across the wire catches that class of bug.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import fs from 'node:fs';
import path from 'node:path';
import { startLlmd, type LlmdHandle } from '@pyxis-llm/core/node';
import { createLlmdClient, createLlmdTransport } from '@pyxis-llm/client/node';
import type { Transport } from '@pyxis-llm/client';
import {
  ensureLlmdSession,
  getLlmdSessionMeta,
  listLlmdSessions,
  setLlmdSessionMeta,
  updateLlmdSessionTags,
  type LlmdClient,
} from '@pyxis-llm/client';

import { fileURLToPath } from 'node:url';

const e2eRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)));
const LLMD_BINARY_PATH = path.join(e2eRoot, '..', '..', '..', 'llmd-e2e-bin');

/**
 * Deliberately NOT using startE2eEnv from ./harness.js: sessions, tags and
 * surface need no LLM provider and no fake model server, so this spins up a
 * bare llmd and talks to it purely through the supported '@pyxis-llm/client'
 * surface — no provider registration, no scripted completions.
 */
let handle: LlmdHandle;
let tenantId: string;
let scopeId: string;
let dataDir: string;
let transport: Transport;

beforeAll(async () => {
  if (!fs.existsSync(LLMD_BINARY_PATH)) {
    throw new Error(
      `llmd binary not found at ${LLMD_BINARY_PATH} — run \`npm run build:llmd\` first.`
    );
  }
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'llmd-tags-e2e-'));
  handle = await startLlmd({
    binaryPath: LLMD_BINARY_PATH,
    env: {
      LLMD_DATABASE_URL: path.join(dataDir, 'agent.sqlite'),
      LLMD_KNOWLEDGE_SQLITE_PATH: path.join(dataDir, 'knowledge.sqlite'),
    },
    connectAddr: '127.0.0.1:0',
    connectToken: `e2e-connect-token-${randomUUID()}`,
  });
  tenantId = `e2e-tenant-${randomUUID()}`;
  scopeId = `e2e-scope-${randomUUID()}`;
  transport = createLlmdTransport({ address: handle.grpcAddress });
}, 60_000);

afterAll(async () => {
  await handle?.stop();
  if (dataDir) fs.rmSync(dataDir, { recursive: true, force: true });
});

/** Session scope for the free-function helpers, bound to the live llmd. */
function scope(sessionId: string) {
  return { transport, tenantId, scopeId, sessionId };
}

function listScope() {
  return { transport, tenantId, scopeId };
}

/** Provisions a session with an explicit surface and returns a bound client. */
async function newSessionWithSurface(
  chatSurface: string,
  tags?: Record<string, string>
): Promise<{ client: LlmdClient; sessionId: string }> {
  const sessionId = `e2e-sess-${randomUUID()}`;
  await ensureLlmdSession({ ...scope(sessionId), chatSurface });
  if (tags) {
    await updateLlmdSessionTags({ ...scope(sessionId), tags });
  }
  const client = createLlmdClient({
    address: handle.grpcAddress,
    tenantId,
    scopeId,
    sessionId,
  });
  return { client, sessionId };
}

describe('session surface', () => {
  it('persists a surface set at ensure time and reads it back', async () => {
    const { sessionId } = await newSessionWithSurface('build');
    const meta = await getLlmdSessionMeta(scope(sessionId));
    expect(meta.chatSurface).toBe('build');
  });

  it('survives a second ensure that omits the surface', async () => {
    // The original bug: EnsureSessionRow's ON CONFLICT clause never assigned
    // chat_surface, so a re-ensure (reconnect, model change) silently blanked it.
    const { sessionId } = await newSessionWithSurface('debug');
    await ensureLlmdSession({
      ...scope(sessionId),
      // deliberately no chatSurface
    });
    const meta = await getLlmdSessionMeta(scope(sessionId));
    expect(meta.chatSurface).toBe('debug');
  });

  it('can be changed later via setSessionMeta without clearing it on omit', async () => {
    const { sessionId } = await newSessionWithSurface('ask');
    await setLlmdSessionMeta({ ...scope(sessionId), title: 'renamed', chatSurface: 'pair' });
    expect((await getLlmdSessionMeta(scope(sessionId))).chatSurface).toBe('pair');

    // A meta write that omits the surface must leave it alone.
    await setLlmdSessionMeta({ ...scope(sessionId), title: 'renamed again' });
    const meta = await getLlmdSessionMeta(scope(sessionId));
    expect(meta.chatSurface).toBe('pair');
    expect(meta.title).toBe('renamed again');
  });

  it('filters the session list by surface', async () => {
    const surface = `surf-${randomUUID().slice(0, 8)}`;
    const a = await newSessionWithSurface(surface);
    const b = await newSessionWithSurface(surface);
    await newSessionWithSurface(`other-${randomUUID().slice(0, 8)}`);

    const page = await listLlmdSessions({ ...listScope(), chatSurface: surface });
    const ids = page.sessions.map((s) => s.sessionId).sort();
    expect(ids).toEqual([a.sessionId, b.sessionId].sort());
    expect(page.totalCount).toBe(2);
    for (const s of page.sessions) expect(s.chatSurface).toBe(surface);
  });
});

describe('session tags', () => {
  it('writes tags and reads them back on the session and in the list', async () => {
    const { sessionId } = await newSessionWithSurface('build', {
      team: 'infra',
      env: 'prod',
    });
    expect((await getLlmdSessionMeta(scope(sessionId))).tags).toEqual({
      team: 'infra',
      env: 'prod',
    });

    const page = await listLlmdSessions({
      ...listScope(),
      tags: { team: 'infra', env: 'prod' },
    });
    const found = page.sessions.find((s) => s.sessionId === sessionId);
    expect(found?.tags).toEqual({ team: 'infra', env: 'prod' });
  });

  it('merges by key and deletes on empty value', async () => {
    const { sessionId } = await newSessionWithSurface('build', { team: 'infra' });

    // A second write naming a different key must not drop the first.
    const merged = await updateLlmdSessionTags({
      ...scope(sessionId),
      tags: { env: 'prod' },
    });
    expect(merged).toEqual({ team: 'infra', env: 'prod' });

    const afterDelete = await updateLlmdSessionTags({
      ...scope(sessionId),
      tags: { team: '' },
    });
    expect(afterDelete).toEqual({ env: 'prod' });
  });

  it('replaces wholesale when asked', async () => {
    const { sessionId } = await newSessionWithSurface('build', {
      team: 'infra',
      env: 'prod',
    });
    const replaced = await updateLlmdSessionTags({
      ...scope(sessionId),
      tags: { only: 'this' },
      replace: true,
    });
    expect(replaced).toEqual({ only: 'this' });
    expect((await getLlmdSessionMeta(scope(sessionId))).tags).toEqual({ only: 'this' });
  });

  it('does not let a setSessionMeta title edit wipe tags', async () => {
    // Tags are a separate RPC precisely so this cannot happen.
    const { sessionId } = await newSessionWithSurface('build', { team: 'infra' });
    await setLlmdSessionMeta({ ...scope(sessionId), title: 'new title' });
    const meta = await getLlmdSessionMeta(scope(sessionId));
    expect(meta.tags).toEqual({ team: 'infra' });
    expect(meta.title).toBe('new title');
  });

  it('ANDs multiple tag filters and matches key presence on empty value', async () => {
    const team = `team-${randomUUID().slice(0, 8)}`;
    const both = await newSessionWithSurface('build', { team, env: 'prod' });
    const onlyTeam = await newSessionWithSurface('build', { team, env: 'dev' });
    await newSessionWithSurface('build', { team: `${team}-other` });

    const list = (tags: Record<string, string>) =>
      listLlmdSessions({ ...listScope(), tags });

    const andBoth = await list({ team, env: 'prod' });
    expect(andBoth.sessions.map((s) => s.sessionId)).toEqual([both.sessionId]);

    const presence = await list({ team, env: '' });
    expect(presence.sessions.map((s) => s.sessionId).sort()).toEqual(
      [both.sessionId, onlyTeam.sessionId].sort()
    );

    const none = await list({ team, env: 'nonexistent' });
    expect(none.sessions).toHaveLength(0);
    expect(none.totalCount).toBe(0);
  });

  it('exposes listSessions and updateSessionTags on LlmdClient itself', async () => {
    // Session listing used to be reachable only as a free function, so a caller
    // holding an LlmdClient could not list or filter at all.
    const team = `team-${randomUUID().slice(0, 8)}`;
    const { client, sessionId } = await newSessionWithSurface('build');

    const stored = await client.updateSessionTags({ team });
    expect(stored).toEqual({ team });

    const page = await client.listSessions({ tags: { team } });
    expect(page.sessions.map((s) => s.sessionId)).toEqual([sessionId]);
  });

  it('rejects a malformed tag key with InvalidArgument', async () => {
    const { sessionId } = await newSessionWithSurface('build');
    await expect(
      updateLlmdSessionTags({ ...scope(sessionId), tags: { 'bad"key': 'v' } })
    ).rejects.toThrow();
  });
});
