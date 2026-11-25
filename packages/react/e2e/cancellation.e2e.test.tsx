/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * UI-level cancellation: clicking ChatPanel's real "Stop" button aborts a
 * real in-flight llmd stream (mirrors packages/client/e2e/cancellation.e2e.test.ts,
 * but through the driver + rendered Composer/ChatPanel instead of calling
 * `chatStreamEvents` directly) — proving the whole stack (button click -> onStop
 * -> AbortController -> gRPC cancel -> llmd -> upstream fetch abort) actually
 * wires up, and that the session is left usable afterwards.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType } from '@pyxis-llm/client';
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
          defaultSendBehavior="interrupt"
          streaming={driver.streaming}
          onStop={driver.stop}
          streamingMessageId={driver.streamingMessageId}
        />
      </div>
    </MantineProvider>
  );
}

describe('ChatPanel + useLlmdChatDriver: cancellation', () => {
  it('clicking Stop aborts a hung turn, finalizes the bubble, and leaves the session usable', async () => {
    latestDriver = undefined;

    // The fake model never responds — the real cancellation path is what
    // ends this turn, not the model finishing.
    env.fakeOpenAi.enqueue({ kind: 'hang' });
    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    const requestsBefore = env.fakeOpenAi.requests.length;
    typeAndSend('This will hang forever.');

    // Wait until llmd has actually issued the upstream request before
    // cancelling. The fake provider's script queue is FIFO, one turn per
    // request: aborting before the request goes out leaves `hang` at the head
    // of the queue, and the follow-up turn below would dequeue IT instead of
    // its own `text` turn and hang forever. The Stop button appearing only
    // proves the client started streaming, not that the upstream call was made.
    await waitFor(() => env.fakeOpenAi.requests.length > requestsBefore, 5_000);

    const stopButton = await (async () => {
      await waitFor(() => container.querySelector('button[aria-label="Stop"]') !== null, 5_000);
      return container.querySelector('button[aria-label="Stop"]') as HTMLButtonElement;
    })();

    act(() => {
      stopButton.click();
    });

    // Streaming settles back to idle; no crash, no permanently-stuck Stop button.
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null, 10_000);
    expect(container.querySelector('button[aria-label="Stop"]')).toBeNull();
    await waitFor(() => latestDriver?.streaming === false);

    // The aborted assistant bubble was finalized (not left as a permanent
    // empty "streaming" placeholder) and marked interrupted.
    const assistant = latestDriver!.messages.find((m) => m.role === 'assistant');
    expect(assistant).toBeDefined();
    expect((assistant!.metadata as { interrupted?: boolean } | undefined)?.interrupted).toBe(true);
    expect((assistant!.metadata as { streamingPlaceholder?: boolean } | undefined)?.streamingPlaceholder).not.toBe(
      true
    );

    // The session is left usable: a normal turn afterwards completes normally,
    // on the SAME driver/session (no need to reconnect or start a new session).
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['still works'] });
    typeAndSend('Are you still there?');
    await waitFor(() => container.textContent?.includes('still works') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);
    expect(latestDriver!.error).toBeNull();
  });

  it('announces the cut turn on the session-events side channel, so another window on the same session finds out', async () => {
    // A second observer of the same session — another window — cancelled
    // nothing, so it has no AbortController to learn from. The ONLY way it can
    // find out the turn ended is llmd announcing it. Before llmd emitted a
    // terminal frame, such an observer folded a turn that stayed active forever
    // and showed a spinner that never resolved.
    // Streams real text and then stalls, so there is a partial worth persisting
    // when we cancel. (A turn that never said anything is cancelled just as
    // correctly, but deliberately persists no assistant row — see
    // TestLlmServer_ChatStream_cancel_before_output_persists_no_assistant_row —
    // which would leave nothing here to check the ordering claim against.)
    env.fakeOpenAi.enqueue({
      kind: 'slow_text',
      deltas: ['Partway through an answer', ' that never finishes'],
      delayMs: 5_000,
    });
    const { sessionId } = await env.newSession();

    const observer = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    const watching = new AbortController();
    const seen: string[] = [];
    void (async () => {
      try {
        for await (const event of observer.subscribeSessionEvents({ signal: watching.signal })) {
          seen.push(event.type);
        }
      } catch {
        // Aborted at the end of the test; nothing to report.
      }
    })();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('Start answering, I will stop you.');
    // Cancel only once real text has streamed, so there is a genuine partial.
    await waitFor(
      () => container.textContent?.includes('Partway through an answer') === true,
      10_000
    );
    await waitFor(() => container.querySelector('button[aria-label="Stop"]') !== null, 5_000);

    act(() => {
      (container.querySelector('button[aria-label="Stop"]') as HTMLButtonElement).click();
    });
    await waitFor(() => latestDriver?.streaming === false, 10_000);

    // The abort reaches the second client as a first-class typed event.
    await waitFor(() => seen.includes(ConversationEventType.ConversationAborted), 10_000);

    // ...and it arrives only after the salvaged partial is durable, so an
    // observer that reacts to the abort by re-reading history sees the finished
    // transcript rather than one still missing the interrupted reply.
    const persisted = await observer.getHistory();
    const lastAssistant = [...persisted].reverse().find((m) => m.role === 'assistant');
    expect(lastAssistant).toBeDefined();
    expect(lastAssistant!.content).toContain('Partway through an answer');
    expect(lastAssistant!.content).not.toContain('never finishes');
    expect((lastAssistant as { finishReason?: string }).finishReason).toBe('cancelled');

    watching.abort();
  });
});
