/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The queue and draft primitives against a real llmd, including the whole
 * drainer round trip: enqueue, claim, run the turn, retire — which is the
 * "queue some work and go to sleep" path, with a persistent client (here, the
 * test; in production, a host process) as the drainer.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { ConversationEventType, UserMessage } from '@pyxis-llm/client';
import { createLlmdClient } from '@pyxis-llm/client/node';

import { startE2eEnv, type E2eEnv } from './harness.js';

/** A brand new client for an existing session — stands in for a reloaded UI. */
function reopen(sessionId: string) {
  return createLlmdClient({
    address: env.handle.grpcAddress,
    tenantId: env.tenantId,
    scopeId: env.scopeId,
    sessionId,
  });
}

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

describe('session drafts', () => {
  it('persist per session and survive a brand new client', async () => {
    const { client, sessionId } = await env.newSession();

    expect(await client.getDraft()).toBe('');
    await client.saveDraft('half a thought');
    expect(await client.getDraft()).toBe('half a thought');

    // A different client for the same session — as if the UI had been reloaded,
    // or the machine had gone to sleep and come back. This is the whole point of
    // persisting a draft server-side.
    const reopened = reopen(sessionId);
    expect(await reopened.getDraft()).toBe('half a thought');

    await reopened.clearDraft();
    expect(await client.getDraft()).toBe('');
  });

  it('does not leak between sessions', async () => {
    const a = await env.newSession();
    const b = await env.newSession();
    await a.client.saveDraft('for session A');
    expect(await b.client.getDraft()).toBe('');
  });
});

