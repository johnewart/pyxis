/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives llmd's own first-class session tools (set_status, set_session_title,
 * add_todo) through the full `ChatPanel` + `useLlmdChatDriver` stack against a
 * real llmd binary. Unlike toolInvocation.e2e.test.tsx, these are llmd-native
 * inline builtins (internal/toolexec/session_tools.go) — llmd executes them
 * itself, with no client-side `execute()` callback and no SubmitToolResults
 * round trip — so this test proves three things end to end:
 *
 *   1. The model can call these tools without the client registering
 *      anything for them (they're always in the merged tool surface).
 *   2. llmd emits the corresponding ConversationStreamFrame
 *      (EvSessionStatus / EvSessionTitleChanged / EvTodosUpdated), which the
 *      driver maps into `sessionStatus` / `sessionTitle` / `todos` state a
 *      host UI can render.
 *   3. The mutations (title, todos) are actually persisted on the session
 *      row (GetSession), not just streamed transiently.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { createLlmdClient } from '@pyxis-llm/client/node';
import type { GetSessionResponse } from '@pyxis-llm/client/gen/llmv1/llm_pb.js';
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

function getSession(sessionId: string): Promise<GetSessionResponse> {
  return env.rawClient.getSession({ sessionId }, { headers: env.headers });
}

let latestDriver: LlmdChatDriver | undefined;
// Every status the panel rendered, in order: the status is turn-scoped, so it
// is only visible DURING the turn and the end state alone can't show it.
let seenStatuses: (string | undefined)[] = [];

function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
  const driver = useLlmdChatDriver(client);
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
          sessionTitle={driver.sessionTitle}
          sessionStatus={driver.sessionStatus}
        />
      </div>
    </MantineProvider>
  );
}

describe('ChatPanel + useLlmdChatDriver: llmd-native session tools', () => {
  it('set_status, set_session_title, and add_todo round-trip into driver state and persist on the session', async () => {
    latestDriver = undefined;
    seenStatuses = [];

    // Three inline-builtin tool calls (llmd executes each itself and
    // re-queries the fake model), then a final text reply.
    env.fakeOpenAi.enqueue(
      {
        kind: 'tool_call',
        id: 'call-status-1',
        name: 'set_status',
        argumentsJson: JSON.stringify({ description: 'Looking for a fish...' }),
      },
      {
        kind: 'tool_call',
        id: 'call-title-1',
        name: 'set_session_title',
        argumentsJson: JSON.stringify({ title: 'Fish-finding session' }),
      },
      {
        kind: 'tool_call',
        id: 'call-todo-1',
        name: 'add_todo',
        argumentsJson: JSON.stringify({ description: 'find a fish' }),
      },
      { kind: 'text', deltas: ["I've started tracking that."] }
    );

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    // Before any tool call: no title/status bar rendered at all — ChatPanel
    // only shows this chrome once the agent has actually set something.
    expect(container.querySelector('.llmd-chat-panel__session-title')).toBeNull();
    expect(container.querySelector('.llmd-chat-panel__session-status')).toBeNull();

    typeAndSend('Please start working and keep me posted.');

    await waitFor(
      () => container.textContent?.includes("I've started tracking that.") === true,
      15_000
    );
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);

    // Driver state, folded live from the stream frames.
    // Re-widen: TS narrows the closure-assigned `let` to `undefined` here.
    const d = latestDriver as LlmdChatDriver | undefined;
    // The status showed while the turn ran, and llmd cleared it when the turn
    // ended: a finished turn must not keep advertising what it was doing.
    expect(seenStatuses).toContain('Looking for a fish...');
    await waitFor(() => (latestDriver as LlmdChatDriver | undefined)?.sessionStatus === undefined);
    expect(d?.sessionTitle).toBe('Fish-finding session');
    expect(d?.todos).toHaveLength(1);
    expect(d?.todos[0]).toMatchObject({
      description: 'find a fish',
      status: 'pending',
    });
    expect(d?.error).toBeNull();

    // The rendered ChatPanel chrome itself changed — this is the whole
    // point of wiring sessionTitle/sessionStatus into the driver: a host
    // that renders ChatPanel with no extra code gets a live-updating title
    // bar and status line for free (the status line gone once the turn ended).
    const titleEl = container.querySelector('.llmd-chat-panel__session-title');
    expect(titleEl?.textContent).toBe('Fish-finding session');
    expect(container.querySelector('.llmd-chat-panel__session-status')).toBeNull();

    // Persisted, not just transiently streamed: the title and todos are
    // durable session state a reconnecting client (or a host's session
    // list) would read back via GetSession.
    const persisted = await getSession(sessionId);
    expect(persisted.meta?.title).toBe('Fish-finding session');
    const persistedTodos = JSON.parse(persisted.meta?.todosJson || '[]');
    expect(persistedTodos).toHaveLength(1);
    expect(persistedTodos[0]).toMatchObject({ description: 'find a fish', status: 'pending' });
    // The turn is over, so there is no status for a reconnecting client to seed.
    expect(persisted.meta?.status ?? '').toBe('');

    // llmd advertised these tools to the model without the client
    // registering anything for them — they're always in the merged surface.
    const firstReq = env.fakeOpenAi.requests[0]?.body;
    const toolNames = (firstReq?.tools ?? []).map((t: any) => t.function?.name);
    expect(toolNames).toContain('set_status');
    expect(toolNames).toContain('set_session_title');
    expect(toolNames).toContain('add_todo');
  });
});
