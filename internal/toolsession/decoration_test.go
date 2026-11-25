package toolsession

import (
	"context"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// TestAddDecoration_TargetsFromEnvelope proves AddDecoration derives the target
// from the envelope (tool_result when ToolCallID is set, turn otherwise), stamps
// the canonical turn identity (turn_seq/turn_id), and round-trips through
// ListDecorations in insertion order. llmd stores Kind/URI verbatim.
func TestAddDecoration_TargetsFromEnvelope(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	svc := New(nil, store.NewSessionRepo(db), store.NewTurnDecorationRepo(db))

	env := ToolInvocationEnvelope{
		Scope:      serverutil.Scope{TenantID: " ws-1 "}, // trimmed on write
		SessionID:  "sess-1",
		TurnID:     "turn-9",
		TurnSeq:    4,
		ToolCallID: "call-1",
		ToolName:   "read_code",
		Source:     "mcp-hub",
	}
	svc.AddDecoration(ctx, env, Decoration{Kind: "host_provenance", URI: "host://provenance/abc"})

	turnEnv := env
	turnEnv.ToolCallID = ""
	svc.AddDecoration(ctx, turnEnv, Decoration{Kind: "entity_reference", URI: "owlbert://entity/char-42"})

	// Empty kind and empty session are silent no-ops.
	svc.AddDecoration(ctx, env, Decoration{Kind: "  "})
	svc.AddDecoration(ctx, ToolInvocationEnvelope{}, Decoration{Kind: "x"})

	rows, err := svc.ListDecorations(ctx, serverutil.Scope{TenantID: "ws-1"}, "sess-1")
	if err != nil {
		t.Fatalf("ListDecorations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 decorations, got %d", len(rows))
	}

	first := rows[0]
	if first.Target != "tool_result" || first.ToolCallID != "call-1" {
		t.Errorf("first: want tool_result/call-1, got %q/%q", first.Target, first.ToolCallID)
	}
	if first.Kind != "host_provenance" || first.URI != "host://provenance/abc" {
		t.Errorf("first: kind/uri = %q/%q, want host_provenance/host://provenance/abc", first.Kind, first.URI)
	}
	if first.TenantID != "ws-1" || first.TurnID != "turn-9" || first.TurnSeq == nil || *first.TurnSeq != 4 {
		t.Errorf("first: identity mismatch: %+v", first)
	}

	second := rows[1]
	if second.Target != "turn" || second.ToolCallID != "" {
		t.Errorf("second: want turn-level, got %q/%q", second.Target, second.ToolCallID)
	}
	if second.Kind != "entity_reference" || second.URI != "owlbert://entity/char-42" {
		t.Errorf("second: kind/uri mismatch: %+v", second)
	}
}

// TestRecordInvocation_PassesDecorationsThrough proves RecordInvocation persists
// every Decoration on the envelope verbatim through the single chokepoint,
// without interpreting Kind or URI, alongside dedup-key recording.
func TestRecordInvocation_PassesDecorationsThrough(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	sessions := store.NewSessionRepo(db)
	svc := New(store.NewToolResultSidecarRepo(db), sessions, store.NewTurnDecorationRepo(db))

	const ws = "ws-dec"
	const sid = "sess-dec"

	env := ToolInvocationEnvelope{
		Scope: serverutil.Scope{TenantID: ws}, SessionID: sid, TurnSeq: 3,
		ToolCallID: "call-1", ToolName: "read_code", Source: "mcp-hub",
		DedupKey: "rev:file:1-100",
		Decorations: []Decoration{
			{Kind: "host_provenance", URI: "host://provenance/gr-1"},
			{Kind: "entity_reference", URI: "owlbert://entity/char-1"},
		},
	}
	svc.RecordInvocation(ctx, env)

	if got := svc.ToolDedupKey(sid, "call-1"); got != "rev:file:1-100" {
		t.Fatalf("ToolDedupKey = %q, want rev:file:1-100", got)
	}

	rows, err := svc.ListDecorations(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("ListDecorations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 decorations, got %d: %+v", len(rows), rows)
	}
	byKind := map[string]store.TurnDecorationRow{}
	for _, r := range rows {
		byKind[r.Kind] = r
	}
	if byKind["host_provenance"].URI != "host://provenance/gr-1" {
		t.Errorf("host_provenance uri = %q", byKind["host_provenance"].URI)
	}
	if byKind["entity_reference"].URI != "owlbert://entity/char-1" {
		t.Errorf("entity_reference uri = %q", byKind["entity_reference"].URI)
	}
	for _, r := range rows {
		if r.TurnSeq == nil || *r.TurnSeq != 3 || r.Target != "tool_result" || r.ToolCallID != "call-1" {
			t.Errorf("decoration identity mismatch: %+v", r)
		}
	}
}

// TestDecorationEmission_NoLongerAttachesCompressionOrDedupRef proves llmd's
// own compression/dedup bookkeeping is no longer mirrored into decorations —
// only caller-supplied Decorations are ever written. The dedup wire mechanism
// itself (DedupRefToolCallID shaping the compressed Wire content) is
// unaffected; only the redundant presentation-row copy of it is gone.
func TestDecorationEmission_NoLongerAttachesCompressionOrDedupRef(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	sessions := store.NewSessionRepo(db)
	svc := New(store.NewToolResultSidecarRepo(db), sessions, store.NewTurnDecorationRepo(db))

	const ws = "ws-dec2"
	const sid = "sess-dec2"
	if err := sessions.UpsertSession(ctx, store.SessionRow{
		TenantID:                       ws,
		SessionID:                      sid,
		ToolResultCompressionEnabled:   boolPtr(true),
		ToolResultMinifyDedup:          boolPtr(true),
		ToolResultCompressionThreshold: int64Ptr(10),
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	content := buildCompressibleContent()
	env1 := ToolInvocationEnvelope{
		Scope: serverutil.Scope{TenantID: ws}, SessionID: sid, TurnSeq: 3,
		ToolCallID: "call-1", ToolName: "read_code", Source: "mcp-hub",
		DedupKey: "rev:file:1-100", DedupSummary: "file summary",
	}
	if wire := svc.IngestDelegatedResult(ctx, env1, content, false); wire == content {
		t.Fatalf("first result was not compressed")
	}

	env2 := env1
	env2.ToolCallID = "call-2"
	wire2 := svc.IngestDelegatedResult(ctx, env2, content, false)
	if wire2 == content {
		t.Fatalf("second result was not deduped")
	}
	if !strings.Contains(wire2, "call-1") {
		t.Fatalf("dedup wire banner should still reference call-1: %s", wire2)
	}

	rows, err := svc.ListDecorations(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("ListDecorations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 decorations (compression/dedup_ref no longer mirrored), got %d: %+v", len(rows), rows)
	}
}

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(n int64) *int64 { return &n }

// TestAddDecoration_NilSafe proves nil service / nil repo are no-ops.
func TestAddDecoration_NilSafe(t *testing.T) {
	ctx := context.Background()
	var nilSvc *Service
	nilSvc.AddDecoration(ctx, ToolInvocationEnvelope{SessionID: "s"}, Decoration{Kind: "k"})
	if rows, err := nilSvc.ListDecorations(ctx, serverutil.Scope{}, "s"); err != nil || rows != nil {
		t.Fatalf("nil service ListDecorations: rows=%v err=%v", rows, err)
	}

	noRepo := New(nil, nil, nil)
	noRepo.AddDecoration(ctx, ToolInvocationEnvelope{SessionID: "s"}, Decoration{Kind: "k"})
	if rows, err := noRepo.ListDecorations(ctx, serverutil.Scope{}, "s"); err != nil || rows != nil {
		t.Fatalf("no-repo ListDecorations: rows=%v err=%v", rows, err)
	}
}
