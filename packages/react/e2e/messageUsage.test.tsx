/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The usage badge on assistant messages: tokens and model time read from the
 * message's `metadata.usage` (what @pyxis-llm/client's history mapping and the
 * chat driver put there), with a host's `usageByMessageId` taking precedence.
 * Pure view behavior: no llmd binary. Run via `npm run test:unit`.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ChatPanel, type ChatMessageData, type ChatMessageUsage } from '@pyxis-llm/react';

let container: HTMLDivElement;
let root: Root;

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

function renderPanel(messages: ChatMessageData[], usageByMessageId?: Map<string, ChatMessageUsage>) {
  act(() => {
    root.render(
      <MantineProvider>
        <ChatPanel messages={messages} onSend={() => {}} usageByMessageId={usageByMessageId} />
      </MantineProvider>
    );
  });
}

function badgeText(id: string): string | null {
  const el = container.querySelector(`[data-message-id="${id}"] .chat-message__usage-badge`);
  return el ? (el.textContent ?? '').replace(/ /g, ' ') : null;
}

function assistant(id: string, usage?: Record<string, unknown>): ChatMessageData {
  return { id, role: 'assistant', content: `reply ${id}`, ...(usage ? { metadata: { usage } } : {}) };
}

describe('usage badge', () => {
  it('shows tokens and model time from metadata.usage', () => {
    renderPanel([
      { id: 'u1', role: 'user', content: 'hi' },
      assistant('a1', { inputTokens: 12000, cacheReadTokens: 400, cacheWriteTokens: 0, outputTokens: 830, modelDurationMs: 14200 }),
    ]);
    expect(badgeText('a1')).toBe('↑ 12.4k · ↓ 830 · 14.2s (cache: 400 read / 0 written)');
    const time = container.querySelector('[data-message-id="a1"] .chat-message__usage-badge-time');
    expect(time?.getAttribute('title')).toBe('The model took 14.2 seconds for this message');
    expect(badgeText('u1')).toBeNull();
  });

  it('formats a call over a minute as minutes and seconds', () => {
    renderPanel([assistant('a1', { inputTokens: 5, outputTokens: 5, modelDurationMs: 65_000 })]);
    expect(badgeText('a1')).toBe('↑ 5 · ↓ 5 · 1m 05s');
  });

  it('shows tokens only when no time was recorded', () => {
    renderPanel([assistant('a1', { inputTokens: 10, outputTokens: 2 })]);
    expect(badgeText('a1')).toBe('↑ 10 · ↓ 2');
  });

  it('shows no badge when the message has no usage', () => {
    renderPanel([assistant('a1')]);
    expect(badgeText('a1')).toBeNull();
  });

  it("prefers the host's usageByMessageId over metadata", () => {
    const hostUsage: ChatMessageUsage = {
      inputTokens: 1,
      outputTokens: 1,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      originalTokens: 0,
      compressedTokens: 0,
    };
    renderPanel(
      [assistant('a1', { inputTokens: 999, outputTokens: 999, modelDurationMs: 3000 })],
      new Map([['a1', hostUsage]])
    );
    expect(badgeText('a1')).toBe('↑ 1 · ↓ 1');
  });
});
