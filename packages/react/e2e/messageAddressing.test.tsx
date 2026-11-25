/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Message addressing: `data-message-id` on every message root, decorations on
 * any role, and `ChatPanel`'s scroll-to-message plumbing.
 *
 * Unlike its siblings in this directory this test spawns no llmd binary and
 * uses no driver — it renders `ChatPanel` against literal message arrays,
 * because everything under test is pure view behavior. Run it via
 * `npm run test:unit` in this package.
 *
 * jsdom has no layout engine, so `scrollTop`, `scrollHeight`, `clientHeight`
 * and `getBoundingClientRect` are all stubbed by `fakeLayout` below to model a
 * fixed-height list in a shorter viewport. That means these tests verify the
 * decision logic — which element is targeted, where scrollTop lands, and when
 * auto-scroll defers — and NOT that a real browser agrees about pixels.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  ChatPanel,
  type ChatMessageData,
  type ChatPanelHandle,
  type ChatPanelScrollRequest,
} from '@pyxis-llm/react';
import type { TurnDecorationView } from '@pyxis-llm/core';

let container: HTMLDivElement;
let root: Root;

const realGetBoundingClientRect = Element.prototype.getBoundingClientRect;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  Element.prototype.getBoundingClientRect = realGetBoundingClientRect;
  act(() => {
    root.unmount();
  });
  container.remove();
});

function msg(id: string, role: 'user' | 'assistant', content = `body of ${id}`): ChatMessageData {
  return { id, role, content } as ChatMessageData;
}

const ROW_HEIGHT = 100;
const VIEWPORT_HEIGHT = 250;

function rect(top: number, height: number): DOMRect {
  return { top, height, bottom: top + height } as DOMRect;
}

/**
 * Installs a fake layout on the panel's scroll viewport: every message is
 * ROW_HEIGHT tall, stacked from the top, inside a VIEWPORT_HEIGHT-tall
 * viewport. `scrollTop` becomes a real read/write property (jsdom's own always
 * reads 0 since it has no scrolling box), and rects are derived from it so they
 * move as the viewport scrolls, like real ones do.
 *
 * The rect override goes on `Element.prototype` and resolves a message's index
 * at call time rather than being stamped onto each node once. That matters:
 * React mounts fresh nodes on re-render, and ChatPanel's effects measure them
 * during that same commit — a per-node patch applied afterwards would hand the
 * code under test zeroed jsdom rects at exactly the moment the retry path runs,
 * and the test would pass while measuring nothing. Restored in `afterEach`.
 */
function fakeLayout(viewport: HTMLElement): void {
  let scrollTop = 0;
  Object.defineProperty(viewport, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (v: number) => {
      scrollTop = v;
    },
  });
  Object.defineProperty(viewport, 'clientHeight', {
    configurable: true,
    get: () => VIEWPORT_HEIGHT,
  });
  Object.defineProperty(viewport, 'scrollHeight', {
    configurable: true,
    get: () => viewport.querySelectorAll('[data-message-id]').length * ROW_HEIGHT,
  });
  Element.prototype.getBoundingClientRect = function (this: Element): DOMRect {
    if (this === viewport) return rect(0, VIEWPORT_HEIGHT);
    if (this.hasAttribute('data-message-id') && viewport.contains(this)) {
      const idx = Array.from(viewport.querySelectorAll('[data-message-id]')).indexOf(this);
      if (idx >= 0) return rect(idx * ROW_HEIGHT - viewport.scrollTop, ROW_HEIGHT);
    }
    return realGetBoundingClientRect.call(this);
  };
}

function renderPanel(props: {
  messages: ChatMessageData[];
  scrollRequest?: ChatPanelScrollRequest;
  decorationsByMessageId?: Map<string, TurnDecorationView[]>;
  renderDecorations?: (d: TurnDecorationView[]) => React.ReactNode;
  handleRef?: React.MutableRefObject<ChatPanelHandle | null>;
}) {
  act(() => {
    root.render(
      <MantineProvider>
        <ChatPanel
          ref={props.handleRef}
          messages={props.messages}
          onSend={() => {}}
          scrollRequest={props.scrollRequest}
          decorationsByMessageId={props.decorationsByMessageId}
          renderDecorations={props.renderDecorations}
        />
      </MantineProvider>
    );
  });
}

function decoration(kind: string): TurnDecorationView {
  return {
    turnSeq: 1,
    turnId: 't1',
    target: 'turn',
    toolCallId: '',
    kind,
    uri: `host://pin/${kind}`,
    createdAtUnix: 0,
  };
}

