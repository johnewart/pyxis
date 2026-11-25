---
title: Keep provider-config TS/React surfaces in sync with llm.proto by hand
summary: Rule requiring manual synchronization of Go, TypeScript client, and
  React form code whenever llmd provider config proto fields change, rejecting
  reflection-driven forms.
---
llmd's per-provider LLM config (`AnthropicLlmConfig`, `OpenAiLlmConfig`, `OllamaLlmConfig`, `BedrockLlmConfig`, `GeminiLlmConfig`, `CursorCliLlmConfig`, `ClaudeCodeLlmConfig` in `proto/llmv1/llm.proto`) is deliberately **not** driven by runtime reflection or a generated schema RPC. `@pyxis-llm/client`'s ts-proto codegen carries no field metadata at runtime, so proto doc comments/labels/secret-flags are lost — a real "auto-updating" form isn't actually free. Given the provider list is small and closed-ish, hand-maintained wrappers are less overall work and avoid reflection corner cases (JSON blobs like `hooks_json`, string-enum-that-isn't-a-proto-enum fields like `permission_mode`, secret-field detection, etc.).

**Rule:** whenever a field is added/removed/renamed on any provider config message in `proto/llmv1/llm.proto`, update all of the following in the same change:
1. `internal/llm/providers/register_proto.go` — `BuildProviderConfigFromRequest`, `BuildProviderConfigFromUpdateRequest`, `BuildGetLlmProviderConfigResponse` (Go request/response ⇄ store-row mapping).
2. `packages/client/src/llm.ts` — `RegisterProviderConfig` type + `registerProviderConfigToRequestFields` + the `getProviderConfig` reverse-mapper.
3. Any per-provider React form component in `@pyxis-llm/react` (e.g. `ProviderConnectForm.tsx` and its eventual create/edit successor) that lists fields explicitly.

Do not build a reflection-driven or server-schema-driven generic form for this — it was evaluated and rejected in favor of this manual-sync approach.