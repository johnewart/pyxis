/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The composer's draft and queue, through the real driver + rendered UI against
 * a real llmd:
 *
 *   - typing persists a draft, and a fresh mount of the same session gets it
 *     back (the reload/crash case the feature exists for);
 *   - sending clears it, so a draft never reappears next to the message it
 *     became;
 *   - queued messages render, reorder and remove through QueueStrip, and pause
 *     round-trips.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { UserMessage } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { ChatPanel } from '@pyxis-llm/react';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/node';

import { startE2eEnv, type E2eEnv } from '../../client/e2e/harness.js';

let env: E2eEnv;
let container: HTMLDivElement;
let root: Root;
let latestDriver: LlmdChatDriver | undefined;

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
  latestDriver = undefined;
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
});

async function waitFor(check: () => boolean, timeoutMs = 10_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: condition never became true');
    await new Promise((resolve) => setTimeout(resolve, 20));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

function textarea(): HTMLTextAreaElement {
  const el = container.querySelector('textarea') as HTMLTextAreaElement;
  expect(el).not.toBeNull();
  return el;
}

/** Types into the real textarea, which drives the controlled draft value. */
function type(text: string): void {
  const el = textarea();
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(el, text);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

function clickSend(): void {
  const btn = container.querySelector('button[aria-label="Send"]') as HTMLButtonElement;
  expect(btn).not.toBeNull();
  act(() => {
    btn.click();
  });
}

/** Submits via the keyboard. */
function pressEnter(mods: { ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean } = {}): void {
  const el = textarea();
  act(() => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, ...mods }));
  });
}

/** A turn that streams for ~10s: long enough to act on mid-stream, and it really streams. */
const LONG_TURN = {
  kind: 'slow_text' as const,
  deltas: ['partial ', ...Array.from({ length: 40 }, () => 'more ')],
  delayMs: 250,
};

/**
 * Waits until LONG_TURN's text is actually arriving. `streaming` flips true
 * before llmd has even called the model, and interrupting that early leaves
 * the scripted fake reply to be consumed by the NEXT turn instead.
 */
async function waitForTurnOneStreaming(): Promise<void> {
  await waitFor(() => latestDriver?.streaming === true);
  await waitFor(() => container.textContent?.includes('partial') === true);
}

function queueContents(): string[] {
  return Array.from(container.querySelectorAll('.llmd-queue-strip__content')).map(
    (el) => el.textContent ?? ''
  );
}

function userMessages(): string[] {
  return latestDriver!.messages.filter((m) => m.role === 'user').map((m) => m.content);
}

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
          composerDraft={driver.draft}
          onComposerValueChange={driver.setDraft}
          queue={driver.queue}
          queuePaused={driver.queuePaused}
          onRemoveQueued={driver.removeQueued}
          onReorderQueue={driver.reorderQueue}
          onSetQueuePaused={driver.setQueuePaused}
          onInterruptWithQueued={driver.interruptWithQueued}
        />
      </div>
    </MantineProvider>
  );
}

function mount(sessionId: string): void {
  act(() => {
    root.render(<Harness sessionId={sessionId} />);
  });
}

describe('composer draft persistence', () => {
  it('restores an unsent draft on a fresh mount of the same session', async () => {
    const { sessionId } = await env.newSession();
    mount(sessionId);

    type('a thought I did not finish');
    // Debounced save — wait for llmd to actually have it, rather than trusting
    // local state that would survive a remount anyway and prove nothing.
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    let saved = '';
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline && saved !== 'a thought I did not finish') {
      saved = await probe.getDraft();
      if (saved !== 'a thought I did not finish') {
        await new Promise((r) => setTimeout(r, 25));
      }
    }
    expect(saved).toBe('a thought I did not finish');

    // Reload: unmount, mount a brand new driver for the same session.
    act(() => {
      root.unmount();
    });
    container.remove();
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
    latestDriver = undefined;
    mount(sessionId);

    await waitFor(() => latestDriver?.draft.get() === 'a thought I did not finish');
    await waitFor(() => textarea().value === 'a thought I did not finish');
  });

  it('clears the draft once the message is actually sent', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['ok'] });
    const { sessionId } = await env.newSession();
    mount(sessionId);

    type('this one gets sent');
    clickSend();
    await waitFor(() => container.textContent?.includes('ok') === true);
    await waitFor(() => latestDriver?.streaming === false);

    expect(latestDriver!.draft.get()).toBe('');
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    // A draft that outlived its own send would reappear in the composer next to
    // the message it turned into.
    let lingering = 'x';
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline && lingering !== '') {
      lingering = await probe.getDraft();
      if (lingering !== '') await new Promise((r) => setTimeout(r, 25));
    }
    expect(lingering).toBe('');
  });
});

