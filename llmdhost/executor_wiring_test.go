package llmdhost

import (
	"path/filepath"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

func TestExecutorWiring(t *testing.T) {
	dir := t.TempDir()
	h, err := New(Config{
		DatabaseDSN:         filepath.Join(dir, "llmd.sqlite"),
		KnowledgeSQLitePath: filepath.Join(dir, "knowledge.sqlite"),
		RuntimeIdleEvict:    -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	ex := h.Handlers().Executor()
	if ex == nil {
		t.Fatal("Handlers().Executor() is nil")
	}
	if ex != toolseam.ExecutorDispatcher(h.toolHub) {
		t.Fatalf("executor %T is not h.toolHub", ex)
	}
	if h.toolHub.InlineNames == nil {
		t.Fatal("toolHub.InlineNames is nil")
	}
	const name = "executor_wiring_probe_tool"
	if err := h.Tools().Register(&llmv1.ToolDefinition{Name: name, Description: "probe"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := h.toolHub.InlineNames()[name]; !ok {
		t.Fatalf("InlineNames() missing %q after registration", name)
	}
}
