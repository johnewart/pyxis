// Package assistanttext chains <memory> stripping with <activity> stripping and <thinking> splitting
// for assistant-visible text, and memory plus <activity> stripping for native reasoning/thinking streams.
package assistanttext

import (
	"strings"

	"github.com/johnewart/pyxis/internal/llm/common/activitytag"
	"github.com/johnewart/pyxis/internal/llm/common/memorytag"
	"github.com/johnewart/pyxis/internal/llm/common/thinkingtag"
)

// ProcessUnary strips <memory> blocks (onMemory per non-empty trimmed block), strips <activity>
// regions from visible text, splits <thinking> from content, then strips memories from nativeThinking
// and appends it to the thinking result.
func ProcessUnary(content, nativeThinking string, onMemory func(string)) (visible, thinking string) {
	return ProcessUnaryWithActivity(content, nativeThinking, onMemory, nil)
}

// ProcessUnaryWithActivity is like ProcessUnary but invokes onActivity (trimmed inner text) for each
// completed <activity>...</activity> block in document order in both the visible stream and
// nativeThinking. When onActivity is nil, activity regions are still removed from both outputs.
func ProcessUnaryWithActivity(content, nativeThinking string, onMemory, onActivity func(string)) (visible, thinking string) {
	memHook := onMemory
	if memHook == nil {
		memHook = func(string) {}
	}
	c := memorytag.StripComplete(content, memHook)
	afterAct := activitytag.StripComplete(c, onActivity)
	tNatMem := memorytag.StripComplete(nativeThinking, memHook)
	tNat := activitytag.StripComplete(tNatMem, onActivity)
	var vis, think strings.Builder
	var sp thinkingtag.Splitter
	sp.Feed(afterAct, func(d string) { vis.WriteString(d) }, func(d string) { think.WriteString(d) })
	sp.Flush(func(d string) { vis.WriteString(d) }, func(d string) { think.WriteString(d) })
	outThink := think.String()
	if tNat != "" {
		outThink += tNat
	}
	return vis.String(), outThink
}

// VisiblePipeline applies memory removal, activity stripping, then thinking splitting on user-visible assistant text.
type VisiblePipeline struct {
	mem   memorytag.Splitter
	act   activitytag.Splitter
	think thinkingtag.Splitter
}

func (v *VisiblePipeline) Reset() {
	v.mem.Reset()
	v.act.Reset()
	v.think.Reset()
}

func (v *VisiblePipeline) Feed(chunk string, onMemory func(text string), onActivity func(headline string), emitText, emitThinking func(delta string)) {
	if onActivity == nil {
		onActivity = func(string) {}
	}
	v.mem.OnBlock = onMemory
	v.mem.Feed(chunk, func(out string) {
		if out == "" {
			return
		}
		v.act.Feed(out, func(pass string) {
			if pass == "" {
				return
			}
			v.think.Feed(pass, emitText, emitThinking)
		}, onActivity)
	})
}

func (v *VisiblePipeline) Flush(onMemory func(text string), onActivity func(headline string), emitText, emitThinking func(delta string)) {
	if onActivity == nil {
		onActivity = func(string) {}
	}
	flushThrough := func(out string) {
		if out == "" {
			return
		}
		v.act.Feed(out, func(pass string) {
			if pass == "" {
				return
			}
			v.think.Feed(pass, emitText, emitThinking)
		}, onActivity)
	}
	v.mem.OnBlock = onMemory
	v.mem.Flush(flushThrough)
	v.act.Flush(func(pass string) {
		if pass == "" {
			return
		}
		v.think.Feed(pass, emitText, emitThinking)
	}, onActivity)
	v.think.Flush(emitText, emitThinking)
}

// ReasoningPipeline strips <memory> blocks and <activity> regions from a reasoning/thinking stream
// without nested <thinking> splitting (providers emit reasoning as a separate channel from visible text).
type ReasoningPipeline struct {
	mem memorytag.Splitter
	act activitytag.Splitter
}

func (r *ReasoningPipeline) Reset() {
	r.mem.Reset()
	r.act.Reset()
}

func (r *ReasoningPipeline) Feed(chunk string, onMemory func(text string), onActivity func(headline string), emitThinking func(delta string)) {
	if onActivity == nil {
		onActivity = func(string) {}
	}
	r.mem.OnBlock = onMemory
	r.mem.Feed(chunk, func(out string) {
		if out == "" {
			return
		}
		r.act.Feed(out, emitThinking, onActivity)
	})
}

func (r *ReasoningPipeline) Flush(onMemory func(text string), onActivity func(headline string), emitThinking func(delta string)) {
	if onActivity == nil {
		onActivity = func(string) {}
	}
	flushThrough := func(out string) {
		if out == "" {
			return
		}
		r.act.Feed(out, emitThinking, onActivity)
	}
	r.mem.OnBlock = onMemory
	r.mem.Flush(flushThrough)
	r.act.Flush(emitThinking, onActivity)
}
