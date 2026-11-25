// Package chatdebug records the actual conversation llmd exchanges with an
// LLM provider: the prompts and payloads going out, and the reply coming
// back.
//
// It wraps backend.Backend — the one seam every provider client implements
// and internal/server resolves through — so capture is provider-agnostic and
// lives in exactly one place. Wrapping here rather than inside each provider
// client means Anthropic, OpenAI, Bedrock, Gemini, Ollama, Claude Code and
// Cursor all record identically, and a new provider gets it for free.
//
// What is recorded is the payload as llmd hands it to the provider: the full
// message list (system prompt included, since that is just the role=system
// message), the tool definitions advertised for the turn, the call options,
// and the assembled reply. It is deliberately NOT the provider's own wire
// format — that is per-provider HTTP traffic, and lives behind
// LLMD_DEBUG_TRAFFIC.
//
// Nothing here is truncated. This file exists to answer "what exactly did we
// send?", and a clipped prompt cannot answer it.
package chatdebug

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/llmdlog"
)

const module = "chat"

// Meta identifies the conversation a recorded exchange belongs to, so records
// from concurrent sessions can be told apart in one NDJSON file.
type Meta struct {
	ProviderID string
	AgentID    string
	SessionID  string
}

// Wrap returns be decorated with chat capture. It returns be unchanged when
// chat debugging is off, so the non-debug path carries no wrapper at all —
// not even a nil check per call.
func Wrap(be backend.Backend, meta Meta) backend.Backend {
	if be == nil || !llmdlog.ChatDebugEnabled() {
		return be
	}
	return &recorder{inner: be, meta: meta}
}

type recorder struct {
	inner backend.Backend
	meta  Meta
}

func (r *recorder) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	r.request("chat_request", "Chat", messages, opt, tools)
	reply, err := r.inner.Chat(ctx, messages, opt, tools)
	r.reply("Chat", reply, err)
	return reply, err
}

func (r *recorder) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	// Generate's two strings are the same conversation in a flattened shape;
	// record them as the messages they stand for so one reader handles both.
	r.request("chat_request", "Generate", []*llmv1.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}, opt, tools)
	reply, err := r.inner.Generate(ctx, systemPrompt, prompt, opt, tools)
	r.reply("Generate", reply, err)
	return reply, err
}

func (r *recorder) GenerateParts(ctx context.Context, systemPrompt string, parts []*llmv1.PromptPart, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	r.request("chat_request", "Generate", []*llmv1.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: backend.JoinPromptParts(parts)},
	}, opt, tools)
	reply, err := backend.GenerateWithParts(ctx, r.inner, systemPrompt, parts, opt, tools)
	r.reply("Generate", reply, err)
	return reply, err
}

func (r *recorder) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	// tools is resolved at send-time by the provider; resolve it here too so
	// the record shows the surface the provider actually saw, not a snapshot
	// taken earlier.
	var defs []*llmv1.ToolDefinition
	if tools != nil {
		defs = tools()
	}
	r.request("chat_request", "ChatStream", messages, opt, defs)

	wrapped := func(f *llmv1.ConversationStreamFrame) error {
		// stream_complete carries the final assembled AssistantReply, so the
		// response is recorded once, whole — not reassembled from deltas.
		if reply := f.GetStreamComplete(); reply != nil {
			r.reply("ChatStream", reply, nil)
		}
		return emit(f)
	}
	err := r.inner.ChatStream(ctx, messages, opt, tools, wrapped)
	if err != nil {
		r.fields("chat_error", map[string]any{"call": "ChatStream", "error": err.Error()},
			"ChatStream failed: %v", err)
	}
	return err
}

// request records one outbound payload.
func (r *recorder) request(event, call string, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) {
	fields := r.base()
	fields["call"] = call
	fields["model"] = opt.GetModel()
	fields["options"] = callOptions(opt)
	fields["messages"] = describeMessages(messages)
	fields["message_count"] = len(messages)
	fields["tools"] = describeTools(tools)
	fields["tool_count"] = len(tools)
	llmdlog.ChatFields(module, event, fields,
		"%s → %s model=%q messages=%d tools=%d",
		call, r.meta.ProviderID, opt.GetModel(), len(messages), len(tools))
}

