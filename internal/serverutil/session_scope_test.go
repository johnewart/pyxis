package serverutil

import "testing"

func TestRequireSessionID(t *testing.T) {
	t.Parallel()
	sid, err := RequireSessionID("sess-1")
	if err != nil || sid != "sess-1" {
		t.Fatalf("valid: sid=%q err=%v", sid, err)
	}
	if _, err := RequireSessionID("  "); err == nil {
		t.Fatal("expected error for empty session_id")
	}
}
