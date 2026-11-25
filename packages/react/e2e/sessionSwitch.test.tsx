/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed test (fake clients, no llmd binary) for useLlmdChatDriver when the
 * HOST KEEPS THE HOOK MOUNTED AND SWAPS `client` — what a tabbed UI does when
 * it holds one driver per page and switching tabs only changes the session.
 *
 * Every piece of driver state is per-session. Regression: none of it was reset
 * on a client change, so the message just sent in tab A showed up in tab B (an
 * in-flight own turn is carried over by hydrate()), A's still-running stream
 * kept writing status/title/todos into B, and B never loaded its own title or
 * todos.
 */

import React from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ConversationEventType, type ConversationEvent, type Message } from '@pyxis-llm/client';
import { useLlmdChatDriver, type LlmdChatDriver } from '@pyxis-llm/react/node';
import { useTurnTimer, type TurnTiming, type TurnTimerState } from '@pyxis-llm/react';

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

async function waitFor(check: () => boolean, timeoutMs = 2_000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: condition never became true');
    await new Promise((resolve) => setTimeout(resolve, 10));
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {});
  }
}

type Todo = { id: string; description: string; status: 'pending' | 'in_progress' | 'completed' };

/**
 * One session's fake client. `chatStreamEvents` emits status/title/todos and a
 * text delta, then waits for `releaseLate()` and emits more frames WITHOUT
 * honouring its abort signal — the worst case of a stream that is slow to
 * unwind after the driver detaches.
 */
class FakeSessionClient {
  turnSignal: AbortSignal | undefined;
  savedDrafts: string[] = [];
  /** llmd's timestamp on this session's UserTurnCommitted (when set, the stream emits one). */
  commitAtMs: number | undefined;
  /** A turn already running server-side when the driver opens this session (rejoin). */
  running: { userMessageId: string; committedAtMs: number } | undefined;
  /** What llmd reports as this session's last finished turn (session status). */
  lastTurn: { startedAtMs: number; endedAtMs: number } | undefined;
  private release!: () => void;
  private late = new Promise<void>((r) => {
    this.release = r;
  });

  constructor(
    readonly sid: string,
    private readonly info: { title: string; todos: Todo[] },
    private readonly history: Message[] = []
  ) {}

  sessionKey(): string {
    return this.sid;
  }

  releaseLate(): void {
    this.release();
  }

  async *chatStreamEvents(
    _messages: Message[],
    options?: { signal?: AbortSignal }
  ): AsyncGenerator<{ kind: 'event'; event: ConversationEvent } | { kind: 'complete'; message: never }> {
    this.turnSignal = options?.signal;
    const sid = this.sid;
    if (this.commitAtMs !== undefined) {
      yield {
        kind: 'event',
        event: {
          type: ConversationEventType.UserTurnCommitted,
          userMessageId: _messages[0]?.id ?? '',
          committedAtMs: this.commitAtMs,
        } as ConversationEvent,
      };
    }
    yield { kind: 'event', event: { type: ConversationEventType.SessionStatus, sessionId: sid, headline: `${sid} working` } as ConversationEvent };
    yield { kind: 'event', event: { type: ConversationEventType.SessionTitleChanged, sessionId: sid, title: `${sid} live title` } as ConversationEvent };
    yield {
      kind: 'event',
      event: {
        type: ConversationEventType.TodosUpdated,
        sessionId: sid,
        todos: [{ id: `${sid}-t`, description: `${sid} todo`, status: 'in_progress' }],
      } as unknown as ConversationEvent,
    };
    yield { kind: 'event', event: { type: ConversationEventType.AssistantTextDelta, delta: `${sid} reply` } as ConversationEvent };
    await this.late;
    yield { kind: 'event', event: { type: ConversationEventType.SessionStatus, sessionId: sid, headline: `${sid} LATE` } as ConversationEvent };
    yield { kind: 'event', event: { type: ConversationEventType.SessionTitleChanged, sessionId: sid, title: `${sid} LATE` } as ConversationEvent };
    yield { kind: 'event', event: { type: ConversationEventType.AssistantTextDelta, delta: ' LATE' } as ConversationEvent };
  }

