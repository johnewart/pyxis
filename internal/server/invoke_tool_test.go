package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newInvokeToolFixture builds a server with two workspaces' worth of fake host
// tool data, so tests can assert InvokeTool resolves the partition the request
// names and nothing else.
func newInvokeToolFixture(t *testing.T) *LlmServer {
	t.Helper()

	relatedByWorkspace := map[string]map[string][]string{
		"ws-a": {"src/only_in_a.go": {"src/only_in_a.go"}},
		"ws-b": {"src/only_in_b.go": {"src/only_in_b.go"}},
	}
	entriesByWorkspace := map[string]map[string]string{
		"ws-a": {"src/only_in_a.go": "package a\n\nfunc A() {}\n"},
		"ws-b": {"src/only_in_b.go": "package b\n\nfunc B() {}\n"},
	}

	reg := toolregistry.New()
	handlers := toolexec.NewHandlers()
	if _, err := InstallBuiltinTools(reg, handlers,
		registerFakeRelatedTool(relatedByWorkspace),
		registerFakeFindTool(entriesByWorkspace)); err != nil {
		t.Fatal(err)
	}

	srv := &LlmServer{
		Tools:           reg,
		Handlers:        handlers,
		SessionRuntimes: &sessionmanager.Manager{},
	}
	return srv
}

// TestInvokeTool_HostRelated asserts InvokeTool resolves a host-registered
// builtin through the composed registry/env and returns the same content the
// direct RunWithHandlers path produces for the identical Invocation — the two
// must not be able to drift.
func TestInvokeTool_HostRelated(t *testing.T) {
	srv := newInvokeToolFixture(t)

	resp, err := srv.InvokeTool(wsScope("ws-a"), &llmv1.InvokeToolRequest{
		ToolId:        "host_related",
		ArgumentsJson: `{"key":"src/only_in_a.go"}`,
	})
	if err != nil {
		t.Fatalf("InvokeTool: %v", err)
	}
	if resp.GetIsError() {
		t.Fatalf("InvokeTool returned error: %s / %s", resp.GetResultJson(), resp.GetErrorMessage())
	}

	ctx := wsScope("ws-a")
	inv := toolexec.Invocation{
		ToolCallID:    "direct-call",
		Name:          "host_related",
		ArgumentsJSON: `{"key":"src/only_in_a.go"}`,
	}
	def := lookupToolDefinition(srv.Tools, "host_related")
	inv = srv.bindWorkspace(ctx, inv)
	wantContent, wantOK, _ := toolexec.RunWithHandlers(ctx, def, inv, srv.handlerSet())
	if !wantOK {
		t.Fatalf("direct RunWithHandlers failed: %s", wantContent)
	}

	var gotPayload, wantPayload fakeRelatedPayload
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &gotPayload); err != nil {
		t.Fatalf("decode InvokeTool payload: %v", err)
	}
	if err := json.Unmarshal([]byte(wantContent), &wantPayload); err != nil {
		t.Fatalf("decode direct payload: %v", err)
	}
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("InvokeTool and direct RunWithHandlers disagree: got %+v want %+v", gotPayload, wantPayload)
	}
}

// TestInvokeTool_HostFind asserts InvokeTool resolves a second host-registered
// builtin the same way, against the workspace's bound entries.
func TestInvokeTool_HostFind(t *testing.T) {
	srv := newInvokeToolFixture(t)

	resp, err := srv.InvokeTool(wsScope("ws-a"), &llmv1.InvokeToolRequest{
		ToolId:        "host_find",
		ArgumentsJson: `{"needle":"func A"}`,
	})
	if err != nil {
		t.Fatalf("InvokeTool: %v", err)
	}
	if resp.GetIsError() {
		t.Fatalf("InvokeTool returned error: %s / %s", resp.GetResultJson(), resp.GetErrorMessage())
	}

	var payload fakeFindPayload
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &payload); err != nil {
		t.Fatalf("decode host_find payload: %v", err)
	}
	if len(payload.Hits) != 1 || payload.Hits[0].Key != "src/only_in_a.go" {
		t.Fatalf("host_find hits: %+v", payload.Hits)
	}
}

// TestInvokeTool_PartitionBindingIsRequestScoped pins that the workspace the
// request names is the one the tool actually reads: ws-a and ws-b hold
// disjoint data, and each request must see only its own workspace.
func TestInvokeTool_PartitionBindingIsRequestScoped(t *testing.T) {
	srv := newInvokeToolFixture(t)

	for ws, wantPath := range map[string]string{
		"ws-a": "src/only_in_a.go",
		"ws-b": "src/only_in_b.go",
	} {
		resp, err := srv.InvokeTool(wsScope(ws), &llmv1.InvokeToolRequest{
			ToolId:        "host_related",
			ArgumentsJson: `{"key":"` + wantPath + `"}`,
		})
		if err != nil {
			t.Fatalf("InvokeTool(%s): %v", ws, err)
		}
		if resp.GetIsError() {
			t.Fatalf("InvokeTool(%s) failed: %s", ws, resp.GetResultJson())
		}
		if !strings.Contains(resp.GetResultJson(), wantPath) {
			t.Fatalf("InvokeTool(%s): expected result to reference %s, got %s", ws, wantPath, resp.GetResultJson())
		}
		other := "src/only_in_a.go"
		if ws == "ws-a" {
			other = "src/only_in_b.go"
		}
		if strings.Contains(resp.GetResultJson(), other) {
			t.Fatalf("InvokeTool(%s): result leaked the other workspace's data: %s", ws, resp.GetResultJson())
		}
	}
}

// TestInvokeTool_MissingWorkspaceFailsCleanly asserts an unresolvable
// workspace is refused rather than silently answered from a default
// partition.
func TestInvokeTool_MissingWorkspaceFailsCleanly(t *testing.T) {
	srv := newInvokeToolFixture(t)

	_, err := srv.InvokeTool(context.Background(), &llmv1.InvokeToolRequest{
		ToolId:        "host_related",
		ArgumentsJson: `{"key":"src/only_in_a.go"}`,
	})
	if err == nil {
		t.Fatal("expected an error for a request with no tenant on incoming metadata")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

// wsScope binds a tenant on incoming metadata the way a real RPC does.
func wsScope(tenant string) context.Context {
	return serverutil.Scope{TenantID: tenant}.IncomingContext(context.Background())
}

// TestInvokeTool_MissingToolIDFailsCleanly asserts an empty tool_id is
// refused explicitly rather than falling through to a nil-def lookup.
func TestInvokeTool_MissingToolIDFailsCleanly(t *testing.T) {
	srv := newInvokeToolFixture(t)

	_, err := srv.InvokeTool(wsScope("ws-a"), &llmv1.InvokeToolRequest{})
	if err == nil {
		t.Fatal("expected an error for a request with no tool_id")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}
