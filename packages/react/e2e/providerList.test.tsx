/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ProviderList, type ProviderListProps } from '@pyxis-llm/react';

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

function clickButton(text: string) {
  const btn = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === text
  );
  if (!btn) throw new Error(`no button "${text}"`);
  act(() => {
    btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
}

function props(over: Partial<ProviderListProps> = {}): ProviderListProps {
  return {
    providers: [
      {
        id: 'p1',
        type: 'anthropic',
        defaultModel: 'm1',
        catalogModelCount: 1,
        userRegistered: true,
        stateful: false,
      },
    ],
    models: [{ providerId: 'p1', modelId: 'm1', displayName: 'Model One' }],
    onRefresh: vi.fn(),
    onAdd: vi.fn(),
    onUpdate: vi.fn(),
    onRemove: vi.fn(),
    loadConfig: vi.fn(async () => ({ type: 'anthropic' as const, apiKey: 'k' })),
    ...over,
  };
}

function render(p: ProviderListProps) {
  act(() => {
    root.render(
      <MantineProvider>
        <ProviderList {...p} />
      </MantineProvider>
    );
  });
}

describe('ProviderList', () => {
  it('lists providers and reveals catalog models on expand', () => {
    render(props());
    expect(container.textContent).toContain('p1');
    clickButton('Show models');
    expect(container.textContent).toContain('Model One');
  });

  it('shows last refresh time or never refreshed', () => {
    const base = props().providers[0]!;
    render(
      props({
        providers: [
          { ...base, id: 'p1', modelsRefreshedUnixMs: 1700000000000 },
          { ...base, id: 'p2', modelsRefreshedUnixMs: 0 },
        ],
      })
    );
    const p1 = container.querySelector('[data-testid="refreshed-p1"]')!.textContent!;
    expect(p1).toContain('refreshed ');
    expect(p1).not.toContain('never');
    expect(container.querySelector('[data-testid="refreshed-p2"]')!.textContent).toBe(
      'never refreshed'
    );
  });

  it('refreshes and removes a provider', async () => {
    const p = props();
    render(p);
    await act(async () => {
      clickButton('Refresh');
    });
    expect(p.onRefresh).toHaveBeenCalledWith('p1');
    await act(async () => {
      clickButton('Remove');
    });
    expect(p.onRemove).toHaveBeenCalledWith('p1');
  });

  it('opens the edit form seeded from loadConfig and saves via onUpdate', async () => {
    const p = props();
    render(p);
    await act(async () => {
      clickButton('Edit');
    });
    expect(p.loadConfig).toHaveBeenCalledWith('p1');
    await act(async () => {
      clickButton('Save');
    });
    expect(p.onUpdate).toHaveBeenCalledWith('p1', expect.objectContaining({ type: 'anthropic' }));
  });

  it('shows the add form', () => {
    render(props());
    clickButton('Add provider');
    expect(container.textContent).toContain('Provider id');
  });
});
