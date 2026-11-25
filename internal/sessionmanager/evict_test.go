package sessionmanager

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func backdate(rt *SessionRuntime, age time.Duration) {
	old := time.Now().Add(-age).UnixMilli()
	rt.lastMS.Store(old)
	rt.loadedMS.Store(old)
}

func TestEvictIdle(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-evict"
	const sid = "sess-evict-1"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	// Fresh runtime: not evicted.
	if ids := mgr.EvictIdle(30 * time.Minute); len(ids) != 0 {
		t.Fatalf("evicted fresh runtime: %v", ids)
	}

	ma, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime in memory")
	}

	// Active stream blocks eviction even when idle-by-timestamp.
	ma.MarkChatStreamStart()
	backdate(ma, time.Hour)
	if ids := mgr.EvictIdle(30 * time.Minute); len(ids) != 0 {
		t.Fatalf("evicted runtime with active stream: %v", ids)
	}
	ma.MarkChatStreamEnd() // touches lastMS: fresh again
	if ids := mgr.EvictIdle(30 * time.Minute); len(ids) != 0 {
		t.Fatalf("evicted just-touched runtime: %v", ids)
	}

	// Idle past the threshold: evicted.
	backdate(ma, time.Hour)
	ids := mgr.EvictIdle(30 * time.Minute)
	if len(ids) != 1 || ids[0] != sid {
		t.Fatalf("evicted = %v, want [%s]", ids, sid)
	}
	if _, ok := mgr.Get(scopeFor(workspaceID), sid); ok {
		t.Fatal("runtime still in memory after eviction")
	}

	// Restore on demand from the persisted session row.
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, scopeFor(workspaceID)); err != nil {
		t.Fatalf("restore after eviction: %v", err)
	}
	restored, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected restored runtime")
	}

	// A restored runtime with an ancient lastMS (CreatedAt) must not be
	// instantly re-evicted: loadedMS anchors the idle reference.
	restored.lastMS.Store(time.Now().Add(-24 * time.Hour).UnixMilli())
	if ids := mgr.EvictIdle(30 * time.Minute); len(ids) != 0 {
		t.Fatalf("evicted freshly restored runtime: %v", ids)
	}
}

func TestEvictIdleDisabled(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "sess-keep"}, scopeFor("workspace-keep")); err != nil {
		t.Fatal(err)
	}
	ma, _ := mgr.Get(scopeFor("workspace-keep"), "sess-keep")
	backdate(ma, 48*time.Hour)
	if ids := mgr.EvictIdle(0); len(ids) != 0 {
		t.Fatalf("maxIdle=0 must disable eviction, evicted %v", ids)
	}
}
