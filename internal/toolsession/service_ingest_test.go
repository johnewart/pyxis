package toolsession

import (
	"context"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/proto"
)

// buildCompressibleContent returns a payload large enough to exceed a small
// threshold and full of blank/comment lines that structural minify strips, so
// compression is guaranteed to shrink it (mirrors internal/server's
// buildCompressibleResult helper).
func buildCompressibleContent() string {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("// throwaway comment line that minify removes entirely\n")
		b.WriteString("\n")
		b.WriteString("kept content line with real signal token number here\n")
	}
	return b.String()
}

// TestIngestDelegatedResult_SourceParity proves IngestDelegatedResult gives the
// gRPC tool loop ("grpc-loop"), the MCP hub path ("mcp-hub"), and the MCP
// HTTP-delegate path ("mcp-http") byte-identical wire output and dedup-key
// recording for identical content/dedup inputs — the only per-source
// difference being the envelope's Source/TurnID/TurnSeq.
//
// It also asserts decoration parity: each source attaches the same Decorations
// to its envelope and IngestDelegatedResult records them through the SAME
// chokepoint, stamped with the envelope's TurnSeq even on the MCP paths where
// the legacy TurnID is empty — the consolidation that lets decorations inherit
// the turn without a separate, turn-less call.
func TestIngestDelegatedResult_SourceParity(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	sidecar := store.NewToolResultSidecarRepo(db)
	sessions := store.NewSessionRepo(db)
	decorations := store.NewTurnDecorationRepo(db)
	svc := New(sidecar, sessions, decorations)

	const workspaceID = "ws-1"

	content := buildCompressibleContent()
	const dedupKey = "rev:parity-content:1"
	const dedupSummary = "parity summary"

	sources := []string{"grpc-loop", "mcp-hub", "mcp-http"}
	wires := make(map[string]string, len(sources))

	for i, source := range sources {
		// Each source gets its own session so the content-identity dedup map
		// (keyed per-session) doesn't collapse the 2nd/3rd calls into a
		// "ref_tool_call_id" back-reference — that's the SAME per-session
		// dedup behavior all three sources share, not a source difference.
		sessionID := "sess-parity-" + source
		sessionRow := store.SessionRow{
			TenantID:                       workspaceID,
			SessionID:                      sessionID,
			ToolResultCompressionEnabled:   proto.Bool(true),
			ToolResultMinifyDedup:          proto.Bool(true),
			ToolResultCompressionThreshold: proto.Int64(10),
			ToolResultAgeKeep:              proto.Int64(4),
		}
		if err := sessions.UpsertSession(ctx, sessionRow); err != nil {
			t.Fatalf("UpsertSession(%q): %v", source, err)
		}

		// Same ToolCallID across sources: the compressed wire banner embeds
		// tool_call_id, so the only inputs allowed to differ per source are
		// Source/TurnID themselves — separate sessions already give call-id
		// isolation, so reusing the id here is safe and required for a true
		// byte-identical-wire assertion.
		env := ToolInvocationEnvelope{
			Scope:        serverutil.Scope{TenantID: workspaceID},
			SessionID:    sessionID,
			ToolCallID:   "call-parity",
			ToolName:     "read_code",
			Source:       source,
			DedupKey:     dedupKey,
			DedupSummary: dedupSummary,
		}
		if source == "grpc-loop" {
			env.TurnID = "turn-42" // real turn id on the gRPC path; "" on MCP paths
		}
		// Every source carries the same turn_seq on the envelope plus a
		// decoration, so we can prove decorations inherit turn_seq through the
		// one chokepoint even when the legacy TurnID is empty (the MCP paths).
		env.TurnSeq = 7
		env.Decorations = []Decoration{{Kind: "host_provenance", URI: "host://provenance/gr-1"}}
		wire := svc.IngestDelegatedResult(ctx, env, content, false)
		if wire == content {
			t.Fatalf("source %q: content was not compressed", source)
		}
		wires[source] = wire

		gotKey := svc.ToolDedupKey(sessionID, env.ToolCallID)
		if gotKey != dedupKey {
			t.Fatalf("source %q: ToolDedupKey = %q, want %q", source, gotKey, dedupKey)
		}

		// Decoration parity: exactly one decoration row per source, stamped
		// with the envelope's TurnSeq (7) regardless of whether TurnID was set.
		rows, err := decorations.ListBySession(ctx, serverutil.Scope{TenantID: workspaceID}, sessionID)
		if err != nil {
			t.Fatalf("source %q: ListBySession: %v", source, err)
		}
		var decoRows int
		for _, r := range rows {
			if r.Kind != "host_provenance" {
				continue
			}
			decoRows++
			if r.TenantID != workspaceID {
				t.Fatalf("source %q: decoration row TenantID = %q, want %q", source, r.TenantID, workspaceID)
			}
			if r.TurnSeq == nil || *r.TurnSeq != 7 {
				t.Fatalf("source %q: decoration row TurnSeq = %v, want 7 (stamped from envelope even when TurnID empty)", source, r.TurnSeq)
			}
			wantTurnID := ""
			if source == "grpc-loop" {
				wantTurnID = "turn-42"
			}
			if r.TurnID != wantTurnID {
				t.Fatalf("source %q: decoration row TurnID = %q, want %q", source, r.TurnID, wantTurnID)
			}
		}
		if decoRows != 1 {
			t.Fatalf("source %q: want exactly 1 decoration row, got %d", source, decoRows)
		}
		_ = i
	}

	first := wires[sources[0]]
	for _, source := range sources[1:] {
		if wires[source] != first {
			t.Fatalf("wire output diverged for source %q: got %q, want %q (same as %q)",
				source, wires[source], first, sources[0])
		}
	}
}

// TestIngestDelegatedResult_ErrorSkipsRecording proves an error result never
// records a dedup key or compresses, matching the existing !isErr guards.
func TestIngestDelegatedResult_ErrorSkipsRecording(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	sidecar := store.NewToolResultSidecarRepo(db)
	sessions := store.NewSessionRepo(db)
	svc := New(sidecar, sessions, store.NewTurnDecorationRepo(db))

	const workspaceID = "ws-err"
	sessionRow := store.SessionRow{
		TenantID:                       workspaceID,
		SessionID:                      "sess-err",
		ToolResultCompressionEnabled:   proto.Bool(true),
		ToolResultMinifyDedup:          proto.Bool(true),
		ToolResultCompressionThreshold: proto.Int64(10),
	}
	if err := sessions.UpsertSession(ctx, sessionRow); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	content := buildCompressibleContent()
	env := ToolInvocationEnvelope{
		Scope:      serverutil.Scope{TenantID: workspaceID},
		SessionID:  sessionRow.SessionID,
		ToolCallID: "call-err",
		ToolName:   "read_code",
		Source:     "grpc-loop",
		DedupKey:   "rev:err-content:1",
	}
	wire := svc.IngestDelegatedResult(ctx, env, content, true)
	if wire != content {
		t.Fatalf("error result was compressed; want content unchanged")
	}
	if got := svc.ToolDedupKey(sessionRow.SessionID, env.ToolCallID); got != "" {
		t.Fatalf("error result recorded a dedup key: %q, want empty", got)
	}
}
