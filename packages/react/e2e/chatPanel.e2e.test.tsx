/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives the full `useLlmdChatDriver` + `ChatPanel` stack against a real
 * llmd binary: click "Send" in a real rendered Composer, the driver calls
 * the real `LlmdClient.chatStreamEvents`, llmd streams back a scripted
 * fake-OpenAI reply, and the DOM updates through real React state — no
 * mocking of the driver or the panel.
 */

import React, { useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ChatPanel, TurnTimer, type TurnTimerState } from '@pyxis-llm/react';
import { useLlmdChatDriver } from '@pyxis-llm/react/node';

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

function Harness({ sessionId }: { sessionId: string }) {
  // Once per session, on the file's shared connection — NOT a new client (and
  // new HTTP/2 transport) on every render, which piled up connections and
  // re-ran the driver's client-keyed callbacks/effects on every streamed delta.
  const client = useMemo(() => env.clientFor(sessionId), [sessionId]);
  const driver = useLlmdChatDriver(client);
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

describe('ChatPanel + useLlmdChatDriver, driven by a real llmd chat stream', () => {
  it('sends a message via the real Composer and renders the real llmd reply', async () => {
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: ['Hello ', 'from ', 'a real ', 'llmd stream.'],
    });
    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        'value'
      )!.set!;
      setter.call(textarea, 'Say hi');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });

    const sendButton = container.querySelector(
      'button[aria-label="Send"]'
    ) as HTMLButtonElement;
    expect(sendButton).not.toBeNull();

    act(() => {
      sendButton.click();
    });

    // The user message renders immediately (optimistic, before any network round-trip).
    await waitFor(() => container.textContent?.includes('Say hi') === true);

    // The real streamed assistant reply arrives and renders.
    await waitFor(
      () => container.textContent?.includes('Hello from a real llmd stream.') === true,
      15_000
    );

    // Streaming settled back to idle (Send button, not Stop) once the turn completed.
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);
    expect(container.querySelector('button[aria-label="Stop"]')).toBeNull();

    // The turn timer stays above the composer after the turn, frozen at how
    // long it took.
    await waitFor(
      () => container.querySelector('[data-testid="llmd-turn-timer"]')?.getAttribute('data-running') === 'false'
    );
    const timer = container.querySelector('[data-testid="llmd-turn-timer"]')!;
    expect(timer.textContent).toMatch(/^took \d+s$/);
    // It sits in the turn bar directly above the composer.
    expect(timer.closest('.llmd-chat-panel__turn-bar')).not.toBeNull();
  });

  it('the turn timer is slottable: renderTurnTimer restyles it in place; showTurnTimer=false lets the host place <TurnTimer> anywhere', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Timed reply.'] });
    const { sessionId } = await env.newSession();

    function SlottedHarness() {
      const client = useMemo(() => env.clientFor(sessionId), []);
      const driver = useLlmdChatDriver(client);
      return (
        <MantineProvider>
          {/* Host-placed timer, in its own chrome, with its own format. */}
          <header data-testid="host-header">
            <TurnTimer
              streaming={driver.streaming}
              format={(s: TurnTimerState) => (s.running ? `⏱ ${s.label}` : `done in ${s.label.replace('took ', '')}`)}
            />
          </header>
          <div style={{ height: 400 }}>
            <ChatPanel
              messages={driver.messages}
              onSend={driver.send}
              streaming={driver.streaming}
              onStop={driver.stop}
              streamingMessageId={driver.streamingMessageId}
              showTurnTimer={false}
            />
          </div>
        </MantineProvider>
      );
    }

    act(() => {
      root.render(<SlottedHarness />);
    });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, 'time me');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });
    act(() => {
      (container.querySelector('button[aria-label="Send"]') as HTMLButtonElement).click();
    });
    await waitFor(() => container.textContent?.includes('Timed reply.') === true, 15_000);
    await waitFor(
      () => container.querySelector('[data-testid="llmd-turn-timer"]')?.getAttribute('data-running') === 'false'
    );

    // Exactly one timer, in the host's header, with the host's format; none in the panel.
    const timers = container.querySelectorAll('[data-testid="llmd-turn-timer"]');
    expect(timers).toHaveLength(1);
    expect(timers[0]!.closest('[data-testid="host-header"]')).not.toBeNull();
    expect(timers[0]!.textContent).toMatch(/^done in \d+s$/);
    expect(container.querySelector('.llmd-chat-panel__turn-bar')).toBeNull();
  });

  it('renderTurnTimer replaces what the panel draws in the timer spot', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Another reply.'] });
    const { sessionId } = await env.newSession();

    function RenderPropHarness() {
      const client = useMemo(() => env.clientFor(sessionId), []);
      const driver = useLlmdChatDriver(client);
      return (
        <MantineProvider>
          <div style={{ height: 400 }}>
            <ChatPanel
              messages={driver.messages}
              onSend={driver.send}
              streaming={driver.streaming}
              onStop={driver.stop}
              streamingMessageId={driver.streamingMessageId}
              renderTurnTimer={(s) => (
                <span data-testid="custom-timer" data-running={String(s.running)}>
                  [{s.label}]
                </span>
              )}
            />
          </div>
        </MantineProvider>
      );
    }

    act(() => {
      root.render(<RenderPropHarness />);
    });
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, 'again');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });
    act(() => {
      (container.querySelector('button[aria-label="Send"]') as HTMLButtonElement).click();
    });
    await waitFor(() => container.textContent?.includes('Another reply.') === true, 15_000);
    await waitFor(() => container.querySelector('[data-testid="custom-timer"]')?.getAttribute('data-running') === 'false');

    const custom = container.querySelector('[data-testid="custom-timer"]')!;
    expect(custom.textContent).toMatch(/^\[took \d+s\]$/);
    expect(custom.closest('.llmd-chat-panel__turn-bar')).not.toBeNull();
    // The default timer is not drawn alongside it.
    expect(container.querySelector('[data-testid="llmd-turn-timer"]')).toBeNull();
  });
});
