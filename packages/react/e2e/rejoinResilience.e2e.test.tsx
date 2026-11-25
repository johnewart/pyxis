/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * A rejoined turn must survive a misbehaving session-events connection.
 *
 * The rebuilt reply of a turn you came back to arrives over the session-events
 * channel (/ws/session-events on the browser binding). In real deployments that
 * socket can drop — a dev-server proxy closing it, a network blip — and the
 * terminal "turn finished" frame can be lost in the gap. The driver must:
 *
 *   - reconnect after the socket is closed cleanly, or errors, and rebuild the
 *     bubble from the turn's start without duplicating text;
 *   - still end the rejoined turn (swap in the persisted reply, stop spinning)
 *     even if no terminal frame ever arrives.
 *
 * Each test wraps the REAL WebSocket feed in a source that misbehaves on cue.
 */

import React, { useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType, type ConversationEvent } from '@pyxis-llm/client';
import { createLlmdWebClient, createWebSocketSessionEvents } from '@pyxis-llm/client/web';
import { ChatPanel } from '@pyxis-llm/react';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/browser';

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
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: condition never became true');
    await new Promise((resolve) => setTimeout(resolve, 20));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

function typeAndSend(text: string): void {
  const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(textarea, text);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
  act(() => {
    (container.querySelector('button[aria-label="Send"]') as HTMLButtonElement).click();
  });
}

type Source = ReturnType<typeof createWebSocketSessionEvents>;

type Misbehavior = {
  /** Break the first `breakConnections` connections after this many events. */
  breakAfter?: number;
  breakConnections?: number;
  mode?: 'close' | 'error';
  /** Drop events matching this, on every connection. */
  drop?: (ev: ConversationEvent) => boolean;
};

/** Wraps the real WebSocket feed so it misbehaves as described; counts connections. */
function misbehavingSource(sessionId: string, how: Misbehavior): { source: Source; connections: () => number } {
  const real = createWebSocketSessionEvents({
    baseUrl: `http://${env.handle.connectAddress}`,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    token: env.connectToken,
  });
  let connections = 0;
  const source: Source = async function* (args) {
    const n = ++connections;
    let delivered = 0;
    for await (const ev of real({ ...args, sessionId })) {
      if (how.drop?.(ev)) continue;
      yield ev;
      delivered++;
      if (how.breakAfter !== undefined && n <= (how.breakConnections ?? 1) && delivered >= how.breakAfter) {
        if (how.mode === 'error') throw new Error('injected session-events failure');
        return; // a clean close, as a proxy dropping the socket looks
      }
    }
  };
  return { source, connections: () => connections };
}

let latestDriver: LlmdChatDriver | undefined;

function Harness({ sessionId, source }: { sessionId: string; source?: Source }) {
  const client = useMemo(
    () =>
      createLlmdWebClient({
        baseUrl: `http://${env.handle.connectAddress}`,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
        token: env.connectToken,
        ...(source ? { sessionEventsSource: source } : {}),
      }),
    [sessionId, source]
  );
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

const shown = (text: string) => container.textContent?.includes(text) === true;

/** Start a slow turn on a healthy panel, leave mid-reply, and come back on a misbehaving one. */
async function leaveAndReturn(how: Misbehavior, deltas: string[], delayMs: number) {
  env.fakeOpenAi.enqueue({ kind: 'slow_text', deltas, delayMs });
  const { sessionId } = await env.newSession();
  act(() => {
    root.render(<Harness sessionId={sessionId} />);
  });
  await new Promise((r) => setTimeout(r, 200));
  typeAndSend('Tell me slowly.');
  await waitFor(() => shown(deltas[0]));
  act(() => {
    root.render(<div>away</div>);
  });
  const flaky = misbehavingSource(sessionId, how);
  latestDriver = undefined;
  act(() => {
    root.render(<Harness sessionId={sessionId} source={flaky.source} />);
  });
  await waitFor(() => latestDriver?.streaming === true);
  return flaky;
}

describe('rejoined turn vs. a misbehaving session-events connection (web)', () => {
  for (const mode of ['close', 'error'] as const) {
    it(`reconnects after the socket ${mode === 'close' ? 'is closed cleanly' : 'errors'} and completes the reply without duplicated text`, async () => {
      const deltas = [`One (${mode}).`, ' Two.', ' Three.'];
      // Break right after the first replayed text delta (committed, started, delta).
      const flaky = await leaveAndReturn({ breakAfter: 3, breakConnections: 1, mode }, deltas, 900);

      await waitFor(() => shown(' Three.'), 20_000);
      await waitFor(() => latestDriver?.streaming === false, 20_000);
      expect(flaky.connections()).toBeGreaterThanOrEqual(2);
      const msgs = latestDriver!.messages;
      const last = msgs[msgs.length - 1];
      expect(last.role).toBe('assistant');
      // Exactly once: a reconnect replays from the turn's start and must rebuild, not append.
      expect(last.content).toBe(deltas.join(''));
      expect(latestDriver!.sessionEventsError).toBeUndefined();
    });
  }

  it('a rejoined turn still ends when its "finished" frame never arrives', async () => {
    const deltas = ['Never told it finished.', ' Still done though.'];
    await leaveAndReturn(
      {
        drop: (ev) =>
          ev.type === ConversationEventType.AssistantTurnFinished ||
          ev.type === ConversationEventType.AssistantTurnFailed ||
          ev.type === ConversationEventType.ConversationAborted,
      },
      deltas,
      700
    );

    // No terminal frame will come; the driver's safety net must notice the turn
    // is done and settle on the persisted reply.
    await waitFor(() => latestDriver?.streaming === false, 20_000);
    const last = latestDriver!.messages[latestDriver!.messages.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.content).toBe(deltas.join(''));
    expect((last.metadata as { streamingPlaceholder?: boolean } | undefined)?.streamingPlaceholder).not.toBe(true);
  });
});
