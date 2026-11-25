//go:build integration

package claudecodeintegration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claudecode "github.com/johnewart/pyxis/internal/llm/claudecode"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

type integrationCLI struct {
	cfg    claudecode.Config
	client *claudecode.V2Client
	opt    *llmv1.CallOptions
}

func newIntegrationMCPCLI(t *testing.T) (integrationCLI, *integrationMCPStub) {
	t.Helper()
	workDir := t.TempDir()
	stub := startIntegrationMCPStub(t, workDir)
	bin := claudecode.SkipUnlessClaudeCLI(t)
	toolName := "mcp__" + integrationMCPServerName + "__write_file"
	strictMCP := true
	cfg := claudecode.Config{
		Binary:             bin,
		Cwd:                workDir,
		DefaultModel:       claudecode.IntegrationCheapModel,
		ThinkingType:       "disabled",
		Effort:             "medium",
		MaxTurns:           10,
		TimeoutMs:          int(claudecode.IntegrationChatTimeout / time.Millisecond),
		SkipAutoMCP:        true,
		McpConfigPath:      stub.configPath,
		PermissionMode:     "bypassPermissions",
		StrictMcpConfig:    &strictMCP,
		AllowedTools:       []string{integrationMCPAllowedToolsWildcard, toolName},
		AppendSystemPrompt: "Invoke MCP tools for actions. Never simulate tool calls in prose or XML.",
	}
	m := claudecode.IntegrationCheapModel
	cli := integrationCLI{
		cfg:    cfg,
		client: claudecode.NewV2(cfg),
		opt: &llmv1.CallOptions{
			Model: m,
			ClaudeCode: &llmv1.ClaudeCodeCallOptions{
				BaseModel:    proto.String(m),
				Effort:       proto.String("medium"),
				ThinkingType: proto.String("disabled"),
			},
		},
	}
	return cli, stub
}

func newIntegrationCLI(t *testing.T) integrationCLI {
	t.Helper()
	bin := claudecode.SkipUnlessClaudeCLI(t)
	cfg := claudecode.Config{
		Binary:        bin,
		Cwd:           t.TempDir(),
		DefaultModel:  claudecode.IntegrationCheapModel,
		ThinkingType:  "disabled",
		Effort:        "low",
		MaxTurns:      1,
		SkipAutoMCP:   true,
		TimeoutMs:     int(claudecode.IntegrationChatTimeout / time.Millisecond),
	}
	m := claudecode.IntegrationCheapModel
	return integrationCLI{
		cfg:    cfg,
		client: claudecode.NewV2(cfg),
		opt: &llmv1.CallOptions{
			Model: m,
			ClaudeCode: &llmv1.ClaudeCodeCallOptions{
				BaseModel:    proto.String(m),
				Effort:       proto.String("low"),
				ThinkingType: proto.String("disabled"),
			},
		},
	}
}

func TestIntegrationClaudeCodeResumeProbe(t *testing.T) {
	bin := claudecode.SkipUnlessClaudeCLI(t)
	r := claudecode.ProbeClaudeCodeResumeCapability(bin, t.TempDir(), claudecode.IntegrationProbeTimeout)
	if !r.OK {
		t.Fatalf("resume probe failed (version=%q): %s — %s", r.Version, r.Error, r.Details)
	}
	t.Logf("claude CLI version %s supports headless resume (probe model=%s)", r.Version, claudecode.IntegrationCheapModel)
}

// Regression: Claude Code rejects --include-partial-messages without --print and an empty
// positional prompt; a bad argv combo used to leave llmd blocked on stream-json init.
func TestIntegrationStreamV2StartupHandshake(t *testing.T) {
	cli := newIntegrationCLI(t)
	claudecode.RunStreamV2StartupHandshakeTest(t, cli.cfg, cli.opt)
}

