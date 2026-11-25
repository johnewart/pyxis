// Package lexstore is a pure-Go lexical search index in pebble: BM25 over
// identifier-split terms plus a trigram index over the names each chunk
// defines or answers to, fused by reciprocal rank. No embeddings, no model,
// no cgo.
//
// Data is partitioned by SPACE (an opaque caller-chosen string: a tenant and
// scope, a workspace, ...) and, within a space, by SOURCE (a repository or
// collection) and PARTITION ("" = live; a derived partition holds only the
// files it re-indexed and reads every other file through its base).
//
// What a corpus is — chunking, naming, reranking — is pluggable (Options);
// see plugins.go.
package lexstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

// IndexID is the identity the store records per (space, source). Bump the
// version when tokenization or stored fields change incompatibly:
// IndexNeedsWipe then reports a rebuild.
const IndexID = "lexical:v1"

// Key prefixes. Components are joined by \x00, so spaces, sources,
// partitions and logical ids must not contain \x00.
const (
	pChunk   = "c/"  // c/<chunk id>                                 -> chunkRec
	pFile    = "f/"  // f/<space>\0<part>\0<src>\0<logical>\0<chunk> -> ""
	pTerm    = "t/"  // t/<space>\0<term>\0<chunk>                   -> posting
	pTri     = "g/"  // g/<space>\0<trigram>\0<chunk>                -> src\0part
	pStat    = "s/"  // s/<space>\0<src>\0<part>                     -> statRec
	pMeta    = "m/"  // m/<space>\0<src>                             -> identity
	pOverlay = "op/" // op/<space>\0<src>\0<part>                    -> overlayRec
	pOwned   = "oo/" // oo/<space>\0<src>\0<part>\0<path>            -> ""
)

func join(parts ...string) string { return strings.Join(parts, "\x00") }

// Store is a lexical search index.
type Store struct {
	kv   *pebble.DB
	path string
	opts Options
	mu   sync.Mutex // serialises writes: stats are read-modify-write
}

// Open opens (creating) the store in directory path.
func Open(path string, opts Options) (*Store, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("lexstore: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("lexstore: mkdir: %w", err)
	}
	opts = opts.withDefaults()
	cache := pebble.NewCache(opts.CacheBytes)
	defer cache.Unref()
	popts := &pebble.Options{Cache: cache, MemTableSize: 8 << 20, Logger: quietLogger{}}
	kv, err := pebble.Open(abs, popts)
	if err != nil {
		// The index is derived data: a store that cannot open is rebuilt.
		_ = os.RemoveAll(abs)
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
		if kv, err = pebble.Open(abs, popts); err != nil {
			return nil, fmt.Errorf("lexstore: open %s: %w", abs, err)
		}
	}
	return &Store{kv: kv, path: abs, opts: opts}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.kv.Close() }

// Path returns the store directory.
func (s *Store) Path() string { return s.path }

type quietLogger struct{}

func (quietLogger) Infof(string, ...interface{})  {}
func (quietLogger) Errorf(string, ...interface{}) {}
func (quietLogger) Fatalf(format string, args ...interface{}) {
	panic(fmt.Sprintf("lexstore: pebble fatal: "+format, args...))
}

// ---- records ----

// DocumentIn is a logical document to index.
type DocumentIn struct {
	LogicalDocumentID string
	SourceID          string
	Name              string
	Content           string
	Metadata          map[string]string
}

type chunkRec struct {
	ID, Space, Source, Partition string
	LogicalID, Name, Content     string
	Metadata                     map[string]string
	MetaJSON                     string
	Hash                         string
	Terms                        []string
	Length                       int
	Tris                         []string
}

type statRec struct{ Count, TotalLen int64 }

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}

func decode(b []byte, v any) error { return gob.NewDecoder(bytes.NewReader(b)).Decode(v) }

func (s *Store) get(key string) ([]byte, bool, error) {
	v, c, err := s.kv.Get([]byte(key))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := append([]byte(nil), v...)
	_ = c.Close()
	return out, true, nil
}

