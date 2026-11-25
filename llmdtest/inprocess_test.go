// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest_test

import (
	"context"
	"encoding/json"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdtest"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
)

// TestStartInProcessRoundTrip exercises the exact sequence a host embedding
// llmd needs: construct llmd in-process with a host-owned tool registered,
// register the fake OpenAI provider, and open a session — all without a
// subprocess or a built llmd binary.
//
// This test lives in llmd's own module, so — unlike an external embedding
// host — it can reach into Env.Server's concrete internal type to assert
// the host tool actually landed in the live registry. An external caller
// only has Env.Client to work with, which is exactly what NewSession below
// exercises.
func TestStartInProcessRoundTrip(t *testing.T) {
	ctx := context.Background()

	registerHostTool := func(reg *toolregistry.Registry, handlers *toolseam.Handlers) error {
		if err := reg.Register(&llmv1.ToolDefinition{
			Name: "host_echo",
			Execution: &llmv1.ToolDefinition_InlineBuiltin{
				InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
			},
		}); err != nil {
			return err
		}
		return handlers.Register("host_echo", func(_ context.Context, args json.RawMessage, _ toolseam.Invocation) (toolseam.Result, error) {
			return toolseam.Result{Content: string(args)}, nil
		})
	}

	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{
		DataDir:       t.TempDir(),
		RegisterTools: registerHostTool,
	})
	if err != nil {
		t.Fatalf("StartInProcess: %v", err)
	}
	defer func() {
		if err := env.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	// RegisterLlmProvider (called internally by StartInProcess) must
	// have already succeeded, or Start would have returned an error above.
	// NewSession is the next workspace-scoped call in the same sequence —
	// this is the exact round trip that previously failed at the provider
	// registration step with a missing-tenant Unauthenticated error.
	sessCtx, err := env.NewSession(ctx, tenantscope.Scope{TenantID: "test-tenant", ScopeID: "test-scope"}, "test-session")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sessCtx == nil {
		t.Fatal("NewSession returned a nil context")
	}

	// The host's own tool must have made it into the live registry — this is
	// the actual point of StartInProcess. Both halves are checked: a definition
	// without a handler is advertised but fails to execute, and a handler
	// without a definition is never offered to a model.
	found := false
	for _, def := range env.Host.Tools().Snapshot() {
		if def.GetName() == "host_echo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("host_echo was not registered into the tool registry")
	}
	if _, ok := env.Host.Handlers().Lookup("host_echo"); !ok {
		t.Fatal("host_echo handler was not registered into the handler set")
	}
}
