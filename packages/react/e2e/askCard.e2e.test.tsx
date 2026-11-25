/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Real end-to-end coverage of llmd's first-class AskCard primitive: a real
 * llmd binary persists the card and the response as actual attachment rows
 * (via UpsertSessionAttachment), and @pyxis-llm/react's <ChatPanel>/<ChatMessage>
 * render the real, server-round-tripped state — asking (interactive form),
 * answering (submit -> persist), and re-rendering as answered, all through
 * real React state and a real gRPC round trip, not mocks.
 */

import React, { useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  UserMessage,
  upsertLlmdSessionAttachment,
  getLlmdSessionAttachmentRows,
} from '@pyxis-llm/client';
import {
  ASK_CARD_KIND,
  encodeAskCard,
  decodeAskCard,
  buildAskCardResponseAttachment,
  indexAskCardResponses,
  llmdMessageAttachmentFromRowContent,
  formatAttachmentTag,
  type AskCard,
  type AskCardResponse,
  type LlmdMessageAttachment,
} from '@pyxis-llm/core';
import { AskCardView, ChatPanel, type AskCardSubmitOptions, type ChatMessageData } from '@pyxis-llm/react';

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

async function lastAssistantMessageId(env: E2eEnv, sessionId: string): Promise<string> {
  const res = await env.getHistory(sessionId);
  const assistant = res.messages.filter((m) => m.role === 'assistant');
  const id = assistant.at(-1)?.id;
  if (!id) throw new Error('no assistant message to anchor to');
  return id;
}

async function loadAttachmentIndex(
  env: E2eEnv,
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

/** Renders ChatPanel over real, server-persisted attachment state, refetching on submit. */
function Harness({
  sessionId,
  anchorMessageId,
  card,
  initialIndex,
}: {
  sessionId: string;
  anchorMessageId: string;
  card: AskCard;
  initialIndex: Map<string, LlmdMessageAttachment>;
}) {
  const [attachmentIndex, setAttachmentIndex] = useState(initialIndex);
  const [askCardResponses, setAskCardResponses] = useState<Map<string, AskCardResponse>>(
    () => indexAskCardResponses(initialIndex.values())
  );

  const messages: ChatMessageData[] = [
    { role: 'user', content: 'anchor turn', id: 'msg-anchor' },
    { role: 'assistant', content: formatAttachmentTag(card.id), id: anchorMessageId },
  ];

  async function handleSubmit(response: AskCardResponse) {
    const responseAttachment = buildAskCardResponseAttachment(card, response);
    await upsertLlmdSessionAttachment({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
      anchorMessageId,
      attachment: responseAttachment,
    });
    const refreshed = await loadAttachmentIndex(env, sessionId);
    setAttachmentIndex(refreshed);
    setAskCardResponses(indexAskCardResponses(refreshed.values()));
  }

  return (
    <MantineProvider>
      <div style={{ height: 400 }}>
        <ChatPanel
          messages={messages}
          onSend={() => {}}
          attachmentIndex={attachmentIndex}
          askCardResponses={askCardResponses}
          onAskCardSubmit={(r) => {
            void handleSubmit(r);
          }}
        />
      </div>
    </MantineProvider>
  );
}

describe('AskCard: asked, answered, and rendered through a real llmd round trip', () => {
  it('renders an interactive card, submits an answer, and re-renders as answered', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['ok'] });
    const { client, sessionId } = await env.newSession();

    // A real turn must exist before an attachment can anchor to it.
    for await (const item of client.chatStreamEvents([
      new UserMessage('anchor turn', 'msg-anchor'),
    ])) {
      void item;
    }
    const anchorMessageId = await lastAssistantMessageId(env, sessionId);

    const card: AskCard = {
      id: 'card-e2e-1',
      title: 'Should I proceed?',
      text: 'Pick one of the options below.',
      type: 'choice',
      allowAdditionalText: false,
      choice: { choices: ['Make cookies', 'Bake bread', 'Take over the world'], allowMultiple: false },
    };

    await upsertLlmdSessionAttachment({
      client: env.rawClient,
      tenantId: env.tenantId,
      scopeId: env.scopeId,
      sessionId,
      anchorMessageId,
      attachment: { id: card.id, kind: ASK_CARD_KIND, payloadJson: encodeAskCard(card) },
    });

    const initialIndex = await loadAttachmentIndex(env, sessionId);
    expect(initialIndex.get(card.id)?.kind).toBe(ASK_CARD_KIND);

    act(() => {
      root.render(
        <Harness
          sessionId={sessionId}
          anchorMessageId={anchorMessageId}
          card={card}
          initialIndex={initialIndex}
        />
      );
    });

    // The card renders interactively with its title and choices.
    await waitFor(() => container.querySelector(`[data-testid="ask-card-${card.id}"]`) !== null);
    expect(container.textContent).toContain('Should I proceed?');
    const choiceRadio = container.querySelector(
      'input[type="radio"][value="Bake bread"]'
    ) as HTMLInputElement | null;
    expect(choiceRadio).not.toBeNull();

    // No answer summary yet — this card is unanswered.
    expect(container.querySelector(`[data-testid="ask-card-answer-${card.id}"]`)).toBeNull();

    act(() => {
      choiceRadio!.click();
    });

    const submitButton = container.querySelector(
      `[data-testid="ask-card-${card.id}-submit"]`
    ) as HTMLButtonElement;
    expect(submitButton).not.toBeNull();
    expect(submitButton.disabled).toBe(false);

    await act(async () => {
      submitButton.click();
      // Let the async handleSubmit (real gRPC round trip) settle.
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    // After submit, the same card re-renders read-only with the answer.
    await waitFor(
      () => container.querySelector(`[data-testid="ask-card-answer-${card.id}"]`) !== null,
      10_000
    );
    expect(container.textContent).toContain('Selected: Bake bread');

    // The response is durably persisted server-side, not just client state.
    const finalIndex = await loadAttachmentIndex(env, sessionId);
    const responseAttachment = finalIndex.get(`${card.id}:response`);
    expect(responseAttachment).toBeDefined();
    expect(responseAttachment?.kind).toBe('ask_card_response');
    expect(responseAttachment?.content).toContain('Selected: Bake bread');
    expect(JSON.parse(responseAttachment!.payloadJson)).toMatchObject({
      cardId: card.id,
      type: 'choice',
      choice: { selected: ['Bake bread'] },
    });
  });
});

