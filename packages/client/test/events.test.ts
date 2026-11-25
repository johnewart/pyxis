/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Pure unit tests for conversationStreamFrameToEvent's mapping of the three
 * llmd-native session tool frames (set_status / set_session_title /
 * add_todo+update_todo) — no llmd binary, no network. The e2e counterpart
 * (packages/react/e2e/sessionTools.e2e.test.tsx) drives the real tools
 * through a real llmd binary and asserts the mapped events reach a rendered
 * ChatPanel; this file pins the wire->ConversationEvent mapping itself.
 *
 * Frames are built with `create(ConversationStreamFrameSchema, ...)`, setting the
 * oneof as `{ frame: { case, value } }`. That replaced ts-proto's flat
 * per-member optional fields (`{ sessionStatus: {...} }`) when both transports
 * moved to one protobuf-es codegen — and since there is now ONE mapper rather
 * than one per transport, this file is the only place that shape is pinned.
 */

import { describe, expect, it } from 'vitest';
import { create, type MessageInitShape } from '@bufbuild/protobuf';
import {
  conversationStreamFrameToEvent,
  ConversationEventType,
  CONVERSATION_EVENT_SCHEMA_VERSION,
} from '../src/events.js';
import {
  ConversationStreamFrameSchema,
  type ConversationStreamFrame,
} from '../src/gen/llmv1/llm_pb.js';

function frame(
  oneof: MessageInitShape<typeof ConversationStreamFrameSchema>['frame']
): ConversationStreamFrame {
  return create(ConversationStreamFrameSchema, {
    schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
    frame: oneof,
  });
}

describe('conversationStreamFrameToEvent: session tool frames', () => {
  it('maps session_status to a SessionStatus event', () => {
    const ev = conversationStreamFrameToEvent(
      frame({
        case: 'sessionStatus',
        value: { sessionId: 'sess-1', headline: 'Looking for a fish...' },
      })
    );
    expect(ev).toEqual({
      type: ConversationEventType.SessionStatus,
      schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
      sessionId: 'sess-1',
      headline: 'Looking for a fish...',
    });
  });

  it('maps session_title_changed to a SessionTitleChanged event', () => {
    const ev = conversationStreamFrameToEvent(
      frame({
        case: 'sessionTitleChanged',
        value: { sessionId: 'sess-1', title: 'Fish-finding session' },
      })
    );
    expect(ev).toEqual({
      type: ConversationEventType.SessionTitleChanged,
      schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
      sessionId: 'sess-1',
      title: 'Fish-finding session',
    });
  });

  it('maps todos_updated, decoding todos_json into structured items', () => {
    const todosJson = JSON.stringify([
      { id: 't1', description: 'find a fish', status: 'pending', created_turn_seq: 1 },
      {
        id: 't2',
        description: 'contemplate the universe',
        status: 'completed',
        created_turn_seq: 1,
        started_turn_seq: 2,
        completed_turn_seq: 3,
      },
    ]);
    const ev = conversationStreamFrameToEvent(
      frame({ case: 'todosUpdated', value: { sessionId: 'sess-1', todosJson } })
    );
    expect(ev).toEqual({
      type: ConversationEventType.TodosUpdated,
      schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
      sessionId: 'sess-1',
      todos: [
        { id: 't1', description: 'find a fish', status: 'pending', createdTurnSeq: 1 },
        {
          id: 't2',
          description: 'contemplate the universe',
          status: 'completed',
          createdTurnSeq: 1,
          startedTurnSeq: 2,
          completedTurnSeq: 3,
        },
      ],
    });
  });

  it('todos_updated with malformed todos_json decodes to an empty list, not a throw', () => {
    const ev = conversationStreamFrameToEvent(
      frame({
        case: 'todosUpdated',
        value: { sessionId: 'sess-1', todosJson: '{not valid json' },
      })
    );
    expect(ev).toEqual({
      type: ConversationEventType.TodosUpdated,
      schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
      sessionId: 'sess-1',
      todos: [],
    });
  });

  it('todos_updated with an empty array decodes to an empty list', () => {
    const ev = conversationStreamFrameToEvent(
      frame({ case: 'todosUpdated', value: { sessionId: 'sess-1', todosJson: '[]' } })
    );
    expect(ev).toEqual({
      type: ConversationEventType.TodosUpdated,
      schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
      sessionId: 'sess-1',
      todos: [],
    });
  });

  it('an empty frame (no oneof member set) maps to undefined', () => {
    expect(conversationStreamFrameToEvent(frame(undefined))).toBeUndefined();
  });

  it('stream_complete is consumed by callers, not mapped to an event', () => {
    // chatStreamEvents/stream read this variant directly to build the final
    // AssistantMessage, so the mapper must ignore it rather than emit a bogus
    // event — pinning it here because the switch's default arm is what makes
    // that true.
    expect(
      conversationStreamFrameToEvent(
        frame({ case: 'streamComplete', value: { content: 'hi', id: 'asst-1' } })
      )
    ).toBeUndefined();
  });
});
