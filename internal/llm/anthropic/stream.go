package anthropicllm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/internal/llm/tools"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
)

type streamBlock struct {
	kind        string
	id          string
	name        string
	partialJSON string
}

// ChatStream performs a Messages streaming call and maps SSE events to ConversationStreamFrame
// (thinking/text/tool arg streaming + terminal turn metadata), then emits stream_complete.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	system, msgParams, err := ProtoMessagesToParams(messages, opt)
	if err != nil {
		return err
	}
	if len(msgParams) == 0 {
		return fmt.Errorf("no messages to send after filtering (need at least one user/assistant turn)")
	}

	turnID, err := common.RandomTurnID()
	if err != nil {
		return err
	}

	body := anthropic.MessageNewParams{
		Model:     c.defaultModel,
		MaxTokens: c.defaultMaxToks,
		Messages:  msgParams,
		System:    system,
	}
	body = c.applyCallOptions(body, opt)
	var toolSnap []*llmv1.ToolDefinition
	if tools != nil {
		toolSnap = tools()
	}
	if len(toolSnap) > 0 {
		at, err := llmtools.ToAnthropicTools(toolSnap)
		if err != nil {
			return err
		}
		body.Tools = at
	}
	body, err = applyToolChoice(body, opt, toolSnap)
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

	s := c.inner.Messages.NewStreaming(ctx, body)
	defer s.Close()

	blocks := map[int64]*streamBlock{}
	var (
		assistantMsgID string
		servedBy       string
		fullText       strings.Builder
		fullThinking   strings.Builder
		toolCalls      []*llmv1.ToolCall
		lastFinish         string
		lastInToks         int64
		lastOutToks        int64
		lastCacheReadToks  int64
		lastCacheWriteToks int64
		vis            assistanttext.VisiblePipeline
		reason         assistanttext.ReasoningPipeline
		visEmitted     string
		thinkEmitted   string
		visHalted      bool
		thinkHalted    bool
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
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "anthropic: visible stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(delta))
		}
		if div {
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "anthropic: visible stream divergence (halting visible deltas for remainder of turn); emitted=%s chunk=%s",
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
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "anthropic: thinking stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(delta))
		}
		if div {
			llmdlog.Stdout("anthropicllm", llmdlog.LevelWarn, "anthropic: thinking stream divergence (halting thinking deltas for remainder of turn); emitted=%s chunk=%s",
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

	for s.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		u := s.Current()
		switch u.Type {
		case "message_start":
			ms := u.AsMessageStart()
			assistantMsgID = ms.Message.ID
			servedBy = string(ms.Message.Model)
			lastCacheReadToks = ms.Message.Usage.CacheReadInputTokens
			lastCacheWriteToks = ms.Message.Usage.CacheCreationInputTokens

		case "content_block_start":
			cs := u.AsContentBlockStart()
			idx := cs.Index
			cb := cs.ContentBlock
			b := &streamBlock{}
			switch cb.Type {
			case "tool_use":
				b.kind = "tool_use"
				b.id = cb.ID
				b.name = cb.Name
				started := &llmv1.EvToolCallStreamStarted{TurnId: turnID, Id: b.id}
				if b.name != "" {
					started.Name = proto.String(b.name)
				}
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame: &llmv1.ConversationStreamFrame_ToolCallStreamStarted{
						ToolCallStreamStarted: started,
					},
				}); err != nil {
					return err
				}
			case "thinking":
				b.kind = "thinking"
			case "text":
				b.kind = "text"
			default:
				b.kind = cb.Type
			}
			blocks[idx] = b

		case "content_block_delta":
			cd := u.AsContentBlockDelta()
			idx := cd.Index
			b := blocks[idx]
			if b == nil {
				continue
			}
			d := cd.Delta
			switch d.Type {
			case "text_delta":
				td := d.AsTextDelta()
				delta := td.Text
				if delta != "" {
					vis.Feed(delta, onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
					if emitErr != nil {
						return emitErr
					}
				}
			case "thinking_delta":
				th := d.AsThinkingDelta()
				delta := th.Thinking
				if delta != "" {
					reason.Feed(delta, onMem, emitActivitySnap, emitThinkDelta)
					if emitErr != nil {
						return emitErr
					}
				}
			case "input_json_delta":
				jd := d.AsInputJSONDelta()
				if b.kind == "tool_use" {
					frag := jd.PartialJSON
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
			}

		case "content_block_stop":
			cs := u.AsContentBlockStop()
			idx := cs.Index
			b := blocks[idx]
			if b != nil && b.kind == "tool_use" {
				args := strings.TrimSpace(b.partialJSON)
				if args == "" {
					args = "{}"
				}
				if err := json.Unmarshal([]byte(args), new(map[string]any)); err != nil {
					return fmt.Errorf("tool %q arguments_json: %w", b.name, err)
				}
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

		case "message_delta":
			md := u.AsMessageDelta()
			lastFinish = string(md.Delta.StopReason)
			lastInToks = md.Usage.InputTokens
			lastOutToks = md.Usage.OutputTokens

		case "message_stop":
			// terminal handled after loop
		}
	}
	if err := s.Err(); err != nil {
		return err
	}

	vis.Flush(onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
	reason.Flush(onMem, emitActivitySnap, emitThinkDelta)
	if emitErr != nil {
		return emitErr
	}

	usage := &llmv1.TokenUsage{
		InputTokens:       proto.Int64(lastInToks),
		OutputTokens:      proto.Int64(lastOutToks),
		CacheReadTokens:   proto.Int64(lastCacheReadToks),
		CacheWriteTokens:  proto.Int64(lastCacheWriteToks),
	}

	reply := &llmv1.AssistantReply{
		Content:         fullText.String(),
		ThinkingContent: fullThinking.String(),
		Id:              assistantMsgID,
		FinishReason:    lastFinish,
		ToolCalls:       toolCalls,
		// message_start reports the concrete model that served the request.
		// Anthropic reports no separate reasoning-token count (thinking tokens
		// are included in output_tokens), so ReasoningTokens stays unset.
		ServedBy: servedBy,
	}

	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId:             turnID,
				AssistantMessageId: proto.String(assistantMsgID),
				FinishReason:       proto.String(lastFinish),
				Usage:              usage,
			},
		},
	}); err != nil {
		return err
	}

	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: reply,
		},
	})
}
