/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Two continuation scenarios:
 *
 * 1. Same-mount continuation: llmd is the single source of truth for
 *    session history — `ChatStream` always prepends the session's
 *    persisted messages server-side (`internal/server/server.go`:
 *    `full := histMsgs ++ incoming`). So a second turn's outbound request
 *    must carry ONLY the new user message, never a client-side replay of
 *    turn 1 (that would double it up server-side, which is exactly the
 *    latent bug this test caught: earlier driver revisions resent the full
 *    accumulated history and produced duplicated context from turn 2
 *    onward — see useLlmdChatDriver's module doc for the fix).
 * 2. Resume-after-remount continuation: a brand new driver instance (as if
 *    the app reloaded) hydrates its DISPLAY transcript from
 *    `client.getHistory()` (persisted server-side via GetSessionHistory),
 *    and its first new turn still sends only the new message — proving a
 *    session can be resumed for DISPLAY purposes without the client ever
 *    needing to replay it through the model.
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
let capturedClient: ReturnType<typeof createLlmdClient> | undefined;

function Harness({
  sessionId,
  onHydrate,
}: {
  sessionId: string;
  onHydrate?: (driver: LlmdChatDriver) => void;
}) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
  capturedClient = client;
  const driver = useLlmdChatDriver(client);
  latestDriver = driver;

  React.useEffect(() => {
    if (onHydrate) onHydrate(driver);
    // Only run this once per mount (fresh-driver hydration is a one-time
    // resume operation, not something that should re-fire on every render).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

describe('ChatPanel + useLlmdChatDriver: continuation', () => {
  it('a second turn on the same mount sends ONLY the new message; llmd supplies context from persisted history', async () => {
    latestDriver = undefined;

    env.fakeOpenAi.enqueue(
      { kind: 'text', deltas: ['My name is Robo.'] },
      { kind: 'text', deltas: ['Your name is Robo.'] }
    );

    const { sessionId } = await env.newSession();
    const requestCountBefore = env.fakeOpenAi.requests.length;

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('What is your name?');
    await waitFor(() => container.textContent?.includes('My name is Robo.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);

    typeAndSend('What did you just say your name was?');
    await waitFor(() => container.textContent?.includes('Your name is Robo.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);

    // Turn 1's UPSTREAM request (what llmd forwards to the fake "model",
    // i.e. llmd's own histMsgs++incoming — not what the React client sent
    // to llmd, see below): just the first user message (fresh session, no
    // persisted history yet).
    const firstTurnRequest = env.fakeOpenAi.requests[requestCountBefore]?.body;
    expect(
      (firstTurnRequest?.messages ?? [])
        .filter((m: any) => m.role === 'user' || m.role === 'assistant')
        .map((m: any) => m.content)
    ).toEqual(['What is your name?']);

    // Turn 2's UPSTREAM request correctly contains turn 1's exchange plus
    // the new message — with NO duplication. Before the fix documented in
    // useLlmdChatDriver's module doc, the React client redundantly resent
    // turn 1's exchange on top of what llmd's own persisted-history
    // prepend already supplied, so upstream saw it TWICE
    // (['What is your name?', 'My name is Robo.', 'What is your name?',
    // 'My name is Robo.', 'What did you just say your name was?']) — a
    // real, latent multi-turn bug this continuation test caught.
    const secondTurnRequest = env.fakeOpenAi.requests[requestCountBefore + 1]?.body;
    expect(
      (secondTurnRequest?.messages ?? [])
        .filter((m: any) => m.role === 'user' || m.role === 'assistant')
        .map((m: any) => m.content)
    ).toEqual(['What is your name?', 'My name is Robo.', 'What did you just say your name was?']);

    // The DISPLAY transcript still reflects both turns, in order (this is
    // purely local bookkeeping in the driver, independent of what's sent).
    expect(latestDriver!.messages.map((m) => m.role)).toEqual([
      'user',
      'assistant',
      'user',
      'assistant',
    ]);
  });

  it('a fresh driver hydrated from getHistory() shows persisted history for DISPLAY, without ever replaying it through the model', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Remembered: blue.'] });
    const { sessionId } = await env.newSession();

    // Turn 1, on an initial mount — establishes persisted server-side history.
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    typeAndSend('My favorite color is blue. What is it?');
    await waitFor(() => container.textContent?.includes('Remembered: blue.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);

    const requestCountAfterTurn1 = env.fakeOpenAi.requests.length;

    // Simulate an app reload: unmount, then mount a BRAND NEW driver/client
    // instance for the SAME session, hydrated from persisted history instead
    // of any in-memory state.
    act(() => {
      root.unmount();
    });
    container.remove();
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
    latestDriver = undefined;

    let hydratedFrom: LlmdChatDriver | undefined;
    act(() => {
      root.render(
        <Harness
          sessionId={sessionId}
          onHydrate={(driver) => {
            hydratedFrom = driver;
          }}
        />
      );
    });
    await waitFor(() => hydratedFrom !== undefined);

    const history = await capturedClient!.getHistory();
    expect(history.map((m) => m.role)).toEqual(['user', 'assistant']);

    act(() => {
      hydratedFrom!.hydrate(history);
    });
    await waitFor(() => container.textContent?.includes('My favorite color is blue') === true);
    await waitFor(() => container.textContent?.includes('Remembered: blue.') === true);

    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Yes, blue — got it.'] });
    typeAndSend('Do you still remember?');
    await waitFor(() => container.textContent?.includes('Yes, blue — got it.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);

    // The resumed turn's UPSTREAM request correctly carries turn 1's
    // exchange (supplied by llmd's own persisted-history prepend, NOT by
    // the client replaying it) plus the new message — proving `hydrate()`
    // only seeded local display state and never became a client-side
    // replay buffer that could duplicate context.
    const resumedTurnRequest = env.fakeOpenAi.requests[requestCountAfterTurn1]?.body;
    expect(
      (resumedTurnRequest?.messages ?? [])
        .filter((m: any) => m.role === 'user' || m.role === 'assistant')
        .map((m: any) => m.content)
    ).toEqual(['My favorite color is blue. What is it?', 'Remembered: blue.', 'Do you still remember?']);
  });
});
