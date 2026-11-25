package toolexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolseam"
)

// fakeStream is an in-memory frameStream driven by channels.
type fakeStream struct {
	in   chan *llmv1.ToolServerToProvider
	out  chan *llmv1.ToolProviderToServer
	done chan struct{}
	once sync.Once
}

func newFakeStream() *fakeStream {
	return &fakeStream{in: make(chan *llmv1.ToolServerToProvider, 8), out: make(chan *llmv1.ToolProviderToServer, 8), done: make(chan struct{})}
}

func (f *fakeStream) Send(m *llmv1.ToolProviderToServer) error {
	select {
	case f.out <- m:
		return nil
	case <-f.done:
		return io.EOF
	}
}

func (f *fakeStream) Recv() (*llmv1.ToolServerToProvider, error) {
	select {
	case m := <-f.in:
		return m, nil
	case <-f.done:
		return nil, io.EOF
	}
}

func (f *fakeStream) close() { f.once.Do(func() { close(f.done) }) }

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) log(level llmdlog.Level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, string(level)+" "+fmt.Sprintf(format, args...))
}

func (l *logCapture) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type harness struct {
	c      *Client
	s      *fakeStream
	logs   *logCapture
	errCh  chan error
	cancel context.CancelFunc
}

func start(t *testing.T, handlers map[string]toolseam.Handler) *harness {
	t.Helper()
	h := toolseam.NewHandlers()
	var allow []string
	for n, fn := range handlers {
		if err := h.Register(n, fn); err != nil {
			t.Fatal(err)
		}
		allow = append(allow, n)
	}
	logs := &logCapture{}
	c, err := New(Options{ExecutorID: "exec-1", Handlers: h, Allowlist: allow, Logger: logs.log})
	if err != nil {
		t.Fatal(err)
	}
	s := newFakeStream()
	ctx, cancel := context.WithCancel(context.Background())
	hr := &harness{c: c, s: s, logs: logs, errCh: make(chan error, 1), cancel: cancel}
	go func() { hr.errCh <- c.Serve(ctx, s, tenantscope.Scope{}) }()
	t.Cleanup(func() {
		cancel()
		s.close()
		select {
		case <-hr.errCh:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return")
		}
	})
	return hr
}

func (h *harness) call(id, tool string, maxDur uint32) {
	h.s.in <- &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCall{ToolCall: &llmv1.ExecutorToolCall{
		CallId: id, ToolName: tool, ArgumentsJson: `{"x":1}`, SessionId: "sess", Origin: llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_MODEL, MaxDurationSeconds: maxDur,
	}}}
}

func (h *harness) result(t *testing.T, within time.Duration) *llmv1.ToolCallResult {
	t.Helper()
	select {
	case m := <-h.s.out:
		return m.GetToolCallResult()
	case <-time.After(within):
		t.Fatal("no ToolCallResult")
		return nil
	}
}

func TestDispatchHelloFiltersAllowlist(t *testing.T) {
	h := toolseam.NewHandlers()
	noop := func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) { return toolseam.Result{}, nil }
	_ = h.Register("runcommand", noop)
	_ = h.Register("secret", noop)
	defs := []*llmv1.ToolDefinition{
		{Name: "runcommand", Execution: &llmv1.ToolDefinition_InlineBuiltin{}},
		{Name: "secret"},
		{Name: "nohandler"},
	}
	c, err := New(Options{ExecutorID: "exec-1", Handlers: h, Definitions: defs, Allowlist: []string{"runcommand", "nohandler"}})
	if err != nil {
		t.Fatal(err)
	}
	hello := c.Hello("inst-1")
	if hello.GetExecutorId() != "exec-1" || hello.GetInstanceId() != "inst-1" {
		t.Fatalf("bad hello ids: %v", hello)
	}
	if len(hello.GetTools()) != 1 || hello.GetTools()[0].GetName() != "runcommand" {
		t.Fatalf("want only runcommand, got %v", hello.GetTools())
	}
	if ex := hello.GetTools()[0].GetExecutor(); ex == nil || ex.GetExecutorId() != "exec-1" {
		t.Fatalf("missing executor binding: %v", hello.GetTools()[0])
	}
	if defs[0].GetExecutor() != nil {
		t.Fatal("original definition mutated")
	}
}

func TestDispatchCompleted(t *testing.T) {
	got := make(chan toolseam.Invocation, 1)
	h := start(t, map[string]toolseam.Handler{"echo": func(_ context.Context, args json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
		got <- inv
		return toolseam.Result{Content: "ok:" + string(args)}, nil
	}})
	h.call("c1", "echo", 0)
	res := h.result(t, time.Second)
	if res.GetCallId() != "c1" || res.GetOutput().GetContent() != `ok:{"x":1}` {
		t.Fatalf("bad result %v", res)
	}
	inv := <-got
	if inv.SessionID != "sess" || inv.Origin != toolseam.OriginChatLoop || inv.ToolCallID != "c1" {
		t.Fatalf("bad invocation %+v", inv)
	}
}

