// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Control endpoints on FakeOpenAI, for processes that cannot call Enqueue
// directly (e.g. cmd/llmd-fake driven over HTTP).
//
//	POST /_script    body: {"turns":[ScriptTurn,...]}  -> 200 {"queued":N}
//	GET  /_requests  -> 200 {"requests":[{"body":{...},"headers":{...}},...]}
//
// ScriptTurn JSON, one object per Turn builder:
//
//	{"kind":"text","deltas":["a","b"]}                         TextTurn
//	{"kind":"slow_text","delay_ms":150,"deltas":["a","b"]}     SlowTextTurn
//	{"kind":"tool_call","id":"c1","name":"n","arguments":"{}"} ToolCallTurn
//	{"kind":"tool_calls","calls":[{"id":"c1","name":"n","arguments":"{}"}]} ToolCallsTurn
//	{"kind":"error","status":429,"message":"slow down"}        ErrorTurn
//	{"kind":"hang"}                                            HangTurn
//
// Any turn may add "usage":{"prompt":1,"completion":2,"reasoning":0}
// (WithUsage). An invalid script is rejected whole with 400.

// ScriptTurn is the JSON form of a Turn.
type ScriptTurn struct {
	Kind      string           `json:"kind"`
	Deltas    []string         `json:"deltas,omitempty"`
	DelayMS   int              `json:"delay_ms,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
	Calls     []ScriptToolCall `json:"calls,omitempty"`
	Status    int              `json:"status,omitempty"`
	Message   string           `json:"message,omitempty"`
	Usage     *ScriptTurnUsage `json:"usage,omitempty"`
}

// ScriptToolCall is one call in a ScriptTurn of kind tool_calls.
type ScriptToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ScriptTurnUsage is the JSON form of WithUsage.
type ScriptTurnUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Reasoning  int `json:"reasoning"`
}

// Turn converts the JSON form to a Turn, rejecting unknown kinds.
func (s ScriptTurn) Turn() (Turn, error) {
	var t Turn
	switch s.Kind {
	case "text":
		t = TextTurn(s.Deltas...)
	case "slow_text":
		t = SlowTextTurn(time.Duration(s.DelayMS)*time.Millisecond, s.Deltas...)
	case "tool_call":
		t = ToolCallTurn(s.ID, s.Name, s.Arguments)
	case "tool_calls":
		calls := make([]ToolCall, 0, len(s.Calls))
		for _, c := range s.Calls {
			calls = append(calls, ToolCall{ID: c.ID, Name: c.Name, ArgumentsJSON: c.Arguments})
		}
		t = ToolCallsTurn(calls...)
	case "error":
		if s.Status < 100 || s.Status > 599 {
			return Turn{}, fmt.Errorf("error turn needs an HTTP status in 100..599, got %d", s.Status)
		}
		t = ErrorTurn(s.Status, s.Message)
	case "hang":
		t = HangTurn()
	default:
		return Turn{}, fmt.Errorf("unknown turn kind %q", s.Kind)
	}
	if s.Usage != nil {
		t = t.WithUsage(s.Usage.Prompt, s.Usage.Completion, s.Usage.Reasoning)
	}
	return t, nil
}

func (f *FakeOpenAI) handleScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var req struct {
		Turns []ScriptTurn `json:"turns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeControlJSON(w, http.StatusBadRequest, map[string]any{"error": "decode body: " + err.Error()})
		return
	}
	turns := make([]Turn, 0, len(req.Turns))
	for i, st := range req.Turns {
		t, err := st.Turn()
		if err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("turn %d: %v", i, err)})
			return
		}
		turns = append(turns, t)
	}
	f.Enqueue(turns...)
	writeControlJSON(w, http.StatusOK, map[string]any{"queued": len(turns)})
}

func (f *FakeOpenAI) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET required"})
		return
	}
	recs := f.Requests()
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, map[string]any{"body": rec.Body, "headers": rec.Headers})
	}
	writeControlJSON(w, http.StatusOK, map[string]any{"requests": out})
}

func writeControlJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already sent; log since the client cannot be told.
		log.Printf("llmdtest: write control response: %v", err)
	}
}
