// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Turn is one scripted response consumed in order, one per incoming
// /chat/completions request. Build one with TextTurn, ToolCallTurn,
// ToolCallsTurn, ErrorTurn, SlowTextTurn or HangTurn; add token usage with
// WithUsage.
type Turn struct {
	kind         string
	deltas       []string
	toolCallID   string
	toolName     string
	toolArgsJSON string
	delay        time.Duration
	toolCalls    []ToolCall
	usage        *turnUsage
	errStatus    int
	errMessage   string
}

// turnUsage is the token usage reported with a scripted reply.
type turnUsage struct {
	prompt, completion, reasoning int
}

// ToolCall is one function call emitted by ToolCallsTurn.
type ToolCall struct {
	ID            string
	Name          string
	ArgumentsJSON string
}

// TextTurn streams the given content deltas as a single assistant message,
// then finishes with finish_reason=stop.
func TextTurn(deltas ...string) Turn {
	return Turn{kind: "text", deltas: deltas}
}

// SlowTextTurn is like TextTurn but paces each delta by delay (default
// 150ms if delay<=0), useful for tests that need a window to act on partial
// streamed output (e.g. mid-stream cancellation).
func SlowTextTurn(delay time.Duration, deltas ...string) Turn {
	return Turn{kind: "slow_text", deltas: deltas, delay: delay}
}

// ToolCallTurn streams a single function tool call.
func ToolCallTurn(toolCallID, name, argumentsJSON string) Turn {
	return Turn{kind: "tool_call", toolCallID: toolCallID, toolName: name, toolArgsJSON: argumentsJSON}
}

// ToolCallsTurn emits several tool calls in one reply, with indexes 0..n-1
// and finish_reason=tool_calls.
func ToolCallsTurn(calls ...ToolCall) Turn {
	return Turn{kind: "tool_calls", toolCalls: calls}
}

// ErrorTurn answers with the given HTTP status and an OpenAI-style error
// JSON body ({"error":{"message":...}}), for streaming and non-streaming
// requests alike.
func ErrorTurn(httpStatus int, message string) Turn {
	return Turn{kind: "error", errStatus: httpStatus, errMessage: message}
}

// HangTurn holds the connection open and never responds, until the fake
// server is closed. For exercising cancellation paths.
func HangTurn() Turn {
	return Turn{kind: "hang"}
}

// WithUsage returns a copy of the turn that reports token usage. Streaming
// replies emit a final usage chunk when the request set
// stream_options.include_usage; non-streaming replies always include a
// usage object. reasoning is reported as
// completion_tokens_details.reasoning_tokens.
func (t Turn) WithUsage(prompt, completion, reasoning int) Turn {
	t.usage = &turnUsage{prompt: prompt, completion: completion, reasoning: reasoning}
	return t
}

func (u *turnUsage) json() map[string]any {
	return map[string]any{
		"prompt_tokens":             u.prompt,
		"completion_tokens":         u.completion,
		"total_tokens":              u.prompt + u.completion,
		"completion_tokens_details": map[string]any{"reasoning_tokens": u.reasoning},
	}
}

func (t Turn) calls() []ToolCall {
	if t.kind == "tool_call" {
		return []ToolCall{{ID: t.toolCallID, Name: t.toolName, ArgumentsJSON: t.toolArgsJSON}}
	}
	return t.toolCalls
}

// RequestRecord captures one request the fake server received, for
// assertions (e.g. that a tool definition was actually sent upstream).
type RequestRecord struct {
	Body    map[string]any
	Headers http.Header
}

// FakeOpenAI is a minimal, scriptable fake of OpenAI's
// /v1/chat/completions endpoint (streaming SSE only), standing in for a
// real provider so callers exercise llmd's real openai backend code path
// without live API calls or nondeterminism. This is exactly how a host
// would register a real OpenAI-compatible endpoint
// (RegisterLlmProvider type=openai, base_url=...) — just pointed at a
// server under test control.
type FakeOpenAI struct {
	// URL is "http://127.0.0.1:PORT" (no trailing slash, no /v1) — pass as
	// the provider's base_url.
	URL string

	srv *http.Server
	ln  net.Listener

	mu       sync.Mutex
	queue    []Turn
	requests []RequestRecord
}

// StartFakeOpenAI starts a fake OpenAI-compatible server on an ephemeral
// loopback port.
func StartFakeOpenAI() (*FakeOpenAI, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("llmdtest: listen fake openai: %w", err)
	}
	f := &FakeOpenAI{
		URL: fmt.Sprintf("http://%s", ln.Addr().String()),
		ln:  ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", f.handle)
	mux.HandleFunc("/v1/chat/completions", f.handle)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/models", handleModels)
	mux.HandleFunc("/_script", f.handleScript)
	mux.HandleFunc("/_requests", f.handleRequests)
	f.srv = &http.Server{Handler: mux}
	go func() {
		_ = f.srv.Serve(ln)
	}()
	return f, nil
}

// handleModels answers the model-discovery request (GET /v1/models) with the
// single "fake-model", so RefreshLlmProviderModels enables it in llmd's
// catalog; Complete and CompleteStream reject models absent from the catalog.
func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   []any{map[string]any{"id": "fake-model", "object": "model"}},
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Enqueue pushes more turns onto the FIFO queue, one per future request.
func (f *FakeOpenAI) Enqueue(turns ...Turn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, turns...)
}