describe('message addressing', () => {
  it('stamps data-message-id and data-message-role on every message root', () => {
    renderPanel({ messages: [msg('m1', 'user'), msg('m2', 'assistant')] });

    const roots = Array.from(container.querySelectorAll('[data-message-id]'));
    expect(roots.map((el) => el.getAttribute('data-message-id'))).toEqual(['m1', 'm2']);
    expect(roots.map((el) => el.getAttribute('data-message-role'))).toEqual([
      'user',
      'assistant',
    ]);
    // The addressable element is the message root, so a host that walks up from
    // a DOM selection finds the id on the same node that carries the bubble.
    expect(roots[0].classList.contains('chat-message')).toBe(true);
  });

  it('renders decorations on user messages, not just assistant ones', () => {
    const decorations = new Map([
      ['m1', [decoration('pin')]],
      ['m2', [decoration('host_provenance')]],
    ]);
    renderPanel({
      messages: [msg('m1', 'user'), msg('m2', 'assistant')],
      decorationsByMessageId: decorations,
      renderDecorations: (ds) => (
        <span data-testid="deco">{ds.map((d) => d.kind).join(',')}</span>
      ),
    });

    const byMessage = Array.from(container.querySelectorAll('[data-message-id]')).map(
      (el) => [
        el.getAttribute('data-message-id'),
        el.querySelector('[data-testid="deco"]')?.textContent ?? null,
      ]
    );
    expect(byMessage).toEqual([
      ['m1', 'pin'],
      ['m2', 'host_provenance'],
    ]);
  });
});

describe('ChatPanel scroll requests', () => {
  const messages = ['m1', 'm2', 'm3', 'm4', 'm5', 'm6'].map((id, i) =>
    msg(id, i % 2 === 0 ? 'user' : 'assistant')
  );

  function setup() {
    const handleRef = React.createRef<ChatPanelHandle>() as React.MutableRefObject<ChatPanelHandle | null>;
    renderPanel({ messages, handleRef });
    const viewport = handleRef.current!.getViewport()!;
    fakeLayout(viewport);
    return { handleRef, viewport };
  }

  it('centers the requested message in the viewport', () => {
    const { handleRef, viewport } = setup();

    act(() => {
      handleRef.current!.scrollToMessage('m3');
    });

    // m3 is index 2: its top sits at 200, so centering it in a 250-tall
    // viewport puts scrollTop at 200 - 125 + 50 = 125.
    expect(viewport.scrollTop).toBe(125);
  });

  it('reports false for a message that is not rendered', () => {
    const { handleRef } = setup();
    let result: boolean | undefined;
    act(() => {
      result = handleRef.current!.scrollToMessage('nope');
    });
    expect(result).toBe(false);
  });

  it('re-fires for the same messageId when the token changes', () => {
    const handleRef = React.createRef<ChatPanelHandle>() as React.MutableRefObject<ChatPanelHandle | null>;
    renderPanel({ messages, handleRef, scrollRequest: { messageId: 'm3', token: 1 } });
    const viewport = handleRef.current!.getViewport()!;
    fakeLayout(viewport);

    // Re-render with the request already applied, then scroll elsewhere as a
    // user would and ask for the same message again.
    renderPanel({ messages, handleRef, scrollRequest: { messageId: 'm3', token: 1 } });
    viewport.scrollTop = 0;

    // Same token: a no-op, exactly like a plain `scrollToMessageId` prop.
    renderPanel({ messages, handleRef, scrollRequest: { messageId: 'm3', token: 1 } });
    expect(viewport.scrollTop).toBe(0);

    // New token, same id: fires again. This is the "click the same pin twice"
    // case that a bare id prop cannot express.
    renderPanel({ messages, handleRef, scrollRequest: { messageId: 'm3', token: 2 } });
    expect(viewport.scrollTop).toBe(125);
  });

  it('retries a request whose target has not loaded yet', () => {
    const handleRef = React.createRef<ChatPanelHandle>() as React.MutableRefObject<ChatPanelHandle | null>;
    const request: ChatPanelScrollRequest = { messageId: 'm5', token: 'deep-link' };

    // Deep link arrives before history does: m5 is not mounted, so nothing moves.
    renderPanel({ messages: messages.slice(0, 2), handleRef, scrollRequest: request });
    const viewport = handleRef.current!.getViewport()!;
    fakeLayout(viewport);
    expect(viewport.scrollTop).toBe(0);

    // History lands. The still-unsatisfied request resolves on its own, with no
    // new token and no imperative nudge — m5 is index 4, top 400, centered in a
    // 250-tall viewport → 400 - 125 + 50 = 325.
    renderPanel({ messages, handleRef, scrollRequest: request });
    expect(viewport.scrollTop).toBe(325);
  });

  it('suspends auto-scroll after a scroll request, and resumes at the bottom', () => {
    const handleRef = React.createRef<ChatPanelHandle>() as React.MutableRefObject<ChatPanelHandle | null>;
    renderPanel({ messages, handleRef });
    const viewport = handleRef.current!.getViewport()!;
    fakeLayout(viewport);

    act(() => {
      handleRef.current!.scrollToMessage('m2');
    });
    const parked = viewport.scrollTop;
    expect(parked).toBeLessThan(viewport.scrollHeight - viewport.clientHeight);

    // A reply streams in while the user is reading up-transcript. The view
    // must stay where it was put — this is the precedence rule.
    const grown = [...messages, msg('m7', 'assistant')];
    renderPanel({ messages: grown, handleRef });
    expect(viewport.scrollTop).toBe(parked);

    // Scrolling back to the bottom by hand re-arms auto-scroll...
    viewport.scrollTop = viewport.scrollHeight - viewport.clientHeight;
    act(() => {
      viewport.dispatchEvent(new Event('scroll'));
    });

    // ...so the next message chases again.
    const grownMore = [...grown, msg('m8', 'assistant')];
    renderPanel({ messages: grownMore, handleRef });
    expect(viewport.scrollTop).toBe(viewport.scrollHeight);
  });
});