func upper(prefix string) []byte {
	u := []byte(prefix)
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] < 0xff {
			u[i]++
			return u[:i+1]
		}
	}
	return nil
}

func (s *Store) scan(prefix string, fn func(key, val []byte) error) error {
	it, err := s.kv.NewIter(&pebble.IterOptions{LowerBound: []byte(prefix), UpperBound: upper(prefix)})
	if err != nil {
		return err
	}
	defer it.Close()
	for it.First(); it.Valid(); it.Next() {
		if err := fn(it.Key()[len(prefix):], it.Value()); err != nil {
			return err
		}
	}
	return it.Error()
}

// ---- normalisation ----

// NormalizeSourceID is the canonical source id: "" for the primary source
// ("", ".", "./"), else a cleaned slash path.
func NormalizeSourceID(id string) string {
	v := strings.TrimSpace(strings.ReplaceAll(id, "\\", "/"))
	v = strings.TrimPrefix(v, "./")
	if v == "." || v == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(v))
}

func normalizePartitionID(id string) string { return strings.TrimSpace(id) }

func normalizeFilePath(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
}

func requireSpace(space string) error {
	if strings.TrimSpace(space) == "" {
		return fmt.Errorf("lexstore: space is required")
	}
	if strings.ContainsRune(space, 0) {
		return fmt.Errorf("lexstore: space must not contain NUL")
	}
	return nil
}

// chunkID is a chunk's id: space, source, partition, logical id, index.
func chunkID(space, partitionID, sourceID, logicalDocumentID string, index int) string {
	owner := space
	if sourceID = NormalizeSourceID(sourceID); sourceID != "" {
		owner += "#" + sourceID
	}
	if partitionID = normalizePartitionID(partitionID); partitionID != "" {
		return fmt.Sprintf("%s@%s:%s_%d", owner, partitionID, logicalDocumentID, index)
	}
	return fmt.Sprintf("%s:%s_%d", owner, logicalDocumentID, index)
}

func metaJSON(md map[string]string) (string, error) {
	if len(md) == 0 {
		return "", nil
	}
	b, err := json.Marshal(md)
	return string(b), err
}

func encodePosting(tf, length int, src, part string) []byte {
	return []byte(fmt.Sprintf("%d\x00%d\x00%s\x00%s", tf, length, src, part))
}

func decodePosting(v []byte) (tf, length int, src, part string) {
	f := strings.SplitN(string(v), "\x00", 4)
	if len(f) != 4 {
		return 0, 0, "", ""
	}
	fmt.Sscan(f[0], &tf)
	fmt.Sscan(f[1], &length)
	return tf, length, f[2], f[3]
}

// ---- write batches ----

type writeBatch struct {
	s       *Store
	b       *pebble.Batch
	stats   map[string]statRec
	staged  map[string]*chunkRec
	deleted map[string]bool // deleted this batch; stored copy visible until commit
	added   int
	removed int
}

func (s *Store) newBatch() *writeBatch {
	return &writeBatch{s: s, b: s.kv.NewBatch(), stats: map[string]statRec{},
		staged: map[string]*chunkRec{}, deleted: map[string]bool{}}
}

func (w *writeBatch) addChunk(rec *chunkRec) error {
	defines, aliases := w.s.opts.Namer.Names(ChunkInfo{LogicalID: rec.LogicalID, Name: rec.Name, Content: rec.Content, Metadata: rec.Metadata})
	terms, length := chunkTerms(rec.Content, defines)
	rec.Length = length
	rec.Terms = rec.Terms[:0]
	for t := range terms {
		rec.Terms = append(rec.Terms, t)
	}
	sort.Strings(rec.Terms)
	rec.Tris = nameTrigrams(defines, aliases)
	enc, err := encode(rec)
	if err != nil {
		return err
	}
	_ = w.b.Set([]byte(pChunk+rec.ID), enc, nil)
	_ = w.b.Set([]byte(pFile+join(rec.Space, rec.Partition, rec.Source, rec.LogicalID, rec.ID)), nil, nil)
	for _, t := range rec.Terms {
		_ = w.b.Set([]byte(pTerm+join(rec.Space, t, rec.ID)), encodePosting(terms[t], length, rec.Source, rec.Partition), nil)
	}
	for _, g := range rec.Tris {
		_ = w.b.Set([]byte(pTri+join(rec.Space, g, rec.ID)), []byte(rec.Source+"\x00"+rec.Partition), nil)
	}
	if length > 0 {
		sk := pStat + join(rec.Space, rec.Source, rec.Partition)
		d := w.stats[sk]
		d.Count++
		d.TotalLen += int64(length)
		w.stats[sk] = d
	}
	w.staged[rec.ID] = rec
	w.added++
	return nil
}

