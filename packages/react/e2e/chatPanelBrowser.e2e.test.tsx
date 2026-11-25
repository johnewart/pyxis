/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Drives the full useLlmdChatDriver + ChatPanel stack over the REAL
 * Connect protocol (connectrpc.com), hitting llmd's dedicated Connect/
 * grpc-web listener (env.handle.connectAddress) — a SEPARATE, token-guarded,
 * loopback-only port from native gRPC (see cmd/llmd/main.go's
 * LLMD_CONNECT_TOKEN/LLMD_CONNECT_HTTP_ADDR and
 * internal/serve/connect_listener.go), proving the real Connect adapter
 * (internal/serve/connect_listener.go) works end to end, not just
 * "should work". @connectrpc/connect-web's transport is plain `fetch`, so
 * this environment (Node 18+) exercises the identical code path a real
 * browser tab would.
 */

import React, { useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { MantineProvider } from '@mantine/core';
import { createLlmdWebClient } from '@pyxis-llm/client/web';
import { ChatPanel } from '@pyxis-llm/react';
import { useLlmdChatDriver } from '@pyxis-llm/react/browser';

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

async function waitFor(check: () => boolean, timeoutMs = 15_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error('waitFor: condition never became true');
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

function Harness({ sessionId }: { sessionId: string }) {
  // Once per session, not on every render (see chatPanel.e2e.test.tsx).
  const client = useMemo(
    () =>
      createLlmdWebClient({
        baseUrl: `http://${env.handle.connectAddress}`,
        tenantId: env.tenantId,
        scopeId: env.scopeId,
        sessionId,
        token: env.connectToken,
      }),
    [sessionId]
  );
  const driver = useLlmdChatDriver(client);
  return (
    <MantineProvider>
      <div style={{ height: 400 }}>
        <ChatPanel
          messages={driver.messages}
          onSend={driver.send}
          streaming={driver.streaming}
          onStop={driver.stop}
          streamingMessageId={driver.streamingMessageId}
        />
      </div>
    </MantineProvider>
  );
}

describe('ChatPanel + useLlmdChatDriver, driven by real llmd over the Connect protocol', () => {
  it('sends a message via the real Composer over Connect (fetch), on llmd\'s dedicated Connect/grpc-web port', async () => {
    expect(env.handle.connectAddress).toBeTruthy();
    env.fakeOpenAi.enqueue({
      kind: 'text',
      deltas: ['Hello ', 'from ', 'the ', 'Connect ', 'protocol.'],
    });
    const { sessionId } = await env.newSession();

    act(() => {
      root.render(<Harness sessionId={sessionId} />);
    });

    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        'value'
      )!.set!;
      setter.call(textarea, 'Say hi over Connect');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });

    const sendButton = container.querySelector(
      'button[aria-label="Send"]'
    ) as HTMLButtonElement;
    expect(sendButton).not.toBeNull();

    act(() => {
      sendButton.click();
    });

    await waitFor(() => container.textContent?.includes('Say hi over Connect') === true);
    await waitFor(
      () => container.textContent?.includes('Hello from the Connect protocol.') === true
    );
    await waitFor(() => container.querySelector('button[aria-label="Send"]') !== null);
    expect(container.querySelector('button[aria-label="Stop"]')).toBeNull();
  });
});
