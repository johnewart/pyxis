package chunk

import (
	"strings"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	p, err := Resolve(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != DefaultChunkSize || p.Overlap != 0 {
		t.Fatalf("defaults: %+v", p)
	}
}

func TestResolveOverlapInvalid(t *testing.T) {
	if _, err := Resolve(100, 100); err == nil {
		t.Fatal("expected error")
	}
}

func TestSingleUTF8(t *testing.T) {
	if got := SingleUTF8("  hello  "); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("SingleUTF8: %q", got)
	}
	if got := SingleUTF8("   "); got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestSplitUTF8(t *testing.T) {
	p := Params{Size: 4, Overlap: 1}
	s := "abcdefghij"
	out := SplitUTF8(s, p)
	if len(out) < 3 {
		t.Fatalf("chunks: %q", out)
	}
	if !strings.Contains(out[0], "abcd") {
		t.Fatalf("first chunk: %q", out[0])
	}
}
