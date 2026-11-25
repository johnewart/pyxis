/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit tests for ToolExecutionStarted's arguments, the field a UI renders a
 * descriptive tool label from ("Reading lines 15-200 of foo.go") rather than
 * showing a bare tool name.
 *
 * This matters most for MCP-dispatched tools (Claude Code / Cursor CLI): their
 * model-side tool call happens inside the CLI, so llmd never emits a
 * ToolCallStreamFinished for them and this event is the only one carrying
 * arguments. The Go side pins the emission
 * (internal/mcpserver/tool_args_frames_test.go); this file pins the
 * wire -> ConversationEvent mapping.
 */

import { describe, expect, it } from 'vitest';
import { create, type MessageInitShape } from '@bufbuild/protobuf';
import { conversationStreamFrameToEvent, ConversationEventType } from '../src/events.js';
import { CONVERSATION_EVENT_SCHEMA_VERSION } from '../src/conversationEvents.js';
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

function startedEvent(value: Record<string, unknown>) {
  const ev = conversationStreamFrameToEvent(
    frame({ case: 'toolExecutionStarted', value } as never)
  );
  if (ev?.type !== ConversationEventType.ToolExecutionStarted) {
    throw new Error(`expected ToolExecutionStarted, got ${String(ev?.type)}`);
  }
  return ev;
}

describe('toolExecutionStarted arguments', () => {
  it('decodes arguments_json into a structured object', () => {
    const ev = startedEvent({
      turnId: 'turn-1',
      toolCallId: 'call-1',
      name: 'read_code',
      argumentsJson: JSON.stringify({ path: 'foo.go', start_line: 15, end_line: 200 }),
    });

    expect(ev.name).toBe('read_code');
    expect(ev.toolCallId).toBe('call-1');
    expect(ev.arguments).toEqual({ path: 'foo.go', start_line: 15, end_line: 200 });
    expect(ev.argumentsTruncated).toBeFalsy();
  });

  // Every older llmd, and any frame predating this field, simply has no
  // arguments — that must be an empty object, not a crash.
  it('yields {} when the frame carries no arguments', () => {
    const ev = startedEvent({ turnId: 'turn-1', toolCallId: 'call-1', name: 'list_todos' });

    expect(ev.arguments).toEqual({});
    expect(ev.argumentsTruncated).toBeFalsy();
  });

  // Shortened payloads stay parseable: llmd elides oversized VALUES and keeps
  // keys, so the identifying fields a label needs are still there.
  it('still exposes identifying fields when llmd shortened the arguments', () => {
    const ev = startedEvent({
      turnId: 'turn-1',
      toolCallId: 'call-1',
      name: 'write_code',
      argumentsJson: JSON.stringify({ path: 'big.go', content: 'xxxxx…' }),
      argumentsTruncated: true,
    });

    expect(ev.argumentsTruncated).toBe(true);
    expect(ev.arguments.path).toBe('big.go');
  });

  // Malformed JSON must degrade, not throw: a frame that cannot be parsed
  // should still deliver the tool name so the UI renders something.
  it('degrades to {} on unparseable arguments without throwing', () => {
    const ev = startedEvent({
      turnId: 'turn-1',
      toolCallId: 'call-1',
      name: 'read_code',
      argumentsJson: '{"path": "truncated-mid',
      argumentsTruncated: true,
    });

    expect(ev.arguments).toEqual({});
    expect(ev.name).toBe('read_code');
    expect(ev.argumentsText).toBe('{"path": "truncated-mid');
  });

  // A JSON array/scalar is not a valid arguments object; normalize to {}
  // rather than handing a caller something it cannot index by field name.
  it('normalizes non-object argument payloads to {}', () => {
    for (const payload of ['[1,2,3]', '"a string"', 'null', '42']) {
      const ev = startedEvent({
        turnId: 'turn-1',
        toolCallId: 'call-1',
        name: 'read_code',
        argumentsJson: payload,
      });
      expect(ev.arguments).toEqual({});
    }
  });

  it('preserves clientWaitNonce alongside arguments', () => {
    const ev = startedEvent({
      turnId: 'turn-1',
      toolCallId: 'call-1',
      name: 'read_code',
      clientWaitNonce: 'nonce-1',
      argumentsJson: JSON.stringify({ path: 'foo.go' }),
    });

    expect(ev.clientWaitNonce).toBe('nonce-1');
    expect(ev.arguments).toEqual({ path: 'foo.go' });
  });
});
