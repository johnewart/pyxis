package toolresult

import (
	"context"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/store"
)

type agingFakeSidecar struct{ rows []store.ToolResultSidecarRow }

func (c *agingFakeSidecar) Insert(_ context.Context, row store.ToolResultSidecarRow) error {
	c.rows = append(c.rows, row)
	return nil
}

func TestAgeToolResult_CollapsesLargeResult(t *testing.T) {
	sc := &agingFakeSidecar{}
	content := strings.Repeat("some fairly large tool output line\n", 500)

	wire, aged := AgeToolResult(context.Background(), content, IngestIDs{
		SessionID:  "s1",
		ToolCallID: "call-1",
		ToolName:   "search",
	}, Deps{Sidecar: sc, Settings: Settings{Enabled: true, MinThresholdToks: 200}})

	if !aged {
		t.Fatal("expected large result to be aged")
	}
	if !IsCompressed(wire) {
		t.Fatalf("aged wire should carry the compression banner: %q", wire)
	}
	if !strings.Contains(wire, `expand_tool_result("call-1")`) {
		t.Errorf("aged wire should reference expand_tool_result for its own call id: %q", wire)
	}
	if len(sc.rows) != 1 {
		t.Fatalf("expected the original persisted to the sidecar, got %d rows", len(sc.rows))
	}
	if sc.rows[0].Original != content {
		t.Error("sidecar must store the full original so the stale ref stays expandable")
	}
	if sc.rows[0].Method != "stale" {
		t.Errorf("expected method=stale, got %q", sc.rows[0].Method)
	}
	if sc.rows[0].WireTokens >= sc.rows[0].OriginalTokens {
		t.Errorf("aged wire (%d) should be smaller than original (%d)", sc.rows[0].WireTokens, sc.rows[0].OriginalTokens)
	}
}

func TestAgeToolResult_SkipsSmallResult(t *testing.T) {
	sc := &agingFakeSidecar{}
	_, aged := AgeToolResult(context.Background(), "tiny output", IngestIDs{ToolCallID: "c"},
		Deps{Sidecar: sc, Settings: Settings{Enabled: true, MinThresholdToks: 200}})
	if aged {
		t.Fatal("small result should not be aged")
	}
	if len(sc.rows) != 0 {
		t.Fatal("small result should not be persisted")
	}
}

func TestAgeToolResult_SkipsAlreadyCompressed(t *testing.T) {
	sc := &agingFakeSidecar{}
	already := "[COMPRESSED tool_call_id=\"x\" original_tokens=999 method=minify expand=expand_tool_result(\"x\")]\nbody"
	_, aged := AgeToolResult(context.Background(), already, IngestIDs{ToolCallID: "x"},
		Deps{Sidecar: sc, Settings: Settings{Enabled: true, MinThresholdToks: 1}})
	if aged {
		t.Fatal("already-compressed content should not be re-aged")
	}
	if len(sc.rows) != 0 {
		t.Fatal("already-compressed content should not be re-persisted")
	}
}

func TestAgeToolResult_NilSidecar(t *testing.T) {
	_, aged := AgeToolResult(context.Background(), strings.Repeat("x\n", 5000),
		IngestIDs{ToolCallID: "c"}, Deps{Settings: Settings{Enabled: true, MinThresholdToks: 1}})
	if aged {
		t.Fatal("aging requires a sidecar to persist the original; must be a no-op without one")
	}
}
