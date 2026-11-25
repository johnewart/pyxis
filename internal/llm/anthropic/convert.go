package anthropicllm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/llmdlog"
)

const (
	roleUser      = "user"
	roleAssistant = "assistant"
	roleSystem    = "system"
	roleTool      = "tool"
)

func systemBlocksFromProto(messages []*llmv1.Message, opt *llmv1.CallOptions) []anthropic.TextBlockParam {
	var blocks []anthropic.TextBlockParam
	for _, m := range messages {
		if m == nil || m.GetRole() != roleSystem {
			continue
		}
		t := strings.TrimSpace(m.GetContent())
		if t == "" {
			continue
		}
		blocks = append(blocks, anthropic.TextBlockParam{Text: t})
	}
	// Place a single prompt-cache breakpoint on the last system block. Anthropic caches
	// the entire prefix up to and including the marked block (tools + all system text),
	// so one marker covers the whole static system prefix and stays within the 4-breakpoint
	// limit even when the prompt is split across many system blocks.
	if len(blocks) > 0 {
		attachAnthropicPromptCacheToTextBlock(&blocks[len(blocks)-1], opt)
	}
	return blocks
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
			// Keep tool rows even when content is empty; Anthropic still needs a tool_result per tool_use_id.
		default:
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "skipping message with unknown role %q", m.GetRole())
			continue
		}
		out = append(out, m)
	}
	return out
}

func mapTrueCount(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

// repairOrphanAnthropicToolResults inserts synthetic role=tool rows immediately after each
// assistant message that has tool_calls but no following tool-role results (e.g. history
// persisted before the client sent tool results). This keeps Anthropic message params valid
// and avoids repeated per-tool_use_id stub logs on every RPC for poisoned sessions.
func repairOrphanAnthropicToolResults(valid []*llmv1.Message) []*llmv1.Message {
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
				"error":   "Tool result was not provided for tool_use_id " + id,
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
		llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn,
			"repaired incomplete tool chains: inserted %d synthetic role=%q message(s) (stale session history; prefer a new session or persist tool results after each tool_use)",
			repairedTotal, roleTool)
	}
	return out
}

// ProtoMessagesToParams converts gRPC messages into Anthropic system blocks and message params.
// After an assistant message with tool_calls, optional blank user placeholders are skipped, then
// consecutive tool-role results (matching tool_call_id) are merged into one user message for Anthropic.
func ProtoMessagesToParams(messages []*llmv1.Message, opt *llmv1.CallOptions) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	system := systemBlocksFromProto(messages, opt)
	valid := repairOrphanAnthropicToolResults(filterMessages(messages))

	var params []anthropic.MessageParam
	for i := 0; i < len(valid); i++ {
		m := valid[i]
		switch m.GetRole() {
		case roleAssistant:
			if len(m.GetToolCalls()) > 0 {
				ap, err := assistantWithToolsToParam(m)
				if err != nil {
					return nil, nil, err
				}
				params = append(params, ap)

				expected := map[string]struct{}{}
				for _, tc := range m.GetToolCalls() {
					if tc.GetId() != "" {
						expected[tc.GetId()] = struct{}{}
					}
				}
				found := map[string]bool{}
				var results []anthropic.ContentBlockParamUnion
				i++
				for i < len(valid) && valid[i].GetRole() == roleUser && strings.TrimSpace(valid[i].GetContent()) == "" {
					i++
				}
				for i < len(valid) && valid[i].GetRole() == roleTool {
					tm := valid[i]
					id := tm.GetToolCallId()
					if id != "" {
						found[id] = true
					}
					body := tm.GetContent()
					if strings.TrimSpace(body) == "" {
						body = "{}"
					}
					results = append(results, anthropic.NewToolResultBlock(id, body, false))
					i++
				}
				i--

				for id := range expected {
					if !found[id] {
						llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn,
							"missing tool_result for tool_use_id %s (expected=%d matched_results=%d); injecting error stub — append role=%q messages with tool_call_id immediately after this assistant turn, or run a tool loop before the next user message",
							id, len(expected), mapTrueCount(found), roleTool)
						payload, _ := json.Marshal(map[string]any{
							"success": false,
							"error":   "Tool result was not provided for tool_use_id " + id,
						})
						results = append(results, anthropic.NewToolResultBlock(id, string(payload), true))
					}
				}

				if len(results) > 0 {
					params = append(params, anthropic.NewUserMessage(results...))
				}
				continue
			}
			if strings.TrimSpace(m.GetContent()) == "" && strings.TrimSpace(m.GetThinkingContent()) == "" {
				return nil, nil, fmt.Errorf("assistant message without tool calls must have non-empty content or thinking_content")
			}
			// History replay sends user-visible text only. Persisted thinking_content is not mapped to
			// Anthropic thinking blocks (those require a model-issued signature on the wire).
			visible := m.GetContent()
			if strings.TrimSpace(visible) == "" && strings.TrimSpace(m.GetThinkingContent()) != "" {
				visible = " "
			}
			params = append(params, anthropic.NewAssistantMessage(anthropic.NewTextBlock(visible)))

		case roleTool:
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "orphaned tool message before assistant pairing; skipping")
			continue

		case roleUser:
			params = append(params, anthropic.NewUserMessage(anthropic.NewTextBlock(m.GetContent())))

		default:
			return nil, nil, fmt.Errorf("unexpected role %q after filtering", m.GetRole())
		}
	}

	return system, params, nil
}

