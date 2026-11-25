/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Leaving mid-reply must not lose the reply. The user sends a message, sees
 * part of the answer, navigates away (the ChatPanel unmounts and its stream
 * is torn down), and comes back. llmd keeps the turn running without the
 * client, and the returning driver:
 *   - learns from getSessionState that the turn is still in progress,
 *   - shows a streaming bubble rebuilt live from the session-events
 *     subscription, starting at the running turn's first frame,
 *   - and swaps in the persisted transcript once the turn commits.
 * The reply is complete, not marked interrupted, and nothing was cancelled.
 */

import React, { useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { ChatPanel } from '@pyxis-llm/react';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/node';

import { startE2eEnv, type E2eEnv } from '../../client/e2e/harness.js';

let env: E2eEnv;
let container: HTMLDivElement;
let root: Root;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
});

async function waitFor(check: () => boolean, timeoutMs = 5_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error('waitFor: condition never became true');
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

function typeAndSend(text: string): void {
  const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
  expect(textarea).not.toBeNull();
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(textarea, text);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
  act(() => {
    (container.querySelector('button[aria-label="Send"]') as HTMLButtonElement).click();
  });
}

let latestDriver: LlmdChatDriver | undefined;

function Harness({ sessionId }: { sessionId: string }) {
  const client = useMemo(
    () =>
      createLlmdClient({
        address: env.handle.grpcAddress,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
      }),
    [sessionId]
  );
  const driver = useLlmdChatDriver(client);
  latestDriver = driver;
  return (
    <MantineProvider>
      <div style={{ height: 400 }}>
        <ChatPanel
          messages={driver.messages}
          onSend={driver.send}
          streaming={driver.streaming}
          onStop={driver.stop}
          streamingMessageId={driver.streamingMessageId}
        />
      </div>
    </MantineProvider>
  );
}

describe('ChatPanel + useLlmdChatDriver: leaving mid-reply and coming back', () => {
  it('the turn finishes without the client, and the returning panel rejoins it and shows the full reply', async () => {
    env.fakeOpenAi.enqueue({
      kind: 'slow_text',
      deltas: ['The first half of the answer', ' and the second half.'],
      delayMs: 1_500,
    });
    const { sessionId } = await env.newSession();

    // 1. Send and watch the first part arrive.
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    typeAndSend('Tell me something.');
    await waitFor(() => container.textContent?.includes('The first half of the answer') === true, 10_000);

    // 2. Navigate away mid-reply: the panel unmounts and its stream is torn down.
    act(() => {
      root.render(<div>somewhere else</div>);
    });

    // 3. Come back while the model is still going.
    latestDriver = undefined;
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    // The returning panel knows a turn is running and shows it streaming...
    await waitFor(() => latestDriver?.streaming === true, 5_000);
    // ...rebuilt from the turn's own frames, including text sent before we came back.
    await waitFor(() => container.textContent?.includes('The first half of the answer') === true, 10_000);
    // ...below the message the user sent — a session load gives it back mid-turn
    // (llmd persists it at turn start), even with no host-side history load.
    expect(latestDriver!.streaming).toBe(true);
    expect(latestDriver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(latestDriver!.messages[0].content).toBe('Tell me something.');

    // 4. The turn completes server-side and the panel settles on the persisted transcript.
    await waitFor(() => container.textContent?.includes('and the second half.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false, 15_000);

    const msgs = latestDriver!.messages;
    expect(msgs.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(msgs[1].content).toBe('The first half of the answer and the second half.');
    expect((msgs[1].metadata as { interrupted?: boolean } | undefined)?.interrupted).not.toBe(true);
    expect((msgs[1].metadata as { streamingPlaceholder?: boolean } | undefined)?.streamingPlaceholder).not.toBe(true);

    // And it is durable: a fresh read of history has the complete, uncancelled reply.
    const history = await createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    }).getHistory();
    const reply = history.find((m) => m.role === 'assistant');
    expect(reply?.content).toBe('The first half of the answer and the second half.');
    expect((reply as { finishReason?: string } | undefined)?.finishReason).not.toBe('cancelled');
  });

  it('a panel mounted after the turn already finished just shows history, with no phantom streaming bubble', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['done already'] });
    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    typeAndSend('Quick one.');
    await waitFor(() => container.textContent?.includes('done already') === true, 10_000);
    await waitFor(() => latestDriver?.streaming === false, 10_000);

    act(() => {
      root.render(<div>elsewhere</div>);
    });
    latestDriver = undefined;
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    // Give the mount-time state check a moment; it must not start a rejoin.
    await new Promise((r) => setTimeout(r, 500));
    await act(async () => {});
    expect(latestDriver?.streaming).toBe(false);
    expect(
      (latestDriver?.messages ?? []).some(
        (m) => (m.metadata as { streamingPlaceholder?: boolean } | undefined)?.streamingPlaceholder
      )
    ).toBe(false);
  });
});
