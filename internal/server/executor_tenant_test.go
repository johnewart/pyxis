package server

import (
	"context"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"google.golang.org/grpc"
)

// execStubStream is a ToolProviderService_ConnectServer that records frames,
// so a test can prove nothing was dispatched to the executor.
type execStubStream struct {
	grpc.ServerStream
	mu   sync.Mutex
	sent []*llmv1.ToolServerToProvider
}

func (s *execStubStream) Send(f *llmv1.ToolServerToProvider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, f)
	return nil
}
func (s *execStubStream) Recv() (*llmv1.ToolProviderToServer, error) { select {} }
func (s *execStubStream) Context() context.Context                   { return context.Background() }

func (s *execStubStream) frames() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

const execTenantTool = "tenant_a_deploy"

func newExecutorTenantFixture(t *testing.T) (*LlmServer, *execStubStream) {
	t.Helper()
	srv := newInvokeToolFixture(t)
	hub := toolprovider.NewHub()
	stream := &execStubStream{}
	def := &llmv1.ToolDefinition{
		Name:      execTenantTool,
		Execution: &llmv1.ToolDefinition_Executor{Executor: &llmv1.ToolExecutionExecutor{ExecutorId: "ex-a"}},
	}
	ack, detach := hub.HandleExecutorHello(serverutil.Scope{TenantID: "tenant-a", ScopeID: "s"},
		&llmv1.ExecutorHello{ExecutorId: "ex-a", InstanceId: "i1", Tools: []*llmv1.ToolDefinition{def}}, stream)
	t.Cleanup(detach)
	if len(ack.GetAcceptedTools()) != 1 {
		t.Fatalf("executor tool not accepted: %+v", ack)
	}
	srv.ToolProviderHub = hub
	return srv, stream
}

func hasTool(defs []*llmv1.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.GetName() == name {
			return true
		}
	}
	return false
}

func TestExecutorTenantListingIsTenantScoped(t *testing.T) {
	srv, _ := newExecutorTenantFixture(t)

	if !hasTool(srv.mergedToolDefinitions(wsScope("tenant-a"), ""), execTenantTool) {
		t.Fatal("tenant A should see its executor tool in mergedToolDefinitions")
	}
	found := false
	for _, e := range srv.effectiveToolProvenance(wsScope("tenant-a"), "") {
		if e.Def.GetName() == execTenantTool {
			found = true
		}
	}
	if !found {
		t.Fatal("tenant A should see its executor tool in effectiveToolProvenance")
	}
	if hasTool(srv.mergedToolDefinitions(wsScope("tenant-b"), ""), execTenantTool) {
		t.Fatal("tenant B must not see tenant A's executor tool")
	}
	if srv.toolAllowedForSession(wsScope("tenant-b"), "", execTenantTool) {
		t.Fatal("tenant B must not be allowed to run tenant A's executor tool")
	}
	if !srv.toolAllowedForSession(wsScope("tenant-a"), "", execTenantTool) {
		t.Fatal("tenant A should be allowed to run its executor tool")
	}
}

func TestExecutorTenantInvokeFromOtherTenantRefused(t *testing.T) {
	srv, stream := newExecutorTenantFixture(t)

	resp, err := srv.InvokeTool(wsScope("tenant-b"), &llmv1.InvokeToolRequest{
		ToolId:        execTenantTool,
		ArgumentsJson: `{}`,
	})
	if err == nil && !resp.GetIsError() {
		t.Fatalf("tenant B invoking tenant A's executor tool must be refused, got %+v", resp)
	}
	if n := stream.frames(); n != 0 {
		t.Fatalf("refused call must never reach the executor; %d frame(s) sent", n)
	}
}
