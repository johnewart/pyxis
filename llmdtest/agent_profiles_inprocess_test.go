// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest_test

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdtest"
	"github.com/johnewart/pyxis/tenantscope"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestStartInProcessWiresAgentProfiles proves the in-process composition
// root wires the dynamic agent-profile tier. An LlmServer without
// AgentProfileRepo answers CreateAgentProfile with codes.FailedPrecondition
// ("dynamic agent profiles not configured"); a wired one persists the
// profile and hands it back through GetAgentProfile.
//
// Like inprocess_test.go this lives in llmd's own module, so it may also
// reach into Env.Server's concrete type to assert the field itself is set.
func TestStartInProcessWiresAgentProfiles(t *testing.T) {
	ctx := context.Background()

	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("StartInProcess: %v", err)
	}
	defer func() {
		if err := env.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	// The dynamic-tier wiring is asserted through the RPCs below rather than by
	// reaching in for a repository field: the round trip is what a consumer
	// actually depends on, and it fails loudly (FailedPrecondition) when the
	// tier is unwired — which is precisely the signal the field check was
	// standing in for.

	// CreateAgentProfile is workspace-scoped: without this metadata it
	// returns Unauthenticated, which would mask the wiring signal.
	wsCtx := tenantscope.Scope{TenantID: "inproc-ws"}.OutgoingContext(ctx)

	created, err := env.Client.CreateAgentProfile(wsCtx, &llmv1.CreateAgentProfileRequest{Name: "inproc-agent"})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			t.Fatalf("CreateAgentProfile returned FailedPrecondition — dynamic tier not wired in StartInProcess: %v", err)
		}
		t.Fatalf("CreateAgentProfile: %v", err)
	}
	id := created.GetProfile().GetId()
	if id == "" {
		t.Fatal("CreateAgentProfile returned a profile with an empty id")
	}

	got, err := env.Client.GetAgentProfile(wsCtx, &llmv1.GetAgentProfileRequest{Id: id})
	if err != nil {
		t.Fatalf("GetAgentProfile(%q): %v", id, err)
	}
	p := got.GetProfile()
	if p.GetIsStatic() {
		t.Fatalf("GetAgentProfile(%q): IsStatic = true, want false for a dynamic profile", id)
	}
	if p.GetName() != "inproc-agent" {
		t.Fatalf("GetAgentProfile(%q): Name = %q, want %q", id, p.GetName(), "inproc-agent")
	}
}
