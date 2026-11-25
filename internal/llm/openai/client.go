package openaillm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/internal/llm/tools"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/protobuf/proto"
)

// Client implements backend.Backend against the OpenAI Chat Completions API
// (and compatible servers when BaseURL is overridden).
type Client struct {
	inner          *openai.Client
	defaultModel   string
	defaultMaxToks int
	// tokenParam is the output-cap field this endpoint gets by default:
	// max_completion_tokens for the official API, max_tokens for compatible servers.
	tokenParam string
	// params remembers, per model, parameters the server rejected.
	params paramMemory
}

// New builds a client. apiKey may be empty only for local gateways that do not require auth.
func New(apiKey, baseURL, model string, maxTokens int) *Client {
	return NewWithHTTPClient(apiKey, baseURL, model, maxTokens, nil)
}

// NewWithHTTPClient is New with an optional custom HTTP client (nil keeps the
// default). Wrappers use it to add request fields and read extra response fields.
func NewWithHTTPClient(apiKey, baseURL, model string, maxTokens int, hc *http.Client) *Client {
	cfg := openai.DefaultConfig(apiKey)
	if hc != nil {
		cfg.HTTPClient = hc
	}
	if strings.TrimSpace(baseURL) != "" {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	if maxTokens <= 0 {
		maxTokens = common.DefaultMaxTokens
	}
	if strings.TrimSpace(model) == "" {
		model = openai.GPT4oMini
	}
	tokenParam := paramMaxTokens
	if isOfficialOpenAI(baseURL) {
		tokenParam = paramMaxCompletionTokens
	}
	return &Client{
		inner:          openai.NewClientWithConfig(cfg),
		defaultModel:   model,
		defaultMaxToks: maxTokens,
		tokenParam:     tokenParam,
	}
}

// relearn records the parameters err says req.Model rejects and reports whether
// any of them changes what the next request sends.
func (c *Client) relearn(req openai.ChatCompletionRequest, err error) bool {
	changed := false
	for _, p := range rejectedParams(err) {
		if c.params.learn(req.Model, p, c.tokenParam) {
			llmdlog.Stdout("openaillm", llmdlog.LevelWarn, "openai: model %q rejected parameter %q; adjusting and retrying (%v)", req.Model, p, err)
			changed = true
		}
	}
	return changed
}

// createChatCompletion sends req, and when the model rejects a parameter this
// client can adjust, remembers that and sends it again without it.
func (c *Client) createChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.inner.CreateChatCompletion(ctx, req)
		if err == nil || attempt >= maxParamRetries || !c.relearn(req, err) {
			return resp, err
		}
		c.params.apply(&req, c.tokenParam)
	}
}

// createChatCompletionStream is createChatCompletion for streams. The server
// rejects parameters before any event is sent, so a retry never repeats output.
func (c *Client) createChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionStream, error) {
	for attempt := 0; ; attempt++ {
		stream, err := c.inner.CreateChatCompletionStream(ctx, req)
		if err == nil || attempt >= maxParamRetries || !c.relearn(req, err) {
			return stream, err
		}
		c.params.apply(&req, c.tokenParam)
	}
}

func (c *Client) chatRequest(messages []openai.ChatCompletionMessage, stream bool, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (openai.ChatCompletionRequest, error) {
	req := openai.ChatCompletionRequest{
		Model:    common.PickModel(opt, c.defaultModel),
		Messages: messages,
		Stream:   stream,
	}
	if stream {
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}
	maxTok := common.PickMaxTokens(opt, c.defaultMaxToks)
	if maxTok > 0 {
		req.MaxTokens = maxTok
	}
	if v, ok := common.PickTemperature(opt); ok {
		req.Temperature = v
	}
	if v, ok := common.PickTopP(opt); ok {
		req.TopP = v
	}
	if len(tools) > 0 {
		tt, err := llmtools.ToOpenAITools(tools)
		if err != nil {
			return openai.ChatCompletionRequest{}, err
		}
		req.Tools = tt
	}
	mode, name, err := common.PickToolChoice(opt, tools)
	if err != nil {
		return openai.ChatCompletionRequest{}, err
	}
	// tool_choice is only valid on the wire when tools are present.
	// Note: the future OpenRouter provider (Story 5) gets this mapping for
	// free if it reuses this openai client and chatRequest.
	if len(tools) > 0 && opt.GetToolChoice() != nil {
		switch mode {
		case common.ToolChoiceAuto:
			req.ToolChoice = "auto"
		case common.ToolChoiceNone:
			req.ToolChoice = "none"
		case common.ToolChoiceRequired:
			req.ToolChoice = "required"
		case common.ToolChoiceNamed:
			req.ToolChoice = openai.ToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ToolFunction{Name: name},
			}
		}
	}
	if rf, ok := common.PickResponseFormat(opt); ok {
		req.ResponseFormat = openAIResponseFormat(rf)
	}
	// Puts the output cap in the field this endpoint and model accept, and leaves
	// out anything the model has rejected before.
	c.params.apply(&req, c.tokenParam)
	return req, nil
}

