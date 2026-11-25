package claudecodeclillm

import (
	"encoding/json"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/toolcapture"
)

// Re-export the shared sink type and context helpers so callers that only
// import this package can still use the standard API.
type ToolCaptureSink = toolcapture.Sink

// ContextWithToolCaptureSink returns a copy of ctx carrying sink.
var ContextWithToolCaptureSink = toolcapture.NewContext

// ToolCaptureSinkFromContext returns the sink stored in ctx, or nil.
var ToolCaptureSinkFromContext = toolcapture.FromContext

// captureAssistantMessage inspects a Claude stream-json "assistant" message object
// and records any tool_use blocks into sink.
func captureAssistantMessage(sink *toolcapture.Sink, msg any) {
	m, ok := msg.(map[string]any)
	if !ok {
		return
	}
	content, _ := m["content"].([]any)
	var toolCalls []*llmv1.ToolCall
	var textParts strings.Builder
	for _, part := range content {
		pm, ok := part.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := pm["type"].(string)
		switch typ {
		case "text", "output_text":
			txt, _ := pm["text"].(string)
			textParts.WriteString(txt)
		case "tool_use":
			id, _ := pm["id"].(string)
			name, _ := pm["name"].(string)
			if id == "" || name == "" {
				continue
			}
			var argsJSON string
			if input := pm["input"]; input != nil {
				b, _ := json.Marshal(input)
				argsJSON = string(b)
			}
			toolCalls = append(toolCalls, &llmv1.ToolCall{
				Id:            id,
				Name:          name,
				ArgumentsJson: argsJSON,
			})
		}
	}
	if len(toolCalls) == 0 {
		return
	}
	sink.Add(&llmv1.Message{
		Role:      "assistant",
		Content:   strings.TrimSpace(textParts.String()),
		ToolCalls: toolCalls,
	})
}

// captureUserMessage inspects a Claude stream-json "user" message object and
// records any tool_result blocks into sink.
func captureUserMessage(sink *toolcapture.Sink, msg any) {
	m, ok := msg.(map[string]any)
	if !ok {
		return
	}
	content, _ := m["content"].([]any)
	for _, part := range content {
		pm, ok := part.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := pm["type"].(string)
		if typ != "tool_result" {
			continue
		}
		toolUseID, _ := pm["tool_use_id"].(string)
		if toolUseID == "" {
			continue
		}
		var resultContent string
		switch c := pm["content"].(type) {
		case string:
			resultContent = c
		case []any:
			var b strings.Builder
			for _, cp := range c {
				cpm, ok := cp.(map[string]any)
				if !ok {
					continue
				}
				if cpm["type"] == "text" {
					txt, _ := cpm["text"].(string)
					b.WriteString(txt)
				}
			}
			resultContent = b.String()
		}
		sink.Add(&llmv1.Message{
			Role:       "tool",
			ToolCallId: toolUseID,
			Content:    resultContent,
		})
	}
}
