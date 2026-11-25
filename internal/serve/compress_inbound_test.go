package serve_test

// This test lives in the serve package's external test package (serve_test),
// not package serve itself: it drives server.LlmServer directly, and
// internal/server imports serve for the debug-logging aliases (see
// internal/server/debug_shim.go), so an internal serve test importing internal/server
// would be a real import cycle. See phase 4 of the llmd/internal/serve
// extraction for context.

import (
	"context"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolresult"
)

// buildCompressibleResult returns a tool-result payload large enough to exceed a
// small threshold and full of blank/comment lines that structuralMinify strips,
// so compression is guaranteed to shrink it.
func buildCompressibleResult() string {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("// throwaway comment line that minify removes entirely\n")
		b.WriteString("\n")
		b.WriteString("kept content line with real signal token number here\n")
	}
	return b.String()
}

// TestCompressToolResultsForSend_WritesSidecarRow drives the chokepoint over the
// provider-bound prompt through a real SQLite sidecar repo and asserts the send copy
// is compressed, the original is left raw, and the sidecar row carries diagnostics.
func TestCompressToolResultsForSend_WritesSidecarRow(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	repo := store.NewToolResultSidecarRepo(db)
	srv := &server.LlmServer{ToolResultSidecar: repo}

	settings := toolresult.Settings{
		Enabled:          true,
		MinThresholdToks: 10,
		MinifyDedup:      true,
	}

	original := buildCompressibleResult()
	const sid = "sess-inbound"
	const callID = "toolcall-1"

	msgs := []*llmv1.Message{
		{Role: "user", Content: "summarize the project"},
		{Role: "tool", ToolCallId: callID, Content: original},
	}

	out := srv.CompressToolResultsForSend(ctx, "agent-1", sid, msgs, settings)

	// The provider-bound copy must carry the compressed wire form.
	got := out[1].GetContent()
	if got == original {
		t.Fatalf("tool message was not compressed in the send copy; content unchanged")
	}
	if !toolresult.IsCompressed(got) {
		t.Fatalf("send-copy content missing compression banner: %q", firstLine(got))
	}
	// The ORIGINAL message must stay uncompressed — history persists raw data.
	if msgs[1].GetContent() != original {
		t.Fatalf("original message was mutated; history would persist compressed data")
	}
	// The non-tool message must be untouched.
	if msgs[0].GetContent() != "summarize the project" {
		t.Fatalf("non-tool message was modified: %q", msgs[0].GetContent())
	}

	// The sidecar row must be persisted with diagnostics populated.
	row, err := repo.GetByToolCallID(ctx, serverutil.Scope{TenantID: ""}, sid, callID)
	if err != nil {
		t.Fatalf("GetByToolCallID: %v", err)
	}
	if row.ToolCallID != callID {
		t.Fatalf("sidecar row not found for %q (got %q)", callID, row.ToolCallID)
	}
	if !row.Compressed {
		t.Fatalf("sidecar row Compressed=false, expected true")
	}
	if row.Original != original {
		t.Fatalf("sidecar Original does not match the uncompressed input")
	}
	if row.Wire != got {
		t.Fatalf("sidecar Wire does not match the rewritten message content")
	}
	if row.OriginalTokens <= row.WireTokens {
		t.Fatalf("expected OriginalTokens (%d) > WireTokens (%d)", row.OriginalTokens, row.WireTokens)
	}
	if row.Method == "" {
		t.Fatalf("sidecar Method not recorded")
	}
}

// TestCompressToolResultsForSend_CompressesHistoryMessages proves the chokepoint
// covers tool results arriving via server-side history (histMsgs), not just the
// latest message — the real client case (hist_msgs>0, incoming=1).
func TestCompressToolResultsForSend_CompressesHistoryMessages(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	repo := store.NewToolResultSidecarRepo(db)
	srv := &server.LlmServer{ToolResultSidecar: repo}

	settings := toolresult.Settings{Enabled: true, MinThresholdToks: 10, MinifyDedup: true}
	const sid = "sess-hist"
	original := buildCompressibleResult()

	// Simulate a full prompt: prior history (tool result) + the latest user message.
	full := []*llmv1.Message{
		{Role: "user", Content: "explore the project"},
		{Role: "assistant", Content: ""},
		{Role: "tool", ToolCallId: "hist-call", Content: original}, // lives in histMsgs
		{Role: "user", Content: "now summarize"},                   // the new incoming message
	}

	out := srv.CompressToolResultsForSend(ctx, "agent-1", sid, full, settings)

	if !toolresult.IsCompressed(out[2].GetContent()) {
		t.Fatalf("history tool result was not compressed for send")
	}
	if full[2].GetContent() != original {
		t.Fatalf("canonical history message was mutated")
	}
	row, _ := repo.GetByToolCallID(ctx, serverutil.Scope{TenantID: ""}, sid, "hist-call")
	if !row.Compressed {
		t.Fatalf("sidecar row for history tool result not written")
	}
}

