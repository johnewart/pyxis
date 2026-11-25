/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package server

import (
	"context"
	"sort"
	"testing"

	llamacppv1 "github.com/johnewart/pyxis/gen/llamacpp/v1"
	"github.com/johnewart/pyxis/internal/llamacpp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The llamacpp service must be REGISTERED on the gRPC server, not merely
// defined: a client discovers it by name, and an unregistered service is
// indistinguishable from a missing feature.
func TestLlamaCppServiceIsRegistered(t *testing.T) {
	srv := grpc.NewServer()
	llamacppv1.RegisterLlamaCppServiceServer(srv, &LlamaCppServer{})

	info, ok := srv.GetServiceInfo()["llamacpp.v1.LlamaCppService"]
	if !ok {
		t.Fatalf("llamacpp.v1.LlamaCppService not registered; got %v", srv.GetServiceInfo())
	}

	got := make([]string, 0, len(info.Methods))
	for _, m := range info.Methods {
		got = append(got, m.Name)
	}
	sort.Strings(got)

	want := []string{
		"Download",
		"EnsureBinary",
		"ListModels",
		"SetManagedState",
		"Start",
		"Status",
		"StatusStream",
		"Stop",
	}
	if len(got) != len(want) {
		t.Fatalf("method count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("methods = %v, want %v", got, want)
		}
	}
}

// Provisioning RPCs have not landed yet: they must fail loudly as Unimplemented
// rather than returning a plausible-looking zero value.
func TestLlamaCppUnimplementedReturnsUnimplemented(t *testing.T) {
	var s LlamaCppServer
	_, err := s.EnsureBinary(context.Background(), &llamacppv1.EnsureBinaryRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("EnsureBinary code = %v, want %v (err=%v)", got, codes.Unimplemented, err)
	}
}

// A handler with no lifecycle service must say the runtime is unavailable — not
// invent an idle status, which a client would render as a healthy stopped
// server.
func TestLlamaCppStatusWithoutServiceIsUnavailable(t *testing.T) {
	var s LlamaCppServer
	_, err := s.Status(context.Background(), &llamacppv1.StatusRequest{})
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("Status code = %v, want %v (err=%v)", got, codes.Unavailable, err)
	}
}

// Status delegates to internal/llamacpp: the app-global active model set there
// is what the RPC reports.
func TestLlamaCppStatusDelegatesToService(t *testing.T) {
	dir := t.TempDir()
	svc := llamacpp.NewService(llamacpp.ServiceOptions{
		Registry: llamacpp.NewRegistry(llamacpp.RegistryOptions{StateDir: dir}),
		State:    llamacpp.NewManagedStateStore(dir),
	})
	if _, err := svc.SetActiveModel("pinned-model"); err != nil {
		t.Fatalf("SetActiveModel: %v", err)
	}

	got, err := NewLlamaCppServer(svc).Status(context.Background(), &llamacppv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.GetActiveModelId() != "pinned-model" {
		t.Errorf("active model = %q, want %q", got.GetActiveModelId(), "pinned-model")
	}
	if got.GetPhase() != llamacppv1.LlamaPhase_LLAMA_PHASE_IDLE {
		t.Errorf("phase = %v, want IDLE", got.GetPhase())
	}
	if !got.GetEnabled() {
		t.Error("unset enabled flag must report enabled")
	}
}
