// Package knowledgestore is llmd's knowledge index: the lexical store
// (lexstore: BM25 + name trigrams in pebble) keyed by (tenant_id, scope_id).
// There are no embeddings; the embedding-config parameters the API still
// carries are accepted and ignored, so the wire protocol is unchanged.
//
// Every read and write is keyed by the exact scope: scope_id "" is
// tenant-wide and does not see scoped documents.
package knowledgestore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/johnewart/pyxis/internal/knowledge/chunk"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/lexstore"
)

// Store is llmd's knowledge index.
type Store struct {
	lex *lexstore.Store
}

// Open opens (creating) the index in directory dir. A regular file at
// legacyPath — the pre-lexical SQLite knowledge database — is removed with
// its WAL sidecars: the index is derived data that clients re-upsert.
func Open(dir, legacyPath string, opts lexstore.Options) (*Store, error) {
	if lp := strings.TrimSpace(legacyPath); lp != "" {
		if fi, err := os.Stat(lp); err == nil && !fi.IsDir() {
			for _, sfx := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(lp + sfx)
			}
		}
	}
	if opts.Reranker == nil {
		opts.Reranker = lexstore.TermOverlapReranker
	}
	lex, err := lexstore.Open(dir, opts)
	if err != nil {
		return nil, err
	}
	return &Store{lex: lex}, nil
}

// Close closes the index.
func (s *Store) Close() error { return s.lex.Close() }

// Path returns the index directory.
func (s *Store) Path() string { return s.lex.Path() }

// Lex exposes the underlying lexical store.
func (s *Store) Lex() *lexstore.Store { return s.lex }

// spaceSep cannot appear in a tenant or scope id that came through the API
// (it is a control character), so (tenant, scope) maps to a space 1:1.
const spaceSep = "\x1f"

func space(sc serverutil.Scope) (string, error) {
	t := strings.TrimSpace(sc.TenantID)
	if t == "" {
		return "", fmt.Errorf("tenant_id is required")
	}
	return t + spaceSep + strings.TrimSpace(sc.ScopeID), nil
}

func scopeOf(sp string) serverutil.Scope {
	t, s, _ := strings.Cut(sp, spaceSep)
	return serverutil.Scope{TenantID: t, ScopeID: s}
}

// EmbedConfig is the retired embedding identity. Accepted for wire
// compatibility; it has no effect.
type EmbedConfig struct {
	IndexModelID string
	ProviderID   string
	Model        string
	Dimensions   int32
}

// EmbedConfigFromLegacyModel builds an EmbedConfig from a legacy model id.
func EmbedConfigFromLegacyModel(modelID string) EmbedConfig { return EmbedConfig{Model: modelID} }

// DocumentIn is a logical document to index.
type DocumentIn = lexstore.DocumentIn

// SearchHit is one ranked result.
type SearchHit = lexstore.SearchHit

// OverlayRecord describes a derived partition.
type OverlayRecord = lexstore.OverlayRecord

// chunkerFor maps the request's chunking to a lexstore chunker: the
// "files" and "ast" strategies mean the caller already chunked (one chunk per
// document); otherwise documents split into cp-sized windows.
func chunkerFor(strategy string, cp chunk.Params) (lexstore.Chunker, bool) {
	switch strings.TrimSpace(strategy) {
	case "files", "ast":
		return nil, true
	}
	return lexstore.ChunkerFunc(func(d lexstore.DocumentIn) []string { return chunk.SplitUTF8(d.Content, cp) }), false
}

func (o UpsertOptions) lex(cp chunk.Params) lexstore.UpsertOptions {
	c, pre := chunkerFor(o.ChunkStrategy, cp)
	return lexstore.UpsertOptions{
		DefaultSourceID: o.DefaultSourceID, PreChunked: pre, Chunker: c,
		InsertOnly: o.InsertOnly, PartitionID: o.PartitionID,
	}
}

// UpsertDocuments replaces each logical document's chunks.
func (s *Store) UpsertDocuments(ctx context.Context, sc serverutil.Scope, _ EmbedConfig, cp chunk.Params, docs []DocumentIn, opts UpsertOptions) (int, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, err
	}
	return s.lex.UpsertDocuments(ctx, sp, docs, opts.lex(cp))
}

// ReplaceFileDocuments re-indexes one file atomically.
func (s *Store) ReplaceFileDocuments(ctx context.Context, sc serverutil.Scope, _ EmbedConfig, cp chunk.Params, filePath string, docs []DocumentIn, opts UpsertOptions) (int, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, err
	}
	return s.lex.ReplaceFileDocuments(ctx, sp, filePath, docs, opts.lex(cp))
}