func (w *writeBatch) deleteChunk(id string) error {
	rec := w.staged[id]
	if rec == nil {
		if w.deleted[id] {
			return nil
		}
		raw, ok, err := w.s.get(pChunk + id)
		if err != nil || !ok {
			return err
		}
		rec = &chunkRec{}
		if err := decode(raw, rec); err != nil {
			return fmt.Errorf("lexstore: chunk %s: %w", id, err)
		}
	}
	delete(w.staged, id)
	w.deleted[id] = true
	w.removed++
	_ = w.b.Delete([]byte(pChunk+id), nil)
	_ = w.b.Delete([]byte(pFile+join(rec.Space, rec.Partition, rec.Source, rec.LogicalID, rec.ID)), nil)
	for _, t := range rec.Terms {
		_ = w.b.Delete([]byte(pTerm+join(rec.Space, t, rec.ID)), nil)
	}
	for _, g := range rec.Tris {
		_ = w.b.Delete([]byte(pTri+join(rec.Space, g, rec.ID)), nil)
	}
	if rec.Length > 0 {
		sk := pStat + join(rec.Space, rec.Source, rec.Partition)
		d := w.stats[sk]
		d.Count--
		d.TotalLen -= int64(rec.Length)
		w.stats[sk] = d
	}
	return nil
}

func (w *writeBatch) abort() { _ = w.b.Close() }

func (w *writeBatch) commit() error {
	defer w.b.Close()
	for sk, d := range w.stats {
		if d.Count == 0 && d.TotalLen == 0 {
			continue
		}
		var cur statRec
		if raw, ok, err := w.s.get(sk); err != nil {
			return err
		} else if ok {
			_ = decode(raw, &cur)
		}
		cur.Count += d.Count
		cur.TotalLen += d.TotalLen
		if cur.Count <= 0 {
			_ = w.b.Delete([]byte(sk), nil)
			continue
		}
		enc, _ := encode(cur)
		_ = w.b.Set([]byte(sk), enc, nil)
	}
	if err := w.b.Commit(pebble.NoSync); err != nil {
		return err
	}
	w.s.opts.Observer.Chunks(w.added, w.removed)
	return nil
}

func (s *Store) observeWrite(op string, start time.Time, err *error) {
	s.opts.Observer.Write(op, time.Since(start), *err)
}

// ---- file lookups ----

// fileChunks returns the chunk ids of exactly filePath (logical ids whose
// FileOf is filePath) in (space, partition, source).
func (s *Store) fileChunks(space, partitionID, sourceID, filePath string) ([]string, error) {
	prefix := pFile + join(space, normalizePartitionID(partitionID), NormalizeSourceID(sourceID)) + "\x00" + filePath
	var ids []string
	err := s.scan(prefix, func(k, _ []byte) error {
		rest := string(k)
		i := strings.LastIndex(rest, "\x00")
		if i < 0 {
			return nil
		}
		if s.opts.FileOf(filePath+rest[:i]) == filePath {
			ids = append(ids, rest[i+1:])
		}
		return nil
	})
	return ids, err
}

func (s *Store) logicalChunks(space, partitionID, sourceID, logicalID string) ([]string, error) {
	prefix := pFile + join(space, normalizePartitionID(partitionID), NormalizeSourceID(sourceID), logicalID) + "\x00"
	var ids []string
	err := s.scan(prefix, func(k, _ []byte) error {
		ids = append(ids, string(k))
		return nil
	})
	return ids, err
}

