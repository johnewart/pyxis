package geminillm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

const (
	roleUser      = "user"
	roleAssistant = "assistant"
	roleSystem    = "system"
	roleTool      = "tool"
)

func systemInstructionFromProto(messages []*llmv1.Message) *genai.Content {
	var parts []*genai.Part
	for _, m := range messages {
		if m == nil || m.GetRole() != roleSystem {
			continue
		}
		if t := strings.TrimSpace(m.GetContent()); t != "" {
			parts = append(parts, genai.NewPartFromText(t))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return &genai.Content{Parts: parts}
}

func filterMessages(messages []*llmv1.Message) []*llmv1.Message {
	var out []*llmv1.Message
	for _, m := range messages {
		if m == nil {
			continue
		}
		switch m.GetRole() {
		case roleSystem:
			continue
		case roleUser:
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
		case roleAssistant:
			if len(m.GetToolCalls()) == 0 && strings.TrimSpace(m.GetContent()) == "" && strings.TrimSpace(m.GetThinkingContent()) == "" {
				continue
			}
		case roleTool:
			if strings.TrimSpace(m.GetToolCallId()) == "" {
				continue
			}
		default:
			llmdlog.Stdout("geminillm", llmdlog.LevelWarn, "skipping message with unknown role %q", m.GetRole())
			continue
		}
		out = append(out, m)
	}
	return out
}

func repairOrphanGeminiToolResults(valid []*llmv1.Message) []*llmv1.Message {
	if len(valid) == 0 {
		return valid
	}
	out := make([]*llmv1.Message, 0, len(valid)+8)
	var repairedTotal int
	for i := 0; i < len(valid); {
		m := valid[i]
		out = append(out, m)
		i++

		if m.GetRole() != roleAssistant || len(m.GetToolCalls()) == 0 {
			continue
		}
		expected := map[string]struct{}{}
		for _, tc := range m.GetToolCalls() {
			if tc.GetId() != "" {
				expected[tc.GetId()] = struct{}{}
			}
		}
		if len(expected) == 0 {
			continue
		}

		found := map[string]bool{}
		for i < len(valid) && valid[i].GetRole() == roleTool {
			tm := valid[i]
			out = append(out, tm)
			if id := tm.GetToolCallId(); id != "" {
				found[id] = true
			}
			i++
		}

		var missing []string
		for id := range expected {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			continue
		}
		sort.Strings(missing)
		repairedTotal += len(missing)
		for _, id := range missing {
			payload, _ := json.Marshal(map[string]any{
				"success": false,
				"error":   "Tool result was not provided for tool_call_id " + id,
			})
			out = append(out, &llmv1.Message{
				Role:       roleTool,
				ToolCallId: id,
				Content:    string(payload),
				Id:         "llmd-repair-" + id,
			})
		}
	}
	if repairedTotal > 0 {
		llmdlog.Stdout("geminillm", llmdlog.LevelWarn,
			"repaired incomplete tool chains: inserted %d synthetic role=%q message(s)",
			repairedTotal, roleTool)
	}
	return out
}

func toolNameByID(calls []*llmv1.ToolCall) map[string]string {
	out := make(map[string]string, len(calls))
	for _, tc := range calls {
		if tc == nil {
			continue
		}
		if id, name := tc.GetId(), tc.GetName(); id != "" && name != "" {
			out[id] = name
		}
	}
	return out
}

// toolResultResponseFromBody maps llmd tool message content to Gemini functionResponse.Response.
// Client-provided tools often return plain text (e.g. "Session title updated to: …"); Anthropic accepts
// that verbatim, but Gemini requires a JSON object map.
func toolResultResponseFromBody(body string) map[string]any {
	body = strings.TrimSpace(body)
	if body == "" {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err == nil && obj != nil {
		return obj
	}
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		return map[string]any{"result": parsed}
	}
	return map[string]any{"result": body}
}

func functionResponseFromTool(name, id, body string) (*genai.FunctionResponse, error) {
	return &genai.FunctionResponse{
		ID:       id,
		Name:     name,
		Response: toolResultResponseFromBody(body),
	}, nil
}

func assistantWithToolsToContent(m *llmv1.Message) (*genai.Content, error) {
	var parts []*genai.Part
	if t := strings.TrimSpace(m.GetContent()); t != "" {
		parts = append(parts, genai.NewPartFromText(t))
	}
	for _, tc := range m.GetToolCalls() {
		part, err := functionCallPartFromToolCall(tc)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return &genai.Content{Role: string(genai.RoleModel), Parts: parts}, nil
}

// ProtoMessagesToGemini converts gRPC messages into Gemini system instruction and contents.
func ProtoMessagesToGemini(messages []*llmv1.Message) (*genai.Content, []*genai.Content, error) {
	system := systemInstructionFromProto(messages)
	valid := repairOrphanGeminiToolResults(filterMessages(messages))

	var contents []*genai.Content
	for i := 0; i < len(valid); i++ {
		m := valid[i]
		switch m.GetRole() {
		case roleAssistant:
			if len(m.GetToolCalls()) > 0 {
				modelContent, err := assistantWithToolsToContent(m)
				if err != nil {
					return nil, nil, err
				}
				contents = append(contents, modelContent)

				names := toolNameByID(m.GetToolCalls())
				expected := map[string]struct{}{}
				for id := range names {
					expected[id] = struct{}{}
				}
				found := map[string]bool{}
				var respParts []*genai.Part
				i++
				for i < len(valid) && valid[i].GetRole() == roleUser && strings.TrimSpace(valid[i].GetContent()) == "" {
					i++
				}
				for i < len(valid) && valid[i].GetRole() == roleTool {
					tm := valid[i]
					id := tm.GetToolCallId()
					name := names[id]
					if name == "" {
						return nil, nil, fmt.Errorf("tool result %q missing matching tool name", id)
					}
					found[id] = true
					fr, err := functionResponseFromTool(name, id, tm.GetContent())
					if err != nil {
						return nil, nil, err
					}
					respParts = append(respParts, &genai.Part{FunctionResponse: fr})
					i++
				}
				i--

				for id := range expected {
					if found[id] {
						continue
					}
					name := names[id]
					payload, _ := json.Marshal(map[string]any{
						"success": false,
						"error":   "Tool result was not provided for tool_call_id " + id,
					})
					fr, err := functionResponseFromTool(name, id, string(payload))
					if err != nil {
						return nil, nil, err
					}
					respParts = append(respParts, &genai.Part{FunctionResponse: fr})
				}
				if len(respParts) > 0 {
					contents = append(contents, &genai.Content{Role: string(genai.RoleUser), Parts: respParts})
				}
				continue
			}
			if strings.TrimSpace(m.GetContent()) == "" && strings.TrimSpace(m.GetThinkingContent()) == "" {
				return nil, nil, fmt.Errorf("assistant message without tool calls must have non-empty content or thinking_content")
			}
			visible := m.GetContent()
			if strings.TrimSpace(visible) == "" && strings.TrimSpace(m.GetThinkingContent()) != "" {
				visible = " "
			}
			contents = append(contents, &genai.Content{
				Role:  string(genai.RoleModel),
				Parts: []*genai.Part{genai.NewPartFromText(visible)},
			})

		case roleTool:
			llmdlog.Stdout("geminillm", llmdlog.LevelWarn, "orphaned tool message before assistant pairing; skipping")
			continue

		case roleUser:
			contents = append(contents, &genai.Content{
				Role:  string(genai.RoleUser),
				Parts: []*genai.Part{genai.NewPartFromText(m.GetContent())},
			})

		default:
			return nil, nil, fmt.Errorf("unexpected role %q after filtering", m.GetRole())
		}
	}

	return system, contents, nil
}