// openAIResponseFormat maps the normalized format onto the OpenAI request.
//
// A schema yields json_schema (the strongest guarantee OpenAI offers); without
// one we fall back to json_object, which constrains syntax but not shape.
func openAIResponseFormat(rf common.ResponseFormat) *openai.ChatCompletionResponseFormat {
	if !rf.JSON {
		return nil
	}
	if schema := rf.SchemaMarshaler(); schema != nil {
		return &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   rf.SchemaName,
				Schema: schema,
				Strict: rf.Strict,
			},
		}
	}
	return &openai.ChatCompletionResponseFormat{
		Type: openai.ChatCompletionResponseFormatTypeJSONObject,
	}
}

// Chat runs a single non-streaming chat completion.
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	msgs, err := ProtoToOpenAIMessages(messages)
	if err != nil {
		return nil, err
	}
	req, err := c.chatRequest(msgs, false, opt, tools)
	if err != nil {
		return nil, err
	}
	resp, err := c.createChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return assistantReplyFromOpenAI(ctx, resp)
}

// Generate maps system prompt + single user message to chat completions.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt must be non-empty")
	}
	var msgs []openai.ChatCompletionMessage
	if t := strings.TrimSpace(systemPrompt); t != "" {
		msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: t})
	}
	msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: prompt})
	req, err := c.chatRequest(msgs, false, opt, tools)
	if err != nil {
		return nil, err
	}
	resp, err := c.createChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return assistantReplyFromOpenAI(ctx, resp)
}

type toolAcc struct {
	id, name, args string
	started        bool
}

func toolIndex(tc openai.ToolCall) int {
	if tc.Index != nil {
		return *tc.Index
	}
	return 0
}

