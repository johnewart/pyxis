/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit test for the web transport's session-event feed against a fake global
 * WebSocket — no real network, no llmd binary. Verifies: the URL is built
 * correctly (ws:// scheme, /ws/session-events path, session_id/tenant_id/
 * scope_id/token query params; scope_id omitted when no scope is set), incoming protojson text messages are decoded into
 * ConversationEvents in order, and the generator stops cleanly when the socket
 * closes.
 *
 * `subscribeSessionEvents` is the one method whose mechanism
 * legitimately differs per transport (WebSocket on web, the
 * SubscribeSessionEvents RPC on node — see web.ts), so this is what stops the
 * shared client from quietly collapsing the web path onto the RPC.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConversationEventType } from '../src/events.js';
import { createLlmdWebClient } from '../src/web.js';

type Listener = (ev: unknown) => void;

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  url: string;
  private listeners: Record<string, Listener[]> = {};
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, fn: Listener) {
    (this.listeners[type] ??= []).push(fn);
  }

  removeEventListener() {
    /* no-op for this fake */
  }

  emitMessage(data: string) {
    for (const fn of this.listeners.message ?? []) fn({ data } as MessageEvent);
  }

  emitClose(code = 1000, reason = '') {
    this.closed = true;
    for (const fn of this.listeners.close ?? []) fn({ code, reason } as CloseEvent);
  }

  close() {
    if (!this.closed) this.emitClose();
  }
}

afterEach(() => {
  FakeWebSocket.instances = [];
  vi.unstubAllGlobals();
});

describe('web transport: subscribeSessionEvents', () => {
  it('builds the ws:// URL with session/tenant/scope/token query params', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    const client = createLlmdWebClient({
      baseUrl: 'http://127.0.0.1:50052',
      tenantId: 'tenant-1',
      scopeId: 'scope-1',
      sessionId: 'sess-1',
      token: 'tok-1',
    });

    const gen = client.subscribeSessionEvents();
    const first = gen.next(); // starts iterating, constructs the WebSocket

    await Promise.resolve(); // let the microtask that constructs WebSocket run
    expect(FakeWebSocket.instances).toHaveLength(1);
    const ws = FakeWebSocket.instances[0]!;
    const url = new URL(ws.url);
    expect(url.protocol).toBe('ws:');
    expect(url.pathname).toBe('/ws/session-events');
    expect(url.searchParams.get('session_id')).toBe('sess-1');
    expect(url.searchParams.get('tenant_id')).toBe('tenant-1');
    expect(url.searchParams.get('scope_id')).toBe('scope-1');
    expect(url.searchParams.has('workspace_id')).toBe(false);
    expect(url.searchParams.get('token')).toBe('tok-1');

    ws.emitClose();
    await first;
    await gen.return(undefined);
  });

  it('omits scope_id when no (or a blank) scope is given', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    for (const scopeId of [undefined, '  ']) {
      FakeWebSocket.instances = [];
      const client = createLlmdWebClient({
        baseUrl: 'http://127.0.0.1:50052',
        tenantId: 'tenant-1',
        scopeId,
        sessionId: 'sess-1',
      });
      const gen = client.subscribeSessionEvents();
      const first = gen.next();
      await Promise.resolve();
      const ws = FakeWebSocket.instances[0]!;
      const url = new URL(ws.url);
      expect(url.searchParams.get('tenant_id')).toBe('tenant-1');
      expect(url.searchParams.has('scope_id')).toBe(false);
      ws.emitClose();
      await first;
      await gen.return(undefined);
    }
  });

  it('decodes incoming protojson frames into ConversationEvents, in order', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    const client = createLlmdWebClient({
      baseUrl: 'http://127.0.0.1:50052',
      tenantId: 'tenant-1',
      sessionId: 'sess-1',
    });

    const gen = client.subscribeSessionEvents();
    const events: unknown[] = [];
    const consume = (async () => {
      for await (const ev of gen) events.push(ev);
    })();

    await Promise.resolve();
    const ws = FakeWebSocket.instances[0]!;

    ws.emitMessage(
      JSON.stringify({
        sessionStatus: { sessionId: 'sess-1', headline: 'Looking for a fish...' },
      })
    );
    ws.emitMessage(
      JSON.stringify({
        toolExecutionStarted: { turnId: 't1', toolCallId: 'c1', name: 'read_code' },
      })
    );
    ws.emitClose();
    await consume;

    expect(events).toHaveLength(2);
    expect((events[0] as { type: string }).type).toBe(ConversationEventType.SessionStatus);
    expect((events[1] as { type: string }).type).toBe(ConversationEventType.ToolExecutionStarted);
    expect((events[1] as { toolCallId: string }).toolCallId).toBe('c1');
  });

  it('skips an unparsable frame instead of killing the subscription', async () => {
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket);
    const client = createLlmdWebClient({
      baseUrl: 'http://127.0.0.1:50052',
      tenantId: 'tenant-1',
      sessionId: 'sess-1',
    });

    const gen = client.subscribeSessionEvents();
    const events: unknown[] = [];
    const consume = (async () => {
      for await (const ev of gen) events.push(ev);
    })();

    await Promise.resolve();
    const ws = FakeWebSocket.instances[0]!;

    ws.emitMessage('{not valid json');
    ws.emitMessage(
      JSON.stringify({ sessionTitleChanged: { sessionId: 'sess-1', title: 'Fish' } })
    );
    ws.emitClose();
    await consume;

    expect(events).toHaveLength(1);
    expect((events[0] as { type: string }).type).toBe(
      ConversationEventType.SessionTitleChanged
    );
  });
});
