package tooltimeout

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func execTool(name string, secs uint32) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name: name,
		Execution: &llmv1.ToolDefinition_Executor{
			Executor: &llmv1.ToolExecutionExecutor{MaxDurationSeconds: secs},
		},
	}
}

func unset(t *testing.T) {
	t.Helper()
	t.Setenv(CapEnvVar, "")
	t.Setenv(DefaultEnvVar, "")
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"  ", 0, false},
		{"12m", 12 * time.Minute, true},
		{"900s", 900 * time.Second, true},
		{"720000", 720 * time.Second, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"-1m", 0, false},
		{"bogus", 0, false},
	}
	for _, c := range cases {
		got, ok := parse("X", c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parse(%q) = (%s, %v), want (%s, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDefault(t *testing.T) {
	unset(t)
	if got := Default(); got != BuiltinDefault {
		t.Fatalf("Default() = %s, want %s", got, BuiltinDefault)
	}
	t.Setenv(DefaultEnvVar, "20m")
	if got := Default(); got != 20*time.Minute {
		t.Fatalf("Default() with override = %s, want 20m", got)
	}
	t.Setenv(CapEnvVar, "15m")
	if got := Default(); got != 15*time.Minute {
		t.Fatalf("Default() above cap = %s, want 15m", got)
	}
}

func TestForTool(t *testing.T) {
	unset(t)
	if got := ForTool(execTool("a", 0)); got != BuiltinDefault {
		t.Errorf("undeclared = %s, want default", got)
	}
	if got := ForTool(&llmv1.ToolDefinition{Name: "inline"}); got != BuiltinDefault {
		t.Errorf("non-executor = %s, want default", got)
	}
	// No cap: declared values are honoured, even very long ones.
	if got := ForTool(execTool("long", 3600)); got != time.Hour {
		t.Errorf("uncapped declared = %s, want 1h", got)
	}
	// Cap set: declared values above it are clamped; below it are kept.
	t.Setenv(CapEnvVar, "5m")
	if got := ForTool(execTool("long", 3600)); got != 5*time.Minute {
		t.Errorf("capped declared = %s, want 5m", got)
	}
	if got := ForTool(execTool("short", 30)); got != 30*time.Second {
		t.Errorf("declared under cap = %s, want 30s", got)
	}
}

func TestMargin(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		30 * time.Second: MinMargin,          // 3s → floor
		5 * time.Minute:  30 * time.Second,   // 10%
		12 * time.Minute: 72 * time.Second,   // 10%
		time.Hour:        MaxMargin,          // 6m → ceiling
	}
	for in, want := range cases {
		if got := Margin(in); got != want {
			t.Errorf("Margin(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestBackstopContext(t *testing.T) {
	if _, ok := BackstopFromContext(context.Background()); ok {
		t.Fatal("empty ctx reported a backstop")
	}
	if _, ok := BackstopFromContext(WithBackstop(context.Background(), 0)); ok {
		t.Fatal("zero backstop was attached")
	}
	got, ok := BackstopFromContext(WithBackstop(context.Background(), 32*time.Minute))
	if !ok || got != 32*time.Minute {
		t.Fatalf("BackstopFromContext = (%s, %v), want (32m, true)", got, ok)
	}
}

func TestBackstop(t *testing.T) {
	unset(t)
	if got, want := Backstop(nil), BuiltinDefault+72*time.Second; got != want {
		t.Errorf("Backstop(nil) = %s, want %s", got, want)
	}
	defs := []*llmv1.ToolDefinition{execTool("a", 30), execTool("b", 300), nil}
	if got, want := Backstop(defs), 330*time.Second; got != want {
		t.Errorf("Backstop(declared) = %s, want %s", got, want)
	}
	// Backstop may exceed the cap so it never races the hub's deadline.
	t.Setenv(CapEnvVar, "5m")
	defs = []*llmv1.ToolDefinition{execTool("long", 3600)}
	if got, want := Backstop(defs), 330*time.Second; got != want {
		t.Errorf("Backstop(capped) = %s, want %s", got, want)
	}
}
