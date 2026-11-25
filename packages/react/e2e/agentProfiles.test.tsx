/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { AgentProfileList, type AgentProfileListProps } from '@pyxis-llm/react';

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

const dynamic = {
  id: 'a1',
  name: 'Reviewer',
  description: 'reviews',
  systemPromptSections: [{ id: 's1', content: 'be nice', tags: [] }],
  memoryEnabled: false,
  toolMapping: [],
  toolPolicy: 0,
  createdAt: 'x',
  updatedAt: 'x',
  isStatic: false,
};
const stat = { ...dynamic, id: 'st', name: 'Builtin', isStatic: true };

function props(over: Partial<AgentProfileListProps> = {}): AgentProfileListProps {
  return {
    profiles: [dynamic, stat],
    tools: [{ name: 'read_code' }],
    modelOptions: [],
    onCreate: vi.fn(),
    onUpdate: vi.fn(),
    onDelete: vi.fn(),
    ...over,
  };
}

function render(p: AgentProfileListProps) {
  act(() => {
    root.render(
      <MantineProvider>
        <AgentProfileList {...p} />
      </MantineProvider>
    );
  });
}

describe('AgentProfileList', () => {
  it('lists profiles; static ones have no Delete', () => {
    render(props());
    expect(container.textContent).toContain('Reviewer');
    expect(container.textContent).toContain('Builtin');
    expect(container.querySelectorAll('button[aria-label^="Delete"]').length).toBe(1);
  });

  it('deletes a dynamic profile', async () => {
    const p = props();
    render(p);
    await act(async () => {
      clickButton('Delete');
    });
    expect(p.onDelete).toHaveBeenCalledWith('a1');
  });

  it('edits and saves a dynamic profile including tool mapping', async () => {
    const p = props();
    render(p);
    clickButton('Edit');
    const select = container.querySelector('select[aria-label="Tool read_code"]') as HTMLSelectElement;
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(select, 'disabled');
      select.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => {
      clickButton('Save');
    });
    expect(p.onUpdate).toHaveBeenCalledWith(
      'a1',
      expect.objectContaining({
        name: 'Reviewer',
        toolMapping: [{ toolName: 'read_code', enabled: false }],
      })
    );
  });

  it('saves an existing PROFILE_ONLY policy untouched, with all fields', async () => {
    const full = {
      ...dynamic,
      toolPolicy: 2,
      memoryEnabled: true,
      llmProviderId: 'prov',
      defaultModel: 'mod',
    };
    const p = props({ profiles: [full] });
    render(p);
    clickButton('Edit');
    await act(async () => {
      clickButton('Save');
    });
    expect(p.onUpdate).toHaveBeenCalledWith(
      'a1',
      expect.objectContaining({
        name: 'Reviewer',
        description: 'reviews',
        systemPromptSections: [{ id: 's1', content: 'be nice', tags: [] }],
        memoryEnabled: true,
        llmProviderId: 'prov',
        defaultModel: 'mod',
        toolPolicy: 2,
      })
    );
  });

  it('saves a changed tool policy', async () => {
    const p = props();
    render(p);
    clickButton('Edit');
    const label = Array.from(container.querySelectorAll('label')).find(
      (l) => l.textContent?.trim() === 'Tool policy'
    )!;
    const select = container.querySelector(`[id="${label.htmlFor}"]`) as HTMLSelectElement;
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(select, '2');
      select.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => {
      clickButton('Save');
    });
    expect(p.onUpdate).toHaveBeenCalledWith('a1', expect.objectContaining({ toolPolicy: 2 }));
  });

  it('opens static profiles read-only', () => {
    render(props());
    clickButton('View');
    expect(container.textContent).toContain('read-only');
    expect(
      Array.from(container.querySelectorAll('button')).some((b) => b.textContent === 'Save')
    ).toBe(false);
  });

  it('creates a profile', async () => {
    const p = props();
    render(p);
    clickButton('New profile');
    const input = container.querySelector('input') as HTMLInputElement;
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, 'Fresh');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => {
      clickButton('Save');
    });
    expect(p.onCreate).toHaveBeenCalledWith(expect.objectContaining({ name: 'Fresh' }));
  });
});
