package ollamallm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// Client implements backend.Backend against Ollama's HTTP /api/chat endpoint.
type Client struct {
	baseURL        string
	httpClient     *http.Client
	defaultModel   string
	defaultMaxToks int
}

// New builds a client. baseURL is the Ollama native API root, used verbatim
// (e.g. http://127.0.0.1:11434); only surrounding space and a trailing slash
// are removed. Callers are responsible for configuring a correct URL.
func New(baseURL, model string, maxTokens int) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	if maxTokens <= 0 {
		maxTokens = common.DefaultMaxTokens
	}
	if strings.TrimSpace(model) == "" {
		model = "llama3.2"
	}
	return &Client{
		baseURL:        baseURL,
		httpClient:     http.DefaultClient,
		defaultModel:   model,
		defaultMaxToks: maxTokens,
	}
}

type chatAPIRequest struct {
	Model    string                    `json:"model"`
	Messages []ollamaChatMessage       `json:"messages"`
	Stream   bool                      `json:"stream"`
	Options  map[string]any            `json:"options,omitempty"`
	Tools    []llmtools.OllamaWireTool `json:"tools,omitempty"`
	// Ollama's structured-output field: either the string "json" or a JSON
	// Schema object. Raw so both shapes go on the wire unchanged.
	Format json.RawMessage `json:"format,omitempty"`
}

type chatAPIResponse struct {
	Model           string            `json:"model"`
	CreatedAt       string            `json:"created_at"`
	Message         ollamaChatMessage `json:"message"`
	Done            bool              `json:"done"`
	PromptEvalCount int               `json:"prompt_eval_count"`
	EvalCount       int               `json:"eval_count"`
}

type chatAPIStreamLine struct {
	Model           string             `json:"model"`
	CreatedAt       string             `json:"created_at"`
	Message         *ollamaChatMessage `json:"message,omitempty"`
	Done            bool               `json:"done"`
	PromptEvalCount int                `json:"prompt_eval_count"`
	EvalCount       int                `json:"eval_count"`
}

func (c *Client) buildOptions(opt *llmv1.CallOptions) map[string]any {
	o := map[string]any{}
	max := common.PickMaxTokens(opt, c.defaultMaxToks)
	if max > 0 {
		o["num_predict"] = max
	}
	if t, ok := common.PickTemperature(opt); ok {
		o["temperature"] = t
	}
	if p, ok := common.PickTopP(opt); ok {
		o["top_p"] = p
	}
	if len(o) == 0 {
		return nil
	}
	return o
}

// buildFormat maps structured-output requests onto Ollama's `format` field.
// Nil (omitted) when the caller asked for nothing, so ordinary turns are
// unchanged on the wire.
func (c *Client) buildFormat(opt *llmv1.CallOptions) json.RawMessage {
	rf, ok := common.PickResponseFormat(opt)
	if !ok || !rf.JSON {
		return nil
	}
	if len(rf.Schema) > 0 {
		return rf.Schema
	}
	// No usable schema: ask for syntactically valid JSON, shape unconstrained.
	return json.RawMessage(`"json"`)
}

func (c *Client) postChat(ctx context.Context, reqBody chatAPIRequest) (*http.Response, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ollama: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// Chat runs a single non-streaming /api/chat call.
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	msgs, err := ProtoToOllamaMessages(messages)
	if err != nil {
		return nil, err
	}
	var ot []llmtools.OllamaWireTool
	if len(tools) > 0 {
		ot, err = llmtools.ToOllamaTools(tools)
		if err != nil {
			return nil, err
		}
	}
	reqBody := chatAPIRequest{
		Model:    common.PickModel(opt, c.defaultModel),
		Messages: msgs,
		Stream:   false,
		Options:  c.buildOptions(opt),
		Tools:    ot,
		Format:   c.buildFormat(opt),
	}
	resp, err := c.postChat(ctx, reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out chatAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	id := out.CreatedAt
	if id == "" {
		id = out.Model
	}
	return assistantReplyFromOllamaMessage(ctx, id, out.Message, "stop")
}

// Generate maps system + user to a chat request.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt must be non-empty")
	}
	var msgs []ollamaChatMessage
	if t := strings.TrimSpace(systemPrompt); t != "" {
		msgs = append(msgs, ollamaChatMessage{Role: "system", Content: t})
	}
	msgs = append(msgs, ollamaChatMessage{Role: "user", Content: prompt})
	var ot []llmtools.OllamaWireTool
	if len(tools) > 0 {
		var err error
		ot, err = llmtools.ToOllamaTools(tools)
		if err != nil {
			return nil, err
		}
	}
	reqBody := chatAPIRequest{
		Model:    common.PickModel(opt, c.defaultModel),
		Messages: msgs,
		Stream:   false,
		Options:  c.buildOptions(opt),
		Tools:    ot,
		Format:   c.buildFormat(opt),
	}
	resp, err := c.postChat(ctx, reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out chatAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	id := out.CreatedAt
	if id == "" {
		id = out.Model
	}
	return assistantReplyFromOllamaMessage(ctx, id, out.Message, "stop")
}

