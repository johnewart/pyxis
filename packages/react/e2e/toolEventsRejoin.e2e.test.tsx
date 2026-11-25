/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Tool updates survive a disconnect. A turn keeps running on llmd after the
 * client leaves, so its tool calls keep happening; a client that comes back
 * must still see them:
 *
 *   - tool calls that ran WHILE the client was away are delivered on return
 *     (replayed from the running turn's first event-log frame), and
 *   - tool calls that run AFTER the client is back arrive live,
 *
 * each exactly once, through `onToolEvent` and driver state (sessionStatus),
 * on both transports: native gRPC (SubscribeSessionEvents RPC) and the browser
 * binding (Connect + the /ws/session-events WebSocket).
 *
 * Uses llmd's inline `set_status` session tool, which llmd executes itself,
 * so the tool genuinely runs with no client attached.
 */

import React, { useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType, type LlmdClient } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { createLlmdWebClient } from '@pyxis-llm/client/web';
import { ChatPanel } from '@pyxis-llm/react';
import { useLlmdChatDriver, type LlmdChatDriver, type ToolConversationEvent } from '@pyxis-llm/react/node';

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
  return transport === 'grpc'
    ? createLlmdClient({
        address: env.handle.grpcAddress,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
      })
    : createLlmdWebClient({
        baseUrl: `http://${env.handle.connectAddress}`,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
        token: env.connectToken,
      });
}

let latestDriver: LlmdChatDriver | undefined;
// Every status the driver held, in order. The status is turn-scoped (llmd
// clears it when the turn ends), so polling for it can miss it entirely.
let seenStatuses: (string | undefined)[] = [];

function Harness(props: { transport: Transport; sessionId: string; events: ToolConversationEvent[] }) {
  const client = useMemo(() => makeClient(props.transport, props.sessionId), [props.transport, props.sessionId]);
  const driver = useLlmdChatDriver(client, undefined, (ev) => {
    props.events.push(ev);
  });
  latestDriver = driver;
  if (seenStatuses[seenStatuses.length - 1] !== driver.sessionStatus) seenStatuses.push(driver.sessionStatus);
  return (
    <MantineProvider>
      <div style={{ height: 400 }}>
        <ChatPanel
          messages={driver.messages}
          onSend={driver.send}
          streaming={driver.streaming}
          onStop={driver.stop}
          streamingMessageId={driver.streamingMessageId}
          sessionStatus={driver.sessionStatus}
        />
      </div>
    </MantineProvider>
  );
}

/** ToolExecutionStarted events for one tool call id — what a "Running <tool> with <params>" row renders from. */
const startsFor = (events: ToolConversationEvent[], callId: string) =>
  events.filter(
    (e): e is Extract<ToolConversationEvent, { type: ConversationEventType.ToolExecutionStarted }> =>
      e.type === ConversationEventType.ToolExecutionStarted && e.toolCallId === callId
  );
const finishesFor = (events: ToolConversationEvent[], callId: string) =>
  events.filter((e) => e.type === ConversationEventType.ToolExecutionFinished && 'toolCallId' in e && e.toolCallId === callId);

/** Leave mid-turn: unmount the panel (its streams are torn down). */
function leave(): void {
  act(() => {
    root.render(<div>away</div>);
  });
}

/** Come back: mount a fresh panel on the same session. */
function comeBack(transport: Transport, sessionId: string, events: ToolConversationEvent[]): void {
  latestDriver = undefined;
  act(() => {
    root.render(<Harness transport={transport} sessionId={sessionId} events={events} />);
  });
}

describe.each<Transport>(['grpc', 'web'])('tool updates on a panel that stays connected (%s)', (transport) => {
  it('each tool event of its own turn reaches onToolEvent exactly once', async () => {
    const callId = `call-own-${transport}`;
    const params = { description: `Own turn, no duplicates (${transport})` };
    env.fakeOpenAi.enqueue(
      { kind: 'tool_call', id: callId, name: 'set_status', argumentsJson: JSON.stringify(params) },
      { kind: 'text', deltas: ['Own turn done.'] }
    );
    const { sessionId } = await env.newSession();
    const events: ToolConversationEvent[] = [];
    act(() => {
      root.render(<Harness transport={transport} sessionId={sessionId} events={events} />);
    });
    // Let the session-events side channel connect before the turn starts,
    // which is the steady state for a mounted panel.
    await new Promise((r) => setTimeout(r, 300));
    typeAndSend('Use the tool once.');
    await waitFor(() => container.textContent?.includes('Own turn done.') === true, 15_000);
    await waitFor(() => latestDriver?.streaming === false, 15_000);
    // Frames can trail the reply slightly on the side channel; give them a beat.
    await new Promise((r) => setTimeout(r, 500));
    await act(async () => {});

    // Both channels carry this turn's frames; exactly one may deliver them.
    const byKind = (type: ConversationEventType) =>
      events.filter(
        (e) =>
          e.type === type &&
          (('toolCallId' in e && e.toolCallId === callId) || ('id' in e && e.id === callId))
      ).length;
    expect(byKind(ConversationEventType.ToolExecutionStarted)).toBe(1);
    expect(byKind(ConversationEventType.ToolExecutionFinished)).toBe(1);
    // Stream-shape events, where the provider streamed the call (args deltas
    // have no unique key, so a duplicate would garble reassembled arguments).
    expect(byKind(ConversationEventType.ToolCallStreamStarted)).toBeLessThanOrEqual(1);
    expect(byKind(ConversationEventType.ToolCallStreamFinished)).toBeLessThanOrEqual(1);
    const [started] = startsFor(events, callId);
    expect(started.name).toBe('set_status');
    expect(started.arguments).toMatchObject(params);
  });
});

