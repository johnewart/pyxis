// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package llmdtest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdtest"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolexecutor"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	e2eToolName   = "e2e_run_command"
	e2eExecutorID = "e2e-executor"
	e2eSecret     = "e2e-shared-secret"
)

var (
	e2eScopeA = tenantscope.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	e2eScopeB = tenantscope.Scope{TenantID: "tenant-b", ScopeID: "s1"}
)

// e2eCommandTool is a TEST FIXTURE: a command-running handler that puts its
// child in its own process group and SIGKILLs the whole group on ctx cancel.
type e2eCommandTool struct {
	calls atomic.Int32
	pidCh chan int
}

func newE2ECommandTool() *e2eCommandTool { return &e2eCommandTool{pidCh: make(chan int, 4)} }

func (c *e2eCommandTool) definition() *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:        e2eToolName,
		Description: "test-only command runner",
		Execution: &llmv1.ToolDefinition_Executor{Executor: &llmv1.ToolExecutionExecutor{
			ExecutorId: e2eExecutorID, MaxDurationSeconds: 120,
		}},
	}
}

func (c *e2eCommandTool) handle(ctx context.Context, args json.RawMessage, _ toolseam.Invocation) (toolseam.Result, error) {
	c.calls.Add(1)
	var in struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolseam.Result{}, fmt.Errorf("parse args: %w", err)
	}
	if len(in.Argv) == 0 {
		return toolseam.Result{}, errors.New("argv is required")
	}
	cmd := exec.CommandContext(ctx, in.Argv[0], in.Argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return toolseam.Result{}, fmt.Errorf("start: %w", err)
	}
	c.pidCh <- cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		return toolseam.Result{}, fmt.Errorf("command failed: %w (output: %s)", err, out.String())
	}
	return toolseam.Result{Content: out.String()}, nil
}

type e2eFixture struct {
	env  *llmdtest.Env
	tool *e2eCommandTool
	ack  *llmv1.ExecutorHelloAck
	cli  *toolexecutor.Client
}

