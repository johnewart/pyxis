package bedrockllm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
)

type streamBlock struct {
	kind        string
	id          string
	name        string
	partialJSON string
}

// normalizeStreamedToolArgumentsJSON returns valid JSON object text for tool input.
// Bedrock can end a tool-use block with truncated or empty streamed JSON; failing the
// whole ChatStream was too brittle (e.g. "unexpected end of JSON input").
func normalizeStreamedToolArgumentsJSON(toolName, raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		preview := s
		if len(preview) > 240 {
			preview = preview[:240] + "…"
		}
		llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "tool %q invalid streamed arguments JSON (%q): %v; using {}", toolName, preview, err)
		return "{}"
	}
	if _, ok := v.(map[string]any); !ok {
		llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "tool %q streamed arguments JSON is not an object; using {}", toolName)
		return "{}"
	}
	return s
}

// ChatStream runs ConverseStream and maps events to ConversationStreamFrame.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, toolsFn func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("bedrock: emit callback is required")
	}
	var tools []*llmv1.ToolDefinition
	if toolsFn != nil {
		tools = toolsFn()
	}
	in, err := c.buildConverseInput(messages, opt, tools)
	if err != nil {
		return err
	}
	streamIn := &bedrockruntime.ConverseStreamInput{
		ModelId:         in.ModelId,
		Messages:        in.Messages,
		System:          in.System,
		InferenceConfig: in.InferenceConfig,
		ToolConfig:      in.ToolConfig,
	}
	out, err := c.rt.ConverseStream(ctx, streamIn)
	if err != nil {
		return err
	}
	stream := out.GetStream()
	defer stream.Close()

	turnID, err := common.RandomTurnID()
	if err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{
				TurnId:             turnID,
				ThinkingEnabled:    proto.Bool(true),
				StreamingToolCalls: proto.Bool(true),
				ParentTurnGroupId:  nil,
			},
		},
	}); err != nil {
		return err
	}

	blocks := map[int32]*streamBlock{}
	var (
		fullText     strings.Builder
		fullThinking strings.Builder
		toolCalls    []*llmv1.ToolCall
		lastFinish   string
		lastInToks   int64
		lastOutToks  int64
		vis          assistanttext.VisiblePipeline
		reason       assistanttext.ReasoningPipeline
		visEmitted   string
		thinkEmitted string
		visHalted    bool
		thinkHalted  bool
	)
	onMem := func(t string) {
		if h := memctx.AssistantMemoryHook(ctx); h != nil {
			h(t)
		}
	}
	var emitErr error
	emitTextDelta := func(delta string) {
		if delta == "" || emitErr != nil || visHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(visEmitted, delta)
		if reg {
			llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "bedrock: visible stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(delta))
		}
		if div {
			llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "bedrock: visible stream divergence (halting visible deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(delta))
			visHalted = true
			return
		}
		if wire == "" {
			visEmitted = ne
			return
		}
		emitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
				AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: wire},
			},
		})
		if emitErr == nil {
			fullText.WriteString(wire)
			visEmitted = ne
		}
	}
	emitThinkDelta := func(delta string) {
		if delta == "" || emitErr != nil || thinkHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(thinkEmitted, delta)
		if reg {
			llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "bedrock: thinking stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(delta))
		}
		if div {
			llmdlog.Stdout("bedrockllm", llmdlog.LevelWarn, "bedrock: thinking stream divergence (halting thinking deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(delta))
			thinkHalted = true
			return
		}
		if wire == "" {
			thinkEmitted = ne
			return
		}
		emitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{
				AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{TurnId: turnID, Delta: wire},
			},
		})
		if emitErr == nil {
			fullThinking.WriteString(wire)
			thinkEmitted = ne
		}
	}
	emitActivitySnap := func(headline string) {
		h := strings.TrimSpace(headline)
		if h == "" || emitErr != nil {
			return
		}
		emitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
				AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: turnID, Headline: h},
			},
		})
	}

	for ev := range stream.Events() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if emitErr != nil {
			return emitErr
		}
		switch v := ev.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			cse := v.Value
			idx := int32(0)
			if cse.ContentBlockIndex != nil {
				idx = *cse.ContentBlockIndex
			}
			switch st := cse.Start.(type) {
			case *types.ContentBlockStartMemberToolUse:
				b := &streamBlock{
					kind: "tool_use",
					id:   deref(st.Value.ToolUseId),
					name: deref(st.Value.Name),
				}
				blocks[idx] = b
				started := &llmv1.EvToolCallStreamStarted{TurnId: turnID, Id: b.id}
				if b.name != "" {
					started.Name = proto.String(b.name)
				}
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolCallStreamStarted{ToolCallStreamStarted: started},
				}); err != nil {
					return err
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			cde := v.Value
			idx := int32(0)
			if cde.ContentBlockIndex != nil {
				idx = *cde.ContentBlockIndex
			}
			b := blocks[idx]
			switch d := cde.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				if d.Value != "" {
					vis.Feed(d.Value, onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
					if emitErr != nil {
						return emitErr
					}
				}
			case *types.ContentBlockDeltaMemberReasoningContent:
				switch rd := d.Value.(type) {
				case *types.ReasoningContentBlockDeltaMemberText:
					if rd.Value != "" {
						reason.Feed(rd.Value, onMem, emitActivitySnap, emitThinkDelta)
						if emitErr != nil {
							return emitErr
						}
					}
				}
			case *types.ContentBlockDeltaMemberToolUse:
				if b == nil || b.kind != "tool_use" {
					continue
				}
				frag := ""
				if d.Value.Input != nil {
					frag = *d.Value.Input
				}
				b.partialJSON += frag
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame: &llmv1.ConversationStreamFrame_ToolCallArgsDelta{
						ToolCallArgsDelta: &llmv1.EvToolCallArgsDelta{
							TurnId:    turnID,
							Id:        b.id,
							ArgsDelta: frag,
						},
					},
				}); err != nil {
					return err
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			cst := v.Value
			idx := int32(0)
			if cst.ContentBlockIndex != nil {
				idx = *cst.ContentBlockIndex
			}
			b := blocks[idx]
			if b != nil && b.kind == "tool_use" {
				args := normalizeStreamedToolArgumentsJSON(b.name, b.partialJSON)
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{
						ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{
							TurnId:        turnID,
							Id:            b.id,
							Name:          b.name,
							ArgumentsJson: args,
						},
					},
				}); err != nil {
					return err
				}
				toolCalls = append(toolCalls, &llmv1.ToolCall{
					Id:            b.id,
					Name:          b.name,
					ArgumentsJson: args,
				})
			}
			delete(blocks, idx)

		case *types.ConverseStreamOutputMemberMessageStop:
			lastFinish = string(v.Value.StopReason)

		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				if v.Value.Usage.InputTokens != nil {
					lastInToks = int64(*v.Value.Usage.InputTokens)
				}
				if v.Value.Usage.OutputTokens != nil {
					lastOutToks = int64(*v.Value.Usage.OutputTokens)
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	if emitErr != nil {
		return emitErr
	}

	vis.Flush(onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
	reason.Flush(onMem, emitActivitySnap, emitThinkDelta)
	if emitErr != nil {
		return emitErr
	}

	usage := &llmv1.TokenUsage{
		InputTokens:  proto.Int64(lastInToks),
		OutputTokens: proto.Int64(lastOutToks),
	}
	msgID := "bedrock"
	reply := &llmv1.AssistantReply{
		Content:         fullText.String(),
		ThinkingContent: fullThinking.String(),
		Id:              msgID,
		FinishReason:    lastFinish,
		ToolCalls:       toolCalls,
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId:             turnID,
				AssistantMessageId: proto.String(msgID),
				FinishReason:       proto.String(lastFinish),
				Usage:              usage,
			},
		},
	}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply},
	})
}
