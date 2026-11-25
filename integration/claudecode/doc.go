//go:build integration

// Package claudecodeintegration runs end-to-end tests against the real Claude Code CLI.
//
// These tests are NOT run by default (go test ./...). Run explicitly:
//
//	go test -tags=integration ./integration/claudecode/... -count=1 -v
//
// Requires claude on PATH (or CLAUDE_CLI_PATH).
package claudecodeintegration
