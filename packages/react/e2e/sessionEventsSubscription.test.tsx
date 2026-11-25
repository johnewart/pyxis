/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed test (no real llmd binary — see sessionTools.e2e.test.tsx /
 * toolInvocationBrowser.e2e.test.tsx for the real-binary versions) for
 * useLlmdChatDriver's automatic side-channel subscription: when `client`
 * also implements `subscribeSessionEvents` (SessionEventsSubscribableClient
 * — a real @pyxis-llm/client does, on either transport), the hook opens
 * it itself, for the component's lifetime, and routes tool events to
 * `onToolEvent` / session status into `sessionStatus` — without the host
 * ever calling send() or subscribeSessionEvents() itself.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConversationEventType, type ConversationEvent, type Message } from '@pyxis-llm/client';
import { useLlmdChatDriver, type LlmdChatDriver, type ToolConversationEvent } from '@pyxis-llm/react/node';

let container: HTMLDivElement;
let root: Root;
let unmounted = false;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  unmounted = false;
});

afterEach(() => {
  if (!unmounted) {
    act(() => {
      root.unmount();
    });
  }
  container.remove();
});

async function waitFor(check: () => boolean, timeoutMs = 2_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error('waitFor: condition never became true');
    }
    await new Promise((resolve) => setTimeout(resolve, 10));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

/** A client whose subscribeSessionEvents yields two events then hangs open — like a real live connection until aborted. */
class FakeSubscribableClient {
  subscribeCalls = 0;

  async *chatStreamEvents(
    _messages: Message[],
    _options?: { signal?: AbortSignal }
  ): AsyncGenerator<
    | { kind: 'event'; event: ConversationEvent }
    | { kind: 'complete'; message: never }
  > {
    // Not exercised by this test.
  }

  async *subscribeSessionEvents(options?: {
    afterSeq?: number;
    signal?: AbortSignal;
  }): AsyncGenerator<ConversationEvent> {
    this.subscribeCalls++;
    yield {
      type: ConversationEventType.SessionStatus,
      sessionId: 'sess-1',
      headline: 'Looking for a fish...',
    };
    yield {
      type: ConversationEventType.ToolExecutionStarted,
      turnId: 'turn-1',
      toolCallId: 'call-1',
      name: 'read_code',
      success: undefined as never,
    } as unknown as ConversationEvent;
    // Stay "open" until aborted, like a real subscription would.
    await new Promise<void>((resolve) => {
      options?.signal?.addEventListener('abort', () => resolve(), { once: true });
    });
  }
}

let latestDriver: LlmdChatDriver | undefined;
let toolEvents: ToolConversationEvent[];

function Harness({ client }: { client: FakeSubscribableClient }) {
  const driver = useLlmdChatDriver(client, undefined, (ev) => {
    toolEvents.push(ev);
  });
  latestDriver = driver;
  return null;
}

describe('useLlmdChatDriver: automatic session-events side channel', () => {
  it('subscribes on its own and routes tool events / session status without send() ever being called', async () => {
    latestDriver = undefined;
    toolEvents = [];
    const client = new FakeSubscribableClient();

    act(() => {
      root.render(<Harness client={client} />);
    });

    await waitFor(() => client.subscribeCalls > 0);
    await waitFor(() => latestDriver?.sessionStatus !== undefined);
    await waitFor(() => toolEvents.length > 0);

    expect(latestDriver!.sessionStatus).toBe('Looking for a fish...');
    expect(toolEvents).toHaveLength(1);
    expect(toolEvents[0]?.type).toBe(ConversationEventType.ToolExecutionStarted);
    expect((toolEvents[0] as { toolCallId: string }).toolCallId).toBe('call-1');

    // Only ever subscribed once for this client identity, never send()-driven.
    expect(client.subscribeCalls).toBe(1);
  });

  it('tears down the subscription on unmount (aborts the signal)', async () => {
    latestDriver = undefined;
    toolEvents = [];
    let abortedSignal: AbortSignal | undefined;
    const client = new FakeSubscribableClient();
    const originalSubscribe = client.subscribeSessionEvents.bind(client);
    client.subscribeSessionEvents = (options) => {
      abortedSignal = options?.signal;
      return originalSubscribe(options);
    };

    act(() => {
      root.render(<Harness client={client} />);
    });
    await waitFor(() => client.subscribeCalls > 0);
    expect(abortedSignal?.aborted).toBe(false);

    act(() => {
      root.unmount();
    });
    unmounted = true;

    expect(abortedSignal?.aborted).toBe(true);
  });

  it('surfaces a failed subscription via sessionEventsError instead of failing silently', async () => {
    latestDriver = undefined;
    toolEvents = [];
    const client = {
      subscribeCalls: 0,
      async *chatStreamEvents() {
        // Not exercised by this test.
      },
      async *subscribeSessionEvents(): AsyncGenerator<ConversationEvent> {
        this.subscribeCalls++;
        throw new Error('connection refused');
        // eslint-disable-next-line no-unreachable
        yield undefined as never;
      },
    };

    act(() => {
      root.render(<Harness client={client as unknown as FakeSubscribableClient} />);
    });

    await waitFor(() => latestDriver?.sessionEventsError !== undefined);
    expect(latestDriver!.sessionEventsError?.message).toBe('connection refused');
    // Must never leak into the turn-scoped `error` field.
    expect(latestDriver!.error).toBeNull();
  });

  it('is self-diagnosing when client lacks subscribeSessionEvents: logs instead of failing silently', async () => {
    latestDriver = undefined;
    toolEvents = [];
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});
    // A client satisfying only ChatStreamCapableClient (e.g. a hand-rolled
    // IPC/REST shim) — the exact shape this diagnostic exists for.
    const client = {
      async *chatStreamEvents() {
        // Not exercised by this test.
      },
    };

    act(() => {
      root.render(<Harness client={client as unknown as FakeSubscribableClient} />);
    });
    await waitFor(() => latestDriver !== undefined);
    // Give the effect a tick to run.
    await act(async () => {});

    expect(debugSpy).toHaveBeenCalledWith(
      expect.stringContaining('does not implement subscribeSessionEvents'),
      'undefined',
      client
    );
    debugSpy.mockRestore();
  });
});

