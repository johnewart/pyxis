//go:build integration

package claudecodeclillm

import (
	"bufio"
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// IntegrationCheapModel is the model used for all Claude CLI integration tests and probes.
const IntegrationCheapModel = claudeCheapCLIModel

// IntegrationProbeTimeout caps the resume probe (version/help + two real haiku API turns).
const IntegrationProbeTimeout = 30 * time.Second

// IntegrationHandshakeTimeout caps stream-json initialize handshake tests.
const IntegrationHandshakeTimeout = 15 * time.Second

// IntegrationChatTimeout caps full Chat/ChatStream integration tests (real API round-trip).
const IntegrationChatTimeout = 60 * time.Second

// IntegrationTestTimeout is deprecated; use the specific timeouts above.
const IntegrationTestTimeout = IntegrationProbeTimeout

// IntegrationTestContext returns a context cancelled when the test ends (probe timeout).
func IntegrationTestContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationProbeTimeout)
}

// IntegrationChatContext returns a longer context for real chat round-trips.
func IntegrationChatContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationChatTimeout)
}

// IntegrationHandshakeContext returns a context for stream-json handshake tests.
func IntegrationHandshakeContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationHandshakeTimeout)
}

func integrationTestContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// SkipUnlessClaudeCLI skips the test when the Claude CLI is missing or not runnable.
func SkipUnlessClaudeCLI(t *testing.T) string {
	t.Helper()
	bin, err := resolveProbeBinary("")
	if err != nil {
		t.Skipf("claude CLI not on PATH (set CLAUDE_CLI_PATH): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), IntegrationHandshakeTimeout)
	defer cancel()
	if _, err := runProbeCmd(ctx, bin, "", "-h"); err != nil {
		t.Skipf("claude CLI not runnable: %v", err)
	}
	return bin
}

// RunStreamV2StartupHandshakeTest exercises stream-json init (control_response handshake).
func RunStreamV2StartupHandshakeTest(t *testing.T, cfg Config, opt *llmv1.CallOptions) {
	t.Helper()
	ctx := IntegrationHandshakeContext(t)

	client := NewV2(cfg)
	args := client.buildStreamArgsV2(ctx, opt, nil, cfg.McpConfigPath, false, "")
	assertIntegrationArgsUseCheapModel(t, args)

	base := &Client{cfg: cfg}
	cmd, err := base.cmd(ctx, args...)
	if err != nil {
		t.Fatalf("cmd: %v", err)
	}
	defer releaseNeutralCwd(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	integrationLog := newCLILog("integration")
	// Owned pipes: cmd.Wait below runs concurrently with reading (see claudeCLIStdio).
	stdio, err := attachClaudeCLIStdio(integrationLog, cmd)
	if err != nil {
		t.Fatalf("stdio pipes: %v", err)
	}
	// Registered before the kill defer, so it runs after the process tree is gone.
	defer stdio.Close()
	stderrSnapshot := stdio.stderrSnapshot
	setClaudeCLIProcessGroup(cmd)
	err = cmd.Start()
	stdio.closeChildEnds()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Group-kill the whole tree (claude + node/MCP children), not just the direct
	// PID, so a stuck handshake can't leave grandchildren spinning at ~100% CPU.
	defer killClaudeCLIProcessTree(cmd)

	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()

	lines := startStreamJSONLineReader(integrationLog, bufio.NewReader(stdio.stdout))
	lineSrc := newStreamJSONLineSource(lines)
	if err := kickClaudeStreamJSONHandshake(ctx, stdin, lineSrc, stderrSnapshot, procDone); err != nil {
		t.Fatalf("stream-json init handshake: %v", err)
	}
	_ = stdin.Close()
}

func assertIntegrationArgsUseCheapModel(t *testing.T, args []string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--model" {
			if args[i+1] != IntegrationCheapModel {
				t.Fatalf("integration tests must use --model %q, got %q", IntegrationCheapModel, args[i+1])
			}
			return
		}
	}
	t.Fatal("integration CLI args missing --model")
}
