package activitytag

import (
	"strings"
	"testing"
)

func TestSplitter_simple(t *testing.T) {
	var s Splitter
	var text strings.Builder
	var acts []string
	emitText := func(d string) { text.WriteString(d) }
	emitAct := func(h string) { acts = append(acts, h) }
	s.Feed("Hello <activity>Planning</activity> world", emitText, emitAct)
	s.Flush(emitText, emitAct)
	if got, want := text.String(), "Hello  world"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if len(acts) != 1 || acts[0] != "Planning" {
		t.Fatalf("acts=%v", acts)
	}
}

func TestSplitter_partialOpenAcrossChunks(t *testing.T) {
	var s Splitter
	var text strings.Builder
	var acts []string
	emitText := func(d string) { text.WriteString(d) }
	emitAct := func(h string) { acts = append(acts, h) }
	s.Feed("a<activ", emitText, emitAct)
	s.Feed("ity>z</activity>b", emitText, emitAct)
	s.Flush(emitText, emitAct)
	if got, want := text.String(), "ab"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if len(acts) != 1 || acts[0] != "z" {
		t.Fatalf("acts=%v", acts)
	}
}

func TestSplitter_multipleStatuses(t *testing.T) {
	var s Splitter
	var text strings.Builder
	var acts []string
	emitText := func(d string) { text.WriteString(d) }
	emitAct := func(h string) { acts = append(acts, h) }
	s.Feed("<activity>Researching auth</activity>", emitText, emitAct)
	s.Feed("Drafting <activity>Writing tests</activity> answer.", emitText, emitAct)
	s.Flush(emitText, emitAct)
	if got, want := text.String(), "Drafting  answer."; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if strings.Join(acts, " | ") != "Researching auth | Writing tests" {
		t.Fatalf("acts=%v", acts)
	}
}

func TestSplitter_emptyActivitySkipped(t *testing.T) {
	var s Splitter
	var text strings.Builder
	var acts []string
	emitText := func(d string) { text.WriteString(d) }
	emitAct := func(h string) { acts = append(acts, h) }
	s.Feed("x<activity>  \n\t</activity>y", emitText, emitAct)
	s.Flush(emitText, emitAct)
	if got, want := text.String(), "xy"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if len(acts) != 0 {
		t.Fatalf("acts=%v", acts)
	}
}

func TestSplitter_unclosedFlushPassesThroughLiteral(t *testing.T) {
	var s Splitter
	var text strings.Builder
	var acts []string
	emitText := func(d string) { text.WriteString(d) }
	emitAct := func(h string) { acts = append(acts, h) }
	s.Feed("<activity>Still working", emitText, emitAct)
	s.Flush(emitText, emitAct)
	if got, want := text.String(), "<activity>Still working"; got != want {
		t.Fatalf("text: got %q want %q", got, want)
	}
	if len(acts) != 0 {
		t.Fatalf("acts=%v", acts)
	}
}

func TestStripComplete_invokesInOrder(t *testing.T) {
	var acts []string
	out := StripComplete(`<activity>One</activity>Hi<activity>Two</activity>`, func(h string) { acts = append(acts, h) })
	if out != "Hi" {
		t.Fatalf("out=%q", out)
	}
	if strings.Join(acts, ",") != "One,Two" {
		t.Fatalf("acts=%v", acts)
	}
}
