/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). The plugin registry: a host adds a fence
 * language, a display card type and its own attachment kind, through a
 * provider or the global registry, and they render (and are placed) with no
 * other wiring. Pins precedence: providers beat global registrations, later
 * plugins beat earlier ones, and anything beats llmd's built-ins.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  ANCHORLESS_CARD_MESSAGE_ID_PREFIX,
  buildDisplayCardAttachment,
  formatAttachmentTag,
  type LlmdMessageAttachment,
} from '@pyxis-llm/core';
import {
  ChatMessage,
  ChatPanel,
  LlmdPluginProvider,
  registerLlmdPlugin,
  resolveLlmdPlugins,
  type AttachmentRenderProps,
  type DisplayCardRenderProps,
  type LlmdPlugin,
} from '@pyxis-llm/react';

type BudgetBody = { total: number };

function BudgetView({ card }: DisplayCardRenderProps<BudgetBody>) {
  return <span data-testid="budget-total">{card.body.total}</span>;
}

function HostThing({ attachment, dismiss }: AttachmentRenderProps) {
  return (
    <button type="button" data-testid={`host-thing-${attachment.id}`} onClick={dismiss}>
      host card
    </button>
  );
}

const budgetPlugin: LlmdPlugin = {
  id: 'test.budget',
  languages: { 'budget-csv': ({ code }) => <span data-testid="budget-fence">{code.length}</span> },
  displayCards: {
    budget: {
      decode: (b) => (typeof b.total === 'number' ? { total: b.total } : undefined),
      render: BudgetView,
    },
  },
  attachments: {
    'host.thing': { render: HostThing, placement: { persistent: true } },
  },
};

const budgetCard = (id: string, total: unknown) =>
  buildDisplayCardAttachment({ id, type: 'budget', title: 'October', caption: 'Rent is up', body: { total } as never });

const index = (...atts: LlmdMessageAttachment[]) => new Map(atts.map((a) => [a.id, a]));

describe('resolveLlmdPlugins', () => {
  it('lets later plugins win and collects placement rules', () => {
    const A = () => null;
    const B = () => null;
    const resolved = resolveLlmdPlugins([
      { id: 'a', languages: { Foo: A } },
      { id: 'b', languages: { foo: B }, attachments: { k: { placement: { persistent: true } } } },
    ]);
    expect(resolved.languages.foo).toBe(B);
    expect(resolved.attachmentKindPlugins).toEqual([{ kind: 'k', persistent: true }]);
  });
});

describe('plugins in the chat', () => {
  let container: HTMLDivElement;
  let root: Root;
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
  });
  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    consoleError.mockRestore();
  });

  function render(node: React.ReactNode) {
    act(() => {
      root.render(<MantineProvider>{node}</MantineProvider>);
    });
  }

  it('renders a host display card type in the shared frame (title + caption)', () => {
    render(
      <LlmdPluginProvider plugins={[budgetPlugin]}>
        <ChatMessage
          message={{ role: 'assistant', content: formatAttachmentTag('b1'), id: 'm1' }}
          attachmentIndex={index(budgetCard('b1', 1234))}
        />
      </LlmdPluginProvider>
    );
    const card = container.querySelector('[data-testid="display-card-b1"]')!;
    expect(card.querySelector('.llmd-display-card__title')?.textContent).toBe('October');
    expect(card.querySelector('[data-testid="budget-total"]')?.textContent).toBe('1234');
    expect(card.querySelector('.llmd-display-card__caption')?.textContent).toBe('Rent is up');
  });

  it('shows "[budget unavailable]" when the type rejects the body', () => {
    render(
      <LlmdPluginProvider plugins={[budgetPlugin]}>
        <ChatMessage
          message={{ role: 'assistant', content: formatAttachmentTag('b2'), id: 'm1' }}
          attachmentIndex={index(budgetCard('b2', 'lots'))}
        />
      </LlmdPluginProvider>
    );
    expect(container.querySelector('[data-testid="card-b2-unavailable"]')?.textContent).toBe('[budget unavailable]');
  });

  it('renders a fence language from a plugin', () => {
    render(
      <LlmdPluginProvider plugins={[budgetPlugin]}>
        <ChatMessage message={{ role: 'assistant', content: '```budget-csv\na,b\n```', id: 'm1' }} />
      </LlmdPluginProvider>
    );
    expect(container.querySelector('[data-testid="budget-fence"]')?.textContent).toBe('3');
  });

  it('renders a host attachment kind, wires dismiss, and places it by the plugin\'s rule', () => {
    const onDismiss = vi.fn();
    const thing: LlmdMessageAttachment = { id: 't1', kind: 'host.thing', payloadJson: '{}' };
    const unregister = registerLlmdPlugin(budgetPlugin);
    try {
      render(
        <ChatPanel
          messages={[{ role: 'assistant', content: 'hi', id: 'a1' }]}
          onSend={() => {}}
          attachmentIndex={index(thing)}
          onAskCardDismiss={onDismiss}
        />
      );
      // persistent placement came from the plugin: an own row at the end.
      const rows = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'));
      expect(rows).toEqual(['a1', `${ANCHORLESS_CARD_MESSAGE_ID_PREFIX}t1`]);
      const btn = container.querySelector('[data-testid="host-thing-t1"]') as HTMLButtonElement;
      act(() => btn.click());
      expect(onDismiss).toHaveBeenCalledWith('t1');
    } finally {
      unregister();
    }
  });

  it('a provider beats the global registry, and anything beats the built-in image card', () => {
    const fromProvider: LlmdPlugin = {
      id: 'test.override',
      displayCards: { image: { render: () => <span data-testid="custom-image">mine</span>, ownsChrome: true } },
    };
    const unregister = registerLlmdPlugin({
      id: 'test.global',
      displayCards: { image: { render: () => <span data-testid="global-image">global</span>, ownsChrome: true } },
    });
    try {
      const att = buildDisplayCardAttachment({ id: 'i1', type: 'image', body: {} });
      render(
        <LlmdPluginProvider plugins={[fromProvider]}>
          <ChatMessage message={{ role: 'assistant', content: formatAttachmentTag('i1'), id: 'm1' }} attachmentIndex={index(att)} />
        </LlmdPluginProvider>
      );
      expect(container.querySelector('[data-testid="custom-image"]')).not.toBeNull();
      expect(container.querySelector('[data-testid="global-image"]')).toBeNull();
    } finally {
      unregister();
    }
  });

  it('an unknown attachment kind degrades to a visible placeholder', () => {
    const att: LlmdMessageAttachment = { id: 'u1', kind: 'nobody.knows', payloadJson: '{}' };
    render(<ChatMessage message={{ role: 'assistant', content: formatAttachmentTag('u1'), id: 'm1' }} attachmentIndex={index(att)} />);
    expect(container.querySelector('[data-testid="card-u1-unavailable"]')?.textContent).toBe('[card unavailable]');
  });
});
