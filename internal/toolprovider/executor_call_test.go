package toolprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolseam"
)

type callOut struct {
	content string
	ok      bool
	errMsg  string
}

func execInv(tenant, name, args string) toolseam.Invocation {
	return toolseam.Invocation{Name: name, ArgumentsJSON: args, SessionID: "sess", Scope: tenantscope.Scope{TenantID: tenant}, Origin: toolseam.OriginChatLoop}
}

// attachExec connects an executor serving "deploy" in tenant t1.
func attachExec(t *testing.T, maxDur uint32) (*Hub, llmv1.ToolProviderService_ConnectClient, *llmv1.ToolDefinition) {
	t.Helper()
	hub, c := startExecServer(t)
	d := execDef("deploy", "ex")
	d.GetExecutor().MaxDurationSeconds = maxDur
	st, ack := mustAck(t, c, "t1", hello("ex", "i1", d))
	if !accepted(ack, "deploy") {
		t.Fatalf("deploy not accepted: %v", ack)
	}
	return hub, st, d
}

func goCall(ctx context.Context, hub *Hub, d *llmv1.ToolDefinition) <-chan callOut {
	out := make(chan callOut, 1)
	go func() {
		c, ok, e := hub.Call(ctx, d, execInv("t1", "deploy", `{"x":1}`))
		out <- callOut{c, ok, e}
	}()
	return out
}

func recvCall(t *testing.T, st llmv1.ToolProviderService_ConnectClient) *llmv1.ExecutorToolCall {
	t.Helper()
	in, err := st.Recv()
	if err != nil {
		t.Fatalf("recv tool_call: %v", err)
	}
	tc := in.GetToolCall()
	if tc == nil {
		t.Fatalf("expected tool_call, got %T", in.GetFrame())
	}
	return tc
}

func reply(t *testing.T, st llmv1.ToolProviderService_ConnectClient, r *llmv1.ToolCallResult) {
	t.Helper()
	if err := st.Send(&llmv1.ToolProviderToServer{Frame: &llmv1.ToolProviderToServer_ToolCallResult{ToolCallResult: r}}); err != nil {
		t.Fatalf("send result: %v", err)
	}
}

func wait(t *testing.T, ch <-chan callOut, d time.Duration) callOut {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatalf("Call did not return within %s", d)
		return callOut{}
	}
}

func output(id, content string, isErr bool) *llmv1.ToolCallResult {
	return &llmv1.ToolCallResult{CallId: id, Outcome: &llmv1.ToolCallResult_Output{Output: &llmv1.ToolCallOutput{Content: content, IsError: isErr}}}
}

func TestExecutorCallCompleted(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	res := goCall(context.Background(), hub, d)
	tc := recvCall(t, st)
	if tc.GetToolName() != "deploy" || tc.GetArgumentsJson() != `{"x":1}` || tc.GetTenantId() != "t1" ||
		tc.GetMaxDurationSeconds() != 30 || tc.GetOrigin() != llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_MODEL || tc.GetCallId() == "" {
		t.Fatalf("unexpected tool_call: %v", tc)
	}
	reply(t, st, output(tc.GetCallId(), "done", false))
	r := wait(t, res, 2*time.Second)
	if !r.ok || r.content != "done" || r.errMsg != "" {
		t.Fatalf("got %+v", r)
	}
}

// The call must carry the session's scope and turn: registration is
// tenant-wide, so the stream cannot tell the host which scope a call is for.
func TestExecutorCallCarriesScopeAndTurn(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	inv := execInv("t1", "deploy", `{}`)
	inv.Scope.ScopeID = "proj-42"
	inv.TurnID = "turn-7"
	res := make(chan callOut, 1)
	go func() {
		c, ok, e := hub.Call(context.Background(), d, inv)
		res <- callOut{c, ok, e}
	}()
	tc := recvCall(t, st)
	if tc.GetTenantId() != "t1" || tc.GetScopeId() != "proj-42" || tc.GetTurnId() != "turn-7" || tc.GetSessionId() != "sess" {
		t.Fatalf("tool_call lost session scope/turn: %v", tc)
	}
	reply(t, st, output(tc.GetCallId(), "done", false))
	if r := wait(t, res, 2*time.Second); !r.ok {
		t.Fatalf("got %+v", r)
	}
}