// ---- writes ----

// UpsertOptions carries per-write parameters.
type UpsertOptions struct {
	// DefaultSourceID applies to documents with no SourceID.
	DefaultSourceID string
	// PreChunked stores each document as one chunk (WholeDocument).
	PreChunked bool
	// Chunker overrides the store's Chunker for this write.
	Chunker Chunker
	// InsertOnly skips per-document deletes (the caller cleared them).
	InsertOnly bool
	// PartitionID writes into a derived partition ("" = live).
	PartitionID string
	// SkipUnchanged makes ReplaceFileDocuments a no-op when the file's
	// stored chunks already match exactly.
	SkipUnchanged bool
	// OnSkipped is called when SkipUnchanged skips a file.
	OnSkipped func()
}

func (s *Store) chunkerFor(opts UpsertOptions) Chunker {
	switch {
	case opts.PreChunked:
		return WholeDocument
	case opts.Chunker != nil:
		return opts.Chunker
	default:
		return s.opts.Chunker
	}
}

func (s *Store) ensureMeta(w *writeBatch, space, sourceID string) error {
	k := pMeta + join(space, NormalizeSourceID(sourceID))
	cur, ok, err := s.get(k)
	if err != nil {
		return err
	}
	if !ok || string(cur) != IndexID {
		_ = w.b.Set([]byte(k), []byte(IndexID), nil)
	}
	return nil
}

type chunkedDocument struct {
	doc   DocumentIn
	parts []string
}

func chunkDocuments(prepared []DocumentIn, c Chunker) ([]chunkedDocument, int) {
	var docs []chunkedDocument
	n := 0
	for _, d := range prepared {
		parts := c.Chunk(d)
		if len(parts) == 0 {
			continue
		}
		docs = append(docs, chunkedDocument{doc: d, parts: parts})
		n += len(parts)
	}
	return docs, n
}

func prepareDocs(docs []DocumentIn, opts UpsertOptions) ([]DocumentIn, error) {
	defaultSource := NormalizeSourceID(opts.DefaultSourceID)
	prepared := make([]DocumentIn, 0, len(docs))
	for _, d := range docs {
		doc := d
		if NormalizeSourceID(doc.SourceID) == "" {
			doc.SourceID = defaultSource
		}
		doc.SourceID = NormalizeSourceID(doc.SourceID)
		if strings.TrimSpace(doc.LogicalDocumentID) == "" {
			return nil, fmt.Errorf("logical_document_id is required")
		}
		prepared = append(prepared, doc)
	}
	return dedupeDocumentsByLogicalID(prepared), nil
}

func (w *writeBatch) insertDocument(space, partitionID string, item chunkedDocument) error {
	mj, err := metaJSON(item.doc.Metadata)
	if err != nil {
		return err
	}
	for i, text := range item.parts {
		sum := sha256.Sum256([]byte(text))
		rec := &chunkRec{
			ID:    chunkID(space, partitionID, item.doc.SourceID, item.doc.LogicalDocumentID, i),
			Space: space, Source: item.doc.SourceID, Partition: normalizePartitionID(partitionID),
			LogicalID: item.doc.LogicalDocumentID, Name: strings.TrimSpace(item.doc.Name), Content: text,
			Metadata: item.doc.Metadata, MetaJSON: mj, Hash: hex.EncodeToString(sum[:]),
		}
		if err := w.deleteChunk(rec.ID); err != nil {
			return err
		}
		if err := w.addChunk(rec); err != nil {
			return err
		}
	}
	return nil
}

