package lexstore

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SearchHit is one ranked result.
type SearchHit struct {
	ChunkID           string
	LogicalDocumentID string
	SourceID          string
	// PartitionID is the partition that answered: the queried one, or its
	// base when the hit was served through it.
	PartitionID string
	Name        string
	Content     string
	Metadata    map[string]string
	Score       float64
}

// QueryMode selects the retrieval method.
type QueryMode string

const (
	// ModeKeyword is BM25 over identifier-split terms.
	ModeKeyword QueryMode = "keyword"
	// ModeName is name-trigram matching: partial and misspelled names.
	ModeName QueryMode = "name"
	// ModeHybrid fuses keyword and name results by reciprocal rank.
	ModeHybrid QueryMode = "hybrid"
)

// DefaultMode is used when QueryOptions.Mode is empty.
const DefaultMode = ModeHybrid

// QueryFilter scopes a query to sources and a partition.
type QueryFilter struct {
	SourceID  string
	SourceIDs []string
	// PartitionID queries a derived partition: its own chunks for the files
	// it analysed, its base's for everything else. "" queries the live
	// partition, which never sees derived chunks.
	PartitionID string
}

// ResolvedSourceIDs returns the effective source filter (nil: all sources).
func (f QueryFilter) ResolvedSourceIDs() []string {
	if sid := NormalizeSourceID(f.SourceID); sid != "" {
		return []string{sid}
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range f.SourceIDs {
		if sid := NormalizeSourceID(raw); sid != "" && !seen[sid] {
			seen[sid] = true
			out = append(out, sid)
		}
	}
	return out
}

// QueryOptions configures Query.
type QueryOptions struct {
	Limit  int       // default 10, max 100
	Mode   QueryMode // "" = DefaultMode
	Rerank bool      // apply Options.Reranker
}

// Query retrieves hits for q, one per logical document, best first.
func (s *Store) Query(ctx context.Context, space, q string, filter QueryFilter, opts QueryOptions) ([]SearchHit, error) {
	start := time.Now()
	mode := opts.Mode
	if mode == "" {
		mode = DefaultMode
	}
	label := string(mode)
	if opts.Rerank && s.opts.Reranker != nil {
		label += "+rerank"
	}
	hits, err := s.query(space, q, filter, mode, opts)
	s.opts.Observer.Query(label, time.Since(start), len(hits), err)
	return hits, err
}

func (s *Store) query(space, q string, filter QueryFilter, mode QueryMode, opts QueryOptions) ([]SearchHit, error) {
	if err := requireSpace(space); err != nil {
		return nil, err
	}
	if strings.TrimSpace(q) == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	k := min(limit*8, 500)
	partitionID := normalizePartitionID(filter.PartitionID)
	overlays, err := s.overlayContexts(space, partitionID)
	if err != nil {
		return nil, err
	}
	if len(overlays) > 0 {
		k = min(k*2, 1000)
	}
	sc := newScope(filter.ResolvedSourceIDs(), partitionID, overlays)

	var hits []SearchHit
	switch mode {
	case ModeKeyword:
		kw, err := s.bm25(space, q, k, sc)
		if err != nil {
			return nil, err
		}
		hits = dedupeByDocument(kw)
	case ModeName:
		nm, err := s.nameMatches(space, q, k, sc)
		if err != nil {
			return nil, err
		}
		hits = dedupeByDocument(nm)
	case ModeHybrid:
		kw, err := s.bm25(space, q, k, sc)
		if err != nil {
			return nil, err
		}
		nm, err := s.nameMatches(space, q, k, sc)
		if err != nil {
			return nil, err
		}
		hits = FuseRRF(dedupeByDocument(kw), dedupeByDocument(nm))
	default:
		return nil, fmt.Errorf("unknown query mode %q", mode)
	}
	if opts.Rerank && s.opts.Reranker != nil {
		return s.opts.Reranker.Rerank(q, hits, limit), nil
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func dedupeByDocument(cand []SearchHit) []SearchHit {
	sort.SliceStable(cand, func(i, j int) bool { return cand[i].Score > cand[j].Score })
	seen := map[string]bool{}
	out := make([]SearchHit, 0, len(cand))
	for _, h := range cand {
		key := h.LogicalDocumentID + "\x00" + h.SourceID
		if !seen[key] {
			seen[key] = true
			out = append(out, h)
		}
	}
	return out
}

// scope decides which postings a query may see from the posting's own
// source and partition, before any chunk is loaded.
type scope struct {
	sources   map[string]bool // nil: every source
	partition string
	overlays  map[string]*overlayCtx
}

func newScope(sources []string, partition string, overlays map[string]*overlayCtx) *scope {
	sc := &scope{partition: partition, overlays: overlays}
	if len(sources) > 0 {
		sc.sources = map[string]bool{}
		for _, s := range sources {
			sc.sources[s] = true
		}
	}
	return sc
}

func (sc *scope) sourceOK(src string) bool { return sc.sources == nil || sc.sources[src] }

func (sc *scope) partitionOK(src, part string) bool {
	if sc.partition == "" {
		return part == ""
	}
	if part == sc.partition {
		return true
	}
	oc := sc.overlays[src]
	return oc != nil && part == oc.baseID
}

func (s *Store) corpusStats(space string, sc *scope) (n int64, avgLen float64, err error) {
	var total int64
	err = s.scan(pStat+space+"\x00", func(k, v []byte) error {
		f := strings.SplitN(string(k), "\x00", 2)
		if len(f) != 2 || !sc.sourceOK(f[0]) || !sc.partitionOK(f[0], f[1]) {
			return nil
		}
		var st statRec
		if decode(v, &st) == nil {
			n += st.Count
			total += st.TotalLen
		}
		return nil
	})
	if n > 0 {
		avgLen = float64(total) / float64(n)
	}
	return n, avgLen, err
}

func (s *Store) bm25(space, q string, k int, sc *scope) ([]SearchHit, error) {
	qterms := Terms(q)
	if len(qterms) == 0 {
		return nil, nil
	}
	n, avgLen, err := s.corpusStats(space, sc)
	if err != nil || n == 0 || avgLen == 0 {
		return nil, err
	}
	scores := map[string]float64{}
	type posting struct {
		id      string
		tf, len float64
	}
	for term := range qterms {
		var ps []posting
		if err := s.scan(pTerm+join(space, term)+"\x00", func(kk, v []byte) error {
			tf, length, src, part := decodePosting(v)
			if sc.sourceOK(src) && sc.partitionOK(src, part) {
				ps = append(ps, posting{string(kk), float64(tf), float64(length)})
			}
			return nil
		}); err != nil {
			return nil, err
		}
		df := float64(len(ps))
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-df+0.5)/(df+0.5))
		for _, p := range ps {
			scores[p.id] += idf * (p.tf * (bm25K1 + 1)) / (p.tf + bm25K1*(1-bm25B+bm25B*p.len/avgLen))
		}
	}
	return s.topHits(scores, k, sc)
}

// nameMinOverlap is the share of a query unit's trigrams a chunk's names must
// contain for that unit to count as matched (see nameUnits).
const nameMinOverlap = 0.5

// nameMaxWords bounds how many words of a query name matching considers, so a
// pasted log or stack trace cannot turn one query into thousands of scans.
const nameMaxWords = 64

// nameMatches ranks chunks by how much of the query their names match.
//
// The query is split into units (nameUnits): each word, and each adjacent
// pair of words joined. A unit counts as matched by a chunk when the chunk's
// names hold at least nameMinOverlap of its trigrams. Each unit is weighted by
// how rare it is among names (log(1 + chunks / chunks matching it), as BM25
// weights a word), and a chunk's score is the weighted share of the query's
// unit trigrams its matched units cover. So "the notes page is hard to
// browse" scores NotesPage on "notes", "page" and "notespage", while a name
// that only shares a common word such as "data" scores far less.
//
// Matching the whole query at once instead (one trigram set, one threshold)
// left long queries with no name matches at all: no name holds half the
// trigrams of a sentence. A one-word query is one unit, so it scores exactly
// as the whole-query match did.
func (s *Store) nameMatches(space, q string, k int, sc *scope) ([]SearchHit, error) {
	units := nameUnits(q)
	if len(units) == 0 {
		return nil, nil
	}
	// unitsOf: the units each distinct trigram belongs to.
	unitsOf := map[string][]int{}
	for i, u := range units {
		for _, g := range u {
			unitsOf[g] = append(unitsOf[g], i)
		}
	}
	// counts: per chunk, how many of each unit's trigrams its names hold.
	counts := map[string][]int{}
	for g, us := range unitsOf {
		if err := s.scan(pTri+join(space, g)+"\x00", func(kk, v []byte) error {
			f := strings.SplitN(string(v), "\x00", 2)
			if len(f) != 2 || !sc.sourceOK(f[0]) || !sc.partitionOK(f[0], f[1]) {
				return nil
			}
			c := counts[string(kk)]
			if c == nil {
				c = make([]int, len(units))
				counts[string(kk)] = c
			}
			for _, u := range us {
				c[u]++
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	passes := func(unit, n int) bool { return float64(n) >= float64(len(units[unit]))*nameMinOverlap }
	// df: how many chunks match each unit.
	df := make([]int, len(units))
	for _, c := range counts {
		for i, n := range c {
			if passes(i, n) {
				df[i]++
			}
		}
	}
	n, _, err := s.corpusStats(space, sc)
	if err != nil {
		return nil, err
	}
	n = max(n, int64(len(counts)), 1)
	// weight: a unit few names match says more than one many names match.
	// total: the weighted size of the whole query, so a one-unit query scores
	// matched/len(unit) whatever its weight.
	weight := make([]float64, len(units))
	total := 0.0
	for i, u := range units {
		weight[i] = math.Log(1 + float64(n)/float64(max(df[i], 1)))
		total += float64(len(u)) * weight[i]
	}
	scores := map[string]float64{}
	for id, c := range counts {
		matched := 0.0
		for i, cnt := range c {
			if passes(i, cnt) {
				matched += float64(cnt) * weight[i]
			}
		}
		if matched > 0 {
			scores[id] = matched / total
		}
	}
	return s.topHits(scores, k, sc)
}

// nameUnits splits a query into the pieces name matching scores separately,
// each as its distinct trigrams: every word (as Terms reads a word, whole and
// lowercased), and every adjacent pair of words joined, so "notes page" also
// asks for "notespage" and a sentence still finds the CamelCase name it
// mentions. Repeated units count once. Units of a single trigram (three-letter
// words such as "the" or "top") are dropped when any longer unit exists: they
// occur inside too many names to say anything. At most nameMaxWords words are
// read.
func nameUnits(q string) [][]string {
	words := queryWords(q, nameMaxWords)
	seen := map[string]bool{}
	var units [][]string
	longest := 0
	add := func(w string) {
		if seen[w] {
			return
		}
		seen[w] = true
		if g := distinctTrigrams(w); len(g) > 0 {
			units = append(units, g)
			longest = max(longest, len(g))
		}
	}
	for i, w := range words {
		add(w)
		if i > 0 {
			add(words[i-1] + w)
		}
	}
	if longest <= 1 {
		return units
	}
	kept := units[:0]
	for _, u := range units {
		if len(u) > 1 {
			kept = append(kept, u)
		}
	}
	return kept
}

// queryWords is the query's words in order, lowercased, read the way Terms
// reads them (runs of letters, digits and '_'), at most limit of them.
func queryWords(q string, limit int) []string {
	var out []string
	var word []rune
	flush := func() {
		if len(word) > 0 && len(out) < limit {
			out = append(out, strings.ToLower(string(word)))
		}
		word = word[:0]
	}
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// distinctTrigrams is trigramsOf(term) without repeats, in first-seen order.
func distinctTrigrams(term string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range trigramsOf(term) {
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

func (s *Store) topHits(scores map[string]float64, k int, sc *scope) ([]SearchHit, error) {
	type scored struct {
		id    string
		score float64
	}
	ranked := make([]scored, 0, len(scores))
	for id, v := range scores {
		ranked = append(ranked, scored{id, v})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].id < ranked[j].id
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	hits := make([]SearchHit, 0, len(ranked))
	for _, r := range ranked {
		raw, ok, err := s.get(pChunk + r.id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var rec chunkRec
		if err := decode(raw, &rec); err != nil {
			return nil, fmt.Errorf("chunk %s: %w", r.id, err)
		}
		hits = append(hits, SearchHit{
			ChunkID: rec.ID, LogicalDocumentID: rec.LogicalID, SourceID: rec.Source, PartitionID: rec.Partition,
			Name: rec.Name, Content: rec.Content, Metadata: rec.Metadata, Score: r.score,
		})
	}
	return s.filterByPartition(hits, sc.partition, sc.overlays), nil
}
