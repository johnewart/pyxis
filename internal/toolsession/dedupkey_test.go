package toolsession

import "testing"

// The semantic dedupKey recording is what gives API providers (OpenAI/ollama,
// whose tool results flow through the gRPC ChatStream path) the same content
// dedup as CLI providers: a delegated tool (e.g. read_code) returns a content
// identity key, we remember it per tool_call_id, and send-time compression keys
// on it instead of the tool_call_id.

func TestRecordAndLookupToolDedupKey(t *testing.T) {
	s := New(nil, nil, nil) // pure in-memory map ops; repos not needed

	s.RecordToolDedupKey("sess-1", "call-A", "rev:README.md:1-20")

	if got := s.ToolDedupKey("sess-1", "call-A"); got != "rev:README.md:1-20" {
		t.Fatalf("ToolDedupKey = %q, want the recorded key", got)
	}

	// A different call in the same session that returned the SAME content key
	// (e.g. a subset read riding the covering read) maps to the same key, so
	// send-time dedup collapses them together.
	s.RecordToolDedupKey("sess-1", "call-B", "rev:README.md:1-20")
	if got := s.ToolDedupKey("sess-1", "call-B"); got != "rev:README.md:1-20" {
		t.Fatalf("call-B key = %q, want shared key", got)
	}

	// Unknown call / session → empty (caller falls back to tool_call_id).
	if got := s.ToolDedupKey("sess-1", "call-unknown"); got != "" {
		t.Fatalf("unknown call key = %q, want empty", got)
	}
	if got := s.ToolDedupKey("sess-other", "call-A"); got != "" {
		t.Fatalf("cross-session key = %q, want empty (session-scoped)", got)
	}
}

func TestRecordToolDedupKey_IgnoresEmpty(t *testing.T) {
	s := New(nil, nil, nil)
	s.RecordToolDedupKey("", "call", "key")
	s.RecordToolDedupKey("sess", "", "key")
	s.RecordToolDedupKey("sess", "call", "") // empty key ⇒ never deduped
	if got := s.ToolDedupKey("sess", "call"); got != "" {
		t.Fatalf("empty-key record should be a no-op, got %q", got)
	}
}

func TestClearDedupRemovesSemanticKeys(t *testing.T) {
	s := New(nil, nil, nil)
	s.RecordToolDedupKey("sess-1", "call-A", "k")
	s.ClearDedup("sess-1")
	if got := s.ToolDedupKey("sess-1", "call-A"); got != "" {
		t.Fatalf("after ClearDedup key = %q, want empty (reused session starts clean)", got)
	}
}

func TestServiceNilSafe(t *testing.T) {
	var s *Service
	s.RecordToolDedupKey("sess", "call", "k") // must not panic
	if got := s.ToolDedupKey("sess", "call"); got != "" {
		t.Fatalf("nil service ToolDedupKey = %q, want empty", got)
	}
}