// Invocation.Scope must be the call's scope (stream tenant + call scope_id),
// and TurnID must come from the call.
func TestDispatchUsesCallScopeAndTurn(t *testing.T) {
	got := make(chan toolseam.Invocation, 2)
	h := start(t, map[string]toolseam.Handler{"echo": func(_ context.Context, _ json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
		got <- inv
		return toolseam.Result{Content: "ok"}, nil
	}})
	h.s.in <- &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCall{ToolCall: &llmv1.ExecutorToolCall{
		CallId: "c1", ToolName: "echo", SessionId: "sess", TenantId: "t1", ScopeId: "proj-42", TurnId: "turn-7",
	}}}
	_ = h.result(t, time.Second)
	inv := <-got
	if inv.Scope.ScopeID != "proj-42" || inv.TurnID != "turn-7" {
		t.Fatalf("call scope/turn not applied: %+v", inv)
	}

	// No scope_id (older llmd): keep the stream's scope.
	h.call("c2", "echo", 0)
	_ = h.result(t, time.Second)
	if inv = <-got; inv.Scope.ScopeID != "" || inv.TurnID != "" {
		t.Fatalf("expected stream scope fallback, got %+v", inv)
	}
}

func TestDispatchHandlerFailure(t *testing.T) {
	h := start(t, map[string]toolseam.Handler{"bad": func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) {
		return toolseam.Result{}, errors.New("boom: exact text")
	}})
	h.call("c2", "bad", 0)
	res := h.result(t, time.Second)
	if res.GetFailure().GetMessage() != "boom: exact text" || res.GetFailure().GetCode() != CodeHandlerError {
		t.Fatalf("bad failure %v", res)
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	h := start(t, nil)
	h.call("c3", "missing", 0)
	res := h.result(t, time.Second)
	if res.GetFailure().GetCode() != CodeUnknownTool || !strings.Contains(res.GetFailure().GetMessage(), "not handled") {
		t.Fatalf("bad failure %v", res)
	}
}

func TestDispatchDeadline(t *testing.T) {
	h := start(t, map[string]toolseam.Handler{"slow": func(ctx context.Context, _ json.RawMessage, _ toolseam.Invocation) (toolseam.Result, error) {
		<-ctx.Done()
		return toolseam.Result{}, ctx.Err()
	}})
	h.call("c4", "slow", 1)
	res := h.result(t, 3*time.Second)
	if res.GetFailure().GetCode() != CodeTimeout || !strings.Contains(res.GetFailure().GetMessage(), "timed out") {
		t.Fatalf("bad failure %v", res)
	}
}

func TestCancelStopsBlockedHandler(t *testing.T) {
	returned := make(chan struct{})
	started := make(chan struct{})
	h := start(t, map[string]toolseam.Handler{"block": func(ctx context.Context, _ json.RawMessage, _ toolseam.Invocation) (toolseam.Result, error) {
		close(started)
		<-ctx.Done()
		close(returned)
		return toolseam.Result{Content: "late"}, nil
	}})
	h.call("c5", "block", 0)
	<-started
	h.s.in <- &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCallCancel{ToolCallCancel: &llmv1.ToolCallCancel{CallId: "c5", Reason: "user"}}}
	res := h.result(t, time.Second)
	if res.GetCallId() != "c5" || res.GetFailure().GetCode() != CodeCancelled || !strings.Contains(res.GetFailure().GetMessage(), "cancelled") {
		t.Fatalf("bad result %v", res)
	}
	select {
	case <-returned:
	default:
		t.Fatal("handler did not return")
	}
	select {
	case m := <-h.s.out:
		t.Fatalf("extra result %v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUnknownIDCancelLogged(t *testing.T) {
	h := start(t, map[string]toolseam.Handler{"echo": func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) {
		return toolseam.Result{Content: "ok"}, nil
	}})
	h.s.in <- &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCallCancel{ToolCallCancel: &llmv1.ToolCallCancel{CallId: "ghost-42"}}}
	h.s.in <- &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_Invoke{}}
	// Serve must keep running: a subsequent call still completes.
	h.call("c6", "echo", 0)
	if res := h.result(t, time.Second); res.GetOutput().GetContent() != "ok" {
		t.Fatalf("bad result %v", res)
	}
	select {
	case err := <-h.errCh:
		t.Fatalf("Serve exited: %v", err)
	default:
	}
	if !h.logs.has("WARN") || !h.logs.has("call_id=ghost-42") || !h.logs.has("executor_id=exec-1") {
		t.Fatalf("unknown id not logged: %v", h.logs.lines)
	}
	if !h.logs.has("unexpected server frame") {
		t.Fatalf("unexpected frame not logged: %v", h.logs.lines)
	}
}