describe('queue UI', () => {
  it('renders queued messages, reorders and removes them, and toggles pause', async () => {
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);

    await act(async () => {
      await latestDriver!.enqueue('first queued');
      await latestDriver!.enqueue('second queued');
    });

    await waitFor(() => container.querySelectorAll('.llmd-queue-strip__item').length === 2);
    const contentsOf = () =>
      Array.from(container.querySelectorAll('.llmd-queue-strip__content')).map(
        (el) => el.textContent ?? ''
      );
    expect(contentsOf()).toEqual(['first queued', 'second queued']);

    // Promote the second entry via the real button.
    const moveUp = container.querySelectorAll(
      'button[aria-label^="Move up"]'
    )[1] as HTMLButtonElement;
    expect(moveUp).not.toBeNull();
    await act(async () => {
      moveUp.click();
    });
    await waitFor(() => contentsOf()[0] === 'second queued');
    expect(contentsOf()).toEqual(['second queued', 'first queued']);

    // Remove the head.
    const remove = container.querySelector(
      'button[aria-label^="Remove from queue"]'
    ) as HTMLButtonElement;
    await act(async () => {
      remove.click();
    });
    await waitFor(() => container.querySelectorAll('.llmd-queue-strip__item').length === 1);
    expect(contentsOf()).toEqual(['first queued']);

    // Pause, and confirm llmd agrees rather than just the local button state.
    const pause = container.querySelector(
      'button[aria-label="Pause queue"]'
    ) as HTMLButtonElement;
    await act(async () => {
      pause.click();
    });
    await waitFor(() => latestDriver?.queuePaused === true);
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    expect((await probe.listQueue()).paused).toBe(true);
    await waitFor(() => container.querySelector('button[aria-label="Resume queue"]') !== null);
  });

  it('shows nothing at all when the queue is empty and running', async () => {
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);
    // An unused queue should take up no space in the composer.
    expect(container.querySelector('.llmd-queue-strip')).toBeNull();
  });

  // With the queue paused the keys invert: Enter sends now, ⌥/Ctrl+Enter queues.
  // (Paused here also keeps the queued entry put: there is no scripted reply
  // for it to drain into.) The unpaused Enter-queues case is covered at unit
  // speed in queueSend.test.tsx / sendMode.test.tsx.
  it('paused queue: ⌥+Enter while a turn is running queues the message instead of interrupting', async () => {
    env.fakeOpenAi.enqueue({ kind: 'hang' });
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);
    await act(async () => {
      await latestDriver!.setQueuePaused(true);
    });

    type('start a long turn');
    clickSend();
    await waitFor(() => latestDriver?.streaming === true);

    // The empty input tells the user what both keys will do.
    expect(textarea().placeholder).toBe('Enter to interrupt · ⌥/Ctrl+Enter to queue');

    type('a follow-up for later');
    // The button says so too, and flips while the modifier is held.
    const sendBtn = () => container.querySelector('.llmd-composer__send') as HTMLButtonElement;
    expect(sendBtn().textContent).toBe('Interrupt');
    act(() => {
      textarea().dispatchEvent(new KeyboardEvent('keydown', { key: 'Alt', altKey: true, bubbles: true }));
    });
    expect(sendBtn().textContent).toBe('Queue');
    pressEnter({ altKey: true });

    await waitFor(() => queueContents().includes('a follow-up for later'));
    // Not interrupted: same turn still running, and the follow-up is not in the transcript.
    expect(latestDriver!.streaming).toBe(true);
    expect(userMessages()).toEqual(['start a long turn']);
    expect(container.querySelector('.chat-message__interrupted-badge')).toBeNull();
    // It left the composer like a sent message does.
    expect(textarea().value).toBe('');

    act(() => latestDriver!.stop());
    await waitFor(() => latestDriver?.streaming === false);
  });

  it('Ctrl/Cmd+Enter while a turn is running interrupts instead of queueing', async () => {
    env.fakeOpenAi.enqueue(LONG_TURN, { kind: 'text', deltas: ['Interrupt reply.'] });
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);

    type('start a long turn');
    clickSend();
    await waitForTurnOneStreaming();

    type('stop that, do this');
    pressEnter({ ctrlKey: true });

    await waitFor(() => container.textContent?.includes('Interrupt reply.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);
    expect(userMessages()).toEqual(['start a long turn', 'stop that, do this']);
    expect(latestDriver!.queue).toEqual([]);
    expect(container.querySelectorAll('.chat-message__interrupted-badge').length).toBe(1);
  });

  it('interrupting with a queued entry (not the head) sends it now and removes it from the queue', async () => {
    env.fakeOpenAi.enqueue(LONG_TURN, { kind: 'text', deltas: ['Pulled reply.'] });
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);
    // Paused so the remaining entry stays queued after the interrupt turn ends.
    await act(async () => {
      await latestDriver!.setQueuePaused(true);
    });

    type('start a long turn');
    clickSend();
    await waitForTurnOneStreaming();

    await act(async () => {
      await latestDriver!.enqueue('first queued');
      await latestDriver!.enqueue('second queued');
    });
    await waitFor(() => queueContents().length === 2);

    // Pick the SECOND entry, via the real button.
    const interruptBtn = container.querySelectorAll(
      'button[aria-label^="Interrupt with"]'
    )[1] as HTMLButtonElement;
    expect(interruptBtn).toBeDefined();
    await act(async () => {
      interruptBtn.click();
    });

    await waitFor(() => container.textContent?.includes('Pulled reply.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false);
    expect(userMessages()).toEqual(['start a long turn', 'second queued']);
    expect(container.querySelectorAll('.chat-message__interrupted-badge').length).toBe(1);

    // Gone from the queue, both locally and on llmd; the other entry is untouched.
    await waitFor(() => queueContents().length === 1);
    expect(queueContents()).toEqual(['first queued']);
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    expect((await probe.listQueue()).queue.map((e) => e.content)).toEqual(['first queued']);
  });

  it('reflects a queue edit made by another window', async () => {
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);

    // A second client enqueues — a different window, or a host process.
    const other = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    await other.enqueueMessage('added elsewhere');

    // This window learns about it from the queue-changed event, without polling.
    await waitFor(() => latestDriver!.queue.some((q) => q.content === 'added elsewhere'));
    await waitFor(() => container.textContent?.includes('added elsewhere') === true);
  });

  // How a queued message drained by llmd (or a message sent from another
  // window) reaches an open panel: a turn this driver did not send. It must
  // render live — the user message and the streaming reply — not only after a
  // reload.
  it('shows a turn it did not send, live, as if it had sent it', async () => {
    const { sessionId } = await env.newSession();
    mount(sessionId);
    await waitFor(() => latestDriver !== undefined);
    // Let the session-events subscription come up before the turn starts.
    await new Promise((resolve) => setTimeout(resolve, 300));

    env.fakeOpenAi.enqueue({
      kind: 'slow_text',
      deltas: ['observed ', 'reply ', 'arrives'],
      delayMs: 150,
    });
    const other = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    const done = (async () => {
      for await (const item of other.chatStreamEvents([new UserMessage('sent elsewhere', 'msg-elsewhere')])) {
        void item;
      }
    })();

    // Live: the user message and a streaming reply show up mid-turn.
    await waitFor(() => userMessages().includes('sent elsewhere'));
    await waitFor(() => latestDriver!.streaming === true);
    await waitFor(() => container.textContent?.includes('observed') === true);

    await done;
    // Settles into the persisted transcript: one user message, one full reply,
    // nothing duplicated, no spinner left behind.
    await waitFor(() => latestDriver!.streaming === false);
    await waitFor(() =>
      latestDriver!.messages.some((m) => m.role === 'assistant' && m.content.includes('observed reply arrives'))
    );
    expect(userMessages().filter((c) => c === 'sent elsewhere')).toHaveLength(1);
    expect(latestDriver!.messages.filter((m) => m.role === 'assistant')).toHaveLength(1);
  });
});