// Requests returns every request received so far, in order.
func (f *FakeOpenAI) Requests() []RequestRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RequestRecord, len(f.requests))
	copy(out, f.requests)
	return out
}

// Close shuts the fake server down.
func (f *FakeOpenAI) Close() error {
	return f.srv.Close()
}

func (f *FakeOpenAI) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	f.requests = append(f.requests, RequestRecord{Body: body, Headers: r.Header.Clone()})
	var turn Turn
	hasTurn := len(f.queue) > 0
	if hasTurn {
		turn = f.queue[0]
		f.queue = f.queue[1:]
	}
	f.mu.Unlock()

	if !hasTurn {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "llmdtest: no scripted turn queued"},
		})
		return
	}

	if turn.kind == "error" {
		// Handled before any SSE headers so streaming requests also see
		// the plain HTTP error status.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(turn.errStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": turn.errMessage, "type": "fake_error", "code": nil},
		})
		return
	}

	if turn.kind == "hang" {
		// Deliberately never respond; the caller drives cancellation and
		// closes the server itself in teardown.
		<-r.Context().Done()
		return
	}

	model, _ := body["model"].(string)
	if model == "" {
		model = "fake-model"
	}

	// OpenAI only streams SSE when "stream": true is explicitly set; any
	// other value (false / omitted, as GenerateOnce/one-shot callers send)
	// gets a single JSON completion object.
	streamRequested, _ := body["stream"].(bool)
	if !streamRequested {
		respondNonStreaming(w, model, turn)
		return
	}

	// Streaming usage is only reported when the request opted in.
	var streamU *turnUsage
	if so, ok := body["stream_options"].(map[string]any); ok {
		if inc, _ := so["include_usage"].(bool); inc {
			streamU = turn.usage
		}
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	switch turn.kind {
	case "text":
		streamText(w, flusher, model, turn.deltas, streamU)
	case "slow_text":
		delay := turn.delay
		if delay <= 0 {
			delay = 150 * time.Millisecond
		}
		streamSlowText(r, w, flusher, model, turn.deltas, delay, streamU)
	case "tool_call", "tool_calls":
		streamToolCalls(w, flusher, model, turn.calls(), streamU)
	}
}

// respondNonStreaming answers a non-streaming ("stream": false or omitted)
// request with a single JSON chat.completion object built from the
// scripted turn, instead of SSE chunks.
func respondNonStreaming(w http.ResponseWriter, model string, turn Turn) {
	id := chunkID()

	var message map[string]any
	var finishReason string
	switch turn.kind {
	case "tool_call", "tool_calls":
		tcs := []any{}
		for _, c := range turn.calls() {
			tcs = append(tcs, map[string]any{
				"id":   c.ID,
				"type": "function",
				"function": map[string]any{
					"name":      c.Name,
					"arguments": c.ArgumentsJSON,
				},
			})
		}
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": tcs}
		finishReason = "tool_calls"
	default: // "text", "slow_text"
		content := ""
		for _, d := range turn.deltas {
			content += d
		}
		message = map[string]any{"role": "assistant", "content": content}
		finishReason = "stop"
	}

	resp := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason}},
	}
	if turn.usage != nil {
		resp["usage"] = turn.usage.json()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func sseChunk(w http.ResponseWriter, flusher http.Flusher, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	if flusher != nil {
		flusher.Flush()
	}
}

// streamUsage writes the final usage chunk (empty choices) if u is non-nil.
func streamUsage(w http.ResponseWriter, flusher http.Flusher, id, model string, u *turnUsage) {
	if u == nil {
		return
	}
	sseChunk(w, flusher, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{}, "usage": u.json(),
	})
}

func chunkID() string {
	return fmt.Sprintf("chatcmpl-fake-%d", time.Now().UnixNano())
}

func streamText(w http.ResponseWriter, flusher http.Flusher, model string, deltas []string, u *turnUsage) {
	id := chunkID()
	for _, delta := range deltas {
		sseChunk(w, flusher, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": delta}, "finish_reason": nil}},
		})
	}
	sseChunk(w, flusher, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	streamUsage(w, flusher, id, model, u)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// streamSlowText writes one delta at a time with a delay between them, and
// stops early (leaving the connection open, never finishing) if the client
// disconnects mid-stream — mirrors how a real upstream provider's stream
// reacts to the downstream client (llmd) going away on cancellation.
func streamSlowText(r *http.Request, w http.ResponseWriter, flusher http.Flusher, model string, deltas []string, delay time.Duration, u *turnUsage) {
	id := chunkID()
	for _, delta := range deltas {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		sseChunk(w, flusher, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": delta}, "finish_reason": nil}},
		})
		select {
		case <-r.Context().Done():
			return
		case <-time.After(delay):
		}
	}
	sseChunk(w, flusher, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	streamUsage(w, flusher, id, model, u)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// streamToolCalls emits all calls in one delta, one tool_calls entry per
// call with index i, then finish_reason=tool_calls.
func streamToolCalls(w http.ResponseWriter, flusher http.Flusher, model string, calls []ToolCall, u *turnUsage) {
	id := chunkID()
	entries := make([]any, 0, len(calls))
	for i, c := range calls {
		entries = append(entries, map[string]any{
			"index": i, "id": c.ID, "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": c.ArgumentsJSON},
		})
	}
	sseChunk(w, flusher, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"tool_calls": entries},
			"finish_reason": nil,
		}},
	})
	sseChunk(w, flusher, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
	})
	streamUsage(w, flusher, id, model, u)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