  async *subscribeSessionEvents(options?: { afterSeq?: number; signal?: AbortSignal }): AsyncGenerator<ConversationEvent> {
    if (this.running) {
      // Replay from the running turn's start, as llmd does for a rejoin.
      yield {
        type: ConversationEventType.UserTurnCommitted,
        userMessageId: this.running.userMessageId,
        textPreview: 'earlier message',
        committedAtMs: this.running.committedAtMs,
      } as ConversationEvent;
    }
    await new Promise<void>((resolve) => {
      options?.signal?.addEventListener('abort', () => resolve(), { once: true });
    });
  }

  async getSessionState() {
    return {
      messages: this.history,
      turnInProgress: !!this.running,
      eventsSeq: 0,
      turnStartSeq: 0,
      ...(this.running ? { turnStartedAtMs: this.running.committedAtMs } : {}),
      ...(this.lastTurn ? { lastTurn: this.lastTurn } : {}),
    };
  }

  async getSessionInfo() {
    return this.info;
  }

  async saveDraft(content: string) {
    this.savedDrafts.push(content);
  }
  async getDraft() {
    return '';
  }
  async clearDraft() {}
  async enqueueMessage(): Promise<never> {
    throw new Error('not used');
  }
  async listQueue() {
    return { queue: [], paused: false };
  }
  async removeQueuedMessage() {
    return false;
  }
  async reorderQueue() {
    return [];
  }
  async setQueuePaused() {}
}

let driver: LlmdChatDriver | undefined;

function Harness({ client }: { client: FakeSessionClient }) {
  driver = useLlmdChatDriver(client);
  return null;
}

const textOf = (d: LlmdChatDriver) => d.messages.map((m) => m.content).join('\n');