// UpsertDocuments replaces each logical document's chunks with a fresh
// chunking of its content. It returns the number of chunks written.
func (s *Store) UpsertDocuments(ctx context.Context, space string, docs []DocumentIn, opts UpsertOptions) (_ int, err error) {
	defer s.observeWrite("upsert", time.Now(), &err)
	if err := requireSpace(space); err != nil {
		return 0, err
	}
	prepared, err := prepareDocs(docs, opts)
	if err != nil {
		return 0, err
	}
	chunked, total := chunkDocuments(prepared, s.chunkerFor(opts))
	if len(chunked) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.newBatch()
	for _, sid := range uniqueSourceIDs(prepared) {
		if err := s.ensureMeta(w, space, sid); err != nil {
			w.abort()
			return 0, err
		}
	}
	for _, item := range chunked {
		if !opts.InsertOnly {
			ids, err := s.logicalChunks(space, opts.PartitionID, item.doc.SourceID, item.doc.LogicalDocumentID)
			if err != nil {
				w.abort()
				return 0, err
			}
			for _, id := range ids {
				if err := w.deleteChunk(id); err != nil {
					w.abort()
					return 0, err
				}
			}
		}
		if err := w.insertDocument(space, opts.PartitionID, item); err != nil {
			w.abort()
			return 0, err
		}
	}
	return total, w.commit()
}

