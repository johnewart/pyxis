package claudecodeclillm

import (
	"context"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/tooltimeout"
)

func mcpToolTimeoutEnv(env []string) string {
	val := ""
	for _, e := range env {
		const p = "MCP_TOOL_TIMEOUT="
		if len(e) >= len(p) && e[:len(p)] == p {
			val = e[len(p):]
		}
	}
	return val
}

func TestAppendMCPToolTimeoutEnv_replacesAmbient(t *testing.T) {
	env := appendMCPToolTimeoutEnv([]string{"A=1", "MCP_TOOL_TIMEOUT=5"}, 90*time.Second)
	n := 0
	for _, e := range env {
		if len(e) >= 17 && e[:17] == "MCP_TOOL_TIMEOUT=" {
			n++
		}
	}
	if n != 1 || mcpToolTimeoutEnv(env) != "90000" {
		t.Fatalf("env = %v, want exactly one MCP_TOOL_TIMEOUT=90000", env)
	}
}

// Last hop: the env of the process cmd() builds carries the server-attached
// backstop, and survives the derived ctx stream_transport hands it (withTimeout).
func TestCmd_envCarriesContextBackstop(t *testing.T) {
	t.Setenv("MCP_TOOL_TIMEOUT", "5") // ambient value must be replaced
	c := &Client{cfg: Config{Binary: "sh", TimeoutMs: 60_000}}
	ctx := tooltimeout.WithBackstop(context.Background(), 32*time.Minute)
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	cmd, err := c.cmd(ctx, "-c", "true")
	if err != nil {
		t.Fatalf("cmd: %v", err)
	}
	t.Cleanup(func() { releaseNeutralCwd(cmd) })
	if got := mcpToolTimeoutEnv(cmd.Env); got != "1920000" {
		t.Fatalf("MCP_TOOL_TIMEOUT = %q, want 1920000 (32m)", got)
	}
}

func TestMCPToolBackstop_prefersContext(t *testing.T) {
	t.Setenv(tooltimeout.CapEnvVar, "")
	t.Setenv(tooltimeout.DefaultEnvVar, "")
	c := &Client{}
	if got, want := c.mcpToolBackstop(context.Background()), tooltimeout.Backstop(nil); got != want {
		t.Fatalf("no ctx backstop: got %s, want fallback %s", got, want)
	}
	ctx := tooltimeout.WithBackstop(context.Background(), 32*time.Minute)
	if got := c.mcpToolBackstop(ctx); got != 32*time.Minute {
		t.Fatalf("ctx backstop: got %s, want 32m", got)
	}
}
