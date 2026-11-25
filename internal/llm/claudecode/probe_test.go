package claudecodeclillm

import (
	"strings"
	"testing"
)

func TestParseClaudeJSONSessionID(t *testing.T) {
	t.Parallel()
	stdout := `{"type":"result","subtype":"success","session_id":"sess-abc-123","result":"ok"}`
	sid, err := parseClaudeJSONSessionID(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if sid != "sess-abc-123" {
		t.Fatalf("session_id=%q", sid)
	}
}

func TestParseClaudeJSONSessionID_missing(t *testing.T) {
	t.Parallel()
	_, err := parseClaudeJSONSessionID(`{"type":"result","subtype":"success","result":"ok"}`)
	if err == nil || !strings.Contains(err.Error(), "session_id") {
		t.Fatalf("want missing session_id error, got %v", err)
	}
}

func TestClaudeV2ProbeUserFacingError(t *testing.T) {
	t.Parallel()
	if !strings.Contains(ClaudeV2ProbeUserFacingError(), "headless session resume") {
		t.Fatalf("message=%q", ClaudeV2ProbeUserFacingError())
	}
}

func TestResumeProbeChatArgsUsesHaiku(t *testing.T) {
	t.Parallel()
	args := resumeProbeChatArgs("ping", "--resume", "sess-1")
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "haiku" {
			return
		}
	}
	t.Fatalf("resume probe args missing --model haiku: %v", args)
}