// TestCompressToolResultsForSend_SkipsAlreadyCompressed ensures resent history
// carrying a banner is left alone (no double-compression, no extra sidecar row).
func TestCompressToolResultsForSend_SkipsAlreadyCompressed(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	repo := store.NewToolResultSidecarRepo(db)
	srv := &server.LlmServer{ToolResultSidecar: repo}

	settings := toolresult.Settings{Enabled: true, MinThresholdToks: 10, MinifyDedup: true}
	const sid = "sess-skip"

	alreadyCompressed := "[COMPRESSED tool_call_id=\"x\" original_tokens=999 wire_tokens=5 method=minify+dedup expand=expand_tool_result(\"x\")]\nkept line"
	msgs := []*llmv1.Message{{Role: "tool", ToolCallId: "x", Content: alreadyCompressed}}

	out := srv.CompressToolResultsForSend(ctx, "agent-1", sid, msgs, settings)

	if out[0].GetContent() != alreadyCompressed {
		t.Fatalf("already-compressed message was modified")
	}
	row, err := repo.GetByToolCallID(ctx, serverutil.Scope{TenantID: ""}, sid, "x")
	if err != nil {
		t.Fatalf("GetByToolCallID: %v", err)
	}
	if row.ToolCallID != "" {
		t.Fatalf("expected no sidecar row for already-compressed content, got %q", row.ToolCallID)
	}
}

// TestCompressToolResultsForSend_DisabledNoop ensures nothing is written or
// rewritten when compression is disabled.
func TestCompressToolResultsForSend_DisabledNoop(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	repo := store.NewToolResultSidecarRepo(db)
	srv := &server.LlmServer{ToolResultSidecar: repo}

	original := buildCompressibleResult()
	const sid = "sess-disabled"
	msgs := []*llmv1.Message{{Role: "tool", ToolCallId: "y", Content: original}}

	out := srv.CompressToolResultsForSend(ctx, "agent-1", sid, msgs, toolresult.Settings{Enabled: false})

	if out[0].GetContent() != original {
		t.Fatalf("message rewritten despite compression disabled")
	}
	row, _ := repo.GetByToolCallID(ctx, serverutil.Scope{TenantID: ""}, sid, "y")
	if row.ToolCallID != "" {
		t.Fatalf("sidecar row written despite compression disabled")
	}
}

// TestCompressToolResultsForSend_DedupsIdenticalResults proves the session-scoped
// dedup map collapses a second, distinct tool call carrying identical content into a
// back-reference to the first call (the "run that exact thing again" savings).
func TestCompressToolResultsForSend_DedupsIdenticalResults(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	repo := store.NewToolResultSidecarRepo(db)
	srv := &server.LlmServer{ToolResultSidecar: repo}

	settings := toolresult.Settings{Enabled: true, MinThresholdToks: 10, MinifyDedup: true}
	const sid = "sess-dedup"
	content := buildCompressibleResult()

	// Dedup is keyed by tool_call_id: llmd owns history and resends the SAME tool
	// result every turn. Turn 1 establishes the anchor; turn 2's resend collapses.
	turn1 := []*llmv1.Message{{Role: "tool", ToolCallId: "call-A", Content: content}}
	out1 := srv.CompressToolResultsForSend(ctx, "agent-1", sid, turn1, settings)
	first := out1[0].GetContent()

	turn2 := []*llmv1.Message{{Role: "tool", ToolCallId: "call-A", Content: content}} // resent next turn
	out2 := srv.CompressToolResultsForSend(ctx, "agent-1", sid, turn2, settings)
	second := out2[0].GetContent()

	// Originals must be untouched — history persists raw, uncompressed data.
	if turn1[0].GetContent() != content || turn2[0].GetContent() != content {
		t.Fatalf("original messages were mutated by send-time compression")
	}

	if !toolresult.IsCompressed(first) {
		t.Fatalf("first result not compressed")
	}
	if strings.Contains(first, "ref_tool_call_id") {
		t.Fatalf("first occurrence should carry full minified content, not a back-reference")
	}
	if !strings.Contains(second, "ref_tool_call_id=\"call-A\"") {
		t.Fatalf("resent result did not dedup to a back-reference to call-A: %q", second)
	}
	// The deduped resend must be dramatically smaller than the first occurrence.
	if len(second) >= len(first) {
		t.Fatalf("deduped result (%d bytes) not smaller than first (%d bytes)", len(second), len(first))
	}

	// A distinct call with identical content must NOT dedup (different identity key).
	distinct := []*llmv1.Message{{Role: "tool", ToolCallId: "call-B", Content: content}}
	distinctOut := srv.CompressToolResultsForSend(ctx, "agent-1", sid, distinct, settings)
	if strings.Contains(distinctOut[0].GetContent(), "ref_tool_call_id") {
		t.Fatalf("a distinct tool_call_id must not back-reference another call, even with identical content")
	}

	// A fresh session (cleared dedup state) must NOT back-reference the prior call.
	srv.ClearSessionDedup(sid)
	fresh := []*llmv1.Message{{Role: "tool", ToolCallId: "call-A", Content: content}}
	freshOut := srv.CompressToolResultsForSend(ctx, "agent-1", sid, fresh, settings)
	if strings.Contains(freshOut[0].GetContent(), "ref_tool_call_id") {
		t.Fatalf("after ClearSessionDedup, content should not back-reference a cleared call")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