// ChatStream streams chat completion deltas into ConversationStreamFrame values.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	msgs, err := ProtoToOpenAIMessages(messages)
	if err != nil {
		return err
	}
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
			},
		},
	}); err != nil {
		return err
	}

	var toolSnap []*llmv1.ToolDefinition
	if tools != nil {
		toolSnap = tools()
	}
	req, err := c.chatRequest(msgs, true, opt, toolSnap)
	if err != nil {
		return err
	}
	stream, err := c.createChatCompletionStream(ctx, req)
	if err != nil {
		return err
	}
	defer stream.Close()

	var (
		fullText     strings.Builder
		fullThinking strings.Builder
		respID       string
		servedBy     string
		toolBuf      = map[int]*toolAcc{}
		lastFinish        string
		lastInToks        int64
		lastOutToks       int64
		lastCacheReadToks int64
		lastReasoningToks int64
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
	var streamEmitErr error
	emitTextDelta := func(d string) {
		if d == "" || streamEmitErr != nil || visHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(visEmitted, d)
		if reg {
			llmdlog.Stdout("openaillm", llmdlog.LevelWarn, "openai: visible stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
		}
		if div {
			llmdlog.Stdout("openaillm", llmdlog.LevelWarn, "openai: visible stream divergence (halting visible deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
			visHalted = true
			return
		}
		if wire == "" {
			visEmitted = ne
			return
		}
		streamEmitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
				AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: wire},
			},
		})
		if streamEmitErr == nil {
			fullText.WriteString(wire)
			visEmitted = ne
		}
	}
	emitThinkDelta := func(d string) {
		if d == "" || streamEmitErr != nil || thinkHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(thinkEmitted, d)
		if reg {
			llmdlog.Stdout("openaillm", llmdlog.LevelWarn, "openai: thinking stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
		}
		if div {
			llmdlog.Stdout("openaillm", llmdlog.LevelWarn, "openai: thinking stream divergence (halting thinking deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
			thinkHalted = true
			return
		}
		if wire == "" {
			thinkEmitted = ne
			return
		}
		streamEmitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{
				AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{TurnId: turnID, Delta: wire},
			},
		})
		if streamEmitErr == nil {
			fullThinking.WriteString(wire)
			thinkEmitted = ne
		}
	}
	emitActivitySnap := func(headline string) {
		h := strings.TrimSpace(headline)
		if h == "" || streamEmitErr != nil {
			return
		}
		streamEmitErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
				AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: turnID, Headline: h},
			},
		})
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if resp.ID != "" {
			respID = resp.ID
		}
		if resp.Model != "" {
			servedBy = resp.Model // concrete model (or routed upstream model on gateways)
		}
		if resp.Usage != nil {
			lastInToks = int64(resp.Usage.PromptTokens)
			lastOutToks = int64(resp.Usage.CompletionTokens)
			if resp.Usage.PromptTokensDetails != nil {
				lastCacheReadToks = int64(resp.Usage.PromptTokensDetails.CachedTokens)
			}
			if resp.Usage.CompletionTokensDetails != nil {
				lastReasoningToks = int64(resp.Usage.CompletionTokensDetails.ReasoningTokens)
			}
		}
		if len(resp.Choices) == 0 {
			continue
		}
		ch := resp.Choices[0]
		if ch.FinishReason != "" {
			lastFinish = string(ch.FinishReason)
		}
		d := ch.Delta
		if d.ReasoningContent != "" {
			reason.Feed(d.ReasoningContent, onMem, emitActivitySnap, emitThinkDelta)
			if streamEmitErr != nil {
				return streamEmitErr
			}
		}
		if d.Content != "" {
			vis.Feed(d.Content, onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
			if streamEmitErr != nil {
				return streamEmitErr
			}
		}
		for _, tc := range d.ToolCalls {
			idx := toolIndex(tc)
			acc := toolBuf[idx]
			if acc == nil {
				acc = &toolAcc{}
				toolBuf[idx] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			frag := tc.Function.Arguments
			if frag != "" {
				acc.args += frag
				idForDelta := acc.id
				if idForDelta == "" {
					idForDelta = fmt.Sprintf("idx_%d", idx)
				}
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame: &llmv1.ConversationStreamFrame_ToolCallArgsDelta{
						ToolCallArgsDelta: &llmv1.EvToolCallArgsDelta{TurnId: turnID, Id: idForDelta, ArgsDelta: frag},
					},
				}); err != nil {
					return err
				}
			}
			if !acc.started && (acc.id != "" || acc.name != "") {
				acc.started = true
				started := &llmv1.EvToolCallStreamStarted{TurnId: turnID}
				if acc.id != "" {
					started.Id = acc.id
				} else {
					started.Id = fmt.Sprintf("idx_%d", idx)
				}
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
		}
	}

	vis.Flush(onMem, emitActivitySnap, emitTextDelta, emitThinkDelta)
	reason.Flush(onMem, emitActivitySnap, emitThinkDelta)
	if streamEmitErr != nil {
		return streamEmitErr
	}

	indices := make([]int, 0, len(toolBuf))
	for k := range toolBuf {
		indices = append(indices, k)
	}
	sort.Ints(indices)
	var protoTools []*llmv1.ToolCall
	for _, idx := range indices {
		acc := toolBuf[idx]
		if acc == nil {
			continue
		}
		id := acc.id
		if id == "" {
			id = fmt.Sprintf("idx_%d", idx)
		}
		if acc.name == "" {
			continue
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
			Id:            id,
			Name:          acc.name,
			ArgumentsJson: args,
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
		InputTokens:      proto.Int64(lastInToks),
		OutputTokens:     proto.Int64(lastOutToks),
		CacheReadTokens:  proto.Int64(lastCacheReadToks),
	}
	if lastReasoningToks > 0 {
		usage.ReasoningTokens = proto.Int64(lastReasoningToks)
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
