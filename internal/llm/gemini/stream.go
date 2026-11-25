package geminillm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/llmdlog"
)

type streamTool struct {
	id, name, args string
	thoughtSig     []byte
	started        bool
}

// ChatStream streams GenerateContent deltas into ConversationStreamFrame values.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	system, contents, err := ProtoMessagesToGemini(messages)
	if err != nil {
		return err
	}
	if len(contents) == 0 {
		return fmt.Errorf("no messages to send after filtering (need at least one user/assistant turn)")
	}

	turnID, err := common.RandomTurnID()
	if err != nil {
		return err
	}

	var toolSnap []*llmv1.ToolDefinition
	if tools != nil {
		toolSnap = tools()
	}
	cfg, err := c.buildConfig(system, opt, toolSnap)
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
			},
		},
	}); err != nil {
		return err
	}

	var (
		fullText     strings.Builder
		fullThinking strings.Builder
		respID       string
		servedBy     string
		toolBuf      = map[string]*streamTool{}
		lastFinish   string
		lastInToks   int64
		lastOutToks  int64
		lastThoughts int64
		vis          assistanttext.VisiblePipeline
		reason       assistanttext.ReasoningPipeline
		visEmitted   string
		thinkEmitted string
		visHalted    bool
		thinkHalted  bool
		protoTools   []*llmv1.ToolCall
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
			llmdlog.Stdout("geminillm", llmdlog.LevelWarn, "gemini: visible stream regression; emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(delta))
		}
		if div {
			llmdlog.Stdout("geminillm", llmdlog.LevelWarn, "gemini: visible stream divergence; halting visible deltas")
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
			llmdlog.Stdout("geminillm", llmdlog.LevelWarn, "gemini: thinking stream regression")
		}
		if div {
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

	for resp, err := range c.inner.Models.GenerateContentStream(ctx, c.model(opt), contents, cfg) {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if resp == nil {
			continue
		}
		if resp.ResponseID != "" {
			respID = resp.ResponseID
		}
		if resp.ModelVersion != "" {
			servedBy = resp.ModelVersion
		}
		if resp.UsageMetadata != nil {
			lastInToks = int64(resp.UsageMetadata.PromptTokenCount)
			lastOutToks = int64(resp.UsageMetadata.CandidatesTokenCount)
			lastThoughts = int64(resp.UsageMetadata.ThoughtsTokenCount)
		}
		if len(resp.Candidates) > 0 {
			lastFinish = string(resp.Candidates[0].FinishReason)
			cand := resp.Candidates[0].Content
			if cand == nil {
				continue
			}
			for _, part := range cand.Parts {
				if part == nil {
					continue
				}
				if part.Thought && part.Text != "" {
					reason.Feed(part.Text, onMem, emitActivitySnap, emitThinkDelta)
					if emitErr != nil {
						return emitErr
					}
					continue
				}
				if part.Text != "" {
					vis.Feed(part.Text, onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
					if emitErr != nil {
						return emitErr
					}
				}
				if fc := part.FunctionCall; fc != nil {
					key := fc.ID
					if key == "" {
						key = fc.Name
					}
					acc := toolBuf[key]
					if acc == nil {
						acc = &streamTool{}
						toolBuf[key] = acc
					}
					if fc.ID != "" {
						acc.id = fc.ID
					}
					if fc.Name != "" {
						acc.name = fc.Name
					}
					if len(fc.Args) > 0 {
						b, _ := json.Marshal(fc.Args)
						acc.args = string(b)
					}
					if len(part.ThoughtSignature) > 0 {
						acc.thoughtSig = cloneThoughtSignature(part.ThoughtSignature)
					}
					if !acc.started && (acc.id != "" || acc.name != "") {
						acc.started = true
						id := acc.id
						if id == "" {
							id = acc.name
						}
						started := &llmv1.EvToolCallStreamStarted{TurnId: turnID, Id: id}
						if acc.name != "" {
							started.Name = proto.String(acc.name)
						}
						if err := emit(&llmv1.ConversationStreamFrame{
							SchemaVersion: conversationevents.SchemaVersion,
							Frame:         &llmv1.ConversationStreamFrame_ToolCallStreamStarted{ToolCallStreamStarted: started},
						}); err != nil {
							return err
						}
					}
					if acc.args != "" {
						id := acc.id
						if id == "" {
							id = acc.name
						}
						if err := emit(&llmv1.ConversationStreamFrame{
							SchemaVersion: conversationevents.SchemaVersion,
							Frame: &llmv1.ConversationStreamFrame_ToolCallArgsDelta{
								ToolCallArgsDelta: &llmv1.EvToolCallArgsDelta{TurnId: turnID, Id: id, ArgsDelta: acc.args},
							},
						}); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	vis.Flush(onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
	reason.Flush(onMem, emitActivitySnap, emitThinkDelta)
	if emitErr != nil {
		return emitErr
	}

	for _, acc := range toolBuf {
		if acc == nil || acc.name == "" {
			continue
		}
		id := acc.id
		if id == "" {
			id = acc.name
		}
		args := strings.TrimSpace(acc.args)
		if args == "" {
			args = "{}"
		}
		if err := json.Unmarshal([]byte(args), new(map[string]any)); err != nil {
			return fmt.Errorf("tool %q arguments_json: %w", acc.name, err)
		}
		if err := emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{
				ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{
					TurnId:        turnID,
					Id:            id,
					Name:          acc.name,
					ArgumentsJson: args,
				},
			},
		}); err != nil {
			return err
		}
		protoTools = append(protoTools, &llmv1.ToolCall{
			Id:               id,
			Name:             acc.name,
			ArgumentsJson:    args,
			ThoughtSignature: cloneThoughtSignature(acc.thoughtSig),
		})
	}

	reply := &llmv1.AssistantReply{
		Content:         fullText.String(),
		ThinkingContent: fullThinking.String(),
		Id:              respID,
		FinishReason:    lastFinish,
		ToolCalls:       protoTools,
		ServedBy:        servedBy,
	}
	usage := &llmv1.TokenUsage{
		InputTokens:  proto.Int64(lastInToks),
		OutputTokens: proto.Int64(lastOutToks),
	}
	if lastThoughts > 0 {
		usage.ReasoningTokens = proto.Int64(lastThoughts)
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId:             turnID,
				AssistantMessageId: proto.String(respID),
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
