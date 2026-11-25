package claudecodeclillm

import (
	"strings"

	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
)

// finalizeClaudeStreamBodies picks authoritative StreamComplete bodies for Claude stream-json.
func finalizeClaudeStreamBodies(jr claudeJSONResult, emittedVisible, emittedThinking, fullThinking string, memHook func(string)) (content, thinking string, err error) {
	resultText := strings.TrimSpace(jr.Result)
	if emittedVisible == "" && resultText != "" {
		content, _ = assistanttext.ProcessUnary(resultText, "", memHook)
	} else {
		content = emittedVisible
	}
	if emittedThinking == "" && fullThinking != "" {
		thinking = fullThinking
	} else {
		thinking = emittedThinking
	}
	if content == "" && resultText != "" && suffixwire.Fingerprint(resultText) != suffixwire.Fingerprint(emittedVisible) {
		content, _ = assistanttext.ProcessUnary(resultText, "", memHook)
	}
	return content, thinking, nil
}
