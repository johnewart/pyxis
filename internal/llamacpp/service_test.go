/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// readyServer answers /health OK and reports the model as loaded, standing in
// for a real llama-server. No binary is downloaded and nothing hits the network.
func readyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"m.gguf","status":{"value":"loaded"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestService(t *testing.T, srv *httptest.Server) *Service {
	t.Helper()
	dir := t.TempDir()
	return NewService(ServiceOptions{
		Registry:   NewRegistry(RegistryOptions{StateDir: dir, TerminateTimeout: time.Second}),
		State:      NewManagedStateStore(dir),
		HTTPClient: srv.Client(),
		ReadyWait:  WaitOptions{PollInterval: time.Millisecond, MaxWait: 5 * time.Second},
		ModelWait:  ModelWaitOptions{WaitOptions: WaitOptions{PollInterval: time.Millisecond, MaxWait: 5 * time.Second}},
		BaseURLFor: func(int) string { return srv.URL + "/v1" },
	})
}

// testChildBinary is a stand-in llama-server: a script that ignores whatever
// arguments the service passes and stays alive until it is stopped.
func testChildBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no shell available: %v", err)
	}
	path := filepath.Join(t.TempDir(), "fake-llama-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

func TestStatusStartStopLifecycle(t *testing.T) {
	srv := readyServer(t)
	svc := newTestService(t, srv)

	if got := svc.Status().Phase; got != PhaseIdle {
		t.Fatalf("initial phase = %v, want idle", got)
	}

	ctx := context.Background()
	status, err := svc.Start(ctx, StartSpec{
		ModelID:    "m.gguf",
		ModelDir:   t.TempDir(),
		BinaryPath: testChildBinary(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.Phase != PhaseReady {
		t.Fatalf("phase = %v, want ready (%s)", status.Phase, status.Error)
	}
	if status.BaseURL != srv.URL+"/v1" {
		t.Errorf("base url = %q", status.BaseURL)
	}
	if status.PID == 0 {
		t.Error("expected a recorded pid for the running child")
	}
	if svc.Status().Phase != PhaseReady {
		t.Error("recorded state should stay ready after Start")
	}

	stopped, err := svc.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.Phase == PhaseReady {
		t.Fatal("Stop must return a non-running status")
	}
	if stopped.PID != 0 {
		t.Errorf("stopped status still carries pid %d", stopped.PID)
	}
}

func TestStatusReportsActiveModelID(t *testing.T) {
	srv := readyServer(t)
	svc := newTestService(t, srv)

	if _, err := svc.SetActiveModel("  pinned-model  "); err != nil {
		t.Fatalf("SetActiveModel: %v", err)
	}
	status := svc.Status()
	if status.ActiveModelID != "pinned-model" {
		t.Fatalf("active model = %q, want %q", status.ActiveModelID, "pinned-model")
	}
	if !status.Enabled {
		t.Error("unset enabled flag should report enabled")
	}

	if _, err := svc.SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if svc.Status().Enabled {
		t.Error("expected Status to report the disabled flag")
	}
}

func TestStatusStreamEmitsOrderedPhases(t *testing.T) {
	srv := readyServer(t)
	svc := newTestService(t, srv)

	stream, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	var observed []Phase
	go func() {
		defer close(done)
		for status := range stream {
			observed = append(observed, status.Phase)
			if status.Phase == PhaseReady || status.Phase == PhaseError {
				return
			}
		}
	}()

	if _, err := svc.Start(context.Background(), StartSpec{
		ModelID:    "m.gguf",
		ModelDir:   t.TempDir(),
		BinaryPath: testChildBinary(t),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("status stream did not terminate at ready")
	}

	if len(observed) == 0 {
		t.Fatal("expected at least one status snapshot")
	}
	// Never assert a message count: this is newest-state fan-out, so
	// intermediate snapshots may be coalesced. Assert ordering and terminal
	// state instead.
	for i := 1; i < len(observed); i++ {
		if observed[i] < observed[i-1] {
			t.Fatalf("phase moved backwards: %v then %v", observed[i-1], observed[i])
		}
	}
	if observed[len(observed)-1] != PhaseReady {
		t.Fatalf("terminal phase = %v, want ready", observed[len(observed)-1])
	}
	// The startup ticks must actually reach the stream, not just the terminal
	// state — the UI renders these phases.
	seen := map[Phase]bool{}
	for _, phase := range observed {
		seen[phase] = true
	}
	for _, want := range []Phase{PhaseStarting, PhaseWaitingHealth, PhaseLoadingModel} {
		if !seen[want] {
			t.Errorf("phase %v never reached the stream (observed %v)", want, observed)
		}
	}
}
