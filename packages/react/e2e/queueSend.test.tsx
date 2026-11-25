/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed tests (fake client, no llmd binary) for how useLlmdChatDriver
 * decides between sending now and queueing.
 *
 * Regression: the driver queued only while IT was streaming. In the gap after
 * a turn ended and before llmd's drainer started the next queued message, a
 * plain Enter therefore sent immediately, jumping the queue (the earlier
 * message stayed listed as queued). Now llmd decides: a send that is not
 * obviously queued goes out with queueIfBusy, and llmd either runs it or
 * queues it.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ConversationEventType, type ConversationEvent, type Message } from '@pyxis-llm/client';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/node';

let container: HTMLDivElement;
let root: Root;

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

async function waitFor(check: () => boolean, timeoutMs = 2_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: condition never became true');
    await new Promise((resolve) => setTimeout(resolve, 10));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

type Call = { text: string; queueIfBusy: boolean };

/**
 * A session whose llmd answers queueIfBusy sends per `busy`: queued (one
 * TurnQueued event, then the stream ends) or run (commit, a reply, complete).
 */
class FakeQueueClient {
  busy = false;
  calls: Call[] = [];
  enqueued: string[] = [];
  listQueueCalls = 0;
  /** Session-events frames to deliver, pushed by tests. */
  private sideQueue: ConversationEvent[] = [];
  private sideWake: (() => void) | null = null;

  sessionKey() {
    return 'S';
  }

  pushSide(ev: ConversationEvent) {
    this.sideQueue.push(ev);
    this.sideWake?.();
  }

  async *chatStreamEvents(messages: Message[], options?: { queueIfBusy?: boolean; signal?: AbortSignal }) {
    const msg = messages[0];
    this.calls.push({ text: msg.content as string, queueIfBusy: options?.queueIfBusy === true });
    if (options?.queueIfBusy && this.busy) {
      yield {
        kind: 'event' as const,
        event: {
          type: ConversationEventType.TurnQueued,
          sessionId: 'S',
          entry: { id: 'q1', content: msg.content as string, state: 'pending', createdAtMs: Date.now() },
        } as ConversationEvent,
      };
      return;
    }
    yield {
      kind: 'event' as const,
      event: { type: ConversationEventType.UserTurnCommitted, userMessageId: msg.id, committedAtMs: Date.now() } as ConversationEvent,
    };
    yield { kind: 'event' as const, event: { type: ConversationEventType.AssistantTextDelta, delta: 'ok' } as ConversationEvent };
  }

  async *subscribeSessionEvents(options?: { signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
    while (!options?.signal?.aborted) {
      const next = this.sideQueue.shift();
      if (next) {
        yield next;
        continue;
      }
      await new Promise<void>((resolve) => {
        this.sideWake = resolve;
        options?.signal?.addEventListener('abort', () => resolve(), { once: true });
      });
    }
  }

  async getSessionState() {
    return { messages: [], turnInProgress: false, eventsSeq: 0, turnStartSeq: 0 };
  }
  async saveDraft() {}
  async getDraft() {
    return '';
  }
  async clearDraft() {}
  async enqueueMessage(content: string) {
    this.enqueued.push(content);
    return { id: 'e1', content, state: 'pending' as const, createdAtMs: Date.now() };
  }
  async listQueue() {
    this.listQueueCalls++;
    return { queue: [], paused: false };
  }
  async removeQueuedMessage() {
    return false;
  }
  async reorderQueue() {
    return [];
  }
  async setQueuePaused() {}
}

let driver: LlmdChatDriver | undefined;
function Harness({ client }: { client: FakeQueueClient }) {
  driver = useLlmdChatDriver(client);
  return null;
}

describe('useLlmdChatDriver: send now vs queue is llmd\'s call', () => {
  it('Enter while nothing streams here asks llmd (queueIfBusy), and runs when llmd is idle', async () => {
    driver = undefined;
    const c = new FakeQueueClient();
    act(() => root.render(<Harness client={c} />));
    act(() => driver!.send('hello'));
    await waitFor(() => driver!.messages.some((m) => m.content === 'ok'));
    expect(c.calls).toEqual([{ text: 'hello', queueIfBusy: true }]);
    expect(driver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
  });

  it('when llmd queues it (the idle gap before a queued turn starts), nothing is drawn and the queue is re-read', async () => {
    driver = undefined;
    const c = new FakeQueueClient();
    c.busy = true;
    act(() => root.render(<Harness client={c} />));
    await act(async () => {});
    const listsBefore = c.listQueueCalls;
    act(() => driver!.send('third'));
    await waitFor(() => c.calls.length === 1 && c.listQueueCalls > listsBefore);
    expect(c.calls[0].queueIfBusy).toBe(true);
    // No user bubble, no spinner: the message is a queue entry, not a turn.
    expect(driver!.messages).toEqual([]);
    expect(driver!.streaming).toBe(false);
    expect(driver!.error).toBeNull();
  });

  it('a queued send does not end a turn that started meanwhile (llmd draining the queue)', async () => {
    driver = undefined;
    const c = new FakeQueueClient();
    c.busy = true;
    act(() => root.render(<Harness client={c} />));
    await act(async () => {});
    // The queued turn llmd starts shows up as an observed turn.
    act(() => {
      c.pushSide({ type: ConversationEventType.UserTurnCommitted, userMessageId: 'queued_x', textPreview: 'second' } as ConversationEvent);
    });
    await waitFor(() => driver!.streaming);
    act(() => driver!.send('third'));
    await waitFor(() => c.enqueued.includes('third') || c.calls.length > 0);
    // Streaming already, so it queued directly, and the running turn is untouched.
    expect(c.enqueued).toEqual(['third']);
    expect(driver!.streaming).toBe(true);
  });

  it('ending our own turn does not end the queued turn llmd started right after it', async () => {
    // Regression: llmd starts the next queued message the moment a turn ends,
    // and the side channel can deliver that turn's commit while our own stream
    // is still closing. Our stream's cleanup then switched streaming off under
    // the queued turn, which ran with no live state (no tool strip) until the
    // chat was reopened.
    driver = undefined;
    const c = new FakeQueueClient();
    const orig = c.chatStreamEvents.bind(c);
    c.chatStreamEvents = async function* (messages: Message[], options?: { queueIfBusy?: boolean }) {
      yield* orig(messages, options);
      c.pushSide({ type: ConversationEventType.UserTurnCommitted, userMessageId: 'queued_next', textPreview: 'next' } as ConversationEvent);
      await new Promise((r) => setTimeout(r, 30)); // our stream is still closing
    } as typeof c.chatStreamEvents;
    act(() => root.render(<Harness client={c} />));
    await act(async () => {});
    act(() => driver!.send('first'));
    await waitFor(() => driver!.messages.some((m) => m.content === 'next'));
    // Let our own stream finish closing.
    await new Promise((r) => setTimeout(r, 80));
    await act(async () => {});
    expect(driver!.streaming).toBe(true);
  });

  it('interrupt (⌘/Ctrl+Enter) always sends now, without asking llmd', async () => {
    driver = undefined;
    const c = new FakeQueueClient();
    c.busy = true;
    act(() => root.render(<Harness client={c} />));
    await act(async () => {});
    act(() => driver!.send('now', 'interrupt'));
    await waitFor(() => driver!.messages.some((m) => m.content === 'ok'));
    expect(c.calls).toEqual([{ text: 'now', queueIfBusy: false }]);
  });

  it('a run-now message whose commit reaches the side channel first is not shown twice', async () => {
    driver = undefined;
    const c = new FakeQueueClient();
    let sentId = '';
    const orig = c.chatStreamEvents.bind(c);
    c.chatStreamEvents = async function* (messages: Message[], options?: { queueIfBusy?: boolean }) {
      sentId = messages[0].id;
      // llmd's event log fans the commit out to the side channel before our
      // own stream delivers anything.
      c.pushSide({ type: ConversationEventType.UserTurnCommitted, userMessageId: sentId, textPreview: 'hello' } as ConversationEvent);
      await new Promise((r) => setTimeout(r, 30));
      yield* orig(messages, options);
    } as typeof c.chatStreamEvents;
    act(() => root.render(<Harness client={c} />));
    await act(async () => {});
    act(() => driver!.send('hello'));
    await waitFor(() => driver!.messages.some((m) => m.content === 'ok'));
    await new Promise((r) => setTimeout(r, 50));
    await act(async () => {});
    expect(driver!.messages.filter((m) => m.role === 'user')).toHaveLength(1);
    expect(driver!.messages.filter((m) => m.role === 'assistant')).toHaveLength(1);
  });
});
