// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package llmdtest_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdtest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	loopWebhookTool  = "loop_webhook"
	loopWebhookReply = "webhook-result-42"
	loopExecOutput   = "executor-output-77"
	loopFinalText    = "all done: both tools ran"
)

// loopWebhook is an httptest server backing a webhook tool.
type loopWebhook struct {
	srv   *httptest.Server
	calls atomic.Int32
}

func newLoopWebhook(t *testing.T) *loopWebhook {
	t.Helper()
	w := &loopWebhook{}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		w.calls.Add(1)
		rw.Header().Set("Content-Type", "text/plain")
		_, _ = rw.Write([]byte(loopWebhookReply))
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *loopWebhook) definition() *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:                 loopWebhookTool,
		Description:          "test webhook tool",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: w.srv.URL},
		},
	}
}

// toolMessages returns tool_call_id -> content for every role=tool message in
// the request body the fake received.
func toolMessages(rec llmdtest.RequestRecord) map[string]string {
	msgs, _ := rec.Body["messages"].([]any)
	out := map[string]string{}
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != "tool" {
			continue
		}
		id, _ := mm["tool_call_id"].(string)
		content, _ := mm["content"].(string)
		out[id] = content
	}
	return out
}

func loopCalls() llmdtest.Turn {
	return llmdtest.ToolCallsTurn(
		llmdtest.ToolCall{ID: "call_hook", Name: loopWebhookTool, ArgumentsJSON: `{}`},
		llmdtest.ToolCall{ID: "call_exec", Name: e2eToolName, ArgumentsJSON: `{"argv":["echo","` + loopExecOutput + `"]}`},
	).WithUsage(11, 5, 2)
}

// The real ChatStream tool loop runs a webhook tool and an executor tool
// scripted through the fake model, sends both results back upstream and
// streams the final text.
func TestToolLoopInProcess_ChatStream(t *testing.T) {
	f := startExecutorE2E(t)
	hook := newLoopWebhook(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sid := "tool-loop-chat"
	sctx, err := f.env.NewSession(ctx, e2eScopeA, sid)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := f.env.Client.RegisterSessionTool(sctx, &llmv1.RegisterSessionToolRequest{SessionId: sid, Tool: hook.definition()}); err != nil {
		t.Fatalf("RegisterSessionTool: %v", err)
	}

	f.env.FakeOpenAI.Enqueue(loopCalls(), llmdtest.TextTurn("all done: ", "both tools ran").WithUsage(20, 6, 0))

	st, err := f.env.Client.ChatStream(sctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "run both tools", Id: "u1"}},
		SessionId: &sid,
		Options:   &llmv1.CallOptions{Model: "fake-model"},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	finished := map[string]bool{}
	var complete *llmv1.AssistantReply
	var lastUsage *llmv1.TokenUsage
	for {
		fr, err := st.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if d := fr.GetAssistantTextDelta(); d != nil {
			text.WriteString(d.GetDelta())
		}
		if e := fr.GetToolExecutionFinished(); e != nil {
			if !e.GetSuccess() {
				t.Errorf("tool %s failed: %s", e.GetName(), e.GetErrorMessage())
			}
			finished[e.GetName()] = true
		}
		if fr.GetAssistantTurnFailed() != nil {
			t.Fatalf("turn failed: %s", fr.GetAssistantTurnFailed().GetError())
		}
		if c := fr.GetStreamComplete(); c != nil {
			complete = c
		}
		if tf := fr.GetAssistantTurnFinished(); tf != nil {
			lastUsage = tf.GetUsage()
		}
	}

	if !finished[loopWebhookTool] || !finished[e2eToolName] {
		t.Errorf("tool executions finished = %v, want both %s and %s", finished, loopWebhookTool, e2eToolName)
	}
	if got := hook.calls.Load(); got != 1 {
		t.Errorf("webhook called %d times, want 1", got)
	}
	if got := f.tool.calls.Load(); got != 1 {
		t.Errorf("executor handler called %d times, want 1", got)
	}
	if complete == nil || complete.GetFinishReason() != "stop" {
		t.Errorf("stream_complete = %v, want finish_reason stop", complete)
	}
	if lastUsage.GetInputTokens() != 20 || lastUsage.GetOutputTokens() != 6 {
		t.Errorf("final turn usage = %v, want input 20 output 6", lastUsage)
	}
	if !strings.Contains(text.String(), loopFinalText) {
		t.Errorf("streamed text = %q, want %q", text.String(), loopFinalText)
	}

	reqs := f.env.FakeOpenAI.Requests()
	if len(reqs) != 2 {
		t.Fatalf("fake model got %d requests, want 2", len(reqs))
	}
	got := toolMessages(reqs[1])
	if !strings.Contains(got["call_hook"], loopWebhookReply) {
		t.Errorf("second request webhook result = %q, want %q", got["call_hook"], loopWebhookReply)
	}
	if !strings.Contains(got["call_exec"], loopExecOutput) {
		t.Errorf("second request executor result = %q, want %q", got["call_exec"], loopExecOutput)
	}
	if len(toolMessages(reqs[0])) != 0 {
		t.Error("first request must not contain tool results")
	}
}