describe('useLlmdChatDriver: switching sessions on a mounted driver', () => {
  it('loads the session title and todos when the session opens', async () => {
    driver = undefined;
    const a = new FakeSessionClient('A', {
      title: 'Stored A title',
      todos: [{ id: 't1', description: 'stored todo', status: 'pending' }],
    });
    act(() => root.render(<Harness client={a} />));

    await waitFor(() => driver?.sessionTitle === 'Stored A title');
    expect(driver!.todos.map((t) => t.description)).toEqual(['stored todo']);
  });

  it('a mid-turn switch shows only the new session, and the old turn cannot write into it', async () => {
    driver = undefined;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    const b = new FakeSessionClient('B', {
      title: 'B title',
      todos: [{ id: 'bt', description: 'B stored todo', status: 'pending' }],
    });

    act(() => root.render(<Harness client={a} />));
    await waitFor(() => driver?.sessionTitle === 'A title');

    act(() => driver!.send('hello from A'));
    await waitFor(() => driver!.sessionStatus === 'A working' && textOf(driver!).includes('A reply'));
    expect(driver!.streaming).toBe(true);
    expect(driver!.todos.map((t) => t.description)).toEqual(['A todo']);

    // Switch tabs: same mounted hook, new client.
    act(() => root.render(<Harness client={b} />));

    // Everything from A is gone at once — not after some later event.
    expect(driver!.messages).toEqual([]);
    expect(driver!.streaming).toBe(false);
    expect(driver!.streamingMessageId).toBeUndefined();
    expect(driver!.sessionStatus).toBeUndefined();
    expect(driver!.sessionTitle).toBeUndefined();
    expect(driver!.todos).toEqual([]);
    // A's stream is detached (aborted), not left consuming into this state.
    expect(a.turnSignal?.aborted).toBe(true);

    // B loads its own stored title/todos.
    await waitFor(() => driver!.sessionTitle === 'B title');
    expect(driver!.todos.map((t) => t.description)).toEqual(['B stored todo']);

    // The host hydrates B's (empty) history: A's in-flight message must NOT be carried over.
    act(() => driver!.hydrate([]));
    expect(driver!.messages).toEqual([]);

    // A's stream keeps producing frames after the switch. None may land on B.
    a.releaseLate();
    await new Promise((r) => setTimeout(r, 50));
    await act(async () => {});
    expect(driver!.sessionStatus).toBeUndefined();
    expect(driver!.sessionTitle).toBe('B title');
    expect(textOf(driver!)).not.toContain('LATE');
    expect(textOf(driver!)).not.toContain('hello from A');
    expect(driver!.streaming).toBe(false);
  });

  it('a new turn on the new session streams normally after a switch', async () => {
    driver = undefined;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    const b = new FakeSessionClient('B', { title: 'B title', todos: [] });

    act(() => root.render(<Harness client={a} />));
    act(() => driver!.send('hello from A'));
    await waitFor(() => driver!.streaming);

    act(() => root.render(<Harness client={b} />));
    act(() => driver!.send('hello from B'));
    await waitFor(() => textOf(driver!).includes('B reply'));

    expect(driver!.streaming).toBe(true);
    expect(driver!.sessionStatus).toBe('B working');
    expect(driver!.messages.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(textOf(driver!)).not.toContain('A ');

    // A finishing late must not end B's turn or touch its state.
    a.releaseLate();
    await new Promise((r) => setTimeout(r, 50));
    await act(async () => {});
    expect(driver!.streaming).toBe(true);
    expect(driver!.sessionStatus).toBe('B working');
  });

  it('a host rebuilding its client for the SAME session every render does not reset (or loop)', async () => {
    driver = undefined;
    let renders = 0;
    const late: FakeSessionClient[] = [];
    // Like a host calling createLlmdClient(...) in render without useMemo.
    function Rebuilding() {
      renders++;
      const c = new FakeSessionClient('A', { title: 'A title', todos: [] });
      late.push(c);
      driver = useLlmdChatDriver(c);
      return null;
    }
    act(() => root.render(<Rebuilding />));
    act(() => driver!.send('hello from A'));
    await waitFor(() => textOf(driver!).includes('A reply'));
    await new Promise((r) => setTimeout(r, 100));
    await act(async () => {});

    // Same session throughout: the in-flight turn survives re-renders.
    expect(driver!.streaming).toBe(true);
    expect(textOf(driver!)).toContain('hello from A');
    expect(renders).toBeLessThan(200);
    for (const c of late) c.releaseLate();
  });

  it('turn timing is per session: llmd\'s start time, and nothing carried over on a switch', async () => {
    driver = undefined;
    const startedA = Date.now() - 90_000;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    a.commitAtMs = startedA;
    const b = new FakeSessionClient('B', { title: 'B title', todos: [] });

    act(() => root.render(<Harness client={a} />));
    act(() => driver!.send('hello from A'));
    // Timed from llmd's commit, not from when this client saw the turn.
    await waitFor(() => driver!.turnTiming.startedAt === startedA);
    expect(driver!.turnTiming.durationMs).toBeUndefined();

    // Switch away mid-turn: B must not show A's clock or a bogus "took Xs".
    act(() => root.render(<Harness client={b} />));
    expect(driver!.turnTiming).toEqual({});
    await waitFor(() => driver!.sessionTitle === 'B title');
    expect(driver!.turnTiming).toEqual({});
    a.releaseLate();
  });

  it('switching back to a running session resumes its clock from llmd\'s start, not from 0', async () => {
    driver = undefined;
    const startedA = Date.now() - 42_000;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    a.running = { userMessageId: 'u-running', committedAtMs: startedA };
    const b = new FakeSessionClient('B', { title: 'B title', todos: [] });

    act(() => root.render(<Harness client={b} />));
    await waitFor(() => driver?.sessionTitle === 'B title');
    act(() => root.render(<Harness client={a} />));

    await waitFor(() => driver!.streaming && driver!.turnTiming.startedAt === startedA);
    expect(driver!.turnTiming.durationMs).toBeUndefined();
  });

  it('a session\'s last finished turn shows its duration when opened, and again after switching away and back', async () => {
    driver = undefined;
    const t0 = Date.now() - 600_000;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    a.lastTurn = { startedAtMs: t0, endedAtMs: t0 + 42_000 };
    const b = new FakeSessionClient('B', { title: 'B title', todos: [] });
    b.lastTurn = { startedAtMs: t0 + 1_000, endedAtMs: t0 + 8_000 };

    // Never watched either turn run: the duration comes from llmd's status.
    act(() => root.render(<Harness client={a} />));
    await waitFor(() => driver!.turnTiming.durationMs === 42_000);
    expect(driver!.turnTiming.startedAt).toBe(t0);

    act(() => root.render(<Harness client={b} />));
    await waitFor(() => driver!.turnTiming.durationMs === 7_000);

    act(() => root.render(<Harness client={a} />));
    await waitFor(() => driver!.turnTiming.durationMs === 42_000);
    expect(driver!.streaming).toBe(false);
  });

  it('turnTiming never lags streaming: every render agrees (hosts may memoize on streaming)', async () => {
    driver = undefined;
    const a = new FakeSessionClient('A', { title: 'A title', todos: [] });
    a.commitAtMs = Date.now() - 1_000;
    const renders: Array<{ streaming: boolean; timing: TurnTiming }> = [];
    function Recording({ client }: { client: FakeSessionClient }) {
      driver = useLlmdChatDriver(client);
      renders.push({ streaming: driver.streaming, timing: driver.turnTiming });
      return null;
    }
    act(() => root.render(<Recording client={a} />));
    // Two turns, so the second starts with the first's finished timing in place.
    // The fake's stream ends once released (it ignores stop() on purpose), and
    // stays released, so the second turn runs straight through.
    act(() => driver!.send('turn 1'));
    await waitFor(() => textOf(driver!).includes('A reply'));
    a.releaseLate();
    await waitFor(() => !driver!.streaming);
    act(() => driver!.send('turn 2'));
    await waitFor(() => driver!.turnTiming.durationMs !== undefined && !driver!.streaming);
    expect(renders.some((r) => r.streaming)).toBe(true);
    for (const r of renders) {
      if (r.streaming) {
        expect(r.timing.startedAt).toBeTypeOf('number');
        expect(r.timing.durationMs).toBeUndefined();
      } else if (r.timing.startedAt !== undefined) {
        expect(r.timing.durationMs).toBeTypeOf('number');
      }
    }
  });

  it('useTurnTimer with turnTiming derives everything from it (no state kept across sessions)', () => {
    let state: TurnTimerState | undefined;
    function TimerHarness({ streaming, timing }: { streaming: boolean; timing: TurnTiming }) {
      state = useTurnTimer(streaming, timing);
      return null;
    }
    // Running, started 65s ago by llmd.
    act(() => root.render(<TimerHarness streaming timing={{ startedAt: Date.now() - 65_000 }} />));
    expect(state!.label).toBe('1m 05s');
    // Switch to a session with no turn: nothing, not a frozen "took 1m 05s".
    act(() => root.render(<TimerHarness streaming={false} timing={{}} />));
    expect(state!.label).toBe('');
    // A finished turn on this session.
    act(() => root.render(<TimerHarness streaming={false} timing={{ startedAt: 1, durationMs: 11_000 }} />));
    expect(state!.label).toBe('took 11s');
    // Back to a still-running one: its own clock, immediately.
    act(() => root.render(<TimerHarness streaming timing={{ startedAt: Date.now() - 5_000 }} />));
    expect(state!.label).toBe('5s');
  });

  it('flushes a pending draft to the session it was typed in, not the new one', async () => {
    driver = undefined;
    const a = new FakeSessionClient('A', { title: '', todos: [] });
    const b = new FakeSessionClient('B', { title: '', todos: [] });

    act(() => root.render(<Harness client={a} />));
    act(() => driver!.setDraft('half-typed for A'));
    act(() => root.render(<Harness client={b} />));

    expect(driver!.draft.get()).toBe('');
    await act(async () => {});
    expect(a.savedDrafts).toEqual(['half-typed for A']);
    // Debounce window passes: nothing lands on B.
    await new Promise((r) => setTimeout(r, 500));
    await act(async () => {});
    expect(b.savedDrafts).toEqual([]);
  });
});
