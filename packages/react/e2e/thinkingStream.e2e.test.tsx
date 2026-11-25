/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Proves llmd's server-side `<thinking>...</thinking>` extraction
 * (internal/llm/common/thinkingtag) round-trips through the real driver +
 * ChatPanel: the fake model emits literal `<thinking>` tags in its raw text
 * stream (exactly what a real reasoning-capable provider not natively
 * supporting a separate thinking channel would do); llmd splits that
 * server-side into `assistant_thinking_delta` events (never forwarded as
 * visible text), and the driver accumulates them LIVE into the streaming
 * assistant message's `metadata.thinking` — independent of the visible-text
 * delta stream. After completion, the persisted message (read back via
 * GetSessionHistory) still carries `thinking_content`, and the rendered
 * bubble never shows the raw tags or thinking text.
 */

import React from 'react';
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
  const sendButton = container.querySelector('button[aria-label="Send"]') as HTMLButtonElement;
  expect(sendButton).not.toBeNull();
  act(() => {
    sendButton.click();
  });
}

let latestDriver: LlmdChatDriver | undefined;

function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
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

describe('ChatPanel + useLlmdChatDriver: thinking-stream extraction', () => {
  it('splits <thinking> into metadata.thinking (live + persisted), never into visible content', async () => {
    latestDriver = undefined;

    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: [
        '<thinking>Let me work through this step by step. ',
        'The answer is clearly forty-two.</thinking>',
        'The answer is 42.',
      ],
    });

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('What is the answer?');

    await waitFor(() => container.textContent?.includes('The answer is 42.') === true, 15_000);
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);

    // Visible DOM never shows the thinking text or the raw tags.
    expect(container.textContent).not.toContain('step by step');
    expect(container.textContent).not.toContain('<thinking>');
    expect(container.textContent).not.toContain('forty-two');

    // The persisted assistant message (round-tripped through GetSessionHistory)
    // carries the extracted thinking content separately from visible content.
    const assistant = latestDriver!.messages.find((m) => m.role === 'assistant');
    expect(assistant).toBeDefined();
    expect(assistant!.content).toBe('The answer is 42.');
    const thinking = (assistant!.metadata as { thinking?: string } | undefined)?.thinking;
    expect(thinking).toBeDefined();
    expect(thinking).toContain('step by step');
    expect(thinking).toContain('forty-two');
    expect(thinking).not.toContain('42.');
  });

  it('accumulates thinking live during the stream, before the turn completes', async () => {
    latestDriver = undefined;

    // Slow enough that we can observe the driver's live streaming state
    // (metadata.thinking populated) before the turn finishes.
    env.fakeOpenAi.enqueue({
      kind: 'slow_text',
      deltas: ['<thinking>Reasoning in progress', '...done.</thinking>', 'Final answer.'],
      delayMs: 250,
    });

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('Think about it.');

    // Wait until we're mid-stream with SOME live thinking content captured,
    // while the turn is still marked as streaming.
    await waitFor(() => {
      if (!latestDriver?.streaming) return false;
      const assistant = latestDriver.messages.find((m) => m.role === 'assistant');
      const thinking = (assistant?.metadata as { thinking?: string } | undefined)?.thinking;
      return Boolean(thinking && thinking.length > 0);
    }, 10_000);

    // Let the turn finish normally.
    await waitFor(() => container.textContent?.includes('Final answer.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);
  });
});
