/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { enumFromJson } from '@bufbuild/protobuf';
// LlmProviderType is re-exported from the package root now; the old
// '@pyxis-llm/client/gen/llmv1/llm.js' ts-proto subpath is gone (the generated file
// is llm_pb.ts, and the generated modules are also available namespaced as
// `llmv1`).
import { LlmProviderType, llmv1, type RegisterProviderConfig } from '@pyxis-llm/client';

/**
 * Canonical, host-agnostic provider kind. This is llmd's `LlmProviderType`
 * proto enum collapsed to a small type-safe string union: one name per kind,
 * independent of any particular host's config vocabulary or UI labels.
 *
 * These names are llmd's OWN vocabulary, not a parallel one: each is exactly
 * `providers.Kind.String()` on the Go side, i.e. the `short_code` that
 * `ListSupportedLlmProviderTypes` advertises and the `type` that
 * `@pyxis-llm/client`'s `RegisterProviderConfig` takes. A provider kind therefore
 * has ONE id across the proto enum, Go, and both TS packages.
 *
 * This file used to invent its own spellings (`claude_v2`, `cursor`), which is
 * how the same SDK ended up disagreeing with itself: normalising a provider
 * type here and handing the result to `registerProvider()` sent llmd a name it
 * rejected — "no configuration shape for provider type claude_v2" — which made
 * Claude Code impossible to add. Do not reintroduce a second name for a kind;
 * if a name must change, change it in llmd's `Kind` and let it flow outward.
 *
 * `LLM_PROVIDER_TYPE_CLAUDE_CODE` (v1) intentionally maps to the same kind as
 * `LLM_PROVIDER_TYPE_CLAUDE_CODE_V2`; there is no live host that still needs
 * to distinguish them.
 *
 * It is an ALIAS of `@pyxis-llm/client`'s `RegisterProviderConfig["type"]`, not a
 * union that repeats the same seven literals: spelling them out again here is
 * exactly how this file drifted the first time. Adding a kind in the proto (and
 * therefore in `RegisterProviderConfig`) widens this automatically, and the
 * switch below then fails to compile until it is handled.
 */
export type ProviderKind = RegisterProviderConfig['type'];

/**
 * Map an llmd `LlmProviderType` enum value to a {@link ProviderKind}.
 *
 * NOTE: protobuf-es strips the redundant `LLM_PROVIDER_TYPE_` prefix from the
 * generated member names, so these are `LlmProviderType.OPENAI` etc. The wire
 * numbers are unchanged. There is also no `UNRECOGNIZED` member any more —
 * protobuf-es represents an unknown wire value as the bare number, which the
 * `default` arm below absorbs.
 */
export function providerKindFromLlmProviderType(
  type: LlmProviderType
): ProviderKind {
  switch (type) {
    case LlmProviderType.OPENAI:
      return 'openai';
    case LlmProviderType.OLLAMA:
      return 'ollama';
    case LlmProviderType.ANTHROPIC:
      return 'anthropic';
    case LlmProviderType.GEMINI:
      return 'gemini';
    case LlmProviderType.OPENROUTER:
      return 'openrouter';
    case LlmProviderType.BEDROCK:
      return 'bedrock';
    case LlmProviderType.CLAUDE_CODE:
    case LlmProviderType.CLAUDE_CODE_V2:
      return 'claude_code_v2';
    case LlmProviderType.CURSOR_CLI:
      return 'cursor_cli';
    case LlmProviderType.UNSPECIFIED:
    default:
      // Do NOT invent a kind here. This used to return 'openai', so an unset or
      // unrecognised provider type silently became a real, plausible provider:
      // garbage in, valid-looking value out, and no caller could tell. An
      // unmapped enum value is a bug or a newer llmd, and both deserve to be
      // seen.
      throw new Error(`unsupported llmd provider type ${String(type)}`);
  }
}

/**
 * Map an llmd `LlmProviderType` JSON enum string (as seen on the wire, e.g.
 * from ListLlmProviders) to a {@link ProviderKind}. Thin wrapper over
 * {@link providerKindFromLlmProviderType} for callers holding the JSON form.
 *
 * Throws on a string that is not a known `LlmProviderType`, for the same reason
 * as {@link providerKindFromLlmProviderType}: this previously swallowed the
 * error and returned 'openai', turning an unparseable wire value into a real
 * provider kind.
 */
export function providerKindFromLlmProviderTypeJson(
  typeJson: string
): ProviderKind {
  let parsed: LlmProviderType;
  try {
    parsed = enumFromJson(llmv1.LlmProviderTypeSchema, typeJson as never);
  } catch {
    throw new Error(
      `unsupported llmd provider type ${JSON.stringify(typeJson)}: ` +
        `not a known LlmProviderType`
    );
  }
  return providerKindFromLlmProviderType(parsed);
}
