/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import type { MessageInitShape } from "@bufbuild/protobuf";
import type { SystemPromptSectionSchema } from "./gen/llmv1/llm_pb.js";
import { grpcSafeString } from "./utf8.js";

/** Wire shape for structured system prompt sections (matches llm.v1.SystemPromptSection). */
export type SystemPromptSectionWire = {
  id: string;
  content: string;
  tags?: string[];
};

/**
 * Build message init shapes for ChatStreamRequest.system_prompt_sections.
 *
 * protobuf-es accepts plain object initialisers for nested messages, so this no
 * longer needs a per-message constructor call the way ts-proto's fromPartial did.
 */
export function systemPromptSectionsToProto(
  sections: readonly SystemPromptSectionWire[]
): MessageInitShape<typeof SystemPromptSectionSchema>[] {
  return sections.map((s) => ({
    id: grpcSafeString(s.id),
    content: grpcSafeString(s.content),
    ...(s.tags?.length ? { tags: s.tags.map((tag) => grpcSafeString(tag)) } : {}),
  }));
}

export function systemPromptSectionsWireJson(
  sections: readonly SystemPromptSectionWire[]
): string {
  return JSON.stringify(
    sections.map((s) => ({
      id: s.id,
      content: s.content,
      ...(s.tags?.length ? { tags: s.tags } : {}),
    }))
  );
}

const B64_ALPHABET =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/**
 * UTF-8 -> base64 without Buffer.
 *
 * This used to be `Buffer.from(...).toString('base64')`, which is Node-only and
 * would have thrown in a browser bundle the moment this module became shared
 * between both transports. TextEncoder is available in Node 18+ and every
 * fetch-capable environment, so encode the bytes ourselves rather than reaching
 * for a global that may not exist.
 */
function base64Utf8(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i]!;
    const b1 = bytes[i + 1];
    const b2 = bytes[i + 2];
    out += B64_ALPHABET[b0 >> 2];
    out += B64_ALPHABET[((b0 & 0x03) << 4) | ((b1 ?? 0) >> 4)];
    out += b1 === undefined ? "=" : B64_ALPHABET[((b1 & 0x0f) << 2) | ((b2 ?? 0) >> 6)];
    out += b2 === undefined ? "=" : B64_ALPHABET[b2 & 0x3f];
  }
  return out;
}

/** Base64-encoded JSON for header fallback (header values must not contain newlines). */
export function systemPromptSectionsWireMetadata(
  sections: readonly SystemPromptSectionWire[]
): string {
  return `b64:${base64Utf8(systemPromptSectionsWireJson(sections))}`;
}
