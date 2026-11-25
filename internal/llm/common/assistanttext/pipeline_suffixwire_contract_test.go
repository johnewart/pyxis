package assistanttext

import (
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
)

// wireFromPipelineDeltas feeds VisiblePipeline, applies suffixwire.Compute to each emitted
// visible/thinking delta (mirrors llmd ChatStream post-pipeline + wire normalizer contract).
func wireFromPipelineDeltas(rawChunks []string) (wireVisible, wireThinking string) {
	var visEmitted, thinkEmitted string
	var pipe VisiblePipeline
	onMem := func(string) {}
	onAct := func(string) {}
	emitText := func(d string) {
		if d == "" {
			return
		}
		ne, w, _, _ := suffixwire.Compute(visEmitted, d)
		if w != "" {
			visEmitted = ne
		}
	}
	emitThink := func(d string) {
		if d == "" {
			return
		}
		ne, w, _, _ := suffixwire.Compute(thinkEmitted, d)
		if w != "" {
			thinkEmitted = ne
		}
	}
	for _, ch := range rawChunks {
		pipe.Feed(ch, onMem, onAct, emitText, emitThink)
	}
	pipe.Flush(onMem, onAct, emitText, emitThink)
	return visEmitted, thinkEmitted
}

func TestVisiblePipeline_suffixwire_duplicateFullVisibleChunk_doesNotDouble(t *testing.T) {
	vis, think := wireFromPipelineDeltas([]string{"Hello", "Hello"})
	if vis != "Hello" || think != "" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
}

func TestVisiblePipeline_suffixwire_cumulativeThenRepeat_noDuplicate(t *testing.T) {
	vis, think := wireFromPipelineDeltas([]string{"He", "Hello", "Hello"})
	if vis != "Hello" || think != "" {
		t.Fatalf("vis=%q think=%q", vis, think)
	}
}

func TestVisiblePipeline_suffixwire_duplicateThinkingSnapshots(t *testing.T) {
	raw := []string{
		"<thinking>plan A</thinking>",
		"<thinking>plan A</thinking>",
		"Visible",
	}
	vis, think := wireFromPipelineDeltas(raw)
	if vis != "Visible" {
		t.Fatalf("vis=%q", vis)
	}
	if think != "plan A" {
		t.Fatalf("think=%q want single plan", think)
	}
}

func TestVisiblePipeline_suffixwire_interleavedCumulativeVisibleAndThinking(t *testing.T) {
	raw := []string{
		"<thinking>p1</thinking>Vi",
		"<thinking>p1p2</thinking>Vis",
		"<thinking>p1p2</thinking>Visi",
		"<thinking>p1p2</thinking>Visible",
	}
	vis, think := wireFromPipelineDeltas(raw)
	if vis != "Visible" {
		t.Fatalf("vis=%q", vis)
	}
	if think != "p1p2" {
		t.Fatalf("think=%q", think)
	}
}

func TestVisiblePipeline_chunkedMemoryActivityThinking_parsedInOrder(t *testing.T) {
	var text, think strings.Builder
	var mems, acts []string
	onMem := func(m string) { mems = append(mems, m) }
	onAct := func(h string) { acts = append(acts, h) }
	emitText := func(d string) { text.WriteString(d) }
	emitThink := func(d string) { think.WriteString(d) }

	var pipe VisiblePipeline
	chunks := []string{
		"<me", "mory>first block</memory>",
		"<activity>Act one</activity>",
		"<thinking>th", "ink line</thinking>",
		"<memory>second</memory>",
		"<activity>Act two</activity>tail",
	}
	for _, c := range chunks {
		pipe.Feed(c, onMem, onAct, emitText, emitThink)
	}
	pipe.Flush(onMem, onAct, emitText, emitThink)

	if strings.Join(mems, "|") != "first block|second" {
		t.Fatalf("mems=%v", mems)
	}
	if strings.Join(acts, "|") != "Act one|Act two" {
		t.Fatalf("acts=%v", acts)
	}
	if think.String() != "think line" {
		t.Fatalf("think=%q", think.String())
	}
	if text.String() != "tail" {
		t.Fatalf("text=%q", text.String())
	}
}
