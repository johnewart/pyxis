package toolprovider

import (
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

func execDef(name, executorID string) *llmv1.ToolDefinition {
	d := &llmv1.ToolDefinition{Name: name}
	if executorID != "" {
		d.Execution = &llmv1.ToolDefinition_Executor{Executor: &llmv1.ToolExecutionExecutor{ExecutorId: executorID}}
	}
	return d
}

func hello(executorID, instanceID string, defs ...*llmv1.ToolDefinition) *llmv1.ExecutorHello {
	return &llmv1.ExecutorHello{ExecutorId: executorID, InstanceId: instanceID, Tools: defs}
}

func tenantScope(t string) serverutil.Scope { return serverutil.Scope{TenantID: t, ScopeID: "s"} }

func rejectedReason(ack *llmv1.ExecutorHelloAck, name string) string {
	for _, r := range ack.GetRejectedTools() {
		if r.GetName() == name {
			return r.GetReason()
		}
	}
	return ""
}

func accepted(ack *llmv1.ExecutorHelloAck, name string) bool {
	for _, n := range ack.GetAcceptedTools() {
		if n == name {
			return true
		}
	}
	return false
}

func TestExecutorRegistryAcceptsAndRejectsInline(t *testing.T) {
	h := NewHub()
	h.InlineNames = func() map[string]struct{} { return map[string]struct{}{"read_file": {}} }
	ack, detach := h.HandleExecutorHello(tenantScope("t1"), hello("ex", "i1", execDef("deploy", "ex"), execDef("read_file", "ex")), newStub())
	defer detach()
	if !accepted(ack, "deploy") {
		t.Fatalf("deploy not accepted: %v", ack)
	}
	if accepted(ack, "read_file") || rejectedReason(ack, "read_file") == "" {
		t.Fatalf("read_file should be rejected with reason: %v", ack)
	}
	if ack.GetMaxResultBytes() != MaxResultBytes {
		t.Fatalf("max_result_bytes=%d", ack.GetMaxResultBytes())
	}
}

func TestExecutorRegistrySameTenantConflict(t *testing.T) {
	h := NewHub()
	_, d1 := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i1", execDef("deploy", "a")), newStub())
	defer d1()
	ack, d2 := h.HandleExecutorHello(tenantScope("t1"), hello("b", "i2", execDef("deploy", "b")), newStub())
	defer d2()
	if accepted(ack, "deploy") || rejectedReason(ack, "deploy") == "" {
		t.Fatalf("second executor should be rejected: %v", ack)
	}
}

func TestExecutorRegistryOtherTenantMayClaim(t *testing.T) {
	h := NewHub()
	_, d1 := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i1", execDef("deploy", "a")), newStub())
	defer d1()
	ack, d2 := h.HandleExecutorHello(tenantScope("t2"), hello("b", "i2", execDef("deploy", "b")), newStub())
	defer d2()
	if !accepted(ack, "deploy") {
		t.Fatalf("other tenant should claim: %v", ack)
	}
}

func TestExecutorRegistryBindingMismatch(t *testing.T) {
	h := NewHub()
	ack, d := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i1", execDef("wrong", "zzz"), execDef("absent", "")), newStub())
	defer d()
	if rejectedReason(ack, "wrong") == "" || rejectedReason(ack, "absent") == "" {
		t.Fatalf("binding mismatches should be rejected: %v", ack)
	}
	if len(ack.GetAcceptedTools()) != 0 {
		t.Fatalf("nothing should be accepted: %v", ack)
	}
}

func TestExecutorRegistryEmptyTenantRefused(t *testing.T) {
	h := NewHub()
	ack, d := h.HandleExecutorHello(tenantScope("  "), hello("a", "i1", execDef("deploy", "a")), newStub())
	defer d()
	if len(ack.GetAcceptedTools()) != 0 || rejectedReason(ack, "deploy") == "" {
		t.Fatalf("empty tenant should be refused: %v", ack)
	}
}

func TestExecutorRegistrySupersedeDrainsOldStream(t *testing.T) {
	h := NewHub()
	_, d1 := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i1", execDef("deploy", "a")), newStub())
	old := h.execReg.executors[executorKey{"t1", "a"}].stream
	ch := make(chan Result, 1)
	h.pendMu.Lock()
	h.pending["e1"] = ch
	h.pendMu.Unlock()
	old.pendMu.Lock()
	old.pending["e1"] = ch
	old.pendMu.Unlock()

	ack, d2 := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i2", execDef("deploy", "a")), newStub())
	defer d2()
	if !accepted(ack, "deploy") {
		t.Fatalf("new instance should own deploy: %v", ack)
	}
	select {
	case r := <-ch:
		if !r.disconnected {
			t.Fatalf("expected disconnected result, got %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter on superseded stream not failed within 1s")
	}

	// (g) stale detach from the superseded instance is a no-op.
	d1()
	if got := h.execReg.executors[executorKey{"t1", "a"}]; got == nil || got.instanceID != "i2" {
		t.Fatalf("new entry removed by stale detach: %+v", got)
	}
	if h.execReg.owners["t1"]["deploy"] != "a" {
		t.Fatal("ownership lost after stale detach")
	}
	ack3, d3 := h.HandleExecutorHello(tenantScope("t1"), hello("b", "i3", execDef("deploy", "b")), newStub())
	defer d3()
	if accepted(ack3, "deploy") {
		t.Fatal("other executor claimed name still owned by new instance")
	}
}

func TestExecutorRegistryDetachLeavesOwnerlessDefs(t *testing.T) {
	h := NewHub()
	_, d1 := h.HandleExecutorHello(tenantScope("t1"), hello("a", "i1", execDef("zeta", "a"), execDef("alpha", "a")), newStub())
	d1()
	defs := h.ExecutorDefinitions("t1")
	if len(defs) != 2 || defs[0].GetName() != "alpha" || defs[1].GetName() != "zeta" {
		t.Fatalf("defs after detach = %v", defs)
	}
	ack, d2 := h.HandleExecutorHello(tenantScope("t1"), hello("b", "i2", execDef("alpha", "b")), newStub())
	defer d2()
	if !accepted(ack, "alpha") {
		t.Fatalf("fresh executor should claim ownerless name: %v", ack)
	}
	if len(h.ExecutorDefinitions("t2")) != 0 {
		t.Fatal("defs leaked across tenants")
	}
}
