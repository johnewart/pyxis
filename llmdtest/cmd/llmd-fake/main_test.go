// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/tenantscope"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// blockingReader blocks until its context is done, like an open stdin.
type blockingReader struct{ ctx context.Context }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, io.EOF
}

func TestRunExitsWhenStdinCloses(t *testing.T) {
	pr, pw := io.Pipe()
	sr, sw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := run(context.Background(), []string{"-listen", "127.0.0.1:0"}, sr, pw)
		_ = pw.Close()
		done <- err
	}()
	if _, err := bufio.NewReader(pr).ReadString('\n'); err != nil {
		t.Fatalf("read ready line: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if err := sw.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not exit after stdin closed")
	}
}

func TestRunScriptedTurnOverTCP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := run(ctx, []string{"-listen", "127.0.0.1:0"}, blockingReader{ctx}, pw)
		_ = pw.Close()
		done <- err
	}()
	stopped := false
	stopRun := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("run did not exit after cancel")
		}
	}
	defer stopRun()

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("read ready line: %v", err)
	}
	var ready readyLine
	if err := json.Unmarshal([]byte(line), &ready); err != nil {
		t.Fatalf("ready line %q: %v", line, err)
	}
	if ready.GRPCAddr == "" || ready.GRPCAddr == "bufconn" || ready.FakeURL == "" || ready.ProviderID == "" {
		t.Fatalf("incomplete ready line: %+v", ready)
	}
	// Keep draining so run never blocks on stdout.
	go func() { _, _ = io.Copy(io.Discard, pr) }()

	post := func(body string) int {
		t.Helper()
		resp, err := http.Post(ready.FakeURL+"/_script", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post script: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"turns":[{"kind":"bogus"}]}`); code != http.StatusBadRequest {
		t.Fatalf("bad script status = %d, want 400", code)
	}
	if code := post(`{"turns":[{"kind":"text","deltas":["hello ","tcp"],"usage":{"prompt":3,"completion":2}}]}`); code != http.StatusOK {
		t.Fatalf("script status = %d, want 200", code)
	}

	conn, err := grpc.NewClient(ready.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := llmv1.NewLlmServiceClient(conn)
	sc := tenantscope.Scope{TenantID: ready.TenantID, ScopeID: "s1"}
	cctx, ccancel := context.WithTimeout(sc.OutgoingContext(context.Background()), 30*time.Second)
	defer ccancel()

	provider, model, sid := ready.ProviderID, "fake-model", "tcp-session"
	if _, err := client.EnsureSession(cctx, &llmv1.EnsureSessionRequest{
		SessionId: sid, LlmProviderId: &provider, DefaultModel: &model,
	}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	st, err := client.ChatStream(cctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId: &sid,
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

	resp, err := http.Get(ready.FakeURL + "/_requests")
	if err != nil {
		t.Fatalf("get requests: %v", err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	if _, err := io.Copy(&raw, resp.Body); err != nil {
		t.Fatalf("read requests: %v", err)
	}
	var got struct {
		Requests []map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(raw.Bytes(), &got); err != nil {
		t.Fatalf("decode requests: %v", err)
	}
	if len(got.Requests) == 0 {
		t.Fatal("no model requests recorded")
	}

	stopRun()
}
