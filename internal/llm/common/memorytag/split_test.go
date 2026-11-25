package memorytag

import (
	"strings"
	"testing"
)

func TestSplitter_basic(t *testing.T) {
	var sp Splitter
	var blocks []string
	sp.OnBlock = func(s string) { blocks = append(blocks, s) }
	var out strings.Builder
	emit := func(o string) { out.WriteString(o) }
	sp.Feed("a<memory>m1</memory>b", emit)
	sp.Flush(emit)
	if got, want := out.String(), "ab"; got != want {
		t.Fatalf("out=%q want %q", got, want)
	}
	if len(blocks) != 1 || blocks[0] != "m1" {
		t.Fatalf("blocks=%v", blocks)
	}
}

func TestSplitter_partialTags(t *testing.T) {
	var sp Splitter
	var blocks []string
	sp.OnBlock = func(s string) { blocks = append(blocks, s) }
	var out strings.Builder
	emit := func(o string) { out.WriteString(o) }
	sp.Feed("x<mem", emit)
	sp.Feed("ory>y</mem", emit)
	sp.Feed("ory>z", emit)
	sp.Flush(emit)
	if got, want := out.String(), "xz"; got != want {
		t.Fatalf("out=%q want %q", got, want)
	}
	if len(blocks) != 1 || blocks[0] != "y" {
		t.Fatalf("blocks=%v", blocks)
	}
}

func TestStripComplete(t *testing.T) {
	var got []string
	out := StripComplete("hi<memory> a </memory>!", func(s string) { got = append(got, s) })
	if out != "hi!" {
		t.Fatalf("out=%q", out)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got=%v", got)
	}
}

func TestSplitter_unclosedMemoryInProse_emitsAsVisibleText(t *testing.T) {
	input := "**VisiblePipeline** — strips `<memory>`, `<activity>` tags"
	var sp Splitter
	var blocks []string
	sp.OnBlock = func(s string) { blocks = append(blocks, s) }
	var out strings.Builder
	emit := func(o string) { out.WriteString(o) }
	sp.Feed(input, emit)
	sp.Flush(emit)
	if got, want := out.String(), input; got != want {
		t.Fatalf("out=%q want %q", got, want)
	}
	if len(blocks) != 0 {
		t.Fatalf("OnBlock called for unclosed prose; blocks=%v", blocks)
	}
}

func TestSplitter_completeMemoryBlock_stillStrips(t *testing.T) {
	var sp Splitter
	var blocks []string
	sp.OnBlock = func(s string) { blocks = append(blocks, s) }
	var out strings.Builder
	emit := func(o string) { out.WriteString(o) }
	sp.Feed("a<memory>m1</memory>b", emit)
	sp.Flush(emit)
	if got, want := out.String(), "ab"; got != want {
		t.Fatalf("out=%q want %q", got, want)
	}
	if len(blocks) != 1 || blocks[0] != "m1" {
		t.Fatalf("blocks=%v", blocks)
	}
}
