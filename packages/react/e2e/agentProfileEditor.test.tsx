/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * AgentProfileEditor's Instructions box and additional sections. Pure view and
 * mapping logic: no llmd binary is spawned.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import {
  AgentProfileEditor,
  INSTRUCTIONS_SECTION_ID,
  IDENTITY_SECTION_ID,
  buildProfileSections,
} from '@pyxis-llm/react';
import type { LlmdAgentProfile } from '@pyxis-llm/client';

describe('buildProfileSections', () => {
  it('puts instructions first and drops fully empty rows', () => {
    const r = buildProfileSections('Be kind.', [
      { id: '', content: ' ', tags: [] },
      { id: ' extra ', content: 'More.', tags: ['t'] },
    ]);
    expect(r).toEqual({
      sections: [
        { id: INSTRUCTIONS_SECTION_ID, content: 'Be kind.', tags: [] },
        { id: 'extra', content: 'More.', tags: ['t'] },
      ],
    });
  });

  it('leaves out blank instructions', () => {
    expect(buildProfileSections('  ', [])).toEqual({ sections: [] });
  });

  it('refuses an additional section with content but no id', () => {
    const r = buildProfileSections('', [{ id: ' ', content: 'Lost?', tags: [] }]);
    expect(r).toEqual({ error: 'Additional section 1 needs an id.' });
  });

  it('refuses the reserved ids', () => {
    for (const id of [INSTRUCTIONS_SECTION_ID, IDENTITY_SECTION_ID]) {
      const r = buildProfileSections('', [{ id, content: 'x', tags: [] }]);
      expect('error' in r && r.error.includes('reserved')).toBe(true);
    }
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
  act(() => root.unmount());
  container.remove();
});

function render(profile: LlmdAgentProfile | undefined, onSave: (...args: unknown[]) => void) {
  act(() => {
    root.render(
      <MantineProvider>
        <AgentProfileEditor profile={profile} tools={[]} modelOptions={[]} onSave={onSave} />
      </MantineProvider>
    );
  });
}

function textareaByLabel(label: string): HTMLTextAreaElement {
  const el = Array.from(container.querySelectorAll('label')).find((l) => l.textContent?.startsWith(label));
  const id = el?.getAttribute('for');
  const ta = id ? (document.getElementById(id) as HTMLTextAreaElement | null) : null;
  if (!ta) throw new Error(`no field labelled ${label}`);
  return ta;
}

describe('AgentProfileEditor', () => {
  it('shows the instructions section in the Instructions box and saves it back with the others', async () => {
    const onSave = vi.fn();
    const profile = {
      id: 'p1',
      name: 'Ada',
      description: '',
      systemPromptSections: [
        { id: 'extra', content: 'More.', tags: [] },
        { id: INSTRUCTIONS_SECTION_ID, content: 'Be kind.', tags: [] },
      ],
      memoryEnabled: false,
      toolMapping: [],
      isStatic: false,
      toolPolicy: 0,
    } as unknown as LlmdAgentProfile;
    render(profile, onSave);

    expect(textareaByLabel('Instructions').value).toBe('Be kind.');
    expect(container.textContent).toContain('additional sections (1)');

    const save = Array.from(container.querySelectorAll('button')).find((b) => b.textContent === 'Save')!;
    await act(async () => {
      save.click();
    });
    expect(onSave).toHaveBeenCalledTimes(1);
    const spec = onSave.mock.calls[0][0] as { systemPromptSections: { id: string }[] };
    expect(spec.systemPromptSections.map((s) => s.id)).toEqual([INSTRUCTIONS_SECTION_ID, 'extra']);
  });
});