func assistantWithToolsToParam(m *llmv1.Message) (anthropic.MessageParam, error) {
	var blocks []anthropic.ContentBlockParamUnion
	if t := strings.TrimSpace(m.GetContent()); t != "" {
		blocks = append(blocks, anthropic.NewTextBlock(t))
	}
	for _, tc := range m.GetToolCalls() {
		if tc.GetId() == "" || tc.GetName() == "" {
			return anthropic.MessageParam{}, fmt.Errorf("tool call missing id or name")
		}
		var input any
		raw := strings.TrimSpace(tc.GetArgumentsJson())
		if raw == "" {
			input = map[string]any{}
		} else {
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				return anthropic.MessageParam{}, fmt.Errorf("tool %q arguments_json: %w", tc.GetName(), err)
			}
		}
		blocks = append(blocks, anthropic.NewToolUseBlock(tc.GetId(), input, tc.GetName()))
	}
	return anthropic.NewAssistantMessage(blocks...), nil
}

func assistantReplyFromMessage(ctx context.Context, msg *anthropic.Message) (*llmv1.AssistantReply, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil anthropic message")
	}
	var text string
	var thinking string
	var calls []*llmv1.ToolCall
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text += b.Text
		case anthropic.ThinkingBlock:
			thinking += b.Thinking
		case anthropic.ToolUseBlock:
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, &llmv1.ToolCall{
				Id:            b.ID,
				Name:          b.Name,
				ArgumentsJson: args,
			})
		default:
			// ignore thinking and other block types for this unary surface
		}
	}
	hook := memctx.AssistantMemoryHook(ctx)
	vis, think := assistanttext.ProcessUnary(text, thinking, hook)
	reply := &llmv1.AssistantReply{
		Content:         vis,
		ThinkingContent: think,
		Id:              msg.ID,
		FinishReason:    string(msg.StopReason),
	}
	if len(calls) > 0 {
		reply.ToolCalls = calls
	}
	return reply, nil
}

// PrepareBedrockConverseMessages applies the same filter and tool-chain repair as the
// Anthropic Messages path so Amazon Bedrock Converse receives a valid tool loop.
func PrepareBedrockConverseMessages(messages []*llmv1.Message) []*llmv1.Message {
	return repairOrphanAnthropicToolResults(filterMessages(messages))
}
