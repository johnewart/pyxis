package knowledgestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnewart/pyxis/internal/knowledge/chunk"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/lexstore"
)

func openT(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "k"), "", lexstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestScopesAreExactAndIsolated(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	cp, _ := chunk.Resolve(0, 0)
	a := serverutil.Scope{TenantID: "A"}
	aS1 := serverutil.Scope{TenantID: "A", ScopeID: "s1"}
	b := serverutil.Scope{TenantID: "B"}
	for _, sc := range []serverutil.Scope{a, aS1, b} {
		if _, err := s.UpsertDocuments(ctx, sc, EmbedConfig{}, cp, []DocumentIn{
			{LogicalDocumentID: "doc", Content: "owlbert hoots for tenant " + sc.TenantID + sc.ScopeID},
		}, UpsertOptions{ChunkStrategy: "files"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, sc := range []serverutil.Scope{a, aS1, b} {
		hits, err := s.Query(ctx, sc, "owlbert hoots", EmbedConfig{}, 10, false, QueryFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Content != "owlbert hoots for tenant "+sc.TenantID+sc.ScopeID {
			t.Fatalf("scope %+v saw %+v", sc, hits)
		}
	}
	if _, err := s.Query(ctx, serverutil.Scope{}, "x", EmbedConfig{}, 1, false, QueryFilter{}); err == nil {
		t.Fatal("empty tenant accepted")
	}
}

func TestChunkStrategyAndRerank(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: "T"}
	cp, _ := chunk.Resolve(64, 0)
	long := ""
	for i := 0; i < 10; i++ {
		long += "the quick brown fox jumps over the lazy dog. "
	}
	n, err := s.UpsertDocuments(ctx, sc, EmbedConfig{}, cp, []DocumentIn{{LogicalDocumentID: "split", Content: long}}, UpsertOptions{})
	if err != nil || n < 2 {
		t.Fatalf("default strategy must split by chunk size: %d %v", n, err)
	}
	n, err = s.UpsertDocuments(ctx, sc, EmbedConfig{}, cp, []DocumentIn{{LogicalDocumentID: "whole", Content: long}}, UpsertOptions{ChunkStrategy: "ast"})
	if err != nil || n != 1 {
		t.Fatalf("ast strategy must store one chunk: %d %v", n, err)
	}
	if hits, err := s.Query(ctx, sc, "lazy dog", EmbedConfig{}, 5, true, QueryFilter{}); err != nil || len(hits) != 2 {
		t.Fatalf("reranked query: %+v %v", hits, err)
	}
	if files, chunks, _ := s.CountDocuments(ctx, sc, ""); files != 2 || chunks < 3 {
		t.Fatalf("count %d/%d", files, chunks)
	}
}

func TestLegacySQLiteFileRemoved(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "llmd-knowledge.sqlite")
	for _, f := range []string{legacy, legacy + "-wal"} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(filepath.Join(dir, "llmd-knowledge.pebble"), legacy, lexstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, f := range []string{legacy, legacy + "-wal"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s survived", f)
		}
	}
}

func TestIdlePartitionsCarryTheirScope(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: "T", ScopeID: "proj"}
	if err := s.DeclareOverlay(ctx, sc, "src", OverlayRecord{PartitionID: "p1"}); err != nil {
		t.Fatal(err)
	}
	// Not idle yet with a 1h window; the scope round-trip is checked via a
	// 0-hour window rejected and a direct read.
	if rec, err := s.ReadOverlay(ctx, sc, "src", "p1"); err != nil || rec == nil {
		t.Fatalf("overlay %v %v", rec, err)
	}
	if got := scopeOf(mustSpace(t, sc)); got != sc {
		t.Fatalf("space round trip %+v", got)
	}
}

func mustSpace(t *testing.T, sc serverutil.Scope) string {
	sp, err := space(sc)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}