describe('useLlmdChatDriver: turn-scoped status and onTurnStart', () => {
  it('fires onTurnStart once per user turn and clears sessionStatus on llmd\'s empty headline', async () => {
    let releaseNextTurn!: () => void;
    const nextTurn = new Promise<void>((resolve) => {
      releaseNextTurn = resolve;
    });
    const client = {
      async *chatStreamEvents() {
        // Not exercised by this test.
      },
      async *subscribeSessionEvents(options?: { signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
        yield { type: ConversationEventType.UserTurnCommitted, userMessageId: 'u-1' };
        yield { type: ConversationEventType.AssistantTurnStarted, turnId: 'round-1' };
        yield { type: ConversationEventType.SessionStatus, sessionId: 'sess-1', headline: 'Grepping for falafel' };
        // Next model round of the SAME user turn: must not reset.
        yield { type: ConversationEventType.AssistantTurnStarted, turnId: 'round-2' };
        // Same user turn delivered again (both channels carry it): must not reset.
        yield { type: ConversationEventType.UserTurnCommitted, userMessageId: 'u-1' };
        await nextTurn;
        // llmd ends turn u-1 and starts u-2: clears arrive as empty headlines.
        yield { type: ConversationEventType.SessionStatus, sessionId: 'sess-1', headline: '' };
        yield { type: ConversationEventType.UserTurnCommitted, userMessageId: 'u-2' };
        await new Promise<void>((resolve) => {
          options?.signal?.addEventListener('abort', () => resolve(), { once: true });
        });
      },
    };

    const turnStarts: string[] = [];
    let driver: LlmdChatDriver | undefined;
    function TurnHarness() {
      driver = useLlmdChatDriver(client as never, undefined, undefined, (turnId) => {
        turnStarts.push(turnId);
      });
      return null;
    }

    act(() => {
      root.render(<TurnHarness />);
    });

    await waitFor(() => driver?.sessionStatus === 'Grepping for falafel');
    // Let the later model round and the duplicate commit drain through.
    await new Promise((resolve) => setTimeout(resolve, 50));
    await act(async () => {});
    expect(driver!.sessionStatus).toBe('Grepping for falafel');
    expect(turnStarts).toEqual(['u-1']);

    releaseNextTurn();
    await waitFor(() => turnStarts.length === 2);
    expect(turnStarts).toEqual(['u-1', 'u-2']);
    expect(driver!.sessionStatus).toBeUndefined();
  });

  it('seeds sessionStatus from the session on every (re)connect, so a status changed while disconnected is picked up', async () => {
    // What llmd reports for the session on each connect: the first turn's
    // headline, then (after a whole turn ran during the gap) the new one.
    const reported = ['Grepping for falafel', 'Slicing shawarma'];
    let infoCalls = 0;
    let subscribeCalls = 0;
    const client = {
      async *chatStreamEvents() {
        // Not exercised by this test.
      },
      async getSessionInfo() {
        const status = reported[Math.min(infoCalls, reported.length - 1)];
        infoCalls++;
        return { title: '', todos: [], status };
      },
      async *subscribeSessionEvents(options?: { signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
        subscribeCalls++;
        // The first connection drops at once (proxy closing an idle socket);
        // the reconnect stays up.
        if (subscribeCalls === 1) return;
        await new Promise<void>((resolve) => {
          options?.signal?.addEventListener('abort', () => resolve(), { once: true });
        });
      },
    };

    let driver: LlmdChatDriver | undefined;
    function SeedHarness() {
      driver = useLlmdChatDriver(client as never);
      return null;
    }
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    act(() => {
      root.render(<SeedHarness />);
    });

    await waitFor(() => infoCalls >= 1 && driver?.sessionStatus !== undefined);
    expect(driver!.sessionStatus).toBe('Grepping for falafel');
    await waitFor(() => driver?.sessionStatus === 'Slicing shawarma', 10_000);
    expect(subscribeCalls).toBeGreaterThanOrEqual(2);
    warn.mockRestore();
  }, 15_000);
});