// ReplaceFileDocuments re-indexes one file atomically: its old chunks go and
// the new ones arrive in one batch. The file's source is
// opts.DefaultSourceID.
//
// A file replaced with nothing (no documents, or documents that chunk to
// nothing) loses its old chunks: it no longer answers searches. Returning
// early instead left such a file answering with its last content forever,
// e.g. a file that a caller stopped indexing (generated code) kept its
// chunks through every incremental reindex. InsertOnly never removes
// anything, so it stays a no-op there.
func (s *Store) ReplaceFileDocuments(ctx context.Context, space, filePath string, docs []DocumentIn, opts UpsertOptions) (_ int, err error) {
	defer s.observeWrite("replace_file", time.Now(), &err)
	if err := requireSpace(space); err != nil {
		return 0, err
	}
	filePath = normalizeFilePath(filePath)
	if filePath == "" {
		return 0, fmt.Errorf("file path is required")
	}
	if len(docs) == 0 {
		return 0, s.clearReplacedFile(space, filePath, opts)
	}
	prepared, err := prepareDocs(docs, opts)
	if err != nil {
		return 0, err
	}
	chunked, total := chunkDocuments(prepared, s.chunkerFor(opts))
	if len(chunked) == 0 {
		return 0, s.clearReplacedFile(space, filePath, opts)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.fileChunks(space, opts.PartitionID, opts.DefaultSourceID, filePath)
	if err != nil {
		return 0, err
	}
	if opts.SkipUnchanged && !opts.InsertOnly {
		same, err := s.chunksUnchanged(existing, chunked)
		if err != nil {
			return 0, err
		}
		if same {
			s.opts.Observer.FileSkipped()
			if opts.OnSkipped != nil {
				opts.OnSkipped()
			}
			return total, nil
		}
	}
	w := s.newBatch()
	for _, sid := range uniqueSourceIDs(prepared) {
		if err := s.ensureMeta(w, space, sid); err != nil {
			w.abort()
			return 0, err
		}
	}
	for _, id := range existing {
		if err := w.deleteChunk(id); err != nil {
			w.abort()
			return 0, err
		}
	}
	for _, item := range chunked {
		if err := w.insertDocument(space, opts.PartitionID, item); err != nil {
			w.abort()
			return 0, err
		}
	}
	return total, w.commit()
}

// clearReplacedFile removes a file's chunks from opts' partition and source
// when ReplaceFileDocuments was given nothing to put in their place.
func (s *Store) clearReplacedFile(space, filePath string, opts UpsertOptions) error {
	if opts.InsertOnly {
		return nil
	}
	_, err := s.deleteByFilePath(space, opts.PartitionID, opts.DefaultSourceID, filePath)
	return err
}

func chunkKey(logicalID, hash, meta, name string) string {
	return logicalID + "\x00" + hash + "\x00" + meta + "\x00" + strings.TrimSpace(name)
}

func (s *Store) chunksUnchanged(existing []string, chunked []chunkedDocument) (bool, error) {
	var want []string
	for _, item := range chunked {
		mj, err := metaJSON(item.doc.Metadata)
		if err != nil {
			return false, err
		}
		for _, part := range item.parts {
			sum := sha256.Sum256([]byte(part))
			want = append(want, chunkKey(item.doc.LogicalDocumentID, hex.EncodeToString(sum[:]), mj, item.doc.Name))
		}
	}
	if len(want) != len(existing) {
		return false, nil
	}
	have := make([]string, 0, len(existing))
	for _, id := range existing {
		raw, ok, err := s.get(pChunk + id)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		var r chunkRec
		if err := decode(raw, &r); err != nil {
			return false, err
		}
		have = append(have, chunkKey(r.LogicalID, r.Hash, r.MetaJSON, r.Name))
	}
	sort.Strings(want)
	sort.Strings(have)
	for i := range want {
		if want[i] != have[i] {
			return false, nil
		}
	}
	return true, nil
}

// DocumentRef names one logical document for deletion.
type DocumentRef struct{ SourceID, LogicalID string }

// DeleteDocuments removes chunks from the live partition by file path (with
// its source), by whole source (bulkSource non-nil; "" is the primary
// source, and its overlays and identity go too), or by document refs.
func (s *Store) DeleteDocuments(ctx context.Context, space string, bulkSource *string, filePath, fileSource string, refs []DocumentRef) (_ int, err error) {
	defer s.observeWrite("delete", time.Now(), &err)
	if err := requireSpace(space); err != nil {
		return 0, err
	}
	if strings.TrimSpace(filePath) != "" {
		return s.deleteByFilePath(space, "", fileSource, filePath)
	}
	if bulkSource != nil {
		return s.deleteBySource(space, NormalizeSourceID(*bulkSource))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.newBatch()
	deleted := 0
	for _, r := range refs {
		if strings.TrimSpace(r.LogicalID) == "" {
			w.abort()
			return 0, fmt.Errorf("logical_document_id is required for each ref")
		}
		ids, err := s.logicalChunks(space, "", r.SourceID, r.LogicalID)
		if err != nil {
			w.abort()
			return 0, err
		}
		for _, id := range ids {
			if err := w.deleteChunk(id); err != nil {
				w.abort()
				return 0, err
			}
		}
		deleted += len(ids)
	}
	return deleted, w.commit()
}

// DeleteFileInPartition removes one file's chunks from one partition.
func (s *Store) DeleteFileInPartition(ctx context.Context, space, partitionID, sourceID, filePath string) (_ int, err error) {
	defer s.observeWrite("delete", time.Now(), &err)
	return s.deleteByFilePath(space, partitionID, sourceID, filePath)
}

func (s *Store) deleteByFilePath(space, partitionID, sourceID, filePath string) (int, error) {
	filePath = normalizeFilePath(filePath)
	if filePath == "" {
		return 0, fmt.Errorf("file path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.fileChunks(space, partitionID, sourceID, filePath)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	w := s.newBatch()
	for _, id := range ids {
		if err := w.deleteChunk(id); err != nil {
			w.abort()
			return 0, err
		}
	}
	return len(ids), w.commit()
}

// PruneFiles deletes the given files' chunks from one source of the live
// partition.
func (s *Store) PruneFiles(ctx context.Context, space, sourceID string, filePaths []string) (_ int, err error) {
	defer s.observeWrite("prune", time.Now(), &err)
	removed := 0
	for _, p := range filePaths {
		n, err := s.deleteByFilePath(space, "", sourceID, p)
		if err != nil {
			return removed, fmt.Errorf("prune %s: %w", p, err)
		}
		removed += n
	}
	return removed, nil
}

func (s *Store) deleteBySource(space, sourceID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	if err := s.scan(pFile+space+"\x00", func(k, _ []byte) error {
		f := strings.SplitN(string(k), "\x00", 4)
		if len(f) == 4 && f[1] == sourceID {
			ids = append(ids, f[3][strings.LastIndex(f[3], "\x00")+1:])
		}
		return nil
	}); err != nil {
		return 0, err
	}
	w := s.newBatch()
	for _, id := range ids {
		if err := w.deleteChunk(id); err != nil {
			w.abort()
			return 0, err
		}
	}
	for _, p := range []string{pOwned, pOverlay} {
		pre := p + join(space, sourceID) + "\x00"
		_ = w.b.DeleteRange([]byte(pre), upper(pre), nil)
	}
	_ = w.b.Delete([]byte(pMeta+join(space, sourceID)), nil)
	return len(ids), w.commit()
}

// ---- status reads ----

type fileRef struct{ src, file string }

func (s *Store) liveFiles(space string, sourceFilter *string) (map[fileRef]int, error) {
	out := map[fileRef]int{}
	prefix := pFile + join(space, "") + "\x00"
	if sourceFilter != nil {
		prefix += *sourceFilter + "\x00"
	}
	err := s.scan(prefix, func(k, _ []byte) error {
		rest := string(k)
		src := ""
		if sourceFilter != nil {
			src = *sourceFilter
		} else {
			i := strings.Index(rest, "\x00")
			if i < 0 {
				return nil
			}
			src, rest = rest[:i], rest[i+1:]
		}
		j := strings.LastIndex(rest, "\x00")
		if j < 0 {
			return nil
		}
		out[fileRef{src, s.opts.FileOf(rest[:j])}]++
		return nil
	})
	return out, err
}

// CountDocuments reports what the live partition holds: distinct files (by
// source and FileOf) and chunks. sourceID "" counts every source.
func (s *Store) CountDocuments(ctx context.Context, space, sourceID string) (fileCount, chunkCount int64, err error) {
	var filter *string
	if sid := NormalizeSourceID(sourceID); sid != "" {
		filter = &sid
	}
	files, err := s.liveFiles(space, filter)
	if err != nil {
		return 0, 0, err
	}
	for _, n := range files {
		chunkCount += int64(n)
	}
	return int64(len(files)), chunkCount, nil
}

// IndexedFilePaths lists the files indexed for exactly one source of the
// live partition ("" = the primary source), sorted.
func (s *Store) IndexedFilePaths(ctx context.Context, space, sourceID string) ([]string, error) {
	sid := NormalizeSourceID(sourceID)
	files, err := s.liveFiles(space, &sid)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f.file)
	}
	sort.Strings(out)
	return out, nil
}

// IndexIdentity reports the identity (space, source) was indexed under, or
// "" when nothing is indexed.
func (s *Store) IndexIdentity(ctx context.Context, space, sourceID string) (string, error) {
	v, ok, err := s.get(pMeta + join(space, NormalizeSourceID(sourceID)))
	if err != nil || !ok {
		return "", err
	}
	return string(v), nil
}

// IndexNeedsWipe reports whether (space, source) was indexed under another
// identity (an older format) that a re-index must start over from.
func (s *Store) IndexNeedsWipe(ctx context.Context, space, sourceID string) (bool, string, error) {
	stored, err := s.IndexIdentity(ctx, space, sourceID)
	if err != nil {
		return false, "", err
	}
	if stored == "" {
		return false, "no stored index identity (nothing indexed yet, or already cleared)", nil
	}
	if stored != IndexID {
		return true, fmt.Sprintf("index format changed: stored %q, current %q", stored, IndexID), nil
	}
	return false, "index identity unchanged (" + IndexID + ")", nil
}

func uniqueSourceIDs(docs []DocumentIn) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range docs {
		sid := NormalizeSourceID(d.SourceID)
		if !seen[sid] {
			seen[sid] = true
			out = append(out, sid)
		}
	}
	return out
}

func dedupeDocumentsByLogicalID(docs []DocumentIn) []DocumentIn {
	if len(docs) <= 1 {
		return docs
	}
	byID := make(map[string]DocumentIn, len(docs))
	order := make([]string, 0, len(docs))
	for _, d := range docs {
		key := d.SourceID + "\x00" + d.LogicalDocumentID
		if _, seen := byID[key]; !seen {
			order = append(order, key)
		}
		byID[key] = d
	}
	out := make([]DocumentIn, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}
