/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Interrupting / injecting a message mid-stream: sending a NEW message while
 * an assistant turn is still actively streaming (no explicit Stop click)
 * aborts the in-flight turn and immediately starts the new one — real
 * llmd streams, real abort, real second turn. Exercises the driver's
 * generation-counter guard (see useLlmdChatDriver's module doc): the
 * interrupted turn's async cleanup must not clobber the new turn's state
 * once it (asynchronously) unwinds after being superseded.
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

/**
 * Submits via Enter in the textarea instead of clicking the Send button —
 * needed to interrupt an in-flight turn: the Composer swaps its Send button
 * for a Stop button while `streaming`, but submitting from the textarea
 * (Enter, no Shift) is not gated on streaming, exactly like a real user
 * typing a correction and hitting Enter mid-reply.
 */
function typeAndSendViaEnter(text: string): void {
  const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
  expect(textarea).not.toBeNull();
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(textarea, text);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
  act(() => {
    textarea.dispatchEvent(
      new (window as any).KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    );
  });
}

let latestDriver: LlmdChatDriver | undefined;
let capturedClient: ReturnType<typeof createLlmdClient> | undefined;

function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
  capturedClient = client;
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

describe('ChatPanel + useLlmdChatDriver: interrupting mid-stream', () => {
  it('sending a new message while streaming aborts the old turn and starts a fresh one', async () => {
    latestDriver = undefined;

    // Turn 1: streams some real partial text, then would keep going — we
    // interrupt before it finishes. Turn 2: a normal fast reply.
    env.fakeOpenAi.enqueue(
      { kind: 'slow_text', deltas: ['This is the start of a long ', 'answer that never finishes...'], delayMs: 250 },
      { kind: 'text', deltas: ['Second turn reply.'] }
    );

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('Tell me something long.');

    // Wait until SOME of turn 1's text has actually streamed in — proves
    // we're genuinely interrupting a live stream, not racing an instant one.
    await waitFor(
      () => container.textContent?.includes('This is the start of a long') === true,
      10_000
    );
    expect((latestDriver as LlmdChatDriver | undefined)?.streaming).toBe(true);
    const firstAssistantId = latestDriver!.streamingMessageId;
    expect(firstAssistantId).toBeDefined();

    // Interrupt: send a new message WITHOUT clicking Stop first. (Submit via
    // Enter, not the Send button — the Composer shows a Stop button, not a
    // Send button, while streaming; see typeAndSendViaEnter's doc.)
    typeAndSendViaEnter('Actually, never mind — say something else.');

    // The new turn's reply arrives.
    await waitFor(() => container.textContent?.includes('Second turn reply.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);
    expect(latestDriver!.error).toBeNull();

    // Final transcript: [interrupted user, interrupted partial assistant,
    // new user, new assistant] — the old turn's partial output was
    // preserved and marked interrupted, not silently discarded or left as
    // an infinite spinner.
    const messages = latestDriver!.messages;
    expect(messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant']);

    const interruptedAssistant = messages[1];
    expect(interruptedAssistant.id).toBe(firstAssistantId);
    expect(interruptedAssistant.content).toContain('This is the start of a long');
    // It never got to stream the rest of turn 1's scripted reply.
    expect(interruptedAssistant.content).not.toContain('never finishes');
    expect((interruptedAssistant.metadata as { interrupted?: boolean } | undefined)?.interrupted).toBe(
      true
    );

    const finalAssistant = messages[3];
    expect(finalAssistant.content).toBe('Second turn reply.');
    expect((finalAssistant.metadata as { interrupted?: boolean } | undefined)?.interrupted).not.toBe(true);

    // The DOM reflects the same: both bubbles visible, no duplicate/ghost
    // "streaming" placeholder left over from the interrupted turn.
    expect(container.textContent).toContain('This is the start of a long');
    expect(container.querySelectorAll('.chat-message--assistant').length).toBe(2);

    // And it is VISIBLE that the first reply was cut short. Driver metadata
    // nobody renders is not a working feature: without this badge the truncated
    // text reads as the model's finished answer.
    const badges = container.querySelectorAll('.chat-message__interrupted-badge');
    expect(badges.length).toBe(1);
    const interruptedBubble = container.querySelectorAll('.chat-message--assistant')[0];
    expect(interruptedBubble.querySelector('.chat-message__interrupted-badge')).not.toBeNull();

    // PERSISTED order, not just the in-memory one: llmd is the source of truth,
    // and what survives a reload is what actually matters. The interrupted
    // partial has to sit BETWEEN the message that prompted it and the message
    // that interrupted it — the server commits it on a detached context after
    // the abort, so this is where an unserialized second turn would show up as
    // a partial ordered after the message that superseded it, or missing.
    const persisted = await capturedClient!.getHistory();
    expect(persisted.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant']);
    expect(persisted[0].content).toContain('Tell me something long');
    expect(persisted[1].content).toContain('This is the start of a long');
    expect(persisted[1].content).not.toContain('never finishes');
    expect(persisted[2].content).toContain('never mind');
    expect(persisted[3].content).toBe('Second turn reply.');

    // The truncation is durable: without a finish_reason on the row, a reloading
    // client cannot tell this partial from an answer the model completed.
    expect((persisted[1] as { finishReason?: string }).finishReason).toBe('cancelled');
    expect((persisted[3] as { finishReason?: string }).finishReason).not.toBe('cancelled');

    // And a fresh driver hydrating from that history renders it as interrupted,
    // exactly as the live session did.
    const rehydrated = await capturedClient!.getHistory();
    act(() => {
      latestDriver!.hydrate(rehydrated);
    });
    await waitFor(
      () => latestDriver!.messages.length === 4 && latestDriver!.messages[1].role === 'assistant'
    );
    const hydratedPartial = latestDriver!.messages[1];
    expect((hydratedPartial.metadata as { interrupted?: boolean } | undefined)?.interrupted).toBe(
      true
    );
    expect(
      (latestDriver!.messages[3].metadata as { interrupted?: boolean } | undefined)?.interrupted
    ).not.toBe(true);

    // The rehydrated transcript renders the marker too — the whole point of the
    // persisted finish_reason is that a reloaded window looks like the live one.
    expect(container.querySelectorAll('.chat-message__interrupted-badge').length).toBe(1);
  });
});
