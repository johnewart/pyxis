/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). With the opt-in `segmentedTurns` driver
 * config (5th useLlmdChatDriver argument), an assistant turn of text, tool
 * call, text, tool call, text keeps its three text segments apart: live,
 * rejoined, and loaded from session history. The message shows the
 * in-between segments as a collapsed "2 steps" list and the final segment in
 * full. With the option off, the same turn is one message.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  AssistantMessage,
  ConversationEventType,
  ToolMessage,
  UserMessage,
  type ConversationEvent,
  type Message,
} from '@pyxis-llm/client';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/node';
import { ChatMessage, type ChatMessageData } from '@pyxis-llm/react';

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
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

const TEXTS = ['First I read the file.', 'Now I edit it.', 'Done: the leak is fixed.'];

function tool(id: string, name: string): ConversationEvent {
  return {
    type: ConversationEventType.ToolExecutionStarted,
    turnId: 'r1',
    toolCallId: id,
    name,
  } as unknown as ConversationEvent;
}

/** text, tool call, text, tool call, text — as session events. */
function* turnEvents(): Generator<ConversationEvent> {
  yield { type: ConversationEventType.AssistantTurnStarted, turnId: 'r1' } as ConversationEvent;
  yield { type: ConversationEventType.AssistantTextDelta, delta: TEXTS[0] } as ConversationEvent;
  yield tool('t1', 'read_file');
  yield { type: ConversationEventType.AssistantTextDelta, delta: TEXTS[1] } as ConversationEvent;
  yield tool('t2', 'write_file');
  yield { type: ConversationEventType.AssistantTextDelta, delta: TEXTS[2] } as ConversationEvent;
}

function hang(signal?: AbortSignal): Promise<void> {
  return new Promise<void>((resolve) => signal?.addEventListener('abort', () => resolve(), { once: true }));
}

/** Persisted form of the same turn: three assistant messages split by tool calls. */
function historyMessages(): Message[] {
  const a1 = new AssistantMessage(TEXTS[0], 'a1');
  a1.toolCalls = [{ id: 't1', name: 'read_file', arguments: {} }];
  const a2 = new AssistantMessage(TEXTS[1], 'a2');
  a2.toolCalls = [{ id: 't2', name: 'write_file', arguments: {} }];
  return [
    new UserMessage('fix the leak', 'u1'),
    a1,
    new ToolMessage('ok', 't1', 'tm1'),
    a2,
    new ToolMessage('ok', 't2', 'tm2'),
    new AssistantMessage(TEXTS[2], 'a3'),
  ];
}

let driver: LlmdChatDriver | undefined;

function Harness({ client, segmentedTurns }: { client: unknown; segmentedTurns: boolean }) {
  driver = useLlmdChatDriver(client as never, undefined, undefined, undefined, { segmentedTurns } as never);
  return null;
}

function mount(client: unknown, segmentedTurns: boolean) {
  driver = undefined;
  act(() => {
    root.render(<Harness client={client} segmentedTurns={segmentedTurns} />);
  });
}

type Segment = { text: string };
const lastAssistant = () => [...(driver?.messages ?? [])].reverse().find((m) => m.role === 'assistant');
const segmentsOf = (m?: ChatMessageData) => (m as { segments?: Segment[] } | undefined)?.segments ?? [];

const liveClient = {
  async *chatStreamEvents() {
    for (const event of turnEvents()) yield { kind: 'event' as const, event };
    await hang();
  },
};

const rejoinClient = {
  async *chatStreamEvents() {},
  async getSessionState() {
    return { messages: [new UserMessage('fix the leak', 'u1')], turnInProgress: true, eventsSeq: 5, turnStartSeq: 1 };
  },
  async *subscribeSessionEvents(options?: { signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
    yield { type: ConversationEventType.UserTurnCommitted, userMessageId: 'u1' } as ConversationEvent;
    yield* turnEvents();
    await hang(options?.signal);
  },
};

const historyClient = {
  async *chatStreamEvents() {},
  async getSessionState() {
    return { messages: historyMessages(), turnInProgress: false, eventsSeq: 0, turnStartSeq: 0 };
  },
  async *subscribeSessionEvents(options?: { signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
    await hang(options?.signal);
  },
};

function expectCollapsedSteps(message: ChatMessageData) {
  act(() => {
    root.render(
      <MantineProvider>
        <ChatMessage message={message} />
      </MantineProvider>
    );
  });
  const text = container.textContent ?? '';
  expect(text).toContain('2 steps');
  expect(text).toContain(TEXTS[2]);
  expect(text).not.toContain(TEXTS[0]);
  expect(text).not.toContain(TEXTS[1]);
  const toggle = Array.from(container.querySelectorAll('button')).find((b) =>
    (b.textContent ?? '').includes('2 steps')
  );
  expect(toggle).toBeTruthy();
  act(() => toggle!.click());
  const open = container.textContent ?? '';
  expect(open).toContain(TEXTS[0]);
  expect(open).toContain(TEXTS[1]);
  expect(container.querySelectorAll('li').length).toBe(2);
}

describe('segmentedTurns driver option on', () => {
  it.each([
    ['live', liveClient, true],
    ['rejoined', rejoinClient, false],
    ['history', historyClient, false],
  ] as const)(
    '%s: three text segments, shown as a collapsed "2 steps" list plus the final text',
    async (_name, client, sends) => {
      mount(client, true);
      if (sends) act(() => driver!.send('fix the leak'));
      await waitFor(() => segmentsOf(lastAssistant()).length === 3);
      expect(segmentsOf(lastAssistant()).map((s) => s.text)).toEqual(TEXTS);
      expectCollapsedSteps(lastAssistant()!);
    }
  );
});

describe('segmentedTurns driver option off', () => {
  it('the same live turn is one joined message', async () => {
    mount(liveClient, false);
    act(() => driver!.send('fix the leak'));
    await waitFor(() => (lastAssistant()?.content ?? '').includes(TEXTS[2]));
    expect(lastAssistant()!.content).toBe(TEXTS.join(''));
    expect(segmentsOf(lastAssistant())).toEqual([]);
    act(() => {
      root.render(
        <MantineProvider>
          <ChatMessage message={lastAssistant()!} />
        </MantineProvider>
      );
    });
    expect(container.textContent).not.toContain('2 steps');
    expect(container.textContent).toContain(TEXTS.join(''));
  });
});
