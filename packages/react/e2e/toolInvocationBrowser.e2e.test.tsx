/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Browser-side (Connect protocol) twin of toolInvocation.e2e.test.tsx: drives
 * a real tool-execution round trip through `ChatPanel` + `useLlmdChatDriver`,
 * over llmd's dedicated Connect/grpc-web listener (see
 * chatPanelBrowser.e2e.test.tsx's module doc for why this is a SEPARATE
 * listener from native gRPC), and asserts `onToolEvent` fires the tool
 * execution lifecycle.
 *
 * Unlike toolInvocation.e2e.test.tsx (which uses a client-defined,
 * client-executed `get_weather` tool via @pyxis-llm/client's local
 * execute()+SubmitToolResults loop), this test drives one of llmd's
 * inline-builtin session tools (set_status — see
 * internal/toolexec/session_tools.go and sessionTools.e2e.test.tsx), which
 * llmd executes itself without any client-side callback. That's still a
 * real ToolExecutionStarted/ToolExecutionFinished round trip on the wire
 * (internal/server/server.go emits both frames around EVERY tool dispatch,
 * inline builtins included — not just client-delegated ones), so it
 * exercises the same onToolEvent wiring this test is meant to cover.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType } from '@pyxis-llm/client';
import { createLlmdWebClient } from '@pyxis-llm/client/web';
import { ChatPanel } from '@pyxis-llm/react';
import {
  useLlmdChatDriver,
  type LlmdChatDriver,
  type ToolConversationEvent,
} from '@pyxis-llm/react/browser';

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

async function waitFor(check: () => boolean, timeoutMs = 15_000): Promise<void> {
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
let toolEvents: ToolConversationEvent[];

// Every status the driver held, in order: the status is turn-scoped (llmd
// clears it when the turn ends), so the end state alone can't show it.
let seenStatuses: (string | undefined)[] = [];

function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdWebClient({
    baseUrl: `http://${env.handle.connectAddress}`,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
    token: env.connectToken,
  });
  const driver = useLlmdChatDriver(client, undefined, (ev) => {
    toolEvents.push(ev);
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

describe('ChatPanel + useLlmdChatDriver (Connect protocol): tool invocation', () => {
  it('fires onToolEvent with the ToolExecutionStarted/Finished lifecycle for a llmd-native session tool call', async () => {
    latestDriver = undefined;
    toolEvents = [];
    seenStatuses = [];

    env.fakeOpenAi.enqueue(
      {
        kind: 'tool_call',
        id: 'call-react-web-status-1',
        name: 'set_status',
        argumentsJson: JSON.stringify({ description: 'Looking for a fish...' }),
      },
      { kind: 'text', deltas: ["I've started tracking that."] }
    );

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('Please start working and keep me posted.');

    await waitFor(
      () => container.textContent?.includes("I've started tracking that.") === true,
      15_000
    );
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);
    expect(container.querySelector('button[aria-label="Stop"]')).toBeNull();

    // Driver state folds the status frame during the turn; llmd clears it at
    // the turn's end.
    const d = latestDriver as LlmdChatDriver | undefined;
    expect(seenStatuses).toContain('Looking for a fish...');
    await waitFor(() => (latestDriver as LlmdChatDriver | undefined)?.sessionStatus === undefined);
    expect(d?.error).toBeNull();

    // onToolEvent fires the execution lifecycle for the tool call, in order.
    const startedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolExecutionStarted
    );
    const finishedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolExecutionFinished
    );
    expect(startedIdx).toBeGreaterThanOrEqual(0);
    expect(finishedIdx).toBeGreaterThan(startedIdx);

    const streamStartedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolCallStreamStarted
    );
    const streamFinishedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolCallStreamFinished
    );
    if (streamStartedIdx >= 0) {
      expect(streamFinishedIdx).toBeGreaterThan(streamStartedIdx);
      expect(streamFinishedIdx).toBeLessThanOrEqual(startedIdx);
    }
  });
});
