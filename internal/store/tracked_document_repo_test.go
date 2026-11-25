package store

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestTrackedDocument_AppendAndDedup(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewTrackedDocumentRepo(db)
	ctx := context.Background()

	ws, sid, doc := serverutil.Scope{TenantID: "ws-1"}, "sess-1", "doc-uuid-1"

	// No versions yet.
	if _, found, err := repo.LatestVersion(ctx, ws, sid, doc); err != nil || found {
		t.Fatalf("LatestVersion empty: found=%v err=%v", found, err)
	}

	// First sighting -> version 0 created.
	v0, created, err := repo.AppendVersion(ctx, ws, sid, TrackedDocInput{
		DocumentID: doc, Name: "plan.md", Content: "line1\nline2\n",
		AnchorTurnID: "turn-1", AnchorMessageID: "user-1",
	})
	if err != nil || !created {
		t.Fatalf("first AppendVersion: created=%v err=%v", created, err)
	}
	if v0.Seq != 0 {
		t.Fatalf("first version seq = %d, want 0", v0.Seq)
	}
	if v0.ContentHash != TrackedDocContentHash("line1\nline2\n") {
		t.Fatalf("content hash mismatch")
	}

	// Same content again -> dedup, no new row, returns existing latest.
	dup, created, err := repo.AppendVersion(ctx, ws, sid, TrackedDocInput{
		DocumentID: doc, Name: "plan.md", Content: "line1\nline2\n",
		AnchorTurnID: "turn-2", AnchorMessageID: "user-2",
	})
	if err != nil {
		t.Fatalf("dup AppendVersion err: %v", err)
	}
	if created {
		t.Fatalf("identical content should not create a new version")
	}
	if dup.ID != v0.ID || dup.AnchorTurnID != "turn-1" {
		t.Fatalf("dedup should return original row with original anchor, got id=%d anchor=%s", dup.ID, dup.AnchorTurnID)
	}

	// Changed content -> version 1 created.
	v1, created, err := repo.AppendVersion(ctx, ws, sid, TrackedDocInput{
		DocumentID: doc, Name: "plan.md", Content: "line1\nline2\nline3\n",
		AnchorTurnID: "turn-3", AnchorMessageID: "user-3",
	})
	if err != nil || !created {
		t.Fatalf("changed AppendVersion: created=%v err=%v", created, err)
	}
	if v1.Seq != 1 {
		t.Fatalf("changed version seq = %d, want 1", v1.Seq)
	}

	latest, found, err := repo.LatestVersion(ctx, ws, sid, doc)
	if err != nil || !found {
		t.Fatalf("LatestVersion after change: found=%v err=%v", found, err)
	}
	if latest.Seq != 1 {
		t.Fatalf("latest seq = %d, want 1", latest.Seq)
	}

	versions, err := repo.ListVersions(ctx, ws, sid, doc)
	if err != nil {
		t.Fatalf("ListVersions err: %v", err)
	}
	if len(versions) != 2 || versions[0].Seq != 0 || versions[1].Seq != 1 {
		t.Fatalf("ListVersions = %+v, want 2 rows seq 0,1", versions)
	}
}

func TestTrackedDocument_IsolationAndDelete(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewTrackedDocumentRepo(db)
	ctx := context.Background()

	ws := serverutil.Scope{TenantID: "ws-1"}
	// Two documents in one session, plus a second session.
	if _, _, err := repo.AppendVersion(ctx, ws, "s1", TrackedDocInput{DocumentID: "a", Content: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AppendVersion(ctx, ws, "s1", TrackedDocInput{DocumentID: "b", Content: "B"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AppendVersion(ctx, ws, "s2", TrackedDocInput{DocumentID: "a", Content: "A2"}); err != nil {
		t.Fatal(err)
	}

	// Documents are isolated by id within a session.
	la, _, _ := repo.LatestVersion(ctx, ws, "s1", "a")
	if la.Content != "A" {
		t.Fatalf("doc a in s1 = %q, want A", la.Content)
	}

	// Same document id in another session is independent (own seq/content).
	sa, _, _ := repo.LatestVersion(ctx, ws, "s2", "a")
	if sa.Content != "A2" || sa.Seq != 0 {
		t.Fatalf("doc a in s2 = %q seq %d, want A2 seq 0", sa.Content, sa.Seq)
	}

	// DeleteBySession only removes the targeted session.
	if err := repo.DeleteBySession(ctx, ws, "s1"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if _, found, _ := repo.LatestVersion(ctx, ws, "s1", "a"); found {
		t.Fatalf("s1 doc a should be gone after delete")
	}
	if _, found, _ := repo.LatestVersion(ctx, ws, "s2", "a"); !found {
		t.Fatalf("s2 doc a should survive s1 delete")
	}
}
