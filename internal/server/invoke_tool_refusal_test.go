package server

import (
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// TestInvokeTool_UnknownToolIsRefusedGenerically covers s2-phase-3: a name
// that is not in the composed registry must fail with a fixed gRPC status
// code and a message naming only the requested tool — never a list of
// registered names, and never a gated-feature identifier (which would let a
// caller confirm a gated feature exists by deliberately naming it wrong).
//
// The same test also invokes a registered builtin (host_related) against the
// same server/fixture so the refusal assertions cannot pass by a server that
// simply refuses everything.
func TestInvokeTool_UnknownToolIsRefusedGenerically(t *testing.T) {
	srv := newInvokeToolFixture(t)

	const unknownName = "definitely_not_a_registered_tool"
	resp, err := srv.InvokeTool(wsScope("ws-a"), &llmv1.InvokeToolRequest{
		ToolId: unknownName,
	})
	if resp != nil {
		t.Fatalf("expected a nil response for an unknown tool, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected an error for an unregistered tool name")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected codes.NotFound, got %v (%v)", status.Code(err), err)
	}

	msg := err.Error()
	if !strings.Contains(msg, unknownName) {
		t.Fatalf("expected error to name the requested tool %q, got %q", unknownName, msg)
	}
	if strings.Contains(msg, ",") {
		t.Fatalf("error must not enumerate multiple names (no comma-separated list): %q", msg)
	}

	// Registered tool names must not leak into the refusal message either.
	reg := toolregistry.New()
	if _, err := InstallBuiltinTools(reg, toolexec.NewHandlers()); err != nil {
		t.Fatal(err)
	}
	for _, d := range reg.Snapshot() {
		n := d.GetName()
		if n == "" || n == unknownName {
			continue
		}
		if strings.Contains(msg, n) {
			t.Fatalf("refusal message leaked registered tool name %q: %q", n, msg)
		}
	}

	// Host-owned, gate-able identifiers must not leak into the refusal
	// message either, even though this fixture never registers them: the
	// message is constructed purely from the requested name, so this asserts
	// the construction itself, not merely today's registration state.
	gatedIdentifiers := []string{
		"gated_collection", // a host-owned collection name, gated by that host
		"gated_doc_kind",   // a host-owned document kind, gated by that host
	}
	for _, g := range gatedIdentifiers {
		if strings.Contains(msg, g) {
			t.Fatalf("refusal message leaked gated identifier %q: %q", g, msg)
		}
	}

	// Non-vacuity: a registered builtin still succeeds against the same
	// server instance and fixture workspace, so the refusal above cannot
	// be explained by a server that refuses every request.
	okResp, okErr := srv.InvokeTool(wsScope("ws-a"), &llmv1.InvokeToolRequest{
		ToolId:        "host_related",
		ArgumentsJson: `{"key":"src/only_in_a.go"}`,
	})
	if okErr != nil {
		t.Fatalf("registered tool call failed: %v", okErr)
	}
	if okResp == nil || okResp.GetIsError() {
		t.Fatalf("expected a successful response for a registered tool, got %+v", okResp)
	}
	if strings.TrimSpace(okResp.GetResultJson()) == "" {
		t.Fatal("expected a non-empty result_json from a registered tool call")
	}
}
