/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { ConversationEventType, UserMessage } from '@pyxis-llm/client';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

describe('chat streaming', () => {
  it('sends a message and streams the assistant reply back in order', async () => {
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: ['Hello', ', ', 'world', '!'],
    });
    const { client, sessionId } = await env.newSession();

    const seenDeltas: string[] = [];
    let finalMessage = '';
    for await (const item of client.chatStreamEvents([
      new UserMessage('Say hello', 'msg-1'),
    ])) {
      if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTextDelta) {
        seenDeltas.push(item.event.delta);
      }
      if (item.kind === 'complete') {
        finalMessage = item.message.content;
      }
    }

    expect(seenDeltas.join('')).toBe('Hello, world!');
    expect(finalMessage).toBe('Hello, world!');

    // Persisted history reflects the same turn (partial + final agree).
    const history = await env.getHistory(sessionId);
    const assistantMsgs = history.messages.filter((m) => m.role === 'assistant');
    expect(assistantMsgs.length).toBeGreaterThan(0);
    expect(assistantMsgs.at(-1)?.content).toBe('Hello, world!');
  });

  it('handles partial delivery: multiple deltas arrive before completion', async () => {
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: ['Partial ', 'streaming ', 'works.'],
    });
    const { client } = await env.newSession();

    let deltaCount = 0;
    let sawComplete = false;
    for await (const item of client.chatStreamEvents([
      new UserMessage('Stream partials', 'msg-2'),
    ])) {
      if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTextDelta) {
        deltaCount++;
      }
      if (item.kind === 'complete') sawComplete = true;
    }
    expect(deltaCount).toBeGreaterThanOrEqual(3);
    expect(sawComplete).toBe(true);
  });
});
