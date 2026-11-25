/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * End-to-end coverage for ForkSession against a REAL llmd (real SQLite, real
 * gRPC): history is cloned up to the requested message, lineage surfaces in
 * both GetSession and ListSessions, and the source is left untouched.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startLlmd, type LlmdHandle } from '@pyxis-llm/core/node';
import { createLlmdClient, createLlmdTransport } from '@pyxis-llm/client/node';
import type { Message, Transport } from '@pyxis-llm/client';
import {
  ensureLlmdSession,
  forkLlmdSession,
  getLlmdSessionMeta,
  listAllLlmdSessions,
  replaceLlmdSessionHistory,
} from '@pyxis-llm/client';

const e2eRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)));
const LLMD_BINARY_PATH = path.join(e2eRoot, '..', '..', '..', 'llmd-e2e-bin');

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
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'llmd-fork-e2e-'));
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

const msg = (role: Message['role'], id: string, content: string): Message => ({ role, id, content });

async function seedSource(): Promise<string> {
  const sessionId = `e2e-src-${randomUUID()}`;
  await ensureLlmdSession({ transport, tenantId, scopeId, sessionId, displayName: 'Source' });
  await replaceLlmdSessionHistory({
    transport,
    tenantId,
    scopeId,
    sessionId,
    messages: [
      msg('user', 'U1', 'first question'),
      msg('assistant', 'A1', 'first answer'),
      msg('user', 'U2', 'second question'),
      msg('assistant', 'A2', 'second answer'),
    ],
  });
  return sessionId;
}

async function historyIds(sessionId: string): Promise<string[]> {
  const client = createLlmdClient({ address: handle.grpcAddress, tenantId, scopeId, sessionId });
  return (await client.getHistory()).map((m) => m.id);
}

describe('forkLlmdSession', () => {
  it('clones history up to the message and records lineage', async () => {
    const src = await seedSource();
    const res = await forkLlmdSession({
      transport,
      tenantId,
      scopeId,
      sourceSessionId: src,
      upToMessageId: 'A1',
      title: 'Branch',
    });
    expect(res.sessionId).not.toBe('');
    expect(res.sessionId).not.toBe(src);
    expect(res.copiedMessages).toBe(2);

    expect(await historyIds(res.sessionId)).toEqual(['U1', 'A1']);
    expect(await historyIds(src)).toEqual(['U1', 'A1', 'U2', 'A2']);

    const meta = await getLlmdSessionMeta({ transport, tenantId, scopeId, sessionId: res.sessionId });
    expect(meta.title).toBe('Branch');
    expect(meta.forkedFromSessionId).toBe(src);
    expect(meta.forkedFromMessageId).toBe('A1');

    const listed = (await listAllLlmdSessions({ transport, tenantId, scopeId })).find(
      (s) => s.sessionId === res.sessionId
    );
    expect(listed?.forkedFromSessionId).toBe(src);
    expect(listed?.forkedFromMessageId).toBe('A1');
  });

  it('clones everything when no cutoff is given and honours newSessionId', async () => {
    const src = await seedSource();
    const newSessionId = `e2e-fork-${randomUUID()}`;
    const res = await forkLlmdSession({ transport, tenantId, scopeId, sourceSessionId: src, newSessionId });
    expect(res.sessionId).toBe(newSessionId);
    expect(res.copiedMessages).toBe(4);
    expect(await historyIds(newSessionId)).toEqual(['U1', 'A1', 'U2', 'A2']);

    await expect(
      forkLlmdSession({ transport, tenantId, scopeId, sourceSessionId: src, newSessionId })
    ).rejects.toThrow();
  });

  it('rejects an unknown message', async () => {
    const src = await seedSource();
    await expect(
      forkLlmdSession({ transport, tenantId, scopeId, sourceSessionId: src, upToMessageId: 'nope' })
    ).rejects.toThrow();
  });
});