func TestIntegrationV2ClientChat(t *testing.T) {
	cli := newIntegrationCLI(t)
	ctx := claudecode.IntegrationChatContext(t)

	msgs := []*llmv1.Message{
		{Role: "user", Content: "Reply with exactly: pong"},
	}
	reply, err := cli.client.Chat(ctx, msgs, cli.opt, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	content := strings.ToLower(strings.TrimSpace(reply.GetContent()))
	if content == "" {
		t.Fatal("empty reply content")
	}
	if !strings.Contains(content, "pong") {
		t.Fatalf("unexpected reply: %q", reply.GetContent())
	}
	t.Logf("session_id=%q reply=%q", reply.GetId(), reply.GetContent())
}

func TestIntegrationV2ClientChatStream(t *testing.T) {
	cli := newIntegrationCLI(t)
	ctx := claudecode.IntegrationChatContext(t)

	msgs := []*llmv1.Message{
		{Role: "user", Content: "Reply with exactly: stream-ok"},
	}
	var (
		text       strings.Builder
		sawStarted bool
		sawDone    bool
	)
	err := cli.client.ChatStream(ctx, msgs, cli.opt, nil, func(f *llmv1.ConversationStreamFrame) error {
		if f.GetAssistantTurnStarted() != nil {
			sawStarted = true
		} else if d := f.GetAssistantTextDelta(); d != nil {
			text.WriteString(d.GetDelta())
		} else if f.GetAssistantTurnFinished() != nil {
			sawDone = true
		} else if f.GetStreamComplete() != nil {
			sawDone = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if !sawStarted {
		t.Fatal("stream missing AssistantTurnStarted")
	}
	if !sawDone {
		t.Fatal("stream missing turn finished / stream complete")
	}
	got := strings.ToLower(strings.TrimSpace(text.String()))
	if got == "" {
		t.Fatal("stream produced no assistant text")
	}
	if !strings.Contains(got, "stream") {
		t.Fatalf("unexpected stream text: %q", text.String())
	}
	t.Logf("stream reply=%q", text.String())
}

// Regression: with --tools "" and MCP configured, Claude must invoke the MCP write_file tool
// instead of claiming it wrote a file in prose (or using removed built-in Write).
func TestIntegrationV2MCPWriteFileViaTool(t *testing.T) {
	cli, stub := newIntegrationMCPCLI(t)
	ctx := claudecode.IntegrationChatContext(t)

	toolName := "mcp__" + integrationMCPServerName + "__write_file"
	targetFile := "integration-write.txt"
	wantContent := "mcp-write-ok"
	prompt := strings.Join([]string{
		"You must call the MCP tool " + toolName + " (not built-in Write).",
		"If needed, use ToolSearch to locate write_file first.",
		"Arguments: path=\"" + targetFile + "\", content=\"" + wantContent + "\".",
		"Do not output <function_calls> XML or describe the call in prose.",
		"After the tool succeeds, reply with exactly: done",
	}, " ")

	reply, err := chatUntilMCPWrite(ctx, cli, stub, toolName, targetFile, wantContent, prompt)
	if err != nil {
		t.Fatalf("Chat: %v (mcp init=%d tools/list=%d write=%d)", err,
			stub.initCallCount(), stub.toolsListCallCount(), stub.writeCallCount())
	}
	if stub.writeCallCount() == 0 {
		t.Fatalf("MCP write_file was never invoked; mcp init=%d tools/list=%d assistant reply=%q (expected real tool call, not text-only claim)",
			stub.initCallCount(), stub.toolsListCallCount(), reply.GetContent())
	}
	gotPath := filepath.Join(cli.cfg.Cwd, targetFile)
	raw, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("expected %q on disk after MCP tool call: %v (stub calls=%d reply=%q)", targetFile, err, stub.writeCallCount(), reply.GetContent())
	}
	if string(raw) != wantContent {
		t.Fatalf("file content: got %q want %q", string(raw), wantContent)
	}
	if !strings.Contains(strings.ToLower(reply.GetContent()), "done") {
		t.Logf("note: reply did not contain 'done' (got %q) but tool write succeeded", reply.GetContent())
	}
	t.Logf("MCP write_file calls=%d path=%q content=%q reply=%q", stub.writeCallCount(), stub.lastPath, stub.lastContent, reply.GetContent())
}

func chatUntilMCPWrite(
	ctx context.Context,
	cli integrationCLI,
	stub *integrationMCPStub,
	toolName, targetFile, wantContent, prompt string,
) (*llmv1.AssistantReply, error) {
	msgs := []*llmv1.Message{{Role: "user", Content: prompt}}
	reply, err := cli.client.Chat(ctx, msgs, cli.opt, nil)
	if err != nil {
		return nil, err
	}
	if stub.writeCallCount() > 0 {
		return reply, nil
	}
	sessionID := strings.TrimSpace(reply.GetId())
	if sessionID == "" {
		return reply, nil
	}
	followUp := strings.Join([]string{
		"You did not invoke " + toolName + ".",
		"Call it now with path=\"" + targetFile + "\" content=\"" + wantContent + "\".",
		"No XML, no prose simulation.",
	}, " ")
	resumeCtx := claudecode.WithResumeSessionID(ctx, sessionID)
	return cli.client.Chat(resumeCtx, []*llmv1.Message{{Role: "user", Content: followUp}}, cli.opt, nil)
}

// TestIntegrationBuildTagActive confirms this package only compiles with -tags=integration.
func TestIntegrationBuildTagActive(t *testing.T) {}
