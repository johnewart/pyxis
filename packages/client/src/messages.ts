/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * The llm.v1.Message-wire-compatible message shapes this client sends and
 * receives. Zero transport dependencies by design — no generated types, no
 * Node or browser built-ins — so this module is safe to import from either a
 * Node process or a browser bundle.
 */

/** Role strings match llm.v1 Message and ai-library conventions. */
export type MessageRole = "system" | "user" | "assistant" | "tool";

export interface Message {
  readonly role: MessageRole;
  content: string;
  id: string;
  metadata?: Record<string, unknown>;
}

export class SystemMessage implements Message {
  readonly role = "system" as const;
  constructor(
    public content: string,
    public id: string,
    public metadata?: Record<string, unknown>
  ) {}
}

/** Multipart user content (persisted in metadata as `contentParts`). */
export type UserTextPart = { type: "text"; text: string };

/**
 * An attachment sent ON a user message (mirrors llm.v1 MessageAttachment).
 * llmd persists it anchored to this message; `content` is provider-facing and
 * spliced into the message at send time; `payloadJson` is client-only.
 */
export interface UserMessageAttachment {
  id: string;
  kind: string;
  payloadJson: string;
  afterToolCallId?: string;
  content?: string;
}

export class UserMessage implements Message {
  readonly role = "user" as const;
  content: string;
  /** Attachments carried by this message (sent with it; persisted by llmd). */
  attachments?: UserMessageAttachment[];
  constructor(
    contentOrParts: string | ReadonlyArray<UserTextPart>,
    public id: string,
    public metadata?: Record<string, unknown>
  ) {
    if (typeof contentOrParts === "string") {
      this.content = contentOrParts;
    } else {
      const parts: UserTextPart[] = contentOrParts.map((p) => ({
        type: "text" as const,
        text:
          p?.type === "text" && typeof p.text === "string"
            ? p.text
            : String((p as { text?: unknown })?.text ?? p),
      }));
      this.content = parts.map((p) => p.text).join("");
      this.metadata = { ...metadata, contentParts: parts };
    }
  }
}

/** OpenAI-style tool call on an assistant message (ai-library shape). */
export interface AssistantToolCall {
  id: string;
  name: string;
  arguments: Record<string, unknown>;
  /** Gemini 3+ opaque signature; required when replaying tool history to Gemini. */
  thoughtSignature?: Uint8Array;
}

export class AssistantMessage implements Message {
  readonly role = "assistant" as const;
  toolCalls?: AssistantToolCall[];
  finishReason?: string;
  constructor(
    public content: string,
    public id: string,
    public metadata?: Record<string, unknown>
  ) {}
}

export class ToolMessage implements Message {
  readonly role = "tool" as const;
  constructor(
    public content: string,
    public toolCallId: string,
    public id: string,
    public metadata?: Record<string, unknown>
  ) {}
}
