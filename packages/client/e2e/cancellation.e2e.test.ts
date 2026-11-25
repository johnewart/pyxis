/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Turn lifetime from the client's side. A turn runs on llmd independently of
 * the stream that started it:
 *
 *   - aborting `chatStreamEvents` only DETACHES this client — the turn keeps
 *     running (navigation, reload and network blips must not stop the model);
 *   - `cancelTurn` is the explicit Stop — the turn ends, the streamed-so-far
 *     partial is persisted as `finish_reason: "cancelled"`, and the SAME
 *     session takes the next message normally.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { ConversationEventType, UserMessage, type LlmdClient } from '@pyxis-llm/client';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

async function waitUntil(check: () => Promise<boolean>, timeoutMs = 10_000): Promise<void> {
  const start = Date.now();
  while (!(await check())) {
    if (Date.now() - start > timeoutMs) throw new Error('waitUntil: condition never became true');
    await new Promise((r) => setTimeout(r, 50));
  }
}

/** Streams a turn until `stopAfter` text has arrived, then calls `act`; returns what was seen. */
async function streamUntil(
  client: LlmdClient,
  text: string,
  id: string,
  stopAfter: string,
  act: (controller: AbortController) => void | Promise<void>
): Promise<{ seen: string[]; text: string; threw: unknown }> {
  const controller = new AbortController();
  const seen: string[] = [];
  let streamed = '';
  let threw: unknown;
  let acted = false;
  try {
    for await (const item of client.chatStreamEvents([new UserMessage(text, id)], { signal: controller.signal })) {
      if (item.kind !== 'event') continue;
      seen.push(item.event.type);
      if (item.event.type === ConversationEventType.AssistantTextDelta) streamed += item.event.delta;
      if (!acted && streamed.includes(stopAfter)) {
        acted = true;
        await act(controller);
      }
    }
  } catch (err) {
    threw = err;
  }
  return { seen, text: streamed, threw };
}

describe('cancellation', () => {
  it('aborting the stream only detaches: the turn keeps running and finishes', async () => {
    env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas: ['First part,', ' then the rest.'], delayMs: 600 });
    const { client } = await env.newSession();

    const { seen, threw } = await streamUntil(client, 'keep going without me', 'msg-detach-1', 'First part,', (c) =>
      c.abort(new Error('e2e detach'))
    );
    expect(threw).toBeDefined();
    expect(seen).not.toContain(ConversationEventType.AssistantTurnFinished);

    // Detached, not stopped: llmd still reports the turn as running...
    expect((await client.getSessionState()).turnInProgress).toBe(true);
    // ...and it finishes on its own and is persisted in full.
    await waitUntil(async () => !(await client.getSessionState()).turnInProgress);
    const history = await client.getHistory();
    const reply = history.find((m) => m.role === 'assistant');
    expect(reply?.content).toBe('First part, then the rest.');
    expect((reply as { finishReason?: string } | undefined)?.finishReason).not.toBe('cancelled');
  });

  it('cancelTurn stops the turn, keeps the partial as cancelled, and the same session stays usable', async () => {
    env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas: ['Partial answer', ' that never arrives.'], delayMs: 2_000 });
    const { client } = await env.newSession();

    const { seen, threw } = await streamUntil(client, 'stop me', 'msg-cancel-1', 'Partial answer', async () => {
      expect(await client.cancelTurn()).toBe(true);
    });
    // The turn ended for this client, without ever completing.
    expect(threw).toBeDefined();
    expect(seen).not.toContain(ConversationEventType.AssistantTurnFinished);

    await waitUntil(async () => !(await client.getSessionState()).turnInProgress);
    const history = await client.getHistory();
    const partial = history.find((m) => m.role === 'assistant');
    expect(partial?.content).toBe('Partial answer');
    expect((partial as { finishReason?: string } | undefined)?.finishReason).toBe('cancelled');

    // Nothing to cancel any more.
    expect(await client.cancelTurn()).toBe(false);

    // The SAME session takes the next message normally (not wedged behind the
    // stopped turn).
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['still works'] });
    let finalMessage = '';
    for await (const item of client.chatStreamEvents([new UserMessage('are you still there?', 'msg-cancel-2')])) {
      if (item.kind === 'complete') finalMessage = item.message.content;
    }
    expect(finalMessage).toBe('still works');
  });
});
