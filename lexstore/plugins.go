package lexstore

import (
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// The pluggable parts of the store. The engine (storage, postings, scoring,
// partitions) is fixed; what a corpus IS — how documents split into chunks,
// which names a chunk defines or answers to, how hits are reordered — is
// supplied by the caller. Every plug-in has a default, so a zero Options
// gives a working general-purpose text index.

// Chunker splits one document into the chunks that are indexed and returned.
type Chunker interface {
	Chunk(doc DocumentIn) []string
}

// ChunkerFunc adapts a function to Chunker.
type ChunkerFunc func(doc DocumentIn) []string

// Chunk implements Chunker.
func (f ChunkerFunc) Chunk(doc DocumentIn) []string { return f(doc) }

// WholeDocument stores each document as exactly one chunk (the caller has
// already chunked it, e.g. one AST symbol per document).
var WholeDocument Chunker = ChunkerFunc(func(doc DocumentIn) []string {
	t := strings.TrimSpace(doc.Content)
	if t == "" {
		return nil
	}
	return []string{t}
})

// CharWindows splits content into windows of size code points overlapping by
// overlap. size <= 0 means 1024.
func CharWindows(size, overlap int) Chunker {
	if size <= 0 {
		size = 1024
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	return ChunkerFunc(func(doc DocumentIn) []string {
		t := strings.TrimSpace(doc.Content)
		if t == "" {
			return nil
		}
		if utf8.RuneCountInString(t) <= size {
			return []string{t}
		}
		runes := []rune(t)
		step := size - overlap
		var out []string
		for start := 0; start < len(runes); start += step {
			end := min(start+size, len(runes))
			out = append(out, string(runes[start:end]))
			if end == len(runes) {
				break
			}
		}
		return out
	})
}

// ChunkInfo is what a Namer sees of one chunk.
type ChunkInfo struct {
	LogicalID string
	Name      string
	Content   string
	Metadata  map[string]string
}

// Namer says which names a chunk DEFINES (their terms are boosted in BM25
// and indexed as trigrams, so a partial or misspelled name still finds the
// definition) and which it merely ANSWERS TO (trigrams only).
type Namer interface {
	Names(c ChunkInfo) (defines, aliases []string)
}

// NamerFunc adapts a function to Namer.
type NamerFunc func(c ChunkInfo) (defines, aliases []string)

// Names implements Namer.
func (f NamerFunc) Names(c ChunkInfo) ([]string, []string) { return f(c) }

// DefaultNamer suits code and documents alike: a chunk defines the
// "symbolName" in its metadata, and answers to the part of its logical id
// after "::", its file's base name (without extension) and its Name's base.
var DefaultNamer Namer = NamerFunc(func(c ChunkInfo) ([]string, []string) {
	var defines, aliases []string
	if sym := strings.TrimSpace(c.Metadata["symbolName"]); sym != "" {
		defines = append(defines, sym)
	}
	if i := strings.Index(c.LogicalID, "::"); i > 0 {
		aliases = append(aliases, c.LogicalID[i+2:])
	}
	base := filepath.Base(FileOfLogicalID(c.LogicalID))
	aliases = append(aliases, strings.TrimSuffix(base, filepath.Ext(base)))
	if n := strings.TrimSpace(c.Name); n != "" && n != c.LogicalID {
		aliases = append(aliases, filepath.Base(n))
	}
	return defines, aliases
})

// Reranker reorders the fused hits of a query and cuts them to limit. It is
// applied only when a query asks for it (QueryOptions.Rerank).
type Reranker interface {
	Rerank(query string, hits []SearchHit, limit int) []SearchHit
}

// RerankerFunc adapts a function to Reranker.
type RerankerFunc func(query string, hits []SearchHit, limit int) []SearchHit

// Rerank implements Reranker.
func (f RerankerFunc) Rerank(q string, hits []SearchHit, limit int) []SearchHit { return f(q, hits, limit) }

// TermOverlapReranker is a general-purpose reranker: it re-sorts hits by
// the share of the query's terms each hit's content contains, keeping the
// retrieval order among equals. Suitable for prose; code corpora usually
// want a symbol-aware reranker instead.
var TermOverlapReranker Reranker = RerankerFunc(func(q string, hits []SearchHit, limit int) []SearchHit {
	qt := Terms(q)
	if len(qt) == 0 {
		if len(hits) > limit {
			hits = hits[:limit]
		}
		return hits
	}
	type scored struct {
		h       SearchHit
		overlap float64
		order   int
	}
	out := make([]scored, len(hits))
	for i, h := range hits {
		ct := Terms(h.Content)
		n := 0
		for t := range qt {
			if ct[t] > 0 {
				n++
			}
		}
		out[i] = scored{h, float64(n) / float64(len(qt)), i}
	}
	sortStable(out, func(a, b scored) bool {
		if a.overlap != b.overlap {
			return a.overlap > b.overlap
		}
		return a.order < b.order
	})
	res := make([]SearchHit, 0, min(limit, len(out)))
	for _, s := range out {
		if len(res) == limit {
			break
		}
		res = append(res, s.h)
	}
	return res
})

func sortStable[T any](xs []T, less func(a, b T) bool) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && less(xs[j], xs[j-1]); j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// FileOfLogicalID is the default Options.FileOf: "path::symbol" chunks
// belong to "path".
func FileOfLogicalID(logicalID string) string {
	if i := strings.Index(logicalID, "::"); i > 0 {
		return logicalID[:i]
	}
	return logicalID
}

// Observer receives timing and volume for every query and write, so a host
// can export metrics without this package choosing a metrics library.
type Observer interface {
	// Query is one query: its mode ("keyword", "name", "hybrid", with
	// "+rerank" when reranked), duration, hit count and error.
	Query(mode string, d time.Duration, hits int, err error)
	// Write is one write operation ("upsert", "replace_file", "delete",
	// "prune", "drop_partition").
	Write(op string, d time.Duration, err error)
	// Chunks reports chunks written and deleted by one committed write.
	Chunks(written, deleted int)
	// FileSkipped reports a file left alone because it was unchanged.
	FileSkipped()
}

type nopObserver struct{}

func (nopObserver) Query(string, time.Duration, int, error) {}
func (nopObserver) Write(string, time.Duration, error)      {}
func (nopObserver) Chunks(int, int)                          {}
func (nopObserver) FileSkipped()                             {}

// Options configures a Store. Zero values select the defaults.
type Options struct {
	// Chunker splits documents (default CharWindows(1024, 0)). A write may
	// override it with UpsertOptions.Chunker or PreChunked.
	Chunker Chunker
	// Namer names chunks (default DefaultNamer).
	Namer Namer
	// Reranker reorders hits when a query asks (default: none; Rerank is a
	// no-op).
	Reranker Reranker
	// FileOf maps a logical id to the file it belongs to, for file-level
	// deletes, counts and partition ownership (default FileOfLogicalID).
	FileOf func(logicalID string) string
	// Observer receives timings (default: none).
	Observer Observer
	// CacheBytes sizes pebble's block cache (default 16 MiB).
	CacheBytes int64
}

func (o Options) withDefaults() Options {
	if o.Chunker == nil {
		o.Chunker = CharWindows(1024, 0)
	}
	if o.Namer == nil {
		o.Namer = DefaultNamer
	}
	if o.FileOf == nil {
		o.FileOf = FileOfLogicalID
	}
	if o.Observer == nil {
		o.Observer = nopObserver{}
	}
	if o.CacheBytes <= 0 {
		o.CacheBytes = 16 << 20
	}
	return o
}
