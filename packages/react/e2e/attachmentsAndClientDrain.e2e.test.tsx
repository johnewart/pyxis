/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Message attachments through the driver, and the opt-in client-side queue
 * drainer, against a real llmd binary.
 *
 * - `send(text, behavior, attachments)`: the attachments ride ON the user
 *   message; the model sees their `content` in that message, and llmd persists
 *   them anchored to it.
 * - `config.drainQueue`: with llmd's own drainer off (as in this shared e2e
 *   llmd), the driver claims and runs queued entries itself — attachments
 *   included — and retires them.
 * - The ask_user loop closes end to end: answering a card queues the answer at
 *   the front, the driver drains it, and the model receives the answer.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { getLlmdSessionAttachmentRows } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { decodeAskCard, llmdMessageAttachmentFromRowContent } from '@pyxis-llm/core';
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
  latestDriver = undefined;
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

function clientFor(sessionId: string) {
  return createLlmdClient({ address: env.handle.grpcAddress, tenantId: env.tenantId, scopeId: env.scopeId, sessionId });
}

function Harness({ sessionId, drainQueue }: { sessionId: string; drainQueue?: boolean }) {
  const client = clientFor(sessionId);
  const driver = useLlmdChatDriver(client, undefined, undefined, undefined, { drainQueue });
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
          attachmentIndex={driver.attachmentIndex}
          onAskCardSubmit={(response, options) => void driver.submitAskCard(response, options)}
        />
      </div>
    </MantineProvider>
  );
}

/** Text of the last user message in the n-th request the fake model received. */
function lastUserText(n: number): string {
  const msgs: any[] = env.fakeOpenAi.requests[n]?.body?.messages ?? [];
  const u = [...msgs].reverse().find((m) => m.role === 'user');
  return typeof u?.content === 'string' ? u.content : JSON.stringify(u?.content ?? '');
}

async function persistedAttachmentIds(sessionId: string): Promise<Map<string, string>> {
  const rows = await getLlmdSessionAttachmentRows({ client: env.rawClient, tenantId: env.tenantId, scopeId: env.scopeId, sessionId });
  const out = new Map<string, string>();
  for (const row of rows) {
    const a = llmdMessageAttachmentFromRowContent(row.content);
    if (a) out.set(a.id, row.content);
  }
  return out;
}

describe('attachments + client queue drainer', () => {
  it('send() carries attachments: the model sees them and llmd persists them on that message', async () => {
    const { sessionId } = await env.newSession();
    const base = env.fakeOpenAi.requests.length;
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Got the notes.'] });
    act(() => root.render(<Harness sessionId={sessionId} />));
    await waitFor(() => latestDriver !== undefined);

    act(() => {
      latestDriver!.send('look at this', 'queue', [
        { id: 'att-send-1', kind: 'composer_attachment', payloadJson: '{"type":"text","name":"notes"}', content: 'SEND-CTX-91c2' },
      ]);
    });
    await waitFor(() => container.textContent?.includes('Got the notes.') === true);

    const text = lastUserText(base);
    expect(text).toContain('look at this');
    expect(text).toContain('SEND-CTX-91c2');
    const rows = await persistedAttachmentIds(sessionId);
    expect(rows.has('att-send-1')).toBe(true);
  });

  it('drainQueue runs a queued entry (with its attachments) and retires it', async () => {
    const { sessionId } = await env.newSession();
    const probe = clientFor(sessionId);
    const base = env.fakeOpenAi.requests.length;
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Drained and done.'] });
    await probe.enqueueMessage('queued hello', [
      { id: 'att-queue-1', kind: 'composer_attachment', payloadJson: '{}', content: 'QUEUE-CTX-55ab' },
    ]);

    act(() => root.render(<Harness sessionId={sessionId} drainQueue />));
    await waitFor(() => container.textContent?.includes('Drained and done.') === true);

    const text = lastUserText(base);
    expect(text).toContain('queued hello');
    expect(text).toContain('QUEUE-CTX-55ab');
    await waitFor(() => (latestDriver?.queue.length ?? 1) === 0);
    expect((await probe.listQueue()).queue).toHaveLength(0);
    const rows = await persistedAttachmentIds(sessionId);
    expect(rows.get('att-queue-1')).toContain('"anchor_message_id":"queued_');
  });

  it('closes the ask_user loop: answering a card resumes the agent with the answer', async () => {
    const { sessionId } = await env.newSession();
    env.fakeOpenAi.enqueue({
      kind: 'tool_call',
      id: 'call-ask-drain',
      name: 'ask_user',
      argumentsJson: JSON.stringify({ title: 'Which flavour?', type: 'choice', choices: ['Vanilla', 'Chocolate'] }),
    });
    act(() => root.render(<Harness sessionId={sessionId} drainQueue />));
    await waitFor(() => latestDriver !== undefined);
    act(() => latestDriver!.send('help me pick'));

    let cardId = '';
    await waitFor(() => {
      for (const a of latestDriver?.attachmentIndex.values() ?? []) {
        if (a.kind === 'ask_card') cardId = decodeAskCard(a.payloadJson)?.id ?? '';
      }
      return cardId !== '' && container.querySelector(`[data-testid="ask-card-${cardId}-submit"]`) !== null && !latestDriver!.streaming;
    }, 15_000);

    const base = env.fakeOpenAi.requests.length;
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Chocolate it is.'] });
    const radio = container.querySelector('input[type="radio"][value="Chocolate"]') as HTMLInputElement;
    act(() => radio.click());
    await act(async () => {
      (container.querySelector(`[data-testid="ask-card-${cardId}-submit"]`) as HTMLButtonElement).click();
      await new Promise((r) => setTimeout(r, 50));
    });

    await waitFor(() => container.textContent?.includes('Chocolate it is.') === true, 15_000);
    const text = lastUserText(base);
    expect(text).toContain('Answer to: Which flavour?');
    expect(text).toContain('In response to "Which flavour?": Selected: Chocolate.');
    expect((await persistedAttachmentIds(sessionId)).has(`${cardId}:response`)).toBe(true);

    // The card is gone (not re-pinned below the reply)...
    expect(container.querySelector(`[data-testid="ask-card-${cardId}"]`)).toBeNull();
    // ...and the "Answer to: …" message carries a chip for it instead, which
    // opens to the question and the answer that was sent.
    await waitFor(() => container.querySelector(`[data-testid="ask-card-chip-${cardId}"]`) !== null);
    const chip = container.querySelector(`[data-testid="ask-card-chip-${cardId}"]`) as HTMLElement;
    expect(chip.textContent).toContain('Which flavour?');
    expect(chip.closest('[data-message-role]')?.getAttribute('data-message-role')).toBe('user');
    act(() => chip.click());
    await waitFor(() => document.querySelector(`[data-testid="ask-card-chip-detail-${cardId}"]`) !== null);
    expect(document.querySelector(`[data-testid="ask-card-chip-detail-${cardId}"]`)?.textContent).toContain(
      'Selected: Chocolate'
    );
  });
});
