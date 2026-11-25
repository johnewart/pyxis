package llmdlog

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetChatSink clears the process-wide file handle between cases, so one
// test's path cannot leak into the next.
func resetChatSink(t *testing.T) {
	t.Helper()
	CloseChatDebugFile()
	t.Cleanup(CloseChatDebugFile)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestChatDebugOffByDefault(t *testing.T) {
	resetChatSink(t)
	t.Setenv(EnvDebugChat, "")
	t.Setenv(EnvDebugChatFile, "")

	if ChatDebugEnabled() {
		t.Fatalf("chat debug must be off when neither %s nor %s is set", EnvDebugChat, EnvDebugChatFile)
	}
	out := captureStdout(t, func() { Chat("server", "unary_call", "should not appear") })
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output with chat debug off, got %q", out)
	}
}

func TestChatDebugToLog(t *testing.T) {
	resetChatSink(t)
	t.Setenv(EnvDebugChat, "1")
	t.Setenv(EnvDebugChatFile, "")

	out := captureStdout(t, func() {
		ChatFields("server", "unary_call", map[string]any{"method": "/llm.v1.LlmService/Chat"}, "unary call %s", "Chat")
	})

	var rec ChatRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("chat log line is not JSON (%v): %q", err, out)
	}
	if rec.Stream != "chat" {
		t.Fatalf("stream = %q, want %q", rec.Stream, "chat")
	}
	if rec.Event != "unary_call" {
		t.Fatalf("event = %q, want %q", rec.Event, "unary_call")
	}
	if rec.Message != "unary call Chat" {
		t.Fatalf("message = %q", rec.Message)
	}
	if rec.Fields["method"] != "/llm.v1.LlmService/Chat" {
		t.Fatalf("fields did not survive as typed JSON: %+v", rec.Fields)
	}
}

// The file is the point of the feature: one JSON object per line, appended,
// parseable without any llmd-specific reader.
func TestChatDebugFileIsNDJSON(t *testing.T) {
	resetChatSink(t)
	path := filepath.Join(t.TempDir(), "llmd-chat.log")
	t.Setenv(EnvDebugChat, "")
	t.Setenv(EnvDebugChatFile, path)

	if !ChatDebugEnabled() {
		t.Fatalf("%s alone must enable chat capture", EnvDebugChatFile)
	}

	// With only the file set, nothing goes to the process log.
	out := captureStdout(t, func() {
		Chat("server", "unary_begin", "first")
		Chat("server", "unary_done", "second")
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("records must not reach the log when only %s is set, got %q", EnvDebugChatFile, out)
	}
	CloseChatDebugFile()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chat file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), data)
	}
	wantEvents := []string{"unary_begin", "unary_done"}
	for i, line := range lines {
		var rec ChatRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not valid JSON (%v): %q", i, err, line)
		}
		if rec.Event != wantEvents[i] {
			t.Fatalf("line %d event = %q, want %q", i, rec.Event, wantEvents[i])
		}
		if rec.Timestamp == "" || rec.Component != "llmd" {
			t.Fatalf("line %d missing envelope fields: %+v", i, rec)
		}
	}
}

func TestChatDebugFileAppendsAcrossOpens(t *testing.T) {
	resetChatSink(t)
	path := filepath.Join(t.TempDir(), "llmd-chat.log")
	t.Setenv(EnvDebugChatFile, path)

	Chat("server", "first", "one")
	CloseChatDebugFile()
	Chat("server", "second", "two")
	CloseChatDebugFile()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chat file: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; got != 2 {
		t.Fatalf("reopening truncated the file instead of appending: %q", data)
	}
}

// A bad path must degrade to a warning, never take a chat turn (or the
// process) down with it.
func TestChatDebugFileUnwritablePathIsNotFatal(t *testing.T) {
	resetChatSink(t)
	// A path under a regular file cannot be opened as a file itself.
	bad := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(bad, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDebugChat, "")
	t.Setenv(EnvDebugChatFile, filepath.Join(bad, "chat.log"))

	Chat("server", "unary_call", "must not panic")
	Chat("server", "unary_call", "must not panic twice")
}

func TestDebugSettingsSummaryNamesEnabledSwitchesOnly(t *testing.T) {
	resetChatSink(t)
	t.Setenv(EnvDebug, "1")
	t.Setenv(EnvDebugChat, "")
	t.Setenv(EnvDebugChatFile, "/tmp/llmd-chat.log")
	t.Setenv(EnvDebugTraffic, "")
	t.Setenv(EnvDebugEmbeddings, "")

	s := ResolveDebugSettings()
	if !s.Any() {
		t.Fatalf("Any() = false with %s=1", EnvDebug)
	}
	got := s.Summary()
	if !strings.Contains(got, EnvDebug+"=1") {
		t.Fatalf("summary omits %s: %q", EnvDebug, got)
	}
	if !strings.Contains(got, EnvDebugChatFile+"=/tmp/llmd-chat.log") {
		t.Fatalf("summary omits the chat file: %q", got)
	}
	if strings.Contains(got, EnvDebugTraffic) || strings.Contains(got, EnvDebugEmbeddings) {
		t.Fatalf("summary named a switch that is off: %q", got)
	}
}

func TestDebugFlagAcceptsTruthyValues(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(EnvDebug, v)
		if !DebugEnabled() {
			t.Fatalf("%s=%q should enable debug", EnvDebug, v)
		}
	}
	for _, v := range []string{"", "0", "false", "off", "no"} {
		t.Setenv(EnvDebug, v)
		if DebugEnabled() {
			t.Fatalf("%s=%q should NOT enable debug", EnvDebug, v)
		}
	}
}
