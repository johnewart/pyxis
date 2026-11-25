/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Token usage and model time on assistant messages: history messages carry
 * them in `metadata.usage`, and the live turn-finished event carries every
 * token count plus the model time.
 */

import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import {
  conversationStreamFrameToEvent,
  ConversationEventType,
  CONVERSATION_EVENT_SCHEMA_VERSION,
} from '../src/events.js';
import {
  MESSAGE_USAGE_METADATA_KEY,
  protoMessageToAssistantMessage,
  protoMessageToLibraryMessage,
} from '../src/convert.js';
import { ConversationStreamFrameSchema, MessageSchema } from '../src/gen/llmv1/llm_pb.js';

describe('protoMessageToAssistantMessage: usage', () => {
  it('keeps every token count and the model time in metadata.usage', () => {
    const msg = protoMessageToAssistantMessage(
      create(MessageSchema, {
        role: 'assistant',
        id: 'a1',
        content: 'done',
        usage: {
          inputTokens: 1200n,
          outputTokens: 80n,
          cacheReadTokens: 5000n,
          cacheWriteTokens: 300n,
          reasoningTokens: 12n,
        },
        modelDurationMs: 14200n,
      })
    );
    expect(msg.metadata?.[MESSAGE_USAGE_METADATA_KEY]).toEqual({
      inputTokens: 1200,
      outputTokens: 80,
      cacheReadTokens: 5000,
      cacheWriteTokens: 300,
      reasoningTokens: 12,
      modelDurationMs: 14200,
    });
  });

  it('keeps tokens without a time for messages recorded before model time existed', () => {
    const msg = protoMessageToAssistantMessage(
      create(MessageSchema, { role: 'assistant', id: 'a2', content: 'x', usage: { inputTokens: 10n, outputTokens: 2n } })
    );
    const usage = msg.metadata?.[MESSAGE_USAGE_METADATA_KEY] as Record<string, unknown>;
    expect(usage).toEqual({ inputTokens: 10, outputTokens: 2, cacheReadTokens: 0, cacheWriteTokens: 0 });
    expect('modelDurationMs' in usage).toBe(false);
  });

  it('adds no usage when llmd recorded none', () => {
    const msg = protoMessageToLibraryMessage(create(MessageSchema, { role: 'assistant', id: 'a3', content: 'x' }));
    expect(msg.metadata?.[MESSAGE_USAGE_METADATA_KEY]).toBeUndefined();
  });
});

describe('conversationStreamFrameToEvent: assistant_turn_finished usage', () => {
  it('maps cache and reasoning tokens and the model time', () => {
    const ev = conversationStreamFrameToEvent(
      create(ConversationStreamFrameSchema, {
        schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
        frame: {
          case: 'assistantTurnFinished',
          value: {
            turnId: 't1',
            assistantMessageId: 'a1',
            usage: {
              inputTokens: 40n,
              outputTokens: 9n,
              cacheReadTokens: 100n,
              cacheWriteTokens: 5n,
              reasoningTokens: 3n,
            },
            modelDurationMs: 2500n,
          },
        },
      })
    );
    expect(ev).toMatchObject({
      type: ConversationEventType.AssistantTurnFinished,
      turnId: 't1',
      assistantMessageId: 'a1',
      usage: { inputTokens: 40, outputTokens: 9, cacheReadTokens: 100, cacheWriteTokens: 5, reasoningTokens: 3 },
      modelDurationMs: 2500,
    });
  });
});