// reply records one inbound assistant turn.
func (r *recorder) reply(call string, reply *llmv1.AssistantReply, err error) {
	if err != nil {
		r.fields("chat_error", map[string]any{"call": call, "error": err.Error()},
			"%s failed: %v", call, err)
		return
	}
	if reply == nil {
		return
	}
	fields := r.base()
	fields["call"] = call
	fields["content"] = reply.GetContent()
	fields["finish_reason"] = reply.GetFinishReason()
	if t := reply.GetThinkingContent(); t != "" {
		fields["thinking_content"] = t
	}
	if h := reply.GetActivityHeadline(); h != "" {
		fields["activity_headline"] = h
	}
	if calls := describeToolCalls(reply.GetToolCalls()); len(calls) > 0 {
		fields["tool_calls"] = calls
	}
	if u := reply.GetUsage(); u != nil {
		fields["usage"] = describeUsage(u)
	}
	llmdlog.ChatFields(module, "chat_response", fields,
		"%s ← %s finish=%q content_bytes=%d tool_calls=%d",
		call, r.meta.ProviderID, reply.GetFinishReason(), len(reply.GetContent()), len(reply.GetToolCalls()))
}

func (r *recorder) fields(event string, extra map[string]any, format string, args ...any) {
	fields := r.base()
	for k, v := range extra {
		fields[k] = v
	}
	llmdlog.ChatFields(module, event, fields, format, args...)
}

func (r *recorder) base() map[string]any {
	f := map[string]any{}
	if r.meta.ProviderID != "" {
		f["provider_id"] = r.meta.ProviderID
	}
	if r.meta.AgentID != "" {
		f["agent_id"] = r.meta.AgentID
	}
	if r.meta.SessionID != "" {
		f["session_id"] = r.meta.SessionID
	}
	return f
}

// describeMessages renders the outbound conversation. Every field that
// reaches the provider is included — including attachment content, which is
// spliced into the outbound message at send-time and is otherwise invisible
// in the persisted message row.
func describeMessages(messages []*llmv1.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		e := map[string]any{
			"role":    m.GetRole(),
			"content": m.GetContent(),
		}
		if id := m.GetId(); id != "" {
			e["id"] = id
		}
		if t := m.GetThinkingContent(); t != "" {
			e["thinking_content"] = t
		}
		if id := m.GetToolCallId(); id != "" {
			e["tool_call_id"] = id
		}
		if calls := describeToolCalls(m.GetToolCalls()); len(calls) > 0 {
			e["tool_calls"] = calls
		}
		if fr := m.GetFinishReason(); fr != "" {
			e["finish_reason"] = fr
		}
		var attachments []map[string]any
		for _, a := range m.GetAttachments() {
			if a == nil || a.GetContent() == "" {
				// payload_json is client display data and never reaches a
				// provider: recording it here would misrepresent the prompt.
				continue
			}
			attachments = append(attachments, map[string]any{
				"id":      a.GetId(),
				"kind":    a.GetKind(),
				"content": a.GetContent(),
			})
		}
		if len(attachments) > 0 {
			e["attachments"] = attachments
		}
		out = append(out, e)
	}
	return out
}

func describeToolCalls(calls []*llmv1.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		if c == nil {
			continue
		}
		out = append(out, map[string]any{
			"id":             c.GetId(),
			"name":           c.GetName(),
			"arguments_json": c.GetArgumentsJson(),
		})
	}
	return out
}

// describeTools records the advertised tool surface with full schemas: "why
// did the model call it that way" is usually a question about the schema it
// was shown.
func describeTools(tools []*llmv1.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		e := map[string]any{"name": t.GetName()}
		if d := t.GetDescription(); d != "" {
			e["description"] = d
		}
		if s := t.GetParametersSchemaJson(); s != "" {
			e["parameters_schema_json"] = s
		}
		out = append(out, e)
	}
	return out
}

func callOptions(opt *llmv1.CallOptions) map[string]any {
	if opt == nil {
		return nil
	}
	f := map[string]any{
		"model":      opt.GetModel(),
		"max_tokens": opt.GetMaxTokens(),
	}
	if opt.Temperature != nil {
		f["temperature"] = opt.GetTemperature()
	}
	if opt.TopP != nil {
		f["top_p"] = opt.GetTopP()
	}
	if opt.TopK != nil {
		f["top_k"] = opt.GetTopK()
	}
	if opt.MaxToolRounds != nil {
		f["max_tool_rounds"] = opt.GetMaxToolRounds()
	}
	if opt.ReadOnly != nil {
		f["read_only"] = opt.GetReadOnly()
	}
	return f
}

func describeUsage(u *llmv1.TokenUsage) map[string]any {
	return map[string]any{
		"input_tokens":  u.GetInputTokens(),
		"output_tokens": u.GetOutputTokens(),
	}
}