// DeleteDocuments removes chunks by file path, by whole source, or by refs.
func (s *Store) DeleteDocuments(ctx context.Context, sc serverutil.Scope, bulkSource *string, deleteFilePath, deleteFileSource string, refs []struct{ SourceID, LogicalID string }) (int, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, err
	}
	lr := make([]lexstore.DocumentRef, len(refs))
	for i, r := range refs {
		lr[i] = lexstore.DocumentRef{SourceID: r.SourceID, LogicalID: r.LogicalID}
	}
	return s.lex.DeleteDocuments(ctx, sp, bulkSource, deleteFilePath, deleteFileSource, lr)
}

// Query retrieves hits (hybrid BM25 + name trigrams), optionally reranked by
// query-term overlap.
func (s *Store) Query(ctx context.Context, sc serverutil.Scope, q string, _ EmbedConfig, limit int, rerank bool, filter QueryFilter) ([]SearchHit, error) {
	sp, err := space(sc)
	if err != nil {
		return nil, err
	}
	return s.lex.Query(ctx, sp, q, lexstore.QueryFilter(filter), lexstore.QueryOptions{Limit: limit, Rerank: rerank})
}

// DeleteFileInPartition removes one file's chunks from one partition.
func (s *Store) DeleteFileInPartition(ctx context.Context, sc serverutil.Scope, partitionID, sourceID, filePath string) (int, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, err
	}
	return s.lex.DeleteFileInPartition(ctx, sp, partitionID, sourceID, filePath)
}

// CountDocuments reports the live partition's files and chunks.
func (s *Store) CountDocuments(ctx context.Context, sc serverutil.Scope, sourceID string) (int64, int64, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, 0, err
	}
	return s.lex.CountDocuments(ctx, sp, sourceID)
}

// DeclareOverlay registers a derived partition.
func (s *Store) DeclareOverlay(ctx context.Context, sc serverutil.Scope, sourceID string, rec OverlayRecord) error {
	sp, err := space(sc)
	if err != nil {
		return err
	}
	return s.lex.DeclareOverlay(ctx, sp, sourceID, rec)
}

// ReadOverlay returns a derived partition's record (nil if live/unknown).
func (s *Store) ReadOverlay(ctx context.Context, sc serverutil.Scope, sourceID, partitionID string) (*OverlayRecord, error) {
	sp, err := space(sc)
	if err != nil {
		return nil, err
	}
	return s.lex.ReadOverlay(ctx, sp, sourceID, partitionID)
}

// RecordOwnedPaths records a derived partition's analysed files.
func (s *Store) RecordOwnedPaths(ctx context.Context, sc serverutil.Scope, sourceID, partitionID string, paths []string) error {
	sp, err := space(sc)
	if err != nil {
		return err
	}
	return s.lex.RecordOwnedPaths(ctx, sp, sourceID, partitionID, paths)
}

// OwnedPaths returns a derived partition's analysed files.
func (s *Store) OwnedPaths(ctx context.Context, sc serverutil.Scope, sourceID, partitionID string) (map[string]struct{}, error) {
	sp, err := space(sc)
	if err != nil {
		return nil, err
	}
	return s.lex.OwnedPaths(ctx, sp, sourceID, partitionID)
}

// TouchPartition stamps a derived partition's last use.
func (s *Store) TouchPartition(ctx context.Context, sc serverutil.Scope, sourceID, partitionID string) error {
	sp, err := space(sc)
	if err != nil {
		return err
	}
	return s.lex.TouchPartition(ctx, sp, sourceID, partitionID)
}

// DropPartition deletes a derived partition.
func (s *Store) DropPartition(ctx context.Context, sc serverutil.Scope, sourceID, partitionID string) (int, error) {
	sp, err := space(sc)
	if err != nil {
		return 0, err
	}
	return s.lex.DropPartition(ctx, sp, sourceID, partitionID)
}

// IdlePartition is one reap candidate.
type IdlePartition struct {
	Scope       serverutil.Scope
	SourceID    string
	PartitionID string
}

// ListIdlePartitions returns derived partitions unused for idleHours.
func (s *Store) ListIdlePartitions(ctx context.Context, idleHours int) ([]IdlePartition, error) {
	idle, err := s.lex.ListIdlePartitions(ctx, idleHours)
	if err != nil {
		return nil, err
	}
	out := make([]IdlePartition, len(idle))
	for i, p := range idle {
		out[i] = IdlePartition{Scope: scopeOf(p.Space), SourceID: p.SourceID, PartitionID: p.PartitionID}
	}
	return out, nil
}
