/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). ChatMessage must call the same hooks on
 * every render: re-rendering one instance as a tool message, or with a
 * custom render, used to hit early returns placed BEFORE its hooks, so React
 * threw "Rendered fewer hooks than expected". Also covers
 * MarkdownPluginProvider reaching ChatMessage's markdown.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  ChatMessage,
  MarkdownPluginProvider,
  type ChatMessageData,
  type ChatMessageProps,
  type MarkdownPlugins,
} from '@pyxis-llm/react';

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
  act(() => {
    root.unmount();
  });
  container.remove();
  consoleError.mockRestore();
});

function render(props: ChatMessageProps, plugins?: MarkdownPlugins) {
  const msg = <ChatMessage {...props} />;
  act(() => {
    root.render(
      <MantineProvider>
        {plugins ? <MarkdownPluginProvider plugins={plugins}>{msg}</MarkdownPluginProvider> : msg}
      </MantineProvider>
    );
  });
}

function hookOrderErrors(): unknown[][] {
  return consoleError.mock.calls.filter((args: unknown[]) =>
    args.some((a: unknown) => /hooks|order of Hooks/i.test(String(a)))
  );
}

const assistant: ChatMessageData = { id: 'm1', role: 'assistant', content: 'hello **world**' };

// Visible content differs from raw, so the raw/clean eye toggle (useState) renders.
const hideSecret: ChatMessageProps['transformVisibleContent'] = (s) => s.replace(' SECRET', '');
const withSecret: ChatMessageData = { ...assistant, content: 'hello **world** SECRET' };

function rawToggle(): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>(
    'button[aria-label="Show raw message"], button[aria-label="Show cleaned message"]'
  );
}

describe('ChatMessage hook order', () => {
  // React 18 does NOT throw when an early return skips *all* hooks: it records
  // an empty hook list and silently remounts them next render. The observable
  // symptom of the old bug was therefore lost state, which is what we assert.
  it('keeps hook state across a role "tool" / custom-render detour', () => {
    for (const detour of [
      { message: { ...withSecret, role: 'tool' } },
      { message: withSecret, renderCustomMessage: () => <span>custom-card</span> },
    ] satisfies ChatMessageProps[]) {
      render({ message: withSecret, transformVisibleContent: hideSecret });
      if (rawToggle()?.getAttribute('aria-label') === 'Show cleaned message') {
        act(() => rawToggle()!.click()); // reset from previous iteration
      }
      act(() => rawToggle()!.click());
      expect(rawToggle()!.getAttribute('aria-label')).toBe('Show cleaned message');

      render({ ...detour, transformVisibleContent: hideSecret });
      render({ message: withSecret, transformVisibleContent: hideSecret });
      expect(rawToggle()!.getAttribute('aria-label')).toBe('Show cleaned message');
    }
    expect(hookOrderErrors()).toEqual([]);
  });

  it('survives switching a rendered message to role "tool" and back', () => {
    render({ message: assistant });
    expect(container.textContent).toContain('world');

    expect(() => render({ message: { ...assistant, role: 'tool' } })).not.toThrow();
    expect(container.textContent).not.toContain('world');

    expect(() => render({ message: assistant })).not.toThrow();
    expect(container.textContent).toContain('world');
    expect(hookOrderErrors()).toEqual([]);
  });

  it('survives toggling renderCustomMessage on and off', () => {
    render({ message: assistant });

    expect(() =>
      render({ message: assistant, renderCustomMessage: () => <span>custom-card</span> })
    ).not.toThrow();
    expect(container.textContent).toContain('custom-card');

    expect(() => render({ message: assistant, renderCustomMessage: () => undefined })).not.toThrow();
    expect(container.textContent).toContain('world');
    expect(hookOrderErrors()).toEqual([]);
  });
});

// Stable (module-level) plugin list, as the MarkdownPlugins docs require.
type HastNode = { type: string; tagName?: string; properties?: Record<string, unknown>; children?: HastNode[] };
const tagParagraphs = () => (tree: HastNode) => {
  const walk = (n: HastNode) => {
    if (n.type === 'element' && n.tagName === 'p') {
      n.properties = { ...n.properties, dataPlugin: 'yes' };
    }
    n.children?.forEach(walk);
  };
  walk(tree);
};
const plugins: MarkdownPlugins = { rehypePlugins: [tagParagraphs] };

describe('MarkdownPluginProvider', () => {
  it('applies registered rehype plugins without dropping GFM', () => {
    render({ message: { ...assistant, content: 'para\n\n| a | b |\n|---|---|\n| 1 | 2 |' } }, plugins);
    expect(container.querySelector('p[data-plugin="yes"]')).not.toBeNull();
    expect(container.querySelector('table')).not.toBeNull();
  });
});
