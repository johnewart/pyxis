// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

// Package llmdtest is a reusable e2e test harness for services that embed
// or wrap llmd. It spawns a real llmd binary (the same lifecycle a host
// process uses), registers a scriptable fake OpenAI-compatible provider
// against it, and hands back a real llmv1.LlmServiceClient wired to a
// session — so a Go program that talks to llmd over gRPC can exercise its
// own integration against real llmd behavior without a live LLM API key or
// network access.
//
// This is the Go equivalent of packages/client/e2e/harness.ts, which does
// the same thing for the TypeScript client. Both drive llmd purely through
// its public gRPC surface (RegisterLlmProvider, EnsureSession, Chat /
// ChatStream) — neither imports llmd's internal packages.
//
// FakeOpenAI script vocabulary (one Turn per request, consumed in order):
//
//   - TextTurn, SlowTextTurn: streamed or JSON assistant text.
//   - ToolCallTurn: one tool call; ToolCallsTurn(ToolCall{...}, ...): several
//     tool calls with indexes 0..n-1, finish_reason=tool_calls.
//   - ErrorTurn(status, message): OpenAI-style error JSON with that HTTP
//     status (e.g. 429, 500, 400), for streaming and non-streaming requests.
//   - Turn.WithUsage(prompt, completion, reasoning): adds token usage. JSON
//     replies include a usage object; streams emit a final usage chunk when
//     the request set stream_options.include_usage.
//   - HangTurn: never responds, for cancellation tests.
//
// For StartInProcess, the llmd-fake binary (ready line, control JSON schema)
// and a Python example, see docs/llmdtest.md.
//
// Typical use:
//
//	env, err := llmdtest.Start(ctx, llmdtest.Options{BinaryPath: "./llmd-e2e-bin"})
//	if err != nil { t.Fatal(err) }
//	defer env.Stop(ctx)
//
//	env.FakeOpenAI.Enqueue(llmdtest.TextTurn("hello there"))
//	sessCtx, client := env.NewSession(ctx, tenantscope.Scope{TenantID: "my-tenant", ScopeID: "my-scope"}, "my-session")
//	reply, err := client.Chat(sessCtx, &llmv1.ChatRequest{ ... })
package llmdtest
