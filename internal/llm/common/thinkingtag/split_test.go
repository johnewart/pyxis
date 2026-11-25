package thinkingtag

import (
	"strings"
	"testing"
)

func TestSplitter_simple(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	s.Feed("Hello <thinking>reason", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Feed("ing</thinking> world", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Flush(func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	if got, want := text.String(), "Hello  world"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if got, want := think.String(), "reasoning"; got != want {
		t.Fatalf("think: got %q want %q", got, want)
	}
}

func TestSplitter_partialOpenAcrossChunks(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	s.Feed("a<thin", emitText, emitThink)
	s.Feed("king>z</thinking>b", emitText, emitThink)
	s.Flush(emitText, emitThink)
	if got, want := text.String(), "ab"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if got, want := think.String(), "z"; got != want {
		t.Fatalf("think: got %q want %q", got, want)
	}
}

func TestSplitter_emptyThinking(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	s.Feed("x<thinking></thinking>y", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Flush(func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	if got, want := text.String(), "xy"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if think.String() != "" {
		t.Fatalf("think: got %q want empty", think.String())
	}
}

func TestSplitter_partialCloseAcrossChunks(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	s.Feed("<thinking>ab", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Feed("c</think", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Feed("ing>tail", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Flush(func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	if got, want := think.String(), "abc"; got != want {
		t.Fatalf("think: got %q want %q", got, want)
	}
	if got, want := text.String(), "tail"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
}

func TestSplitter_noTags(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	s.Feed("plain", func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	s.Flush(func(d string) { text.WriteString(d) }, func(d string) { think.WriteString(d) })
	if text.String() != "plain" || think.String() != "" {
		t.Fatalf("got text=%q think=%q", text.String(), think.String())
	}
}

func TestSplitter_unclosedThinkingFlushPassesThroughLiteral(t *testing.T) {
	var s Splitter
	var text, think strings.Builder
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	s.Feed("<thinking>orphan", emitText, emitThink)
	s.Flush(emitText, emitThink)
	if got, want := text.String(), "<thinking>orphan"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if think.String() != "" {
		t.Fatalf("think: got %q want empty", think.String())
	}
}
