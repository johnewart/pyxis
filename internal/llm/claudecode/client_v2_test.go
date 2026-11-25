package claudecodeclillm

import (
	"context"
	"slices"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestBuildStreamArgsV2IncludesPrintForPartialMessages(t *testing.T) {
	c := NewV2(Config{})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "", false, "")
	if !slices.Contains(args, "--print") {
		t.Fatalf("buildStreamArgsV2 must include --print for --include-partial-messages: %v", args)
	}
	if !slices.Contains(args, "--include-partial-messages") {
		t.Fatalf("buildStreamArgsV2 must include --include-partial-messages: %v", args)
	}
	idxPrint := slices.Index(args, "--print")
	idxPartial := slices.Index(args, "--include-partial-messages")
	if idxPrint < 0 || idxPartial < 0 || idxPrint >= idxPartial {
		t.Fatalf("--print must precede --include-partial-messages: %v", args)
	}
	idxP := slices.Index(args, "-p")
	if idxP < 0 || idxP >= len(args)-1 || args[idxP+1] != "" {
		t.Fatalf("buildStreamArgsV2 must end with -p \"\": %v", args)
	}
}

func TestBuildStreamArgsV2HeadlessMCPDisablesBuiltinToolsAndBypassesPermissions(t *testing.T) {
	c := NewV2(Config{})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "/tmp/mcp.json", false, "")
	assertCLIArgPair(t, args, "--permission-mode", "bypassPermissions")
	assertCLIArgPair(t, args, "--tools", claudeHeadlessMCPEagerBuiltinTools)
	assertCLIArgPair(t, args, "--mcp-config", "/tmp/mcp.json")
}

func TestBuildStreamArgsV2HeadlessMCPDefersToolSchemasWhenOptedIn(t *testing.T) {
	c := NewV2(Config{DeferToolSchemas: true})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "/tmp/mcp.json", false, "")
	assertCLIArgPair(t, args, "--tools", claudeHeadlessMCPDeferredBuiltinTools)
}

func TestBuildStreamArgsV2HeadlessMCPRespectsConfiguredPermissionMode(t *testing.T) {
	c := NewV2(Config{PermissionMode: "plan"})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "", true, "")
	assertCLIArgPair(t, args, "--permission-mode", "plan")
	assertCLIArgPair(t, args, "--tools", claudeHeadlessMCPEagerBuiltinTools)
}

// A spawn with no MCP bridge must still disable the CLI's native tools. This is
// the regression: argv had no --tools, and Claude came up with Bash, Read,
// Write, Edit, WebFetch and the rest enabled.
func TestBuildStreamArgsV2WithoutMCPStillDisablesBuiltinTools(t *testing.T) {
	c := NewV2(Config{})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "", false, "")
	assertCLIArgPair(t, args, "--tools", claudeHeadlessMCPEagerBuiltinTools)
	if slices.Contains(args, "--mcp-config") {
		t.Fatalf("no MCP path was given, so --mcp-config must be absent: %v", args)
	}
}

// An operator who explicitly allowed specific built-ins keeps them.
func TestBuildStreamArgsV2WithoutMCPRespectsExplicitAllowedTools(t *testing.T) {
	c := NewV2(Config{AllowedTools: []string{"Read"}})
	args := c.buildStreamArgsV2(context.Background(), nil, nil, "", false, "")
	if slices.Contains(args, "--tools") {
		t.Fatalf("explicit AllowedTools must not be overridden by a --tools restriction: %v", args)
	}
}

// Tools requested with no MCP bridge configured must fail the turn before any
// CLI is spawned, rather than run the model without the tools it was told about.
func TestRunStreamAttemptV2RefusesToolsWithoutMCPBridge(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	c := NewV2(Config{Binary: "/nonexistent/claude-must-not-spawn"})
	tools := []*llmv1.ToolDefinition{{Name: "host_fetch"}}
	_, err := c.runStreamAttemptV2(context.Background(), nil, nil, tools, nil)
	if err == nil {
		t.Fatal("expected an error when tools are requested with no MCP bridge")
	}
	if !strings.Contains(err.Error(), "no llmd MCP bridge is configured") {
		t.Fatalf("expected the no-MCP-bridge refusal, got: %v", err)
	}
}

// SkipAutoMCP is an explicit opt-out and must not trip the refusal.
func TestRunStreamAttemptV2HonoursSkipAutoMCP(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	c := NewV2(Config{Binary: "/nonexistent/claude-must-not-spawn", SkipAutoMCP: true})
	tools := []*llmv1.ToolDefinition{{Name: "host_fetch"}}
	_, err := c.runStreamAttemptV2(context.Background(), nil, nil, tools, nil)
	if err != nil && strings.Contains(err.Error(), "no llmd MCP bridge is configured") {
		t.Fatalf("SkipAutoMCP must bypass the no-MCP refusal, got: %v", err)
	}
}

func assertCLIArgPair(t *testing.T, args []string, flag, want string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			if args[i+1] != want {
				t.Fatalf("%s: got %q want %q in %v", flag, args[i+1], want, args)
			}
			return
		}
	}
	t.Fatalf("missing %s in %v", flag, args)
}