describe('AskCardView: explained options', () => {
  it('shows each option\'s description and the recommendation reason under its own option', () => {
    // Exactly the shape llmd's ask_user handler stores (session_tools.go
    // askCardPayload), so this also covers decodeAskCard reading `options`.
    const card = decodeAskCard(
      JSON.stringify({
        id: 'card-opts',
        title: 'Pick a DB',
        text: '',
        type: 'choice',
        allowAdditionalText: false,
        status: 'pending',
        choice: {
          choices: ['Postgres', 'SQLite'],
          allowMultiple: false,
          options: [
            { label: 'Postgres', description: 'Uses the existing pool.' },
            { label: 'SQLite', description: 'Single file.', recommendation: 'Matches session storage.' },
          ],
        },
      })
    )!;
    expect(card.choice?.options).toHaveLength(2);

    act(() => {
      root.render(
        <MantineProvider>
          <AskCardView card={card} onSubmit={() => {}} />
        </MantineProvider>
      );
    });

    const desc = (label: string) =>
      container.querySelector(`[data-testid="ask-card-card-opts-desc-${label}"]`)?.textContent;
    const rec = (label: string) =>
      container.querySelector(`[data-testid="ask-card-card-opts-rec-${label}"]`)?.textContent;
    expect(desc('Postgres')).toBe('Uses the existing pool.');
    expect(desc('SQLite')).toBe('Single file.');
    expect(rec('SQLite')).toBe('★ Recommended: Matches session storage.');
    expect(rec('Postgres')).toBeUndefined();

    // The recommendation sits with its own option, below that option's description.
    const sqliteRadio = container.querySelector('input[type="radio"][value="SQLite"]')!;
    const sqliteRoot = sqliteRadio.closest('.mantine-Radio-root')!;
    const recEl = sqliteRoot.querySelector('[data-testid="ask-card-card-opts-rec-SQLite"]');
    const descEl = sqliteRoot.querySelector('[data-testid="ask-card-card-opts-desc-SQLite"]');
    expect(recEl).not.toBeNull();
    expect(descEl!.compareDocumentPosition(recEl!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    // The recommendation is not pre-selected: nothing can be submitted yet.
    expect((sqliteRadio as HTMLInputElement).checked).toBe(false);
    const submit = container.querySelector('[data-testid="ask-card-card-opts-submit"]') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });

  it('falls back to plain choices when options are malformed', () => {
    const card = decodeAskCard(
      JSON.stringify({
        id: 'card-bad',
        title: 'q',
        type: 'choice',
        choice: { choices: ['A', 'B'], allowMultiple: false, options: [{ description: 'no label' }] },
      })
    )!;
    expect(card.choice).toEqual({ choices: ['A', 'B'], allowMultiple: false });
  });
});

describe('Ask cards that cannot be shown stay dismissable', () => {
  function renderPanelWith(payloadJson: string, dismissed: string[]) {
    const attachmentIndex = new Map<string, LlmdMessageAttachment>([
      ['broken', { id: 'broken', kind: ASK_CARD_KIND, payloadJson }],
    ]);
    act(() => {
      root.render(
        <MantineProvider>
          <div style={{ height: 400 }}>
            <ChatPanel
              messages={[{ role: 'user', content: 'still here', id: 'u1' }]}
              onSend={() => {}}
              attachmentIndex={attachmentIndex}
              onAskCardSubmit={() => {}}
              onAskCardDismiss={(id) => dismissed.push(id)}
            />
          </div>
        </MantineProvider>
      );
    });
  }

  it('an undecodable card shows a muted line with a working ✕', () => {
    const dismissed: string[] = [];
    // Pending (so it gets a row) but no id/type: decodeAskCard rejects it.
    renderPanelWith(JSON.stringify({ status: 'pending', title: 'x' }), dismissed);
    const line = container.querySelector('[data-testid="ask-card-broken-unavailable"]');
    expect(line?.textContent).toContain('[card unavailable]');
    act(() => {
      (container.querySelector('[data-testid="ask-card-broken-dismiss"]') as HTMLButtonElement).click();
    });
    expect(dismissed).toEqual(['broken']);
  });

  it('a card that crashes while rendering is contained and still dismissable', () => {
    const dismissed: string[] = [];
    // Decodes fine, but acceptedMimeTypes is a string, so the file input's
    // `.join` throws during render.
    renderPanelWith(
      JSON.stringify({
        id: 'broken',
        title: 'Upload?',
        type: 'file',
        status: 'pending',
        file: { acceptedMimeTypes: 'image/png' },
      }),
      dismissed
    );
    // The rest of the transcript survived.
    expect(container.textContent).toContain('still here');
    const line = container.querySelector('[data-testid="ask-card-broken-unavailable"]');
    expect(line?.textContent).toContain('failed to render');
    act(() => {
      (container.querySelector('[data-testid="ask-card-broken-dismiss"]') as HTMLButtonElement).click();
    });
    expect(dismissed).toEqual(['broken']);
  });
});

describe('AskCardView: multi-select choice', () => {
  // Regression: the checkbox handler read e.currentTarget.checked inside the
  // setSelected updater, which React runs after the event has been dispatched
  // (currentTarget is null by then), crashing the render on toggle.
  it('toggles checkboxes without crashing and submits the selection via "Answer & stop"', async () => {
    const card: AskCard = {
      id: 'card-multi',
      title: 'Which ones?',
      text: '',
      type: 'choice',
      allowAdditionalText: false,
      choice: { choices: ['Alpha', 'Beta', 'Gamma'], allowMultiple: true },
    };
    const submitted: Array<{ response: AskCardResponse; options: AskCardSubmitOptions }> = [];

    act(() => {
      root.render(
        <MantineProvider>
          <AskCardView card={card} onSubmit={(response, options) => submitted.push({ response, options })} />
        </MantineProvider>
      );
    });

    const box = (label: string) =>
      Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(
        (el) => el.closest('label, .mantine-Checkbox-root')?.textContent?.includes(label)
      )!;
    expect(box('Alpha')).toBeDefined();

    // Several toggles in a row: later ones are not eagerly computed by React,
    // so they exercise the deferred updater path that used to throw.
    for (const label of ['Alpha', 'Gamma', 'Beta', 'Beta']) {
      act(() => {
        box(label).click();
      });
    }
    expect(container.querySelector(`[data-testid="ask-card-${card.id}"]`)).not.toBeNull();
    expect(box('Alpha').checked).toBe(true);
    expect(box('Beta').checked).toBe(false);
    expect(box('Gamma').checked).toBe(true);

    const stop = container.querySelector(
      `[data-testid="ask-card-${card.id}-submit-stop"]`
    ) as HTMLButtonElement;
    expect(stop).not.toBeNull();
    expect(stop.disabled).toBe(false);
    await act(async () => {
      stop.click();
    });

    expect(submitted).toHaveLength(1);
    expect(submitted[0]!.options).toEqual({ continueWithTools: false });
    expect(submitted[0]!.response).toMatchObject({
      cardId: card.id,
      type: 'choice',
      choice: { selected: ['Alpha', 'Gamma'] },
    });
  });
});
