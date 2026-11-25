/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-level render coverage for ProviderConnectForm — no real llmd process
 * needed, this component only assembles a config object and calls back; the
 * gRPC round trip is the host's job (see docs on ProviderConnectFormProps).
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ProviderConnectForm, type ProviderConnectConfig } from '@pyxis-llm/react';

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

function render(ui: React.ReactElement) {
  act(() => {
    root.render(<MantineProvider>{ui}</MantineProvider>);
  });
}

function labelMatches(el: HTMLElement, label: string): boolean {
  const wrapper = el.closest(
    '.mantine-InputWrapper-root, .mantine-TextInput-root, .mantine-Textarea-root, .mantine-NumberInput-root'
  );
  if (!wrapper) return false;
  const labelEl = wrapper.querySelector('label');
  return labelEl?.textContent?.trim().startsWith(label) ?? false;
}

function fillInput(label: string, value: string) {
  const input = Array.from(container.querySelectorAll('input, textarea')).find((el) =>
    labelMatches(el as HTMLElement, label)
  ) as HTMLInputElement | HTMLTextAreaElement | undefined;
  if (!input) throw new Error(`no input found for label "${label}"`);
  const proto =
    input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, 'value')!.set!;
  act(() => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

function clickButton(text: string) {
  const btn = Array.from(container.querySelectorAll('button')).find((b) =>
    b.textContent?.trim().includes(text)
  );
  if (!btn) throw new Error(`no button found with text "${text}"`);
  act(() => {
    btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
}

describe('ProviderConnectForm', () => {
  it('create mode: assembles an anthropic config and calls onSubmit', async () => {
    const onSubmit = vi.fn(async (_config: ProviderConnectConfig) => {});

    render(
      <ProviderConnectForm providerId="my-anthropic" onProviderIdChange={() => {}} onSubmit={onSubmit} />
    );

    fillInput('API key', 'sk-test-123');
    fillInput('Default model (optional)', 'claude-opus-4');

    await act(async () => {
      clickButton('Connect');
    });

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const config = onSubmit.mock.calls[0][0] as ProviderConnectConfig;
    expect(config.type).toBe('anthropic');
    expect(config).toMatchObject({
      type: 'anthropic',
      apiKey: 'sk-test-123',
      defaultModel: 'claude-opus-4',
    });
  });

  it('disables submit until a required field (API key) is filled', () => {
    render(
      <ProviderConnectForm providerId="p1" onProviderIdChange={() => {}} onSubmit={vi.fn()} />
    );
    const connectBtn = Array.from(container.querySelectorAll('button')).find((b) =>
      b.textContent?.trim().includes('Connect')
    ) as HTMLButtonElement;
    expect(connectBtn.disabled).toBe(true);
  });

  it('edit mode: seeds fields from initialConfig, locks provider id, and saves', async () => {
    const onSubmit = vi.fn(async () => {});
    const initialConfig: ProviderConnectConfig = {
      type: 'bedrock',
      region: 'us-west-2',
      profile: 'default',
      defaultModel: 'anthropic.claude-3',
    };

    render(
      <ProviderConnectForm
        mode="edit"
        providerId="my-bedrock"
        onProviderIdChange={() => {}}
        onSubmit={onSubmit}
        initialConfig={initialConfig}
      />
    );

    const providerIdInput = container.querySelector(
      'input[value="my-bedrock"]'
    ) as HTMLInputElement;
    expect(providerIdInput.disabled).toBe(true);

    const regionInput = Array.from(container.querySelectorAll('input')).find(
      (i) => (i as HTMLInputElement).value === 'us-west-2'
    ) as HTMLInputElement;
    expect(regionInput).toBeTruthy();

    await act(async () => {
      clickButton('Save');
    });

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect((onSubmit.mock.calls as unknown[][])[0][0]).toMatchObject({
      type: 'bedrock',
      region: 'us-west-2',
      profile: 'default',
      defaultModel: 'anthropic.claude-3',
    });
  });

  it('renders claude_code_v2 fields when that type is preselected via edit mode', () => {
    render(
      <ProviderConnectForm
        mode="edit"
        providerId="my-claude"
        onProviderIdChange={() => {}}
        onSubmit={vi.fn()}
        initialConfig={{ type: 'claude_code_v2', permissionMode: 'bypassPermissions' }}
      />
    );
    const label = Array.from(container.querySelectorAll('label')).find((l) =>
      l.textContent?.startsWith('Permission mode')
    );
    expect(label).toBeTruthy();
  });
});
