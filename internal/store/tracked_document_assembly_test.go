package store

import (
	"strings"
	"testing"
)

func TestRenderTrackedDocInjection_InitialVsUpdated(t *testing.T) {
	t.Parallel()

	initial := RenderTrackedDocInjection(TrackedDocumentVersionRow{
		DocumentID: "doc-1", Name: "plan.md", Seq: 0, Content: "hello",
	})
	if !strings.Contains(initial, `change="initial"`) {
		t.Fatalf("seq 0 should be initial: %s", initial)
	}
	if !strings.Contains(initial, `version="0"`) || !strings.Contains(initial, "hello") {
		t.Fatalf("initial block missing version/content: %s", initial)
	}

	updated := RenderTrackedDocInjection(TrackedDocumentVersionRow{
		DocumentID: "doc-1", Name: "plan.md", Seq: 2, Content: "hello world",
	})
	if !strings.Contains(updated, `change="updated"`) || !strings.Contains(updated, `version="2"`) {
		t.Fatalf("seq>0 should be updated with version: %s", updated)
	}
	// Full body is re-sent on update (no diffing).
	if !strings.Contains(updated, "hello world") {
		t.Fatalf("updated block must carry full body: %s", updated)
	}
}

func TestRenderTrackedDocInjection_EscapesAttributes(t *testing.T) {
	t.Parallel()
	out := RenderTrackedDocInjection(TrackedDocumentVersionRow{
		DocumentID: `a"b`, Name: `x<y>&z`, Seq: 0, Content: "c",
	})
	if strings.Contains(out, `id="a"b"`) {
		t.Fatalf("id attribute not escaped: %s", out)
	}
	if !strings.Contains(out, "&quot;") || !strings.Contains(out, "&lt;") || !strings.Contains(out, "&amp;") {
		t.Fatalf("attributes not escaped: %s", out)
	}
}

func TestTrackedDocInjectionsForTurn_FiltersAndOrders(t *testing.T) {
	t.Parallel()
	versions := []TrackedDocumentVersionRow{
		{DocumentID: "b", Name: "b.md", Seq: 0, Content: "B0", AnchorTurnID: "t1"},
		{DocumentID: "a", Name: "a.md", Seq: 0, Content: "A0", AnchorTurnID: "t1"},
		{DocumentID: "a", Name: "a.md", Seq: 1, Content: "A1", AnchorTurnID: "t2"},
	}

	t1 := TrackedDocInjectionsForTurn(versions, "t1")
	// Both t1 docs present, ordered a before b.
	ai := strings.Index(t1, "A0")
	bi := strings.Index(t1, "B0")
	if ai < 0 || bi < 0 || ai > bi {
		t.Fatalf("t1 should contain A0 before B0: %s", t1)
	}
	if strings.Contains(t1, "A1") {
		t.Fatalf("t1 must not contain t2's version A1: %s", t1)
	}

	t2 := TrackedDocInjectionsForTurn(versions, "t2")
	if !strings.Contains(t2, "A1") || strings.Contains(t2, "A0") {
		t.Fatalf("t2 should contain only A1: %s", t2)
	}

	if got := TrackedDocInjectionsForTurn(versions, "nope"); got != "" {
		t.Fatalf("unknown turn should be empty, got %q", got)
	}
	if got := TrackedDocInjectionsForTurn(versions, ""); got != "" {
		t.Fatalf("empty turn should be empty, got %q", got)
	}
}

func TestTrackedDocInjectionsByTurn_Buckets(t *testing.T) {
	t.Parallel()
	versions := []TrackedDocumentVersionRow{
		{DocumentID: "a", Seq: 0, Content: "A0", AnchorTurnID: "t1"},
		{DocumentID: "a", Seq: 1, Content: "A1", AnchorTurnID: "t2"},
		{DocumentID: "orphan", Seq: 0, Content: "X", AnchorTurnID: ""},
	}
	byTurn := TrackedDocInjectionsByTurn(versions)
	if len(byTurn) != 2 {
		t.Fatalf("expected 2 turns, got %d (%v)", len(byTurn), byTurn)
	}
	if !strings.Contains(byTurn["t1"], "A0") || !strings.Contains(byTurn["t2"], "A1") {
		t.Fatalf("bucketed wrong: %v", byTurn)
	}
	if _, ok := byTurn[""]; ok {
		t.Fatalf("orphan (no anchor) must not produce a bucket")
	}
}

func TestPrependContextAndFirstTurnContext(t *testing.T) {
	t.Parallel()

	if got := PrependContext("", "just this"); got != "just this" {
		t.Fatalf("empty context should return user text, got %q", got)
	}
	got := PrependContext("SEED", "user words")
	if got != "SEED\n\n---\n\nuser words" {
		t.Fatalf("unexpected prepend: %q", got)
	}

	// seed + tracked docs compose seed-first.
	ctx := BuildFirstTurnContext("SEED", "DOCS")
	if ctx != "SEED\n\nDOCS" {
		t.Fatalf("first-turn context order wrong: %q", ctx)
	}
	if BuildFirstTurnContext("", "") != "" {
		t.Fatalf("empty first-turn context should be empty")
	}
	if BuildFirstTurnContext("", "DOCS") != "DOCS" {
		t.Fatalf("seedless context should be docs only")
	}
}
