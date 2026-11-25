package assistanttext

import (
	"strings"
	"testing"
)

func TestProcessUnary_stripsActivityFromVisible(t *testing.T) {
	vis, think := ProcessUnary("<activity>Planning</activity>Hello", "", nil)
	if vis != "Hello" || think != "" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
}

func TestProcessUnary_activityThinkingAndVisible(t *testing.T) {
	vis, think := ProcessUnary("<activity>Researching</activity><thinking>notes</thinking>OK", "", nil)
	if vis != "OK" || think != "notes" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
}

func TestProcessUnaryWithActivity_collectsHeadlinesInOrder(t *testing.T) {
	var got []string
	vis, think := ProcessUnaryWithActivity(
		"<activity>One</activity>Hi<activity>Two</activity><thinking>t</thinking>Z",
		"",
		nil,
		func(h string) { got = append(got, h) },
	)
	if vis != "HiZ" || think != "t" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
	if strings.Join(got, "|") != "One|Two" {
		t.Fatalf("got=%v", got)
	}
}

func TestProcessUnaryWithActivity_nativeThinkingActivity(t *testing.T) {
	var got []string
	vis, think := ProcessUnaryWithActivity(
		"Visible",
		"<activity>Reasoning status</activity>Actual reasoning",
		nil,
		func(h string) { got = append(got, h) },
	)
	if vis != "Visible" || think != "Actual reasoning" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
	if len(got) != 1 || got[0] != "Reasoning status" {
		t.Fatalf("got=%v", got)
	}
}

func TestReasoningPipeline_chunkedActivityInThinking(t *testing.T) {
	var think strings.Builder
	var acts []string
	onMem := func(string) {}
	onAct := func(h string) { acts = append(acts, h) }
	emitThink := func(d string) { think.WriteString(d) }

	var pipe ReasoningPipeline
	pipe.Feed("<activity>", onMem, onAct, emitThink)
	pipe.Feed("Indexing files</activity>Step one", onMem, onAct, emitThink)
	pipe.Feed("<activity>Second wave</activity>Done", onMem, onAct, emitThink)
	pipe.Flush(onMem, onAct, emitThink)

	if strings.Join(acts, " || ") != "Indexing files || Second wave" {
		t.Fatalf("acts=%v", acts)
	}
	if think.String() != "Step oneDone" {
		t.Fatalf("think=%q", think.String())
	}
}

func TestVisiblePipeline_chunkedConversation_statusThinkingAnswer(t *testing.T) {
	var text, think strings.Builder
	var acts []string
	onMem := func(string) {}
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	onAct := func(h string) { acts = append(acts, h) }

	var pipe VisiblePipeline
	pipe.Feed("<activity>", onMem, onAct, emitText, emitThink)
	pipe.Feed("Researching auth flows</activity>", onMem, onAct, emitText, emitThink)
	pipe.Feed("<thinking>Compare", onMem, onAct, emitText, emitThink)
	pipe.Feed(" JWT vs sessions.</thinking>", onMem, onAct, emitText, emitThink)
	pipe.Feed("<activity>Writing the summary</activity>Here is ", onMem, onAct, emitText, emitThink)
	pipe.Feed("the answer.", onMem, onAct, emitText, emitThink)
	pipe.Flush(onMem, onAct, emitText, emitThink)

	if strings.Join(acts, " || ") != "Researching auth flows || Writing the summary" {
		t.Fatalf("acts=%v", acts)
	}
	if think.String() != "Compare JWT vs sessions." {
		t.Fatalf("think=%q", think.String())
	}
	if text.String() != "Here is the answer." {
		t.Fatalf("text=%q", text.String())
	}
}

func TestVisiblePipeline_memoryThenActivityThenThinking(t *testing.T) {
	var text, think strings.Builder
	var acts []string
	var mems []string
	onMem := func(m string) { mems = append(mems, m) }
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	onAct := func(h string) { acts = append(acts, h) }

	var pipe VisiblePipeline
	pipe.Feed("<memory>note</memory><activity>Indexing repo</activity>Visible<thinking>reason</thinking>End", onMem, onAct, emitText, emitThink)
	pipe.Flush(onMem, onAct, emitText, emitThink)

	if strings.Join(mems, ",") != "note" {
		t.Fatalf("mems=%v", mems)
	}
	if len(acts) != 1 || acts[0] != "Indexing repo" {
		t.Fatalf("acts=%v", acts)
	}
	if think.String() != "reason" {
		t.Fatalf("think=%q", think.String())
	}
	if text.String() != "VisibleEnd" {
		t.Fatalf("text=%q", text.String())
	}
}

func TestVisiblePipeline_whitespaceOnlyActivitySkipped(t *testing.T) {
	var text, think strings.Builder
	var acts []string
	onMem := func(string) {}
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	onAct := func(h string) { acts = append(acts, h) }

	var pipe VisiblePipeline
	pipe.Feed("A<activity> \t\n </activity>B", onMem, onAct, emitText, emitThink)
	pipe.Flush(onMem, onAct, emitText, emitThink)

	if len(acts) != 0 {
		t.Fatalf("acts=%v", acts)
	}
	if text.String() != "AB" || think.String() != "" {
		t.Fatalf("text=%q think=%q", text.String(), think.String())
	}
}

func TestVisiblePipeline_unclosedMemoryTagInProse_preservesFullText(t *testing.T) {
	input := "**VisiblePipeline** — strips `<memory>`, `<activity>`, `<thinking>` tags"
	var text, think strings.Builder
	var mems []string
	onMem := func(m string) { mems = append(mems, m) }
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }
	onAct := func(string) {}

	var pipe VisiblePipeline
	pipe.Feed(input, onMem, onAct, emitText, emitThink)
	pipe.Flush(onMem, onAct, emitText, emitThink)

	if got, want := text.String(), input; got != want {
		t.Fatalf("text=%q want %q", got, want)
	}
	if len(mems) != 0 {
		t.Fatalf("mems=%v want none for prose mention", mems)
	}
	if think.String() != "" {
		t.Fatalf("think=%q", think.String())
	}
}

func TestVisiblePipeline_unclosedMemoryTagInProse_chunked_preservesFullText(t *testing.T) {
	input := "**VisiblePipeline** — strips `<memory>`, `<activity>`, `<thinking>` tags"
	chunks := []string{
		"**VisiblePipeline** — strips `<mem",
		"ory>`, `<activity>`, `<thinking>` tags",
	}
	var text strings.Builder
	var mems []string
	onMem := func(m string) { mems = append(mems, m) }
	emitText := func(d string) { text.WriteString(d) }
	onAct := func(string) {}

	var pipe VisiblePipeline
	for _, ch := range chunks {
		pipe.Feed(ch, onMem, onAct, emitText, func(string) {})
	}
	pipe.Flush(onMem, onAct, emitText, func(string) {})

	if got, want := text.String(), input; got != want {
		t.Fatalf("text=%q want %q", got, want)
	}
	if len(mems) != 0 {
		t.Fatalf("mems=%v", mems)
	}
}
