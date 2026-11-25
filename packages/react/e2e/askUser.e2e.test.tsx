/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives llmd's server-registered `ask_user` tool (see
 * internal/toolexec/session_tools.go's askUserHandler) through the full
 * `ChatPanel` + `useLlmdChatDriver` stack against a real llmd binary.
 *
 * ask_user is an ordinary inline builtin: the model calls it, llmd persists
 * an `ask_card` attachment (its existing, already-shipped AskCard primitive
 * — see packages/react/e2e/askCard.e2e.test.tsx) server-side, and returns
 * immediately. There is no wait, no nonce, no SubmitToolResults — the turn
 * finishes normally. The card then renders exactly the way any ask card
 * does (via ChatPanel's `attachmentIndex`/`askCardResponses`/
 * `onAskCardSubmit`), and the user answers it whenever, via the same
 * UpsertSessionAttachment RPC a manually-created card already uses. This
 * test proves the whole thing end to end with NO client tool registration
 * at all — the client never learns `ask_user` exists as a tool; it only
 * ever sees the resulting attachment.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { getLlmdSessionAttachmentRows } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';
import {
  decodeAskCard,
  llmdMessageAttachmentFromRowContent,
  type LlmdMessageAttachment,
} from '@pyxis-llm/core';
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

async function loadAttachmentIndex(
  sessionId: string
): Promise<Map<string, LlmdMessageAttachment>> {
  const rows = await getLlmdSessionAttachmentRows({
    client: env.rawClient,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
  const index = new Map<string, LlmdMessageAttachment>();
  for (const row of rows) {
    const attachment = llmdMessageAttachmentFromRowContent(row.content);
    if (attachment) index.set(attachment.id, attachment);
  }
  return index;
}

let latestDriver: LlmdChatDriver | undefined;

/** The whole host-side card wiring is two driver props: the driver loads,
 * live-updates and saves attachments, and ChatPanel gives an anchorless
 * pending card (ask_user fires before any assistant text exists) its own row.
 * If this harness ever needs more than that, the library regressed. */
function Harness({ sessionId }: { sessionId: string }) {
  const client = createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
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
          attachmentIndex={driver.attachmentIndex}
          onAskCardSubmit={(response, options) => void driver.submitAskCard(response, options)}
          onAskCardDismiss={(cardId) => void driver.dismissAskCard(cardId)}
        />
      </div>
    </MantineProvider>
  );
}

describe('ChatPanel + useLlmdChatDriver: llmd-native ask_user tool', () => {
  it('creates an ask_card attachment with no client tool registration, renders it, and records the answer', async () => {
    latestDriver = undefined;

    // Two turns here purely to exercise ask_user mid-conversation (not a
    // requirement — ChatAttachmentRepo.ensureAnchorTurn handles a session's
    // very first turn too; see internal/toolexec/session_tools_test.go's
    // TestAskUser_FirstTurnOfSessionWithNoPriorHistory).
    // llmd forces the turn to end as soon as ask_user succeeds — it never
    // re-queries the model for a follow-up reply this turn (see
    // internal/server/server.go's forceEndTurn) — so only ONE fake response
    // is consumed for the second turn below, not a tool_call + a text reply.
    env.fakeOpenAi.enqueue(
      { kind: 'text', deltas: ['Sure, happy to help.'] },
      {
        kind: 'tool_call',
        id: 'call-ask-1',
        name: 'ask_user',
        argumentsJson: JSON.stringify({
          title: 'Should I proceed?',
          text: 'Pick one of the options below.',
          type: 'choice',
          choices: ['Make cookies', 'Bake bread', 'Take over the world'],
        }),
      }
    );

    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    typeAndSend('Hi there');
    await waitFor(
      () => container.textContent?.includes('Sure, happy to help.') === true,
      15_000
    );
    await waitFor(() => latestDriver?.streaming === false);

    typeAndSend('What should we do today?');

    // llmd advertised ask_user to the model without this test registering
    // any tool at all.
    await waitFor(() => env.fakeOpenAi.requests.length >= 2);
    const secondReq = env.fakeOpenAi.requests[1]?.body;
    const toolNames = (secondReq?.tools ?? []).map((t: any) => t.function?.name);
    expect(toolNames).toContain('ask_user');

    // The turn ends immediately after ask_user — no follow-up model round,
    // no follow-up text this turn (see internal/server/server.go's
    // forceEndTurn). Only ONE fake response was enqueued for this turn
    // (the tool call itself); if llmd queried the model again it would hit
    // "no scripted turn queued" and this would time out instead of settling.
    await waitFor(
      () =>
        container.querySelector('[data-testid^="ask-card-"]') !== null && latestDriver?.streaming === false,
      15_000
    );
    expect(env.fakeOpenAi.requests).toHaveLength(2);
    expect((latestDriver as LlmdChatDriver | undefined)?.error).toBeNull();

    // The card is a real, durable attachment llmd created server-side.
    const persisted = await loadAttachmentIndex(sessionId);
    const cardEntries = [...persisted.values()].filter((a) => a.kind === 'ask_card');
    expect(cardEntries).toHaveLength(1);
    const card = decodeAskCard(cardEntries[0]!.payloadJson)!;
    expect(card.title).toBe('Should I proceed?');
    expect(card.type).toBe('choice');

    // No manual refetch: the card must show up from the live
    // attachment_upserted event alone.
    await waitFor(() => container.querySelector(`[data-testid="ask-card-${card.id}"]`) !== null);
    expect(container.textContent).toContain('Should I proceed?');
    const choiceRadio = container.querySelector(
      'input[type="radio"][value="Bake bread"]'
    ) as HTMLInputElement | null;
    expect(choiceRadio).not.toBeNull();

    act(() => {
      choiceRadio!.click();
    });
    const submitButton = container.querySelector(
      `[data-testid="ask-card-${card.id}-submit"]`
    ) as HTMLButtonElement;
    expect(submitButton).not.toBeNull();

    await act(async () => {
      submitButton.click();
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    // Once answered (here: as soon as the answer is queued, before any turn
    // has run it) the card goes away — it is not re-pinned at the bottom of
    // the transcript. Its record is the "Answer to: …" message and its chip.
    await waitFor(() => container.querySelector(`[data-testid="ask-card-${card.id}"]`) === null, 10_000);
    expect(container.textContent).not.toContain('Pick one of the options below.');
    expect(latestDriver?.attachmentsError).toBeUndefined();

    // Answering resumes the agent: llmd queued "Answer to: <title>" at the
    // FRONT of the queue, carrying the answer with the server-built summary.
    // (This shared e2e llmd runs with server draining disabled — see
    // packages/client/e2e/harness.ts — so the entry stays queued here; the
    // drain → persist-on-the-answer-message → model-sees-it path is covered
    // by internal/server's TestAnswerAskCard_* and
    // TestChatStream_IncomingUserMessageAttachments.)
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    const { queue } = await probe.listQueue();
    expect(queue[0]?.content).toBe('Answer to: Should I proceed?');
    const queuedAnswer = queue[0]?.attachments.find((a) => a.id === `${card.id}:response`);
    expect(queuedAnswer?.kind).toBe('ask_card_response');
    expect(queuedAnswer?.content).toBe('In response to "Should I proceed?": Selected: Bake bread.');
    expect(JSON.parse(queuedAnswer!.payloadJson)).toMatchObject({
      cardId: card.id,
      type: 'choice',
      choice: { selected: ['Bake bread'] },
    });
    // Not persisted as a session attachment until its turn runs.
    expect((await loadAttachmentIndex(sessionId)).get(`${card.id}:response`)).toBeUndefined();

    // The stored card says so too (AnswerAskCard marks it answered).
    const storedCard = (await loadAttachmentIndex(sessionId)).get(card.id);
    expect(JSON.parse(storedCard!.payloadJson).status).toBe('answered');

    // Reopen the session on a fresh mount: once the driver has loaded the
    // attachments and the (queued) answer, the card still does not come back.
    act(() => {
      root.unmount();
    });
    root = createRoot(container);
    latestDriver = undefined;
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    await waitFor(
      () => (latestDriver?.attachmentIndex.has(card.id) ?? false) && (latestDriver?.queue.length ?? 0) === 1,
      10_000
    );
    expect(container.querySelector(`[data-testid="ask-card-${card.id}"]`)).toBeNull();
  });

  it('dismissing a card removes it as though it was never asked: no row, no queued turn, gone after reload', async () => {
    latestDriver = undefined;
    env.fakeOpenAi.enqueue({
      kind: 'tool_call',
      id: 'call-ask-dismiss',
      name: 'ask_user',
      argumentsJson: JSON.stringify({ title: 'Broken question?', type: 'choice', choices: ['A', 'B'] }),
    });
    const { sessionId } = await env.newSession();
    const requestsBefore = env.fakeOpenAi.requests.length;

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    typeAndSend('Ask me something');
    // Wait for the card itself. (The Send button is not a "turn over" signal:
    // a send is not drawn until llmd has decided to run it, so the button is
    // still there in the instant right after sending.)
    await waitFor(
      () => container.querySelector('[data-testid^="ask-card-"][data-testid$="-dismiss"]') !== null,
      15_000
    );
    await waitFor(() => latestDriver?.streaming === false, 15_000);

    const card = decodeAskCard(
      [...(await loadAttachmentIndex(sessionId)).values()].find((a) => a.kind === 'ask_card')!.payloadJson
    )!;
    await waitFor(() => container.querySelector(`[data-testid="ask-card-${card.id}-dismiss"]`) !== null);

    await act(async () => {
      (container.querySelector(`[data-testid="ask-card-${card.id}-dismiss"]`) as HTMLButtonElement).click();
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    // Gone from the screen, with no placeholder left behind.
    await waitFor(() => container.querySelector(`[data-testid="ask-card-${card.id}"]`) === null);
    expect(container.textContent).not.toContain('Broken question?');
    expect(container.textContent).not.toContain('card unavailable');
    expect(latestDriver?.attachmentsError).toBeUndefined();

    // Gone from llmd: no attachment row, nothing queued, no new model call.
    expect((await loadAttachmentIndex(sessionId)).has(card.id)).toBe(false);
    const probe = createLlmdClient({
      address: env.handle.grpcAddress,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
    });
    expect((await probe.listQueue()).queue).toHaveLength(0);
    expect(env.fakeOpenAi.requests.length).toBe(requestsBefore + 1);

    // And it stays gone on a fresh mount.
    act(() => {
      root.unmount();
    });
    root = createRoot(container);
    latestDriver = undefined;
    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });
    // This harness does not load the transcript on mount, only attachments
    // (see the reload in the test above), so give the driver's mount-time
    // attachment load time to land, then check the card did not come back.
    await waitFor(() => latestDriver !== undefined);
    await new Promise((resolve) => setTimeout(resolve, 500));
    await act(async () => {});
    expect(latestDriver?.attachmentIndex.has(card.id)).toBe(false);
    expect(container.querySelector(`[data-testid="ask-card-${card.id}"]`)).toBeNull();
    expect(container.textContent).not.toContain('card unavailable');
  });
});
