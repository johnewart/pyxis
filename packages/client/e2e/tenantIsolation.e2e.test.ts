/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Tenant isolation over the real wire: two tenants that pick the SAME scope id
 * and the SAME session id must never see each other's history — the tenant is
 * part of the key, not decoration. Also proves a provider registered
 * tenant-wide (tenant-only headers, no scope) resolves from a scoped session.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { randomUUID } from 'node:crypto';
import { UserMessage, type LlmdClient } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

async function sendTurn(client: LlmdClient, text: string, reply: string): Promise<string> {
  env.fakeOpenAi.enqueue({ kind: 'text', deltas: [reply] });
  let final = '';
  for await (const item of client.chatStreamEvents([new UserMessage(text, `msg-${randomUUID()}`)])) {
    if (item.kind === 'complete') final = item.message.content;
  }
  return final;
}

describe('tenant isolation', () => {
  it('two tenants with the same scopeId and sessionId cannot read each other', async () => {
    // Tenant A is the harness tenant: the fake provider is registered
    // tenant-wide under it. Tenant B needs its own provider registration.
    const tenantA = env.tenantId;
    const tenantB = `e2e-tenant-${randomUUID()}`;
    const scopeId = `e2e-scope-${randomUUID()}`;
    const sessionId = `e2e-sess-${randomUUID()}`;

    const adminB = createLlmdClient({ address: env.handle.grpcAddress, tenantId: tenantB });
    await adminB.registerProvider(env.providerId, {
      type: 'openai',
      apiKey: 'fake-key',
      baseUrl: env.fakeOpenAi.url,
      defaultModel: 'fake-model',
    });

    const a = await env.newSession({ tenantId: tenantA, scopeId, sessionId });
    const b = await env.newSession({ tenantId: tenantB, scopeId, sessionId });
    expect(a.sessionId).toBe(b.sessionId);

    const msgA = `hello from tenant A ${randomUUID()}`;
    const msgB = `hello from tenant B ${randomUUID()}`;
    expect(await sendTurn(a.client, msgA, 'reply A')).toBe('reply A');
    expect(await sendTurn(b.client, msgB, 'reply B')).toBe('reply B');

    const histA = await env.rawClient.getSessionHistory(
      { sessionId },
      { headers: env.headersFor({ tenantId: tenantA, scopeId }) }
    );
    const histB = await env.rawClient.getSessionHistory(
      { sessionId },
      { headers: env.headersFor({ tenantId: tenantB, scopeId }) }
    );

    const userA = histA.messages.filter((m) => m.role === 'user').map((m) => m.content);
    const userB = histB.messages.filter((m) => m.role === 'user').map((m) => m.content);
    expect(userA).toContain(msgA);
    expect(userA).not.toContain(msgB);
    expect(userB).toContain(msgB);
    expect(userB).not.toContain(msgA);
  });

  it('a tenant-wide provider resolves from a scoped session', async () => {
    // The harness registered the provider with tenant-only headers (no scope).
    const scopeId = `e2e-scope-${randomUUID()}`;
    expect(scopeId).not.toBe('');
    const { client, sessionId } = await env.newSession({ scopeId });
    expect(await sendTurn(client, 'scoped turn', 'tenant-wide ok')).toBe('tenant-wide ok');

    const hist = await env.rawClient.getSessionHistory(
      { sessionId },
      { headers: env.headersFor({ tenantId: env.tenantId, scopeId }) }
    );
    expect(hist.messages.filter((m) => m.role === 'assistant').at(-1)?.content).toBe(
      'tenant-wide ok'
    );
  });
});
