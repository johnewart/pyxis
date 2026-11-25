/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import {
  UserMessage,
  upsertLlmdSessionAttachment,
  deleteLlmdSessionAttachment,
  getLlmdSessionAttachmentRows,
} from '@pyxis-llm/client';
import { llmdMessageAttachmentFromRowContent } from '@pyxis-llm/core';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

async function lastAssistantMessageId(env: E2eEnv, sessionId: string): Promise<string> {
  const res = await env.getHistory(sessionId);
  const assistant = res.messages.filter((m) => m.role === 'assistant');
  const id = assistant.at(-1)?.id;
  if (!id) throw new Error('no assistant message to anchor to');
  return id;
}

describe('attachments', () => {
  it('round-trips an attachment through upsert -> read -> delete', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['ok'] });
    const { client, sessionId } = await env.newSession();

    // Anchor the attachment to a real assistant message (a turn must exist first).
    for await (const item of client.chatStreamEvents([
      new UserMessage('anchor turn', 'msg-anchor'),
    ])) {
      void item;
    }

    const anchorMessageId = await lastAssistantMessageId(env, sessionId);

    const attachmentId = 'att-e2e-1';
    await upsertLlmdSessionAttachment({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
      anchorMessageId,
      attachment: {
        id: attachmentId,
        kind: 'build_target_suggestion',
        payloadJson: JSON.stringify({ status: 'pending', label: 'e2e attachment' }),
      },
    });

    const rowsAfter = await getLlmdSessionAttachmentRows({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    const parsedAfter = rowsAfter
      .map((r) => llmdMessageAttachmentFromRowContent(r.content))
      .filter((a): a is NonNullable<typeof a> => a !== undefined);
    const found = parsedAfter.find((a) => a.id === attachmentId);
    expect(found).toBeDefined();
    expect(found?.kind).toBe('build_target_suggestion');
    expect(JSON.parse(found!.payloadJson)).toMatchObject({ label: 'e2e attachment' });

    await deleteLlmdSessionAttachment({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
      attachmentId,
    });

    const rowsFinal = await getLlmdSessionAttachmentRows({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    const parsedFinal = rowsFinal
      .map((r) => llmdMessageAttachmentFromRowContent(r.content))
      .filter((a): a is NonNullable<typeof a> => a !== undefined);
    expect(parsedFinal.find((a) => a.id === attachmentId)).toBeUndefined();
  });
});
