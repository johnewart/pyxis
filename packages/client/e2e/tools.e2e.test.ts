/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { ConversationEventType, ToolClassification, UserMessage } from '@pyxis-llm/client';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

describe('tool visibility and execution', () => {
  it('the model sees a registered tool, calls it, and the local execute result round-trips', async () => {
    // First response: the fake model calls our tool. Second: after
    // SubmitToolResults, it replies with plain text using the tool's output.
    env.fakeOpenAi.enqueue(
      {
        kind: 'tool_call',
        id: 'call-1',
        name: 'get_weather',
        argumentsJson: JSON.stringify({ city: 'Springfield' }),
      },
      { kind: 'text', deltas: ['It is sunny in Springfield.'] }
    );

    const { client } = await env.newSession();

    let executed: Record<string, unknown> | undefined;
    const events: string[] = [];
    let finalMessage = '';

    for await (const item of client.chatStreamEvents(
      [new UserMessage('What is the weather?', 'msg-tools-1')],
      {
        tools: [
          {
            name: 'get_weather',
            description: 'Get the weather for a city',
            parameters: {
              type: 'object',
              properties: { city: { type: 'string' } },
              required: ['city'],
            },
            classification: ToolClassification.QUERY,
            execute: async (args) => {
              executed = args;
              return `sunny in ${args.city as string}`;
            },
          },
        ],
      }
    )) {
      if (item.kind === 'event') events.push(item.event.type);
      if (item.kind === 'complete') finalMessage = item.message.content;
    }

    // Tool visibility: the fake server actually received the tool definition
    // in the outbound request payload (llmd forwarded it to the "model").
    const firstReq = env.fakeOpenAi.requests[0]?.body;
    const toolNames = (firstReq?.tools ?? []).map((t: any) => t.function?.name);
    expect(toolNames).toContain('get_weather');

    // The tool call round-tripped through llmd to our local execute handler.
    expect(executed).toEqual({ city: 'Springfield' });

    // Full event sequence surfaced tool-call streaming + execution lifecycle.
    expect(events).toContain(ConversationEventType.ToolCallStreamStarted);
    expect(events).toContain(ConversationEventType.ToolCallStreamFinished);
    expect(events).toContain(ConversationEventType.ToolExecutionStarted);
    expect(events).toContain(ConversationEventType.ToolExecutionFinished);

    // And the second (post-tool-result) model turn produced the final reply.
    expect(finalMessage).toBe('It is sunny in Springfield.');
  });
});