func TestExecutorCallCarriesToolContext(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	inv := execInv("t1", "deploy", `{}`)
	inv.ToolContext = map[string]string{"k": "v"}
	res := make(chan callOut, 1)
	go func() {
		c, ok, e := hub.Call(context.Background(), d, inv)
		res <- callOut{c, ok, e}
	}()
	tc := recvCall(t, st)
	if got := tc.GetToolContext(); len(got) != 1 || got["k"] != "v" {
		t.Fatalf("tool_call tool_context=%v", got)
	}
	reply(t, st, output(tc.GetCallId(), "done", false))
	if r := wait(t, res, 2*time.Second); !r.ok {
		t.Fatalf("got %+v", r)
	}
}

func TestExecutorCallHandlerFailurePassthrough(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	res := goCall(context.Background(), hub, d)
	tc := recvCall(t, st)
	reply(t, st, output(tc.GetCallId(), "boom: disk full", true))
	r := wait(t, res, 2*time.Second)
	if r.ok || r.content != "boom: disk full" || r.errMsg != "boom: disk full" {
		t.Fatalf("output is_error: got %+v", r)
	}

	res = goCall(context.Background(), hub, d)
	tc = recvCall(t, st)
	reply(t, st, &llmv1.ToolCallResult{CallId: tc.GetCallId(), Outcome: &llmv1.ToolCallResult_Failure{Failure: &llmv1.ToolCallFailure{Code: "bad_args", Message: "missing env"}}})
	r = wait(t, res, 2*time.Second)
	if r.ok || r.content != "missing env" || r.errMsg != "bad_args: missing env" {
		t.Fatalf("failure: got %+v", r)
	}
}

func TestExecutorCallCancelBeforeResult(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	ctx, cancel := context.WithCancel(context.Background())
	res := goCall(ctx, hub, d)
	tc := recvCall(t, st)
	cancel()
	r := wait(t, res, time.Second)
	if r.ok || r.errMsg != "cancelled" {
		t.Fatalf("got %+v", r)
	}
	in, err := st.Recv()
	if err != nil {
		t.Fatalf("recv cancel: %v", err)
	}
	if cc := in.GetToolCallCancel(); cc == nil || cc.GetCallId() != tc.GetCallId() {
		t.Fatalf("expected cancel for %s, got %v", tc.GetCallId(), in)
	}
	if hub.DeliverCallResult("t1", output(tc.GetCallId(), "late", false)) {
		t.Fatal("late result was delivered")
	}
}

func TestExecutorCallDeadline(t *testing.T) {
	hub, st, d := attachExec(t, 1)
	start := time.Now()
	res := goCall(context.Background(), hub, d)
	_ = recvCall(t, st)
	r := wait(t, res, 3*time.Second)
	if r.ok || !strings.Contains(r.errMsg, "timed out after 1s") {
		t.Fatalf("got %+v", r)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("deadline took %s", el)
	}
}

func TestExecutorCallOversize(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	res := goCall(context.Background(), hub, d)
	tc := recvCall(t, st)
	r := output(tc.GetCallId(), strings.Repeat("a", MaxResultBytes+10), false)
	// Deliver directly: the gRPC transport's own message cap is not under test.
	if !hub.DeliverCallResult("t1", r) {
		t.Fatal("oversize result not routed to waiter")
	}
	got := wait(t, res, 2*time.Second)
	if got.ok || !strings.Contains(got.errMsg, "exceeds limit") || !strings.Contains(got.errMsg, "1048576") {
		t.Fatalf("got %+v", got)
	}
}

func waitDetached(t *testing.T, hub *Hub) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, e := hub.resolveExecutor("t1", "deploy"); e == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("executor never detached")
}

func TestExecutorCallUnattached(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	if err := st.CloseSend(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitDetached(t, hub)
	r := wait(t, goCall(context.Background(), hub, d), time.Second)
	if r.ok || !strings.Contains(r.errMsg, `executor "ex" is not attached`) {
		t.Fatalf("got %+v", r)
	}
	if c, ok, e := hub.Call(context.Background(), d, execInv("t2", "deploy", "{}")); ok || !strings.Contains(e, "not registered") {
		t.Fatalf("other tenant: %q %v %q", c, ok, e)
	}
}

func TestExecutorCallStreamDrop(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	res := goCall(context.Background(), hub, d)
	_ = recvCall(t, st)
	if err := st.CloseSend(); err != nil {
		t.Fatalf("close: %v", err)
	}
	r := wait(t, res, time.Second)
	if r.ok || r.errMsg != `executor "ex" disconnected, outcome unknown` {
		t.Fatalf("got %+v", r)
	}
}