// startExecutorE2E boots in-process llmd and attaches the executor client for tenant A only.
func startExecutorE2E(t *testing.T) *e2eFixture {
	t.Helper()
	// The secret reaches llmd only through llmdhost.Config (via
	// InProcessOptions); nothing reads the environment.
	ctx := context.Background()
	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{DataDir: t.TempDir(), ToolProviderSecret: e2eSecret})
	if err != nil {
		t.Fatalf("StartInProcess: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	tool := newE2ECommandTool()
	handlers := toolseam.NewHandlers()
	if err := handlers.Register(e2eToolName, tool.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ackCh := make(chan *llmv1.ExecutorHelloAck, 4)
	cli, err := toolexecutor.New(toolexecutor.Options{
		ExecutorID:   e2eExecutorID,
		Handlers:     handlers,
		Definitions:  []*llmv1.ToolDefinition{tool.definition()},
		Allowlist:    []string{e2eToolName},
		SharedSecret: e2eSecret,
		BackoffBase:  50 * time.Millisecond,
		BackoffMax:   500 * time.Millisecond,
		OnAck: func(_ tenantscope.Scope, ack *llmv1.ExecutorHelloAck) {
			select {
			case ackCh <- ack:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("toolexecutor.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := cli.Run(runCtx, llmv1.NewToolProviderServiceClient(env.Conn), []tenantscope.Scope{e2eScopeA}); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("executor Run: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })

	select {
	case ack := <-ackCh:
		return &e2eFixture{env: env, tool: tool, ack: ack, cli: cli}
	case <-time.After(10 * time.Second):
		t.Fatal("executor hello was never acked")
		return nil
	}
}

func (f *e2eFixture) invoke(ctx context.Context, sc tenantscope.Scope, argv ...string) (*llmv1.InvokeToolResponse, error) {
	b, err := json.Marshal(map[string]any{"argv": argv})
	if err != nil {
		return nil, err
	}
	return f.env.Client.InvokeTool(sc.OutgoingContext(ctx), &llmv1.InvokeToolRequest{ToolId: e2eToolName, ArgumentsJson: string(b)})
}

func TestExecutorE2E_AckAndTenantListing(t *testing.T) {
	f := startExecutorE2E(t)
	if got := f.ack.GetAcceptedTools(); len(got) != 1 || !strings.Contains(fmt.Sprint(got), e2eToolName) {
		t.Fatalf("ack accepted = %v, want [%s]", got, e2eToolName)
	}
	if rej := f.ack.GetRejectedTools(); len(rej) != 0 {
		t.Fatalf("ack rejected tools: %v", rej)
	}
	if rej := f.cli.Rejections(); len(rej) != 0 {
		t.Fatalf("client recorded rejections: %v", rej)
	}

	ctx := context.Background()
	listed := func(sc tenantscope.Scope) bool {
		t.Helper()
		const sid = "listing-session"
		if _, err := f.env.NewSession(ctx, sc, sid); err != nil {
			t.Fatalf("NewSession(%s): %v", sc, err)
		}
		resp, err := f.env.Client.ListSessionTools(sc.OutgoingContext(ctx), &llmv1.ListSessionToolsRequest{SessionId: sid})
		if err != nil {
			t.Fatalf("ListSessionTools(%s): %v", sc, err)
		}
		for _, d := range resp.GetTools() {
			if d.GetName() == e2eToolName {
				return true
			}
		}
		return false
	}
	if !listed(e2eScopeA) {
		t.Error("tenant A session should list e2e_run_command")
	}
	if listed(e2eScopeB) {
		t.Error("tenant B session must not list tenant A's executor tool")
	}

	// Session-independent listing: same tenant isolation, no session needed.
	listTools := func(sc tenantscope.Scope) (hasExec bool, n int) {
		t.Helper()
		resp, err := f.env.Client.ListTools(sc.OutgoingContext(ctx), &llmv1.ListToolsRequest{})
		if err != nil {
			t.Fatalf("ListTools(%s): %v", sc, err)
		}
		for _, d := range resp.GetTools() {
			if d.GetName() == e2eToolName {
				hasExec = true
			}
		}
		return hasExec, len(resp.GetTools())
	}
	if has, n := listTools(e2eScopeA); !has || n < 2 {
		t.Errorf("ListTools(tenant A): has executor tool=%v, total=%d; want it plus registered tools", has, n)
	}
	if has, n := listTools(e2eScopeB); has || n == 0 {
		t.Errorf("ListTools(tenant B): has tenant A's executor tool=%v, total=%d", has, n)
	}
	if _, err := f.env.Client.ListTools(ctx, &llmv1.ListToolsRequest{}); err == nil {
		t.Error("ListTools without tenant metadata must be refused")
	}
}

// Agent profiles must accept and materialize executor tools exactly as
// ListTools reports them for the tenant. PROFILE_ONLY makes the profile's
// enabled mapping the only way the tool can reach the session.
func TestExecutorE2E_AgentProfileMapsExecutorTool(t *testing.T) {
	f := startExecutorE2E(t)
	ctx := context.Background()
	mapping := []*llmv1.ToolMapping{{ToolName: e2eToolName, Enabled: true}}

	ctxA := e2eScopeA.OutgoingContext(ctx)
	created, err := f.env.Client.CreateAgentProfile(ctxA, &llmv1.CreateAgentProfileRequest{
		Name: "exec-profile", ToolMapping: mapping, ToolPolicy: llmv1.ToolPolicy_TOOL_POLICY_PROFILE_ONLY,
	})
	if err != nil {
		t.Fatalf("CreateAgentProfile with executor tool (tenant A): %v", err)
	}
	const sid = "exec-profile-session"
	if _, err := f.env.Client.CreateSessionFromAgentProfile(ctxA, &llmv1.CreateSessionFromAgentProfileRequest{
		SessionId: sid, AgentProfileId: created.GetProfile().GetId(),
	}); err != nil {
		t.Fatalf("CreateSessionFromAgentProfile: %v", err)
	}
	resp, err := f.env.Client.ListSessionTools(ctxA, &llmv1.ListSessionToolsRequest{SessionId: sid})
	if err != nil {
		t.Fatalf("ListSessionTools: %v", err)
	}
	found := false
	for _, d := range resp.GetTools() {
		if d.GetName() == e2eToolName {
			found = true
		}
	}
	if !found {
		t.Fatalf("PROFILE_ONLY session from profile lacks enabled executor tool %q: %v", e2eToolName, resp.GetTools())
	}

	// Tenant B has no such executor tool: the mapping is unknown there.
	_, err = f.env.Client.CreateAgentProfile(e2eScopeB.OutgoingContext(ctx), &llmv1.CreateAgentProfileRequest{
		Name: "exec-profile", ToolMapping: mapping,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("tenant B profile mapping tenant A's executor tool: err=%v, want InvalidArgument", err)
	}
}

func TestExecutorE2E_InvokeToolReturnsOutput(t *testing.T) {
	f := startExecutorE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := f.invoke(ctx, e2eScopeA, "echo", "executor-e2e-ok")
	if err != nil {
		t.Fatalf("InvokeTool(A): %v", err)
	}
	if resp.GetIsError() {
		t.Fatalf("InvokeTool(A) is_error: %s", resp.GetErrorMessage())
	}
	if !strings.Contains(resp.GetResultJson(), "executor-e2e-ok") {
		t.Fatalf("result %q missing command output", resp.GetResultJson())
	}
	before := f.tool.calls.Load()

	respB, err := f.invoke(ctx, e2eScopeB, "echo", "should-not-run")
	if err == nil && !respB.GetIsError() {
		t.Fatalf("tenant B invocation must be refused, got %+v", respB)
	}
	if after := f.tool.calls.Load(); after != before {
		t.Fatalf("handler ran for tenant B (calls %d -> %d)", before, after)
	}
}

func TestExecutorE2E_CancelKillsProcessGroup(t *testing.T) {
	f := startExecutorE2E(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type res struct {
		resp *llmv1.InvokeToolResponse
		err  error
	}
	done := make(chan res, 1)
	go func() {
		r, err := f.invoke(ctx, e2eScopeA, "sh", "-c", "sleep 60 & wait")
		done <- res{r, err}
	}()

	var pgid int
	select {
	case pgid = <-f.tool.pidCh:
	case <-time.After(15 * time.Second):
		t.Fatal("handler never started the command")
	}
	// Let sh fork its grandchild sleep before cancelling.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case r := <-done:
		if r.err == nil && !r.resp.GetIsError() {
			t.Fatalf("cancelled InvokeTool reported success: %+v", r.resp)
		}
		t.Logf("cancelled InvokeTool returned: err=%v resp=%v", r.err, r.resp)
	case <-time.After(5 * time.Second):
		t.Fatal("InvokeTool did not return within 5s of cancel")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still alive 5s after cancel (kill -0: %v)", pgid, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
