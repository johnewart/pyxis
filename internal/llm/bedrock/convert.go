package bedrockllm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/anthropic"
	"github.com/johnewart/pyxis/llmdlog"
)

const (
	roleUser      = "user"
	roleAssistant = "assistant"
	roleSystem    = "system"
	roleTool      = "tool"
)

func bedrockSystemFromProto(messages []*llmv1.Message) []types.SystemContentBlock {
	var out []types.SystemContentBlock
	for _, m := range messages {
		if m == nil || m.GetRole() != roleSystem {
			continue
		}
		if t := strings.TrimSpace(m.GetContent()); t != "" {
			out = append(out, &types.SystemContentBlockMemberText{Value: t})
		}
	}
	return out
}

func jsonDocument(raw string) (brdoc.Interface, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return brdoc.NewLazyDocument(map[string]any{}), nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return brdoc.NewLazyDocument(v), nil
}

func toolResultContentBlock(toolUseID, body string) (types.ContentBlock, error) {
	id := strings.TrimSpace(toolUseID)
	if id == "" {
		return nil, fmt.Errorf("tool_use_id is required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		body = "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(id),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: body},
				},
			},
		}, nil
	}
	// Bedrock ConverseStream requires toolResult.content[].json to be a JSON *object*.
	// Arrays (e.g. [] from list_todos), scalars, and null must use the text block instead.
	if obj, ok := v.(map[string]any); ok {
		return &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(id),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberJson{Value: brdoc.NewLazyDocument(obj)},
				},
			},
		}, nil
	}
	return &types.ContentBlockMemberToolResult{
		Value: types.ToolResultBlock{
			ToolUseId: aws.String(id),
			Content: []types.ToolResultContentBlock{
				&types.ToolResultContentBlockMemberText{Value: body},
			},
		},
	}, nil
}

func assistantMessageWithToolUses(m *llmv1.Message) (types.Message, error) {
	var blocks []types.ContentBlock
	if t := strings.TrimSpace(m.GetContent()); t != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: t})
	}
	for _, tc := range m.GetToolCalls() {
		if tc.GetId() == "" || tc.GetName() == "" {
			return types.Message{}, fmt.Errorf("tool call missing id or name")
		}
		doc, err := jsonDocument(tc.GetArgumentsJson())
		if err != nil {
			return types.Message{}, fmt.Errorf("tool %q arguments_json: %w", tc.GetName(), err)
		}
		blocks = append(blocks, &types.ContentBlockMemberToolUse{
			Value: types.ToolUseBlock{
				ToolUseId: aws.String(tc.GetId()),
				Name:      aws.String(tc.GetName()),
				Input:     doc,
			},
		})
	}
	return types.Message{Role: types.ConversationRoleAssistant, Content: blocks}, nil
}

// ProtoToBedrockMessages maps gRPC messages to Bedrock Converse message/system fields
// (same ordering and tool-loop repair as the Anthropic Messages path).
func ProtoToBedrockMessages(messages []*llmv1.Message, opt *llmv1.CallOptions) ([]types.SystemContentBlock, []types.Message, error) {
	_ = opt
	system := bedrockSystemFromProto(messages)
	valid := anthropicllm.PrepareBedrockConverseMessages(messages)
	var out []types.Message
	for i := 0; i < len(valid); i++ {
		m := valid[i]
		switch m.GetRole() {
		case roleAssistant:
			if len(m.GetToolCalls()) > 0 {
				amsg, err := assistantMessageWithToolUses(m)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, amsg)

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
				var tblocks []types.ContentBlock
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
					blk, err := toolResultContentBlock(id, body)
					if err != nil {
						return nil, nil, err
					}
					tblocks = append(tblocks, blk)
					i++
				}
				i--

				for id := range expected {
					if !found[id] {
						llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "injecting missing tool_result for tool_use_id %s", id)
						payload, _ := json.Marshal(map[string]any{
							"success": false,
							"error":   "Tool result was not provided for tool_use_id " + id,
						})
						blk, err := toolResultContentBlock(id, string(payload))
						if err != nil {
							return nil, nil, err
						}
						tblocks = append(tblocks, blk)
					}
				}
				if len(tblocks) > 0 {
					out = append(out, types.Message{Role: types.ConversationRoleUser, Content: tblocks})
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
			out = append(out, types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: visible}},
			})

		case roleTool:
			llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "orphaned tool message before assistant pairing; skipping")
			continue

		case roleUser:
			out = append(out, types.Message{
				Role:    types.ConversationRoleUser,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: m.GetContent()}},
			})

		default:
			return nil, nil, fmt.Errorf("unexpected role %q after filtering", m.GetRole())
		}
	}
	return system, out, nil
}
