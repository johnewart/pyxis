/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives a real client-defined-tool round trip through `ChatPanel` +
 * `useLlmdChatDriver`: the fake model calls a tool, the driver's
 * `LlmdClient.chatStreamEvents` executes it locally (via the `tools` option
 * threaded through from the hook), submits the result back to llmd, and the
 * fake model's second (post-tool) reply renders in the DOM — no mocking of
 * the driver, ChatPanel, or the tool-execution loop.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType, ToolClassification } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { ChatPanel } from '@pyxis-llm/react';
import {
  useLlmdChatDriver,
  type LlmdChatDriver,
  type ToolConversationEvent,
} from '@pyxis-llm/react/node';

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

let executedArgs: Record<string, unknown> | undefined;
let latestDriver: LlmdChatDriver | undefined;
let toolEvents: ToolConversationEvent[];

function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
  const driver = useLlmdChatDriver(
    client,
    {
      tools: [
        {
          name: 'get_weather',
          description: 'Get the weather for a city',
          parameters: {
            type: 'object',
            properties: { city: { type: 'string' } },
            required: ['city'],
          },
          classification: ToolClassification.QUERY,
          execute: async (args: Record<string, unknown>) => {
            executedArgs = args;
            return `sunny in ${args.city as string}`;
          },
        },
      ],
    },
    (ev) => {
      toolEvents.push(ev);
    }
  );
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

describe('ChatPanel + useLlmdChatDriver: tool invocation', () => {
  it('renders the tool-calling turn, executes the local tool, and renders the post-tool reply', async () => {
    executedArgs = undefined;
    latestDriver = undefined;
    toolEvents = [];

    env.fakeOpenAi.enqueue(
      {
        kind: 'tool_call',
        id: 'call-react-1',
        name: 'get_weather',
        argumentsJson: JSON.stringify({ city: 'Springfield' }),
      },
      { kind: 'text', deltas: ['It is sunny in Springfield.'] }
    );

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('What is the weather in Springfield?');

    await waitFor(
      () => container.textContent?.includes('It is sunny in Springfield.') === true,
      15_000
    );

    // Streaming settled back to idle.
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);
    expect(container.querySelector('button[aria-label="Stop"]')).toBeNull();

    // The driver's own execute() handler actually ran, with the model's arguments.
    expect(executedArgs).toEqual({ city: 'Springfield' });

    // llmd forwarded the tool definition to the "model" (visible in the fake server's
    // recorded request), proving tool registration flowed through the whole stack.
    const firstReq = env.fakeOpenAi.requests[env.fakeOpenAi.requests.length - 2]?.body;
    const toolNames = (firstReq?.tools ?? []).map((t: any) => t.function?.name);
    expect(toolNames).toContain('get_weather');

    // Final transcript: user turn, then a single assistant turn with the final text
    // (the intermediate tool-call-only assistant reply is not a separate persisted
    // message — the driver's `complete` event carries the post-tool-result turn).
    const roles = latestDriver!.messages.map((m) => m.role);
    expect(roles).toEqual(['user', 'assistant']);
    expect(latestDriver!.error).toBeNull();

    // onToolEvent fires the execution lifecycle for the tool call, in order
    // — even though (per above) none of it is folded into `messages`.
    const startedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolExecutionStarted
    );
    const finishedIdx = toolEvents.findIndex(
      (ev) => ev.type === ConversationEventType.ToolExecutionFinished
    );
    expect(startedIdx).toBeGreaterThanOrEqual(0);
    expect(finishedIdx).toBeGreaterThan(startedIdx);

    // If the provider streamed tool-call deltas, they arrive before execution starts.
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