describe('session queue', () => {
  it('enqueues, reorders and removes', async () => {
    const { client } = await env.newSession();

    const first = await client.enqueueMessage('first');
    const second = await client.enqueueMessage('second');
    const third = await client.enqueueMessage('third');
    expect((await client.listQueue()).queue.map((q) => q.content)).toEqual([
      'first',
      'second',
      'third',
    ]);

    await client.reorderQueue([third.id, first.id, second.id]);
    expect((await client.listQueue()).queue.map((q) => q.content)).toEqual([
      'third',
      'first',
      'second',
    ]);

    expect(await client.removeQueuedMessage(first.id)).toBe(true);
    expect((await client.listQueue()).queue.map((q) => q.content)).toEqual(['third', 'second']);
    // Removing it again is reported, not thrown: another window may have got there first.
    expect(await client.removeQueuedMessage(first.id)).toBe(false);
  });

  it('runs a queued message end to end: claim, turn, retire', async () => {
    env.fakeOpenAi.enqueue({ kind: 'text', deltas: ['Answered while you were away.'] });
    const { client } = await env.newSession();

    await client.enqueueMessage('do this later please');

    // --- the drainer loop, as a host process would run it ---
    const owner = 'e2e-drainer-1';
    const claim = await client.claimNextQueuedMessage(owner);
    expect(claim.paused).toBe(false);
    expect(claim.entry?.content).toBe('do this later please');
    const entry = claim.entry!;
    expect(entry.state).toBe('running');
    expect(entry.claimOwner).toBe(owner);

    let turnId = '';
    let finalText = '';
    for await (const item of client.chatStreamEvents([new UserMessage(entry.content, 'q-msg-1')])) {
      if (item.kind === 'event' && item.event.type === ConversationEventType.AssistantTurnStarted) {
        turnId = item.event.turnId;
        // Recorded BEFORE the turn finishes: if this drainer died now, whoever
        // reclaimed would see the message was already delivered.
        await client.markQueuedMessageStarted(entry.id, owner, turnId);
      }
      if (item.kind === 'complete') finalText = item.message.content;
    }
    expect(turnId).not.toBe('');
    expect(finalText).toBe('Answered while you were away.');

    await client.retireQueuedMessage(entry.id, owner);
    // --- end drainer loop ---

    expect((await client.listQueue()).queue).toEqual([]);
    // The queued message really became a turn in the transcript.
    const history = await client.getHistory();
    expect(history.map((m) => m.role)).toEqual(['user', 'assistant']);
    expect(history[0].content).toBe('do this later please');
    expect(history[1].content).toBe('Answered while you were away.');
  });

  it('hands each entry to exactly one of two competing drainers', async () => {
    const { client } = await env.newSession();
    for (let i = 0; i < 6; i++) await client.enqueueMessage(`msg-${i}`);

    // Two drainers looping at once — a background daemon and an open window.
    const claimedBy = new Map<string, string>();
    const drain = async (owner: string) => {
      for (;;) {
        const res = await client.claimNextQueuedMessage(owner);
        if (!res.entry) return;
        if (claimedBy.has(res.entry.id)) {
          throw new Error(
            `entry ${res.entry.id} was claimed twice (${claimedBy.get(res.entry.id)} and ${owner}) — that would answer the same message twice`
          );
        }
        claimedBy.set(res.entry.id, owner);
        await client.retireQueuedMessage(res.entry.id, owner);
      }
    };
    await Promise.all([drain('drainer-a'), drain('drainer-b')]);

    expect(claimedBy.size).toBe(6);
    expect((await client.listQueue()).queue).toEqual([]);
  });

  it('pause is enforced by llmd, and reports itself distinctly from an empty queue', async () => {
    const { client } = await env.newSession();
    await client.enqueueMessage('held');

    await client.setQueuePaused(true);
    expect((await client.listQueue()).paused).toBe(true);

    const paused = await client.claimNextQueuedMessage('drainer');
    expect(paused.entry).toBeUndefined();
    // A drainer must be able to tell "paused" from "nothing left", or it would
    // stop polling and never come back.
    expect(paused.paused).toBe(true);

    await client.setQueuePaused(false);
    const resumed = await client.claimNextQueuedMessage('drainer');
    expect(resumed.entry?.content).toBe('held');
    await client.retireQueuedMessage(resumed.entry!.id, 'drainer');

    const drained = await client.claimNextQueuedMessage('drainer');
    expect(drained.entry).toBeUndefined();
    expect(drained.paused).toBe(false);
  });

  it('refuses drainer operations from a client that does not own the claim', async () => {
    const { client } = await env.newSession();
    await client.enqueueMessage('mine');
    const claim = await client.claimNextQueuedMessage('owner-a');

    await expect(client.retireQueuedMessage(claim.entry!.id, 'owner-b')).rejects.toThrow();
    await expect(client.heartbeatQueuedMessage(claim.entry!.id, 'owner-b')).rejects.toThrow();
    // The real owner still can.
    await client.heartbeatQueuedMessage(claim.entry!.id, 'owner-a');
    await client.retireQueuedMessage(claim.entry!.id, 'owner-a');
  });

  it('releases an unstarted claim back to its original position', async () => {
    const { client } = await env.newSession();
    await client.enqueueMessage('first');
    await client.enqueueMessage('second');

    const claim = await client.claimNextQueuedMessage('quitting-drainer');
    expect(claim.entry?.content).toBe('first');
    await client.releaseQueuedMessage(claim.entry!.id, 'quitting-drainer');

    expect((await client.listQueue()).queue.map((q) => q.content)).toEqual(['first', 'second']);
    const next = await client.claimNextQueuedMessage('next-drainer');
    expect(next.entry?.content).toBe('first');
  });

  it('announces queue changes on the session-events side channel', async () => {
    const { client } = await env.newSession();
    const watching = new AbortController();
    const seen: { queue: string[]; paused: boolean }[] = [];
    void (async () => {
      try {
        for await (const event of client.subscribeSessionEvents({ signal: watching.signal })) {
          if (event.type === ConversationEventType.QueueChanged) {
            seen.push({ queue: event.queue.map((q) => q.content), paused: event.paused });
          }
        }
      } catch {
        // aborted at the end of the test
      }
    })();

    await client.enqueueMessage('watch me');
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline && !seen.some((s) => s.queue.includes('watch me'))) {
      await new Promise((r) => setTimeout(r, 25));
    }
    expect(seen.some((s) => s.queue.includes('watch me'))).toBe(true);

    await client.setQueuePaused(true);
    const pauseDeadline = Date.now() + 10_000;
    while (Date.now() < pauseDeadline && !seen.some((s) => s.paused)) {
      await new Promise((r) => setTimeout(r, 25));
    }
    expect(seen.some((s) => s.paused)).toBe(true);
    watching.abort();
  });
});
