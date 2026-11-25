/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed tests for the send mode: which behavior plain Enter / the button
 * and modifier+Enter give, what the button says, and that holding ⌥/Ctrl/⌘ in
 * the input flips the real Composer's button live.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  Composer,
  sendBehaviorFor,
  sendHintFor,
  sendLabelFor,
  useSendMode,
  type SendModeContext,
} from '@pyxis-llm/react';

describe('sendBehaviorFor / sendLabelFor', () => {
  const idle: SendModeContext = { streaming: false };
  const running: SendModeContext = { streaming: true };
  const paused: SendModeContext = { streaming: false, queuePaused: true };
  const pausedRunning: SendModeContext = { streaming: true, queuePaused: true };

  it('normal queue: Enter queues, the modifier interrupts', () => {
    expect(sendBehaviorFor(running, false)).toBe('queue');
    expect(sendBehaviorFor(running, true)).toBe('interrupt');
    expect(sendLabelFor('queue', running)).toBe('Queue');
    expect(sendLabelFor('interrupt', running)).toBe('Interrupt');
  });

  it('idle with an empty queue: both just send', () => {
    expect(sendLabelFor(sendBehaviorFor(idle, false), idle)).toBe('Send');
    expect(sendLabelFor(sendBehaviorFor(idle, true), idle)).toBe('Send');
  });

  it('idle but entries waiting: Enter queues behind them', () => {
    const waiting = { streaming: false, queueLength: 2 };
    expect(sendLabelFor(sendBehaviorFor(waiting, false), waiting)).toBe('Queue');
    expect(sendLabelFor(sendBehaviorFor(waiting, true), waiting)).toBe('Send');
  });

  it('paused queue inverts: Enter sends now, the modifier queues', () => {
    expect(sendBehaviorFor(paused, false)).toBe('interrupt');
    expect(sendBehaviorFor(paused, true)).toBe('queue');
    expect(sendLabelFor(sendBehaviorFor(paused, false), paused)).toBe('Send');
    expect(sendLabelFor(sendBehaviorFor(paused, true), paused)).toBe('Queue');
    expect(sendLabelFor(sendBehaviorFor(pausedRunning, false), pausedRunning)).toBe('Interrupt');
    expect(sendLabelFor(sendBehaviorFor(pausedRunning, true), pausedRunning)).toBe('Queue');
  });

  it('a pinned default wins; the modifier still gives the other', () => {
    const pinned = { streaming: true, queuePaused: true, defaultSendBehavior: 'queue' as const };
    expect(sendBehaviorFor(pinned, false)).toBe('queue');
    expect(sendBehaviorFor(pinned, true)).toBe('interrupt');
  });

  it('the hint names both keys', () => {
    expect(sendHintFor(running)).toBe('Enter to queue · ⌥/Ctrl+Enter to interrupt');
    expect(sendHintFor(paused)).toBe('Enter to send · ⌥/Ctrl+Enter to queue');
  });
});

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

const sent: Array<{ text: string; alternate: boolean }> = [];

function Harness({ ctx }: { ctx: SendModeContext }) {
  const sendMode = useSendMode(ctx);
  return (
    <MantineProvider>
      <Composer
        sendMode={sendMode}
        streaming={ctx.streaming}
        onSend={(text, alternate) => sent.push({ text, alternate })}
        value="draft"
      />
    </MantineProvider>
  );
}

const sendButton = () => container.querySelector('.llmd-composer__send') as HTMLButtonElement;
const textarea = () => container.querySelector('textarea') as HTMLTextAreaElement;
function key(type: 'keydown' | 'keyup', init: KeyboardEventInit) {
  act(() => {
    textarea().dispatchEvent(new KeyboardEvent(type, { bubbles: true, ...init }));
  });
}

describe('Composer + useSendMode', () => {
  it('labels the button and flips it while ⌥/Ctrl is held in the input', () => {
    act(() => root.render(<Harness ctx={{ streaming: true }} />));
    expect(sendButton().textContent).toBe('Queue');
    expect(sendButton().getAttribute('aria-label')).toBe('Queue');

    key('keydown', { key: 'Alt', altKey: true });
    expect(sendButton().textContent).toBe('Interrupt');
    key('keyup', { key: 'Alt', altKey: false });
    expect(sendButton().textContent).toBe('Queue');

    key('keydown', { key: 'Control', ctrlKey: true });
    expect(sendButton().textContent).toBe('Interrupt');
    // Leaving the input with the key still down must not leave it flipped.
    act(() => {
      textarea().dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    });
    expect(sendButton().textContent).toBe('Queue');
  });

  it('paused queue: the button says Send, and Queue while the modifier is held', () => {
    act(() => root.render(<Harness ctx={{ streaming: false, queuePaused: true }} />));
    expect(sendButton().textContent).toBe('Send');
    key('keydown', { key: 'Alt', altKey: true });
    expect(sendButton().textContent).toBe('Queue');
  });

  it('⌥+Enter and Ctrl+Enter both submit as the alternate send; plain Enter does not', () => {
    sent.length = 0;
    act(() => root.render(<Harness ctx={{ streaming: true }} />));
    key('keydown', { key: 'Enter' });
    key('keydown', { key: 'Enter', altKey: true });
    key('keydown', { key: 'Enter', ctrlKey: true });
    expect(sent.map((s) => s.alternate)).toEqual([false, true, true]);
  });
});
