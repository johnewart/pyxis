package lexstore

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func openT(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "lex"), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sym(path, name, content string) DocumentIn {
	return DocumentIn{LogicalDocumentID: path + "::" + name, Name: path, Content: content,
		Metadata: map[string]string{"symbolName": name}}
}

func put(t *testing.T, s *Store, space, path string, opts UpsertOptions, docs ...DocumentIn) {
	t.Helper()
	opts.PreChunked = true
	if _, err := s.ReplaceFileDocuments(context.Background(), space, path, docs, opts); err != nil {
		t.Fatal(err)
	}
}

func ids(hits []SearchHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.LogicalDocumentID)
	}
	return out
}

func q(t *testing.T, s *Store, space, text string, f QueryFilter, o QueryOptions) []SearchHit {
	t.Helper()
	h, err := s.Query(context.Background(), space, text, f, o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestTermsSplitIdentifiers(t *testing.T) {
	got := Terms("HTTPServer embedPullPaths v2_api")
	for _, w := range []string{"httpserver", "http", "server", "embedpullpaths", "embed", "pull", "paths", "v2_api", "api"} {
		if got[w] == 0 {
			t.Errorf("missing term %q in %v", w, got)
		}
	}
}

func TestFindsByWordsAndPartialNames(t *testing.T) {
	s := openT(t, Options{})
	put(t, s, "sp", "pull.go", UpsertOptions{},
		sym("pull.go", "embedPullPathsParallel", "func embedPullPathsParallel(paths []string) error { return nil }"),
		sym("pull.go", "otherThing", "func otherThing() {}"))
	put(t, s, "sp", "server.go", UpsertOptions{}, sym("server.go", "NewHTTPServer", "func NewHTTPServer(addr string) *Server { return nil }"))
	for _, c := range []struct{ q, want string }{
		{"embed pull paths", "pull.go::embedPullPathsParallel"},
		{"embedPullPathsParallel", "pull.go::embedPullPathsParallel"},
		{"http server", "server.go::NewHTTPServer"},
		{"HTTPServr", "server.go::NewHTTPServer"},
	} {
		if h := q(t, s, "sp", c.q, QueryFilter{}, QueryOptions{Limit: 5}); len(h) == 0 || h[0].LogicalDocumentID != c.want {
			t.Errorf("%q -> %v, want %s first", c.q, ids(h), c.want)
		}
	}
	if h := q(t, s, "other", "embed pull", QueryFilter{}, QueryOptions{}); len(h) != 0 {
		t.Fatalf("space isolation: %v", ids(h))
	}
}

// A chat opener is a sentence, not a name. Name matching must still find the
// CamelCase name it mentions, and rank it above a chunk of English prose that
// shares more of the sentence's words.
func TestLongQueryFindsTheNameItMentions(t *testing.T) {
	s := openT(t, Options{})
	put(t, s, "sp", "web/pages/NotesPage.tsx", UpsertOptions{},
		sym("web/pages/NotesPage.tsx", "NotesPage", "export function NotesPage() { return renderList(listNotes()); }"))
	put(t, s, "sp", "web/components/DatePicker.tsx", UpsertOptions{},
		sym("web/components/DatePicker.tsx", "DatePicker", "export function DatePicker() { return pick(today()); }"))
	put(t, s, "sp", "server/prompts.go", UpsertOptions{},
		sym("server/prompts.go", "pairPrompt", "const pairPrompt = `Keep replies short; a wall of text is hard to read, so put the most important "+
			"things on top. Things that seem right are not evidence: take a look first. Notes are scratch pads, "+
			"keep the list of notes easy to browse, newest on top, and date each page you write.`"))
	const opener = "The notes page is hard to browse - things don't seem to have the right date and it doesn't seem " +
		"to be sorted with the newest things on top, etc. can you take a look?"

	if h := q(t, s, "sp", opener, QueryFilter{}, QueryOptions{Mode: ModeName}); len(h) == 0 || h[0].LogicalDocumentID != "web/pages/NotesPage.tsx::NotesPage" {
		t.Fatalf("name mode: %v, want NotesPage first", ids(h))
	}
	if h := q(t, s, "sp", opener, QueryFilter{}, QueryOptions{}); len(h) == 0 || h[0].LogicalDocumentID != "web/pages/NotesPage.tsx::NotesPage" {
		t.Fatalf("hybrid: %v, want NotesPage first", ids(h))
	}
}

func TestNameUnits(t *testing.T) {
	join := func(units [][]string) []string {
		var out []string
		for _, u := range units {
			out = append(out, strings.Join(u, ""))
		}
		return out
	}
	// One word is one unit: the whole-query match it always was.
	if got := nameUnits("embedPullPathsParallel"); len(got) != 1 || !reflect.DeepEqual(got[0], distinctTrigrams("embedpullpathsparallel")) {
		t.Fatalf("single word: %v", join(got))
	}
	// Words and joined pairs, in query order. "the" (one trigram) is dropped
	// because longer units exist, but it still joins its neighbour.
	got := nameUnits("the notes page")
	want := [][]string{distinctTrigrams("notes"), distinctTrigrams("thenotes"), distinctTrigrams("page"), distinctTrigrams("notespage")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the notes page: %v, want %v", join(got), join(want))
	}
	// Repeats count once.
	if got := nameUnits("page page"); len(got) != 2 {
		t.Fatalf("page page: %v, want page and pagepage", join(got))
	}
	// Bounded: a pasted wall of text reads at most nameMaxWords words.
	if n := len(queryWords(strings.Repeat("word ", 10*nameMaxWords), nameMaxWords)); n != nameMaxWords {
		t.Fatalf("queryWords read %d words, want %d", n, nameMaxWords)
	}
}

// A file replaced with nothing stops answering searches: its old chunks go,
// other files' stay. InsertOnly never deletes, so it is a no-op there.
func TestReplaceWithNothingClearsTheFile(t *testing.T) {
	s := openT(t, Options{})
	ctx := context.Background()
	put(t, s, "sp", "gen.go", UpsertOptions{}, sym("gen.go", "GeneratedThing", "func GeneratedThing() {}"))
	put(t, s, "sp", "keep.go", UpsertOptions{}, sym("keep.go", "KeepThing", "func KeepThing() {}"))
	if h := q(t, s, "sp", "GeneratedThing", QueryFilter{}, QueryOptions{}); len(h) == 0 || h[0].LogicalDocumentID != "gen.go::GeneratedThing" {
		t.Fatalf("baseline: %v, want gen.go::GeneratedThing", ids(h))
	}

	if _, err := s.ReplaceFileDocuments(ctx, "sp", "gen.go", nil, UpsertOptions{InsertOnly: true}); err != nil {
		t.Fatal(err)
	}
	if files, _, _ := s.CountDocuments(ctx, "sp", ""); files != 2 {
		t.Fatalf("InsertOnly with nothing must not delete: %d files", files)
	}

	if _, err := s.ReplaceFileDocuments(ctx, "sp", "gen.go", nil, UpsertOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, h := range q(t, s, "sp", "GeneratedThing", QueryFilter{}, QueryOptions{}) {
		if strings.HasPrefix(h.LogicalDocumentID, "gen.go") {
			t.Fatalf("gen.go still answers after being replaced with nothing: %v", h.LogicalDocumentID)
		}
	}
	if h := q(t, s, "sp", "KeepThing", QueryFilter{}, QueryOptions{}); len(h) == 0 || h[0].LogicalDocumentID != "keep.go::KeepThing" {
		t.Fatalf("other files must stay: %v", ids(h))
	}
	if files, chunks, _ := s.CountDocuments(ctx, "sp", ""); files != 1 || chunks != 1 {
		t.Fatalf("after clearing gen.go: %d files, %d chunks, want 1 and 1", files, chunks)
	}
}

func TestReplaceSkipPruneAndStats(t *testing.T) {
	s := openT(t, Options{})
	ctx := context.Background()
	doc := func(body string) DocumentIn { return DocumentIn{LogicalDocumentID: "a.go::L1", Name: "a.go", Content: body} }
	put(t, s, "sp", "a.go", UpsertOptions{}, doc("Alpha returns one"))
	put(t, s, "sp", "a.go", UpsertOptions{}, doc("Zephyr calculates the quotient"))
	if h := q(t, s, "sp", "alpha", QueryFilter{}, QueryOptions{Mode: ModeKeyword}); len(h) != 0 {
		t.Fatalf("stale chunk: %v", ids(h))
	}
	if n, _, _ := s.corpusStats("sp", newScope(nil, "", nil)); n != 1 {
		t.Fatalf("stats count %d, want 1", n)
	}
	skipped := 0
	put(t, s, "sp", "a.go", UpsertOptions{SkipUnchanged: true, OnSkipped: func() { skipped++ }}, doc("Zephyr calculates the quotient"))
	if skipped != 1 {
		t.Fatal("unchanged file rewritten")
	}
	if files, chunks, _ := s.CountDocuments(ctx, "sp", ""); files != 1 || chunks != 1 {
		t.Fatalf("count = %d/%d", files, chunks)
	}
	if n, err := s.PruneFiles(ctx, "sp", "", []string{"a.go"}); err != nil || n != 1 {
		t.Fatalf("prune %d %v", n, err)
	}
	if n, _, _ := s.corpusStats("sp", newScope(nil, "", nil)); n != 0 {
		t.Fatalf("stats after prune %d", n)
	}
}

func TestSourcesAndIdentity(t *testing.T) {
	s := openT(t, Options{})
	ctx := context.Background()
	put(t, s, "sp", "go.mod", UpsertOptions{DefaultSourceID: "a"}, DocumentIn{LogicalDocumentID: "go.mod", Content: "module a"})
	put(t, s, "sp", "go.mod", UpsertOptions{DefaultSourceID: "b"}, DocumentIn{LogicalDocumentID: "go.mod", Content: "module b"})
	if files, _, _ := s.CountDocuments(ctx, "sp", ""); files != 2 {
		t.Fatalf("files %d", files)
	}
	if h := q(t, s, "sp", "module", QueryFilter{SourceID: "b"}, QueryOptions{}); len(h) != 1 || h[0].SourceID != "b" {
		t.Fatalf("source filter %+v", h)
	}
	a := "a"
	if n, _ := s.DeleteDocuments(ctx, "sp", &a, "", "", nil); n != 1 {
		t.Fatalf("bulk delete %d", n)
	}
	if id, _ := s.IndexIdentity(ctx, "sp", "a"); id != "" {
		t.Fatal("identity survived clear")
	}
	if id, _ := s.IndexIdentity(ctx, "sp", "b"); id != IndexID {
		t.Fatalf("identity %q", id)
	}
}

func TestOverlayReadsThroughBase(t *testing.T) {
	s := openT(t, Options{})
	ctx := context.Background()
	put(t, s, "sp", "a.go", UpsertOptions{}, sym("a.go", "Shared", "func Shared() { oldBody() }"))
	put(t, s, "sp", "b.go", UpsertOptions{}, sym("b.go", "BaseOnly", "func BaseOnly() {}"))
	put(t, s, "sp", "c.go", UpsertOptions{}, sym("c.go", "Gone", "func Gone() {}"))
	if err := s.DeclareOverlay(ctx, "sp", "", OverlayRecord{PartitionID: "p1"}); err != nil {
		t.Fatal(err)
	}
	put(t, s, "sp", "a.go", UpsertOptions{PartitionID: "p1"}, sym("a.go", "Shared", "func Shared() { newBody() }"))
	_ = s.RecordOwnedPaths(ctx, "sp", "", "p1", []string{"a.go", "c.go"})
	p1 := QueryFilter{PartitionID: "p1"}
	if h := q(t, s, "sp", "newBody", p1, QueryOptions{}); len(h) != 1 || h[0].PartitionID != "p1" {
		t.Fatalf("child %+v", h)
	}
	for _, h := range q(t, s, "sp", "oldBody", p1, QueryOptions{}) {
		if h.PartitionID != "p1" {
			t.Fatalf("overridden base served %+v", h)
		}
	}
	if h := q(t, s, "sp", "BaseOnly", p1, QueryOptions{}); len(h) != 1 {
		t.Fatalf("read-through %v", ids(h))
	}
	if h := q(t, s, "sp", "Gone", p1, QueryOptions{}); len(h) != 0 {
		t.Fatalf("deleted file served %v", ids(h))
	}
	for _, h := range q(t, s, "sp", "newBody", QueryFilter{}, QueryOptions{}) {
		if h.PartitionID != "" {
			t.Fatalf("derived leaked into live %+v", h)
		}
	}
	if n, err := s.DropPartition(ctx, "sp", "", "p1"); err != nil || n != 1 {
		t.Fatalf("drop %d %v", n, err)
	}
	if _, err := s.DropPartition(ctx, "sp", "", ""); err == nil {
		t.Fatal("dropping live must fail")
	}
}

// ---- plug-ins ----

func TestPluggableChunkerNamerRerankerFileOf(t *testing.T) {
	var namerCalls int
	s := openT(t, Options{
		// Paragraph chunker.
		Chunker: ChunkerFunc(func(d DocumentIn) []string { return strings.Split(d.Content, "\n\n") }),
		// Documents define their title (front matter "title").
		Namer: NamerFunc(func(c ChunkInfo) ([]string, []string) {
			namerCalls++
			return []string{c.Metadata["title"]}, nil
		}),
		// Reverse order, to prove it ran.
		Reranker: RerankerFunc(func(_ string, hits []SearchHit, limit int) []SearchHit {
			out := append([]SearchHit(nil), hits...)
			sort.SliceStable(out, func(i, j int) bool { return out[i].LogicalDocumentID > out[j].LogicalDocumentID })
			if len(out) > limit {
				out = out[:limit]
			}
			return out
		}),
		// Logical ids are "doc#section".
		FileOf: func(id string) string { return strings.SplitN(id, "#", 2)[0] },
	})
	ctx := context.Background()
	if n, err := s.UpsertDocuments(ctx, "sp", []DocumentIn{
		{LogicalDocumentID: "guide#intro", Content: "Owlbert hoots softly.\n\nIt nests in pines.", Metadata: map[string]string{"title": "OwlbertGuide"}},
		{LogicalDocumentID: "faq#one", Content: "Owlbert eats mice.", Metadata: map[string]string{"title": "Feeding"}},
	}, UpsertOptions{}); err != nil || n != 3 {
		t.Fatalf("upsert %d %v (paragraph chunker must give 3 chunks)", n, err)
	}
	if namerCalls != 3 {
		t.Fatalf("namer called %d times", namerCalls)
	}
	if h := q(t, s, "sp", "owlbrt guide", QueryFilter{}, QueryOptions{Mode: ModeName}); len(h) == 0 || h[0].LogicalDocumentID != "guide#intro" {
		t.Fatalf("custom namer trigrams: %v", ids(h))
	}
	h := q(t, s, "sp", "owlbert", QueryFilter{}, QueryOptions{Rerank: true})
	if len(h) != 2 || h[0].LogicalDocumentID != "guide#intro" || h[1].LogicalDocumentID != "faq#one" {
		t.Fatalf("custom reranker order: %v", ids(h))
	}
	if files, chunks, _ := s.CountDocuments(ctx, "sp", ""); files != 2 || chunks != 3 {
		t.Fatalf("custom FileOf count = %d files %d chunks", files, chunks)
	}
	if n, _ := s.DeleteDocuments(ctx, "sp", nil, "guide", "", nil); n != 2 {
		t.Fatalf("file delete via FileOf removed %d", n)
	}
}

type recObs struct {
	mu      sync.Mutex
	queries []string
	writes  []string
	written int
	skipped int
}

func (o *recObs) Query(mode string, _ time.Duration, _ int, _ error) {
	o.mu.Lock()
	o.queries = append(o.queries, mode)
	o.mu.Unlock()
}
func (o *recObs) Write(op string, _ time.Duration, _ error) {
	o.mu.Lock()
	o.writes = append(o.writes, op)
	o.mu.Unlock()
}
func (o *recObs) Chunks(w, _ int) { o.mu.Lock(); o.written += w; o.mu.Unlock() }
func (o *recObs) FileSkipped()    { o.mu.Lock(); o.skipped++; o.mu.Unlock() }

func TestObserverSeesEveryOperation(t *testing.T) {
	o := &recObs{}
	s := openT(t, Options{Observer: o})
	put(t, s, "sp", "a.go", UpsertOptions{}, sym("a.go", "A", "func A() {}"))
	put(t, s, "sp", "a.go", UpsertOptions{SkipUnchanged: true}, sym("a.go", "A", "func A() {}"))
	q(t, s, "sp", "a", QueryFilter{}, QueryOptions{Mode: ModeKeyword})
	if !reflect.DeepEqual(o.writes, []string{"replace_file", "replace_file"}) || o.written != 1 || o.skipped != 1 ||
		!reflect.DeepEqual(o.queries, []string{"keyword"}) {
		t.Fatalf("observer = %+v", o)
	}
}

func TestDataSurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lex")
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, "sp", "a.go", UpsertOptions{}, sym("a.go", "Persisted", "func Persisted() {}"))
	s.Close()
	s, err = Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if h := q(t, s, "sp", "persisted", QueryFilter{}, QueryOptions{}); len(h) != 1 {
		t.Fatalf("after reopen %v", ids(h))
	}
}

func TestSpaceValidation(t *testing.T) {
	s := openT(t, Options{})
	if _, err := s.UpsertDocuments(context.Background(), "", []DocumentIn{{LogicalDocumentID: "x", Content: "y"}}, UpsertOptions{}); err == nil {
		t.Fatal("empty space accepted")
	}
	if _, err := s.Query(context.Background(), "a\x00b", "x", QueryFilter{}, QueryOptions{}); err == nil {
		t.Fatal("NUL in space accepted")
	}
}
