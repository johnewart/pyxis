package claudecodeclillm

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsClaudeAuthFailureTextMatchesCredentialFailures(t *testing.T) {
	for _, text := range []string{
		"OAuth token has expired. Please obtain a new token.",
		"Invalid API key · Please run /login",
		"API Error: 401 {\"type\":\"authentication_error\"}",
		"You are not logged in",
		"  UNAUTHORIZED  ",
	} {
		if !IsClaudeAuthFailureText(text) {
			t.Errorf("expected auth failure for %q", text)
		}
	}
}

func TestIsClaudeAuthFailureTextIgnoresUnrelatedFailures(t *testing.T) {
	// These are the failures that must keep their own diagnosis: routing them
	// into a login flow would send the user to fix something that is not broken.
	for _, text := range []string{
		"",
		"claude-code-cli-v2: waiting for initialize: stdout closed",
		"Claude Code 2.1.100 is too old for llmd",
		"tool call failed: host_fetch: no such entry",
		"connection refused",
	} {
		if IsClaudeAuthFailureText(text) {
			t.Errorf("expected no auth failure for %q", text)
		}
	}
}

func TestAnnotateClaudeAuthErrorTagsFromStderrText(t *testing.T) {
	base := errors.New("claude-code-cli-v2: waiting for initialize: process exited")
	err := annotateClaudeAuthError(base, "OAuth token has expired")
	if !IsClaudeAuthExpired(err) {
		t.Fatalf("expected auth-expired sentinel, got %v", err)
	}
	// The original error must stay in the chain so existing messages survive.
	if !errors.Is(err, base) {
		t.Fatalf("expected wrapped base error, got %v", err)
	}
}

func TestAnnotateClaudeAuthErrorLeavesUnrelatedErrorsAlone(t *testing.T) {
	base := errors.New("claude-code-cli-v2: stdin: broken pipe")
	err := annotateClaudeAuthError(base, "some unrelated stderr")
	if IsClaudeAuthExpired(err) {
		t.Fatalf("did not expect auth sentinel for %v", err)
	}
	if err != base {
		t.Fatalf("expected the original error unchanged, got %v", err)
	}
}

func TestAnnotateClaudeAuthErrorIsNilSafeAndIdempotent(t *testing.T) {
	if err := annotateClaudeAuthError(nil, "oauth token has expired"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	already := fmt.Errorf("%w: boom", ErrClaudeAuthExpired)
	if got := annotateClaudeAuthError(already, "invalid api key"); got != already {
		t.Fatalf("expected already-tagged error unchanged, got %v", got)
	}
}