// ChatStream streams /api/chat with stream=true and maps deltas to ConversationStreamFrame.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	msgs, err := ProtoToOllamaMessages(messages)
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

	var ot []llmtools.OllamaWireTool
	if tools != nil {
		toolSnap := tools()
		if len(toolSnap) > 0 {
			var convErr error
			ot, convErr = llmtools.ToOllamaTools(toolSnap)
			if convErr != nil {
				return convErr
			}
		}
	}
	reqBody := chatAPIRequest{
		Model:    common.PickModel(opt, c.defaultModel),
		Messages: msgs,
		Stream:   true,
		Options:  c.buildOptions(opt),
		Tools:    ot,
		Format:   c.buildFormat(opt),
	}
	resp, err := c.postChat(ctx, reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	const maxScan = 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScan)

	var (
		fullText     strings.Builder
		fullThinking strings.Builder
		visPipe      assistanttext.VisiblePipeline
		respID       string
		lastFinish   = "stop"
		sawDone      bool
		lastInToks   int64
		lastOutToks  int64
		finalMsg     *ollamaChatMessage
		visEmitted   string
		thinkEmitted string
		visHalted    bool
		thinkHalted  bool
	)

	var emitErr error
	emitTextDelta := func(d string) {
		if d == "" || emitErr != nil || visHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(visEmitted, d)
		if reg {
			llmdlog.Stdout("ollamallm", llmdlog.LevelWarn, "ollama: visible stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
		}
		if div {
			llmdlog.Stdout("ollamallm", llmdlog.LevelWarn, "ollama: visible stream divergence (halting visible deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
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
	emitThinkDelta := func(d string) {
		if d == "" || emitErr != nil || thinkHalted {
			return
		}
		ne, wire, div, reg := suffixwire.Compute(thinkEmitted, d)
		if reg {
			llmdlog.Stdout("ollamallm", llmdlog.LevelWarn, "ollama: thinking stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
		}
		if div {
			llmdlog.Stdout("ollamallm", llmdlog.LevelWarn, "ollama: thinking stream divergence (halting thinking deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
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

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk chatAPIStreamLine
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return fmt.Errorf("ollama stream line: %w", err)
		}
		if chunk.CreatedAt != "" {
			respID = chunk.CreatedAt
		}
		if chunk.Message != nil && chunk.Message.Content != "" {
			visPipe.Feed(chunk.Message.Content, memctx.AssistantMemoryHook(ctx), emitActivitySnap, emitTextDelta, emitThinkDelta)
			if emitErr != nil {
				return emitErr
			}
		}
		if chunk.Done {
			sawDone = true
			lastInToks = int64(chunk.PromptEvalCount)
			lastOutToks = int64(chunk.EvalCount)
			if chunk.Message != nil {
				finalMsg = chunk.Message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// A body that ends without done:true was cut off (dropped connection, server
	// crash). Fail the turn instead of saving the partial text as a complete reply.
	if !sawDone {
		return fmt.Errorf("ollama: stream ended before done (response truncated; got %d chars)", fullText.Len())
	}
	visPipe.Flush(memctx.AssistantMemoryHook(ctx), emitActivitySnap, emitTextDelta, emitThinkDelta)
	if emitErr != nil {
		return emitErr
	}

	var protoTools []*llmv1.ToolCall
	if finalMsg != nil && len(finalMsg.ToolCalls) > 0 {
		for _, tc := range finalMsg.ToolCalls {
			argsJSON := "{}"
			if tc.Function.Arguments != nil {
				b, err := json.Marshal(tc.Function.Arguments)
				if err != nil {
					return fmt.Errorf("tool %q arguments: %w", tc.Function.Name, err)
				}
				argsJSON = string(b)
			}
			started := &llmv1.EvToolCallStreamStarted{TurnId: turnID, Id: tc.ID}
			if tc.Function.Name != "" {
				started.Name = proto.String(tc.Function.Name)
			}
			if err := emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_ToolCallStreamStarted{
					ToolCallStreamStarted: started,
				},
			}); err != nil {
				return err
			}
			if err := emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{
					ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{
						TurnId:        turnID,
						Id:            tc.ID,
						Name:          tc.Function.Name,
						ArgumentsJson: argsJSON,
					},
				},
			}); err != nil {
				return err
			}
			protoTools = append(protoTools, &llmv1.ToolCall{
				Id:            tc.ID,
				Name:          tc.Function.Name,
				ArgumentsJson: argsJSON,
			})
		}
	}

	content := fullText.String()
	thinking := fullThinking.String()
	if strings.TrimSpace(content) == "" && strings.TrimSpace(thinking) == "" && finalMsg != nil && strings.TrimSpace(finalMsg.Content) != "" {
		content, thinking = assistanttext.ProcessUnary(finalMsg.Content, "", memctx.AssistantMemoryHook(ctx))
	}
	if respID == "" {
		respID = common.PickModel(opt, c.defaultModel)
	}

	reply := &llmv1.AssistantReply{
		Content:         content,
		ThinkingContent: thinking,
		Id:              respID,
		FinishReason:    lastFinish,
		ToolCalls:       protoTools,
	}
	usage := &llmv1.TokenUsage{
		InputTokens:  proto.Int64(lastInToks),
		OutputTokens: proto.Int64(lastOutToks),
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
