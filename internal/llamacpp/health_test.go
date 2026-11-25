/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthWaitForServerReadyPollsUntilOK(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected probe path %q", r.URL.Path)
		}
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The /v1 suffix must be stripped before /health is appended.
	err := WaitForServerReady(context.Background(), srv.URL+"/v1", WaitOptions{
		PollInterval: time.Millisecond,
		MaxWait:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitForServerReady: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected at least 3 probes, got %d", got)
	}
}

func TestHealthWaitForServerReadyCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := WaitForServerReady(ctx, srv.URL, WaitOptions{
		PollInterval: 5 * time.Millisecond,
		MaxWait:      ServerReadyMaxWait,
	})
	if err == nil {
		t.Fatal("expected a context error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("returned only after %v; cancellation should be immediate", elapsed)
	}
}

// lookupShell finds a shell for the exit-during-startup test. No real
// llama-server binary is ever involved.
func lookupShell() (string, error) { return exec.LookPath("sh") }

type fakeProc struct {
	code *int
	tail string
}

func (f fakeProc) ExitCode() *int     { return f.code }
func (f fakeProc) StderrTail() string { return f.tail }

func TestHealthStartupExitCarriesStderrTail(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(RegistryOptions{StateDir: dir, TerminateTimeout: time.Second})
	sh, err := lookupShell()
	if err != nil {
		t.Skipf("no shell available: %v", err)
	}

	const marker = "STARTUP-FAILURE-MARKER"
	if _, err := reg.EnsureStarted(context.Background(), SpawnSpec{
		CacheKey:   "boom",
		BinaryPath: sh,
		Args:       []string{"-c", "echo " + marker + " 1>&2; exit 7"},
	}); err != nil {
		t.Fatalf("EnsureStarted: %v", err)
	}

	child := reg.child("boom")
	if child == nil {
		t.Fatal("expected a managed child")
	}
	select {
	case <-child.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit")
	}

	err = ProcessStartupError(child)
	if err == nil {
		t.Fatal("expected a startup error for an exited child")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("error missing exit code: %v", err)
	}
	if !strings.Contains(err.Error(), marker) {
		t.Errorf("error missing stderr tail marker: %v", err)
	}
}

func TestHealthProcessStartupErrorNilWhileRunning(t *testing.T) {
	if err := ProcessStartupError(fakeProc{}); err != nil {
		t.Fatalf("running process should not error: %v", err)
	}
	if err := ProcessStartupError(nil); err != nil {
		t.Fatalf("nil probe should not error: %v", err)
	}
}

func TestHealthParseModelsPayload(t *testing.T) {
	body := `{"data":[
	  {"id":"a","path":"/m/a.gguf","status":{"value":"loaded"}},
	  {"id":"b","status":{"value":"loading"}},
	  {"id":"c","status":{"value":"loaded","failed":true,"exit_code":9}},
	  {"id":"d","status":{"value":"something-else"}},
	  {"id":"","path":""}
	]}`
	entries := ParseModelsPayload([]byte(body))
	if len(entries) != 4 {
		t.Fatalf("expected 4 rows (empty row dropped), got %d", len(entries))
	}
	want := []ModelLoadState{ModelLoaded, ModelLoading, ModelFailed, ModelUnknown}
	for i, state := range want {
		if entries[i].Status != state {
			t.Errorf("row %d: status = %q, want %q", i, entries[i].Status, state)
		}
	}
	if entries[2].ExitCode == nil || *entries[2].ExitCode != 9 {
		t.Errorf("failed row should carry exit code 9, got %v", entries[2].ExitCode)
	}
	if ParseModelsPayload([]byte(`not json`)) != nil {
		t.Error("garbage payload should parse to no rows")
	}
}

func TestHealthModelEntryMatchesCandidate(t *testing.T) {
	entry := ModelEntry{ID: "Gemma-3-12B.gguf", Path: "/models/Gemma-3-12B.gguf"}
	cases := []struct {
		name       string
		candidates []string
		want       bool
	}{
		{"exact id", []string{"Gemma-3-12B.gguf"}, true},
		{"stem", []string{"gemma-3-12b"}, true},
		{"path, case-insensitive", []string{"/MODELS/gemma-3-12B.GGUF"}, true},
		{"unrelated", []string{"llama-7b"}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModelEntryMatchesCandidate(entry, tc.candidates); got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}
}

func modelsServer(t *testing.T, bodies []string) *httptest.Server {
	t.Helper()
	var idx int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		i := int(atomic.AddInt32(&idx, 1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	}))
}

func TestHealthWaitForModelLoaded(t *testing.T) {
	cases := []struct {
		name    string
		bodies  []string
		wantErr string
	}{
		{
			name: "loading then loaded",
			bodies: []string{
				`{"data":[{"id":"m.gguf","status":{"value":"loading"}}]}`,
				`{"data":[{"id":"m.gguf","status":{"value":"loaded"}}]}`,
			},
		},
		{
			name:   "sleeping counts as loaded",
			bodies: []string{`{"data":[{"id":"m.gguf","status":{"value":"sleeping"}}]}`},
		},
		{
			name:    "failed names the exit code",
			bodies:  []string{`{"data":[{"id":"m.gguf","status":{"value":"loading","failed":true,"exit_code":3}}]}`},
			wantErr: "exit 3",
		},
		{
			name:    "absent row stays unknown until the cap",
			bodies:  []string{`{"data":[]}`},
			wantErr: "did not finish loading",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := modelsServer(t, tc.bodies)
			defer srv.Close()
			err := WaitForModelLoaded(context.Background(), srv.URL+"/v1", []string{"m"}, ModelWaitOptions{
				WaitOptions: WaitOptions{PollInterval: time.Millisecond, MaxWait: 300 * time.Millisecond},
			})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("WaitForModelLoaded: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
