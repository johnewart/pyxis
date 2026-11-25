/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Nothing the assistant SAID is lost to a disconnect. Live tool-event
 * notifications missed while away may stay missed, but the conversation may
 * not: text streamed while the client was gone, and replies that finished
 * while it was gone, must be on screen when it comes back.
 *
 *   - Turn still running on return: the rebuilt streaming bubble already
 *     contains the text produced while away (replayed from the running turn's
 *     first event), then keeps streaming, then settles on the persisted reply.
 *   - Turn finished while away: the returning panel shows the complete reply
 *     from history, not marked interrupted, with no phantom streaming bubble.
 *
 * Run on both transports: native gRPC and the browser binding
 * (Connect + /ws/session-events).
 */

import React, { useEffect, useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import type { LlmdClient } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { createLlmdWebClient } from '@pyxis-llm/client/web';
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

async function waitFor(check: () => boolean, timeoutMs = 10_000): Promise<void> {
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

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

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

type Transport = 'grpc' | 'web';

function makeClient(transport: Transport, sessionId: string): LlmdClient {
  // gRPC: the file's shared connection (env.clientFor), not a fresh
  // createLlmdClient per mount — each of those left an idle HTTP/2 connection
  // open for 15 minutes and kept the test worker from exiting.
  return transport === 'grpc'
    ? env.clientFor(sessionId)
    : createLlmdWebClient({
        baseUrl: `http://${env.handle.connectAddress}`,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
        token: env.connectToken,
      });
}

let latestDriver: LlmdChatDriver | undefined;
/** Set once the mounted panel has hydrated from history (a real page does this on load, before anyone can type). */
let hydrated = false;

/**
 * A panel the way a host mounts one: build the client, hydrate the transcript
 * from history, render. Hydrating matters here — it is what a real page does on
 * load, and it must not wipe a rejoined turn's live bubble.
 */
function Harness({ transport, sessionId }: { transport: Transport; sessionId: string }) {
  const client = useMemo(() => makeClient(transport, sessionId), [transport, sessionId]);
  const driver = useLlmdChatDriver(client);
  latestDriver = driver;
  const { hydrate } = driver;
  useEffect(() => {
    let live = true;
    void client.getHistory().then((msgs) => {
      if (live) {
        hydrate(msgs);
        hydrated = true;
      }
    });
    return () => {
      live = false;
    };
  }, [client, hydrate]);
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

async function mount(transport: Transport, sessionId: string): Promise<void> {
  latestDriver = undefined;
  hydrated = false;
  act(() => {
    root.render(<Harness transport={transport} sessionId={sessionId} />);
  });
  await waitFor(() => hydrated);
}

function leave(): void {
  act(() => {
    root.render(<div>away</div>);
  });
}

const shown = (text: string) => container.textContent?.includes(text) === true;
const isStreaming = () => latestDriver?.streaming === true;

describe.each<Transport>(['grpc', 'web'])('messages missed while disconnected show up (%s)', (transport) => {
  it('text streamed while away is in the rejoined bubble, and the reply completes', async () => {
    const seen = `Seen before leaving (${transport}).`;
    const missed = ' Said while you were away.';
    const after = ' Said after you came back.';
    env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas: [seen, missed, after], delayMs: 1_000 });
    const { sessionId } = await env.newSession();

    await mount(transport, sessionId);
    typeAndSend('Talk to me slowly.');
    await waitFor(() => shown(seen));

    // Leave, and stay away long enough for the next chunk to be produced.
    leave();
    await sleep(1_400);

    await mount(transport, sessionId);
    // The chunk produced while nobody was watching is on screen, while the
    // turn is still running (this is the rebuilt live bubble, not history).
    await waitFor(() => shown(missed));
    expect(shown(seen)).toBe(true);
    // Mid-turn, the reloaded page shows the user's message (from history,
    // persisted at turn start) above the live partial reply — once each, even
    // though both the host's hydrate and the driver's rejoin seed it.
    expect(isStreaming()).toBe(true);
    expect(latestDriver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(latestDriver!.messages[0].content).toBe('Talk to me slowly.');

    // The rest streams in and the panel settles on the persisted reply.
    await waitFor(() => shown(after), 15_000);
    await waitFor(() => !isStreaming(), 15_000);
    const msgs = latestDriver!.messages;
    expect(msgs.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(msgs[1].content).toBe(seen + missed + after);
    expect((msgs[1].metadata as { interrupted?: boolean } | undefined)?.interrupted).not.toBe(true);
  });

  it('a reply that started and finished entirely while away is shown on return', async () => {
    const reply = `Everything here was said while you were gone (${transport}).`;
    env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas: ['…', reply], delayMs: 600 });
    const { sessionId } = await env.newSession();

    await mount(transport, sessionId);
    const before = env.fakeOpenAi.requests.length;
    typeAndSend('Answer after I leave.');
    // Leave as soon as llmd is working on it — before any text is shown.
    await waitFor(() => env.fakeOpenAi.requests.length > before);
    leave();
    expect(shown(reply)).toBe(false);

    // Stay away until the turn has certainly finished and committed.
    await sleep(2_500);

    await mount(transport, sessionId);
    await waitFor(() => shown(reply));
    await sleep(300);
    await act(async () => {});
    expect(isStreaming()).toBe(false);
    const msgs = latestDriver!.messages;
    expect(msgs.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(msgs[1].content).toBe('…' + reply);
    expect((msgs[1].metadata as { interrupted?: boolean } | undefined)?.interrupted).not.toBe(true);
    expect(
      msgs.some((m) => (m.metadata as { streamingPlaceholder?: boolean } | undefined)?.streamingPlaceholder)
    ).toBe(false);
  });

  it('a hydrate that lands mid-turn keeps the live turn, and the finished turn is not duplicated', async () => {
    const first = `First part (${transport}).`;
    const rest = ' Second part.';
    env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas: [first, rest], delayMs: 1_000 });
    const { sessionId } = await env.newSession();

    await mount(transport, sessionId);
    typeAndSend('Hydrate me mid-turn.');
    await waitFor(() => shown(first));

    // A late history load (slow network, a host refreshing its transcript):
    // history does not contain this turn yet.
    const history = await makeClient(transport, sessionId).getHistory();
    expect(history.some((m) => m.role === 'assistant')).toBe(false);
    act(() => {
      latestDriver!.hydrate(history);
    });

    // The live turn survived: user message and streaming bubble still there...
    expect(shown('Hydrate me mid-turn.')).toBe(true);
    expect(shown(first)).toBe(true);
    expect(isStreaming()).toBe(true);
    // ...and it keeps streaming into the same bubble.
    await waitFor(() => shown(rest), 15_000);
    await waitFor(() => !isStreaming(), 15_000);
    expect(latestDriver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(latestDriver!.messages[1].content).toBe(first + rest);

    // Hydrating again after it finished yields exactly one copy of the turn.
    const after = await makeClient(transport, sessionId).getHistory();
    act(() => {
      latestDriver!.hydrate(after);
    });
    expect(latestDriver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(latestDriver!.messages[1].content).toBe(first + rest);
  });

  it('a multi-step turn (tool call, then text) that ran while away shows its reply on return', async () => {
    const reply = `Done with the tool step while you were away (${transport}).`;
    env.fakeOpenAi.enqueue(
      {
        kind: 'tool_call',
        id: `call-missed-msg-${transport}`,
        name: 'set_status',
        argumentsJson: JSON.stringify({ description: 'working' }),
        delayMs: 300,
      },
      { kind: 'slow_text', deltas: [reply, ' And a bit more.'], delayMs: 1_200 }
    );
    const { sessionId } = await env.newSession();

    await mount(transport, sessionId);
    const before = env.fakeOpenAi.requests.length;
    typeAndSend('Use a tool, then answer.');
    await waitFor(() => env.fakeOpenAi.requests.length > before);
    leave();

    // Away through the tool step and the first chunk of the answer.
    await waitFor(() => env.fakeOpenAi.requests.length >= before + 2);
    await sleep(1_400);

    await mount(transport, sessionId);
    await waitFor(() => shown(reply));
    await waitFor(() => shown(' And a bit more.'), 15_000);
    await waitFor(() => !isStreaming(), 15_000);
    const last = latestDriver!.messages[latestDriver!.messages.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.content).toContain(reply);
    expect(last.content).toContain(' And a bit more.');
  });
});
