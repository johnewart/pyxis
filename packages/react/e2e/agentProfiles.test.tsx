/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  AgentProfileList,
  buildAgentProfileOverlay,
  overlayFormState,
  type AgentProfileListProps,
} from '@pyxis-llm/react';

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

  it('opens a static profile in the customize editor and saves only the changes', async () => {
    const base = {
      ...stat,
      systemPromptSections: [{ id: 'workspace', content: 'Built-in rules', tags: [] }],
      toolMapping: [{ toolName: 'read_code', enabled: true }],
      customized: [],
    };
    const onLoadOverlay = vi.fn(async () => ({
      overlay: { baseId: 'st', toolOverrides: [], sectionReplacements: [], extraSections: [], updatedAt: '' },
      base,
    }));
    const onSaveOverlay = vi.fn();
    const p = props({ profiles: [{ ...stat, customized: [4] }], onLoadOverlay, onSaveOverlay, onResetOverlay: vi.fn() });
    render(p);
    expect(container.textContent).toContain('customized');
    await act(async () => {
      clickButton('Customize');
    });
    expect(onLoadOverlay).toHaveBeenCalledWith('st');
    expect(container.textContent).toContain('Built-in rules');
    // Nothing stored yet, so there is nothing to reset.
    const reset = Array.from(container.querySelectorAll('button')).find((b) => b.textContent === 'Reset to built-in')!;
    expect(reset.disabled).toBe(true);
    const select = container.querySelector('select[aria-label="Tool read_code"]') as HTMLSelectElement;
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(select, 'off');
      select.dispatchEvent(new Event('change', { bubbles: true }));
    });
    expect(container.textContent).toContain('changed');
    await act(async () => {
      clickButton('Save changes');
    });
    expect(onSaveOverlay).toHaveBeenCalledWith({
      baseId: 'st',
      toolOverrides: [{ toolName: 'read_code', enabled: false }],
      sectionReplacements: [],
      extraSections: [],
    });
  });

  it('without overlay callbacks a static profile stays read-only', () => {
    render(props());
    expect(Array.from(container.querySelectorAll('button')).some((b) => b.textContent === 'Customize')).toBe(false);
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

describe('buildAgentProfileOverlay', () => {
  const base = {
    ...dynamic,
    id: 'ed',
    isStatic: true,
    customized: [],
    systemPromptSections: [{ id: 'workspace', content: 'rules', tags: [] }],
    toolMapping: [{ toolName: 'review', enabled: true }],
  };
  const empty = { model: null, memory: 'inherit' as const, tools: {}, replacements: {}, extras: [] };

  it('drops settings equal to the built-in ones', () => {
    const r = buildAgentProfileOverlay(base, {
      ...empty,
      memory: 'off',
      tools: { review: true, other: false },
      replacements: { workspace: 'rules' },
      extras: [{ id: '', content: ' ', tags: [] }],
    });
    expect(r).toEqual({ overlay: { baseId: 'ed', toolOverrides: [], sectionReplacements: [], extraSections: [] } });
  });

  it('keeps real changes', () => {
    const r = buildAgentProfileOverlay(base, {
      model: { providerId: 'p', modelId: 'm' },
      memory: 'on',
      tools: { review: false, web: true },
      replacements: { workspace: 'my rules' },
      extras: [{ id: ' mine ', content: 'x', tags: [] }],
    });
    expect(r).toEqual({
      overlay: {
        baseId: 'ed',
        llmProviderId: 'p',
        defaultModel: 'm',
        memoryEnabled: true,
        toolOverrides: [
          { toolName: 'review', enabled: false },
          { toolName: 'web', enabled: true },
        ],
        sectionReplacements: [{ id: 'workspace', content: 'my rules', tags: [] }],
        extraSections: [{ id: 'mine', content: 'x', tags: [] }],
      },
    });
  });

  it('refuses an extra section that reuses a built-in id', () => {
    const r = buildAgentProfileOverlay(base, { ...empty, extras: [{ id: 'workspace', content: 'x', tags: [] }] });
    expect('error' in r && r.error).toContain('override it instead');
  });

  it('round-trips a stored overlay through the form state', () => {
    const ov = {
      baseId: 'ed',
      defaultModel: 'm',
      llmProviderId: 'p',
      memoryEnabled: false,
      toolOverrides: [{ toolName: 'review', enabled: false }],
      sectionReplacements: [{ id: 'workspace', content: 'mine', tags: [] }],
      extraSections: [],
      updatedAt: 'x',
    };
    const r = buildAgentProfileOverlay({ ...base, memoryEnabled: true }, overlayFormState(ov));
    const { updatedAt: _u, ...rest } = ov;
    expect(r).toEqual({ overlay: rest });
  });
});
