package claudecodeclillm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClaudeStreamInitFromLine_controlResponseCompletesHandshake(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !claudeStreamInitFromLine(context.Background(), raw) {
		t.Fatal("control_response must complete init handshake")
	}
}

func TestIsClaudeControlResponseLine_hugeInitializeCatalog(t *testing.T) {
	t.Parallel()
	commands := make([]map[string]any, 0, 200)
	for i := 0; i < 200; i++ {
		commands = append(commands, map[string]any{
			"name":        fmt.Sprintf("skill-%d", i),
			"description": strings.Repeat("x", 512),
		})
	}
	raw, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success",
			"response": map[string]any{
				"commands": commands,
				"agents":   []any{map[string]any{"name": "Explore"}},
				"models":   []any{map[string]any{"value": "haiku"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isClaudeControlResponseLine(raw) {
		t.Fatalf("expected huge control_response to be recognized (%d bytes)", len(raw))
	}
}

func TestWaitForClaudeStreamInit_buffersInitBeforeControlResponse(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 2)
	initLine, err := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "sess-buffered"})
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]map[string]any, 0, 50)
	for i := 0; i < 50; i++ {
		commands = append(commands, map[string]any{"name": fmt.Sprintf("cmd-%d", i), "description": "d"})
	}
	ctrl, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":  "success",
			"response": map[string]any{"commands": commands},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch <- streamJSONLine{line: initLine}
	ch <- streamJSONLine{line: ctrl}
	close(ch)
	src := newStreamJSONLineSource(ch)
	if err := waitForClaudeStreamInitFromSource(context.Background(), src, func() string { return "" }, make(chan struct{})); err != nil {
		t.Fatalf("waitForClaudeStreamInitFromSource: %v", err)
	}
	if !lineSrcHasBufferedSystemInit(src) {
		t.Fatal("expected buffered system/init after handshake")
	}
}

func TestMaybeCaptureClaudeBackendSessionID_systemInit(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "sess-xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	var captured string
	ctx := WithBackendSessionSink(context.Background(), func(id string) { captured = id })
	maybeCaptureClaudeBackendSessionID(ctx, raw)
	if captured != "sess-xyz" {
		t.Fatalf("session_id sink: got %q want sess-xyz", captured)
	}
}

func TestWaitForClaudeStreamInit_completesOnControlResponse(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 1)
	ctrl, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success"}})
	if err != nil {
		t.Fatal(err)
	}
	ch <- streamJSONLine{line: ctrl}
	close(ch)
	if err := waitForClaudeStreamInit(context.Background(), ch, func() string { return "" }, make(chan struct{})); err != nil {
		t.Fatalf("waitForClaudeStreamInit: %v", err)
	}
}

func TestWaitForClaudeStreamInit_capturesSessionFromInitBeforeControlResponse(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 2)
	initLine, err := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "sess-early"})
	if err != nil {
		t.Fatal(err)
	}
	ctrl, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success"}})
	if err != nil {
		t.Fatal(err)
	}
	ch <- streamJSONLine{line: initLine}
	ch <- streamJSONLine{line: ctrl}
	close(ch)
	var captured string
	ctx := WithBackendSessionSink(context.Background(), func(id string) { captured = id })
	if err := waitForClaudeStreamInit(ctx, ch, func() string { return "" }, make(chan struct{})); err != nil {
		t.Fatalf("waitForClaudeStreamInit: %v", err)
	}
	if captured != "sess-early" {
		t.Fatalf("session_id sink: got %q want sess-early", captured)
	}
}

func TestClaudeStreamInitFromLine_systemInitDoesNotCompleteHandshake(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "sess-abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	var captured string
	ctx := WithBackendSessionSink(context.Background(), func(id string) { captured = id })
	if claudeStreamInitFromLine(ctx, raw) {
		t.Fatal("system/init alone must not complete init handshake")
	}
	if captured != "sess-abc" {
		t.Fatalf("session_id sink: got %q want sess-abc", captured)
	}
}

func TestWaitForClaudeSystemInitLine_buffersUntilInit(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 2)
	status, err := json.Marshal(map[string]any{"type": "system", "subtype": "status", "status": "requesting"})
	if err != nil {
		t.Fatal(err)
	}
	initLine, err := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "sess-bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	ch <- streamJSONLine{line: status}
	ch <- streamJSONLine{line: initLine}
	close(ch)
	src := newStreamJSONLineSource(ch)
	var captured string
	ctx := WithBackendSessionSink(context.Background(), func(id string) { captured = id })
	if err := waitForClaudeSystemInitLine(ctx, src, func() string { return "" }, make(chan struct{})); err != nil {
		t.Fatalf("waitForClaudeSystemInitLine: %v", err)
	}
	if captured != "sess-bootstrap" {
		t.Fatalf("session_id sink: got %q want sess-bootstrap", captured)
	}
	sl, err := src.read(context.Background())
	if err != nil {
		t.Fatalf("read buffered status line: %v", err)
	}
	if !strings.Contains(string(sl.line), "requesting") {
		t.Fatalf("expected buffered status line, got %s", sl.line)
	}
}

func TestWaitForClaudeStreamInit_readsInitLine(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 1)
	ctrl, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success"}})
	if err != nil {
		t.Fatal(err)
	}
	ch <- streamJSONLine{line: ctrl}
	close(ch)
	if err := waitForClaudeStreamInit(context.Background(), ch, func() string { return "" }, make(chan struct{})); err != nil {
		t.Fatalf("waitForClaudeStreamInit: %v", err)
	}
}

func TestWaitForClaudeStreamInit_processExitIncludesStderr(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine)
	close(ch)
	procDone := make(chan struct{})
	close(procDone)
	err := waitForClaudeStreamInit(context.Background(), ch, func() string { return "boom" }, procDone)
	if err == nil {
		t.Fatal("expected error when process exits before init")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestKickClaudeStreamJSONHandshake_readsInitAfterInitialize(t *testing.T) {
	t.Parallel()
	// io.Pipe deadlocks here: kick writes initialize to stdin before returning, but
	// nothing reads the pipe until after the handshake completes.
	var stdin bytes.Buffer
	ch := make(chan streamJSONLine, 2)
	ctrl, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		ch <- streamJSONLine{line: ctrl}
		close(ch)
	}()
	procDone := make(chan struct{})
	lineSrc := newStreamJSONLineSource(ch)
	if err := kickClaudeStreamJSONHandshake(context.Background(), &stdin, lineSrc, func() string { return "" }, procDone); err != nil {
		t.Fatalf("kickClaudeStreamJSONHandshake: %v", err)
	}
	var initReq map[string]any
	if err := json.Unmarshal(stdin.Bytes(), &initReq); err != nil {
		t.Fatalf("read initialize frame: %v", err)
	}
	if initReq["type"] != "control_request" {
		t.Fatalf("expected control_request on stdin, got %#v", initReq["type"])
	}
}

func TestDrainStreamJSONLineSource_exitsWhenProcessDone(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine)
	src := newStreamJSONLineSource(ch)
	procDone := make(chan struct{})
	done := make(chan struct{})
	go func() {
		drainStreamJSONLineSource(src, procDone)
		close(done)
	}()
	close(procDone)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drainStreamJSONLineSource blocked after process done")
	}
}
