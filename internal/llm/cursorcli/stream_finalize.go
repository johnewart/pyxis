package cursorclillm

import (
	"fmt"
	"strings"

	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/llmdlog"
)

// finalizeCursorCLIStreamBodies picks authoritative StreamComplete bodies for Cursor stream-json.
//
// Primary sources:
//   - Visible + thinking parsed from jr.Result via assistanttext.ProcessUnary (same as non-streaming).
//   - Native thinking lines are folded in by passing fullThinkingRaw as the unary nativeThinking input.
//
// Stream folds (warned):
//   - When processed visible is empty but the stream successfully emitted normalized visible text,
//     use emittedVisible (never the legacy raw stream accumulator).
//   - When processed thinking is empty but emittedThinking is non-empty, use emittedThinking.
//
// Errors:
//   - When visible is still empty and there was no tool activity in the stream, return an error so we
//     do not silently persist an empty assistant body for a chat turn that should have produced text.
func finalizeCursorCLIStreamBodies(jr jsonResult, emittedVisible, emittedThinking, fullThinkingRaw string, memHook func(string), sawToolActivity bool) (content, thinking string, err error) {
	content, thinking = assistanttext.ProcessUnary(strings.TrimSpace(jr.Result), strings.TrimSpace(fullThinkingRaw), memHook)
	content = strings.TrimSpace(content)
	thinking = strings.TrimSpace(thinking)

	if content == "" && strings.TrimSpace(emittedVisible) != "" {
		llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: stream-json result visible empty (%s); using emitted visible stream fold (%s)",
			suffixwire.Fingerprint(strings.TrimSpace(jr.Result)), suffixwire.Fingerprint(emittedVisible))
		content = strings.TrimSpace(emittedVisible)
	}

	if thinking == "" && strings.TrimSpace(emittedThinking) != "" {
		llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: stream-json result thinking empty (%s); using emitted thinking stream fold (%s)",
			suffixwire.Fingerprint(strings.TrimSpace(jr.Result)), suffixwire.Fingerprint(emittedThinking))
		thinking = strings.TrimSpace(emittedThinking)
	}

	if content == "" && !sawToolActivity {
		return "", "", fmt.Errorf("cursor-cli: empty assistant result (no processed result content, no emitted visible text, and no tool activity; result_fp=%s)",
			suffixwire.Fingerprint(strings.TrimSpace(jr.Result)))
	}

	return content, thinking, nil
}
