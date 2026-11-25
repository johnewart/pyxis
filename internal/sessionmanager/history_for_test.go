package sessionmanager

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// HistoryFor serves a persisted transcript without loading the session's
// runtime: reading history must not build a runtime (session-row read,
// tools_json unmarshal) as a side effect.
func TestHistoryFor_readsPersistedHistoryWithoutLoadingRuntime(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	sc := serverutil.Scope{TenantID: "ws-hf"}
	const sid = "sess-hf"

	// Persist rows directly, as a previous process would have.
	h := mgr.makeHistory(sc, sid)
	h.Store(sid, &llmv1.Message{Role: "user", Id: "u1", Content: "hi"})
	h.Store(sid, &llmv1.Message{Role: "assistant", Id: "a1", Content: "hello"})

	got := mgr.HistoryFor(sc, sid)
	if _, ok := got.(*store.History); !ok {
		t.Fatalf("HistoryFor = %T; want *store.History", got)
	}
	if msgs := got.History(sid, 0); len(msgs) != 2 {
		t.Fatalf("history: want 2 messages, got %d", len(msgs))
	}
	if n := mgr.RuntimeCount(); n != 0 {
		t.Fatalf("reading history loaded %d runtime(s); want 0", n)
	}
	// Scoped: the same session id under another tenant reads as empty.
	if msgs := mgr.HistoryFor(serverutil.Scope{TenantID: "other"}, sid).History(sid, 0); len(msgs) != 0 {
		t.Fatalf("history leaked across tenants: %d messages", len(msgs))
	}
}