describe.each<Transport>(['grpc', 'web'])('tool updates after reconnecting (%s)', (transport) => {
  it('a tool call that starts after reconnecting shows up with its name and params, once', async () => {
    const callId = `call-after-${transport}`;
    const params = { description: `Indexing repo after reconnect (${transport})` };
    env.fakeOpenAi.enqueue(
      { kind: 'tool_call', id: callId, name: 'set_status', argumentsJson: JSON.stringify(params), delayMs: 2_000 },
      { kind: 'text', deltas: ['Finished.'] }
    );
    const { sessionId } = await env.newSession();
    seenStatuses = [];

    act(() => {
      root.render(<Harness transport={transport} sessionId={sessionId} events={[]} />);
    });
    const before = env.fakeOpenAi.requests.length;
    typeAndSend('Start, I will be right back.');
    await waitFor(() => env.fakeOpenAi.requests.length > before);

    // Disconnect and reconnect well before the model emits the tool call.
    leave();
    await new Promise((r) => setTimeout(r, 100));
    const events: ToolConversationEvent[] = [];
    comeBack(transport, sessionId, events);

    // "Running set_status with {description: …}" — name and params intact.
    await waitFor(() => startsFor(events, callId).length >= 1);
    const [started] = startsFor(events, callId);
    expect(started.name).toBe('set_status');
    expect(started.arguments).toMatchObject(params);

    // It completes, and nothing is delivered twice.
    await waitFor(() => finishesFor(events, callId).length >= 1);
    await waitFor(() => seenStatuses.includes(params.description));
    await waitFor(() => container.textContent?.includes('Finished.') === true, 15_000);
    expect(startsFor(events, callId)).toHaveLength(1);
    expect(finishesFor(events, callId)).toHaveLength(1);
  });

  it('after missing tool calls while disconnected, the next tool call still arrives', async () => {
    const missedId = `call-missed-${transport}`;
    const nextId = `call-next-${transport}`;
    const nextParams = { description: `Second step, seen after reconnect (${transport})` };
    env.fakeOpenAi.enqueue(
      // Runs while the client is gone — missing it is acceptable.
      {
        kind: 'tool_call',
        id: missedId,
        name: 'set_status',
        argumentsJson: JSON.stringify({ description: 'first step, while away' }),
        delayMs: 300,
      },
      // Runs after the client is back — this one must be delivered.
      { kind: 'tool_call', id: nextId, name: 'set_status', argumentsJson: JSON.stringify(nextParams), delayMs: 2_500 },
      { kind: 'text', deltas: ['All steps done.'] }
    );
    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness transport={transport} sessionId={sessionId} events={[]} />);
    });
    const before = env.fakeOpenAi.requests.length;
    typeAndSend('Do two steps.');
    await waitFor(() => env.fakeOpenAi.requests.length > before);
    leave();

    // While away: the first tool runs and the model is asked for the next step.
    await waitFor(() => env.fakeOpenAi.requests.length >= before + 2);

    const events: ToolConversationEvent[] = [];
    comeBack(transport, sessionId, events);

    await waitFor(() => startsFor(events, nextId).length >= 1);
    const [started] = startsFor(events, nextId);
    expect(started.name).toBe('set_status');
    expect(started.arguments).toMatchObject(nextParams);
    await waitFor(() => finishesFor(events, nextId).length >= 1);
    await waitFor(() => container.textContent?.includes('All steps done.') === true, 15_000);
    expect(startsFor(events, nextId)).toHaveLength(1);
    expect(finishesFor(events, nextId)).toHaveLength(1);
  });
});
