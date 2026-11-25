// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest_test

import (
	"context"
	"io"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdtest"
	"github.com/johnewart/pyxis/tenantscope"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestTenantIsolationInProcess drives two tenants that share a scope ID and a
// session ID over real gRPC and checks neither sees the other's history.
func TestTenantIsolationInProcess(t *testing.T) {
	ctx := context.Background()
	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("StartInProcess: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	const sessionID = "shared-session"
	const secret = "alpha-secret-reply"
	scopeA := tenantscope.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	scopeB := tenantscope.Scope{TenantID: "tenant-b", ScopeID: "s1"}

	ensure := func(sc tenantscope.Scope) context.Context {
		t.Helper()
		// NewSession registers the fake provider tenant-wide for this tenant.
		if _, err := env.NewSession(ctx, sc, "provider-bootstrap"); err != nil {
			t.Fatalf("NewSession(%s): %v", sc, err)
		}
		sctx := sc.OutgoingContext(ctx)
		model, provider := "fake-model", env.ProviderID
		if _, err := env.Client.EnsureSession(sctx, &llmv1.EnsureSessionRequest{
			SessionId:     sessionID,
			LlmProviderId: &provider,
			DefaultModel:  &model,
		}); err != nil {
			t.Fatalf("EnsureSession(%s): %v", sc, err)
		}
		return sctx
	}
	ctxA := ensure(scopeA)
	ctxB := ensure(scopeB)

	t.Run("history isolated", func(t *testing.T) {
		env.FakeOpenAI.Enqueue(llmdtest.TextTurn(secret))
		sid := sessionID
		st, err := env.Client.ChatStream(ctxA, &llmv1.ChatStreamRequest{
			Messages:       []*llmv1.Message{{Role: "user", Content: "hello from a", Id: "ua1"}},
			SessionId:      &sid,
			PersistSession: true,
		})
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		for {
			if _, err := st.Recv(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("Recv: %v", err)
			}
		}

		contains := func(c context.Context) bool {
			t.Helper()
			resp, err := env.Client.GetSessionHistory(c, &llmv1.GetSessionHistoryRequest{SessionId: sessionID})
			if err != nil {
				t.Fatalf("GetSessionHistory: %v", err)
			}
			for _, m := range resp.GetMessages() {
				if strings.Contains(m.GetContent(), secret) || strings.Contains(m.GetContent(), "hello from a") {
					return true
				}
			}
			return false
		}
		if !contains(ctxA) {
			t.Error("tenant A history missing its own turn")
		}
		if contains(ctxB) {
			t.Error("tenant B history leaked tenant A content")
		}
	})

	t.Run("old workspace key rejected", func(t *testing.T) {
		oldCtx := metadata.AppendToOutgoingContext(ctx, "llmd-workspace-id", "tenant-a")
		_, err := env.Client.GetSessionHistory(oldCtx, &llmv1.GetSessionHistoryRequest{SessionId: sessionID})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("got %v, want Unauthenticated", err)
		}
	})
}
