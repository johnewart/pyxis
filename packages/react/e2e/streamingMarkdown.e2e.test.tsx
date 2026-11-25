/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Real end-to-end coverage: spawns a real llmd binary (same lifecycle
 * contract host apps use), drives a chat turn through @pyxis-llm/client's
 * real ChatStream against a scripted fake-OpenAI backend, and feeds the
 * actual streamed deltas into @pyxis-llm/react's <StreamingMarkdown> — asserting
 * on real DOM output from a real React render, not a mocked stream.
 *
 * Reuses @pyxis-llm/client's e2e harness (llmd process + fake OpenAI server)
 * rather than duplicating it.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { ConversationEventType, UserMessage } from '@pyxis-llm/client';
import { LlmdPluginProvider, StreamingMarkdown } from '@pyxis-llm/react';
import { mermaidPlugin } from '@pyxis-llm/react/mermaid';

import { startE2eEnv, type E2eEnv } from '../../client/e2e/harness.js';

let env: E2eEnv;
let container: HTMLDivElement;
let root: Root;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

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

/** Polls until `check()` returns true or `timeoutMs` elapses. */
async function waitFor(check: () => boolean, timeoutMs = 5_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error('waitFor: condition never became true');
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

describe('@pyxis-llm/react streaming markdown, driven by a real llmd chat stream', () => {
  it('renders a live llmd reply progressively, then as finalized markdown', async () => {
    // Scripted as several small SSE deltas so the assistant text arrives in
    // pieces — this is what actually exercises StreamingMarkdown's
    // committed-block/live-tail split, not just its final output.
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: [
        '# Heading\n\n',
        'Some *text* with a list:\n\n',
        '- one\n',
        '- two\n\n',
        '```js\n',
        "console.log('hi');\n",
        '```\n',
      ],
    });
    const { client } = await env.newSession();

    let text = '';
    let sawLiveTail = false;
    let finalContent = '';

    for await (const item of client.chatStreamEvents([
      new UserMessage('Show markdown', 'msg-1'),
    ])) {
      if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTextDelta) {
        text += item.event.delta;
        act(() => {
          root.render(
            <MantineProvider>
              <StreamingMarkdown text={text} />
            </MantineProvider>
          );
        });
        if (container.querySelector('.streaming-markdown-tail')) {
          sawLiveTail = true;
        }
      }
      if (item.kind === 'complete') {
        finalContent = item.message.content;
      }
    }

    // The progressive path (plain-text tail while the trailing block is still
    // "live") actually ran during the stream, not just the finalized render.
    expect(sawLiveTail).toBe(true);
    expect(finalContent).toContain("console.log('hi');");

    // Re-render as the persisted/complete message, the way a transcript
    // reload does: the whole thing is real parsed markdown, not raw text.
    act(() => {
      root.render(
        <MantineProvider>
          <StreamingMarkdown text={finalContent} complete />
        </MantineProvider>
      );
    });

    expect(container.querySelector('h1')?.textContent).toBe('Heading');
    expect(container.querySelectorAll('li')).toHaveLength(2);
    expect(container.querySelector('em')?.textContent).toBe('text');
    const codeEl = container.querySelector('code');
    expect(codeEl?.textContent).toContain("console.log('hi');");
  });

  it('routes a real llmd-streamed ```mermaid fence into the mermaid embed', async () => {
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: ['```mermaid\nflowchart LR\n  A --> B\n```\n'],
    });
    const { client } = await env.newSession();

    let finalContent = '';
    for await (const item of client.chatStreamEvents([
      new UserMessage('Show a diagram', 'msg-2'),
    ])) {
      if (item.kind === 'complete') {
        finalContent = item.message.content;
      }
    }

    act(() => {
      root.render(
        <MantineProvider>
          <LlmdPluginProvider plugins={[mermaidPlugin]}>
            <StreamingMarkdown text={finalContent} complete />
          </LlmdPluginProvider>
        </MantineProvider>
      );
    });

    // The fence was routed to MermaidDiagram (not rendered as a plain code
    // block) and it settled into either a successful SVG render or a caught
    // render error — jsdom lacks real SVG layout (getBBox etc.), so a render
    // error is an expected, non-flaky outcome here; either way the component
    // must not have thrown, and the routing itself is what this test proves.
    expect(container.querySelector('.mermaid-diagram-embed')).not.toBeNull();
    await waitFor(
      () =>
        container.querySelector('.mermaid-diagram-embed__chart-host')?.innerHTML.trim() !== '' ||
        container.querySelector('.mermaid-diagram-embed__error') !== null
    );
  });
});