// Complete never runs tools: it returns the scripted tool calls to the
// caller, and the caller sends results back in a second Complete.
func TestToolLoopInProcess_Complete(t *testing.T) {
	f := startExecutorE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sctx, err := f.env.NewSession(ctx, e2eScopeA, "tool-loop-complete")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := f.env.Client.RefreshLlmProviderModels(sctx, &llmv1.RefreshLlmProviderModelsRequest{ProviderId: f.env.ProviderID}); err != nil {
		t.Fatalf("RefreshLlmProviderModels: %v", err)
	}
	hook := newLoopWebhook(t)
	tools := []*llmv1.ToolDefinition{
		{Name: loopWebhookTool, Description: "webhook", ParametersSchemaJson: `{"type":"object","properties":{}}`},
		{Name: e2eToolName, Description: "executor", ParametersSchemaJson: `{"type":"object","properties":{"argv":{"type":"array","items":{"type":"string"}}}}`},
	}
	opts := &llmv1.CallOptions{Model: "fake-model"}
	user := &llmv1.Message{Role: "user", Content: "run both tools", Id: "u1"}

	f.env.FakeOpenAI.Enqueue(loopCalls(), llmdtest.TextTurn(loopFinalText).WithUsage(20, 6, 0))

	r1, err := f.env.Client.Complete(sctx, &llmv1.CompleteRequest{
		Messages: []*llmv1.Message{user}, Options: opts, ProviderId: f.env.ProviderID, Tools: tools,
	})
	if err != nil {
		t.Fatalf("Complete #1: %v", err)
	}
	calls := r1.GetReply().GetToolCalls()
	if len(calls) != 2 || calls[0].GetId() != "call_hook" || calls[1].GetId() != "call_exec" {
		t.Fatalf("Complete #1 tool calls = %v, want call_hook and call_exec", calls)
	}
	if got := hook.calls.Load() + f.tool.calls.Load(); got != 0 {
		t.Fatalf("Complete must not execute tools, but %d calls ran", got)
	}

	// The caller (not llmd) runs the tools: the webhook directly and the
	// executor tool through InvokeTool.
	resp, err := http.Post(hook.srv.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("call webhook: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read webhook body: %v", err)
	}
	execRes, err := f.invoke(sctx, e2eScopeA, "echo", loopExecOutput)
	if err != nil || execRes.GetIsError() {
		t.Fatalf("InvokeTool: err=%v resp=%v", err, execRes)
	}

	r2, err := f.env.Client.Complete(sctx, &llmv1.CompleteRequest{
		Messages: []*llmv1.Message{
			user,
			{Role: "assistant", ToolCalls: calls},
			{Role: "tool", ToolCallId: "call_hook", Content: string(body)},
			{Role: "tool", ToolCallId: "call_exec", Content: execRes.GetResultJson()},
		},
		Options: opts, ProviderId: f.env.ProviderID, Tools: tools,
	})
	if err != nil {
		t.Fatalf("Complete #2: %v", err)
	}
	if got := r2.GetReply().GetContent(); got != loopFinalText {
		t.Errorf("Complete #2 content = %q, want %q", got, loopFinalText)
	}

	reqs := f.env.FakeOpenAI.Requests()
	if len(reqs) != 2 {
		t.Fatalf("fake model got %d requests, want 2", len(reqs))
	}
	got := toolMessages(reqs[1])
	if !strings.Contains(got["call_hook"], loopWebhookReply) || !strings.Contains(got["call_exec"], loopExecOutput) {
		t.Errorf("second request tool results = %v, want webhook and executor output", got)
	}
}

// An ErrorTurn must surface to the gRPC client as an error on both Complete
// and ChatStream.
func TestToolLoopInProcess_ErrorTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("StartInProcess: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	sid := "error-turn"
	sctx, err := env.NewSession(ctx, e2eScopeA, sid)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := env.Client.RefreshLlmProviderModels(sctx, &llmv1.RefreshLlmProviderModelsRequest{ProviderId: env.ProviderID}); err != nil {
		t.Fatalf("RefreshLlmProviderModels: %v", err)
	}
	one := int32(1)
	opts := &llmv1.CallOptions{Model: "fake-model", MaxAttempts: &one}

	t.Run("Complete", func(t *testing.T) {
		env.FakeOpenAI.Enqueue(llmdtest.ErrorTurn(400, "scripted failure complete"))
		_, err := env.Client.Complete(sctx, &llmv1.CompleteRequest{
			Messages: []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}}, Options: opts, ProviderId: env.ProviderID,
		})
		if err == nil {
			t.Fatal("Complete returned no error for an ErrorTurn")
		}
		if c := status.Code(err); c != codes.InvalidArgument {
			t.Errorf("Complete error code = %v (%v), want InvalidArgument for upstream 400", c, err)
		}
	})

	t.Run("ChatStream", func(t *testing.T) {
		env.FakeOpenAI.Enqueue(llmdtest.ErrorTurn(400, "scripted failure chat"))
		st, err := env.Client.ChatStream(sctx, &llmv1.ChatStreamRequest{
			Messages:  []*llmv1.Message{{Role: "user", Content: "hi", Id: "u2"}},
			SessionId: &sid,
			Options:   opts,
		})
		var failure string
		if err != nil {
			failure = err.Error()
		}
		for err == nil {
			var fr *llmv1.ConversationStreamFrame
			fr, err = st.Recv()
			if err == nil {
				if f := fr.GetAssistantTurnFailed(); f != nil {
					failure += f.GetError()
				}
				continue
			}
			if !errors.Is(err, io.EOF) {
				failure += err.Error()
			}
		}
		if !strings.Contains(failure, "status=400") {
			t.Errorf("ChatStream error/failure = %q, want the upstream 400 reported", failure)
		}
	})
}
