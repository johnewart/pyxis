package toolresult

import (
	"context"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/tokencount"
)

// MockSidecarRepo implements ToolResultSidecarRepo for testing.
type MockSidecarRepo struct {
	rows map[string]*store.ToolResultSidecarRow
}

func NewMockSidecarRepo() *MockSidecarRepo {
	return &MockSidecarRepo{
		rows: make(map[string]*store.ToolResultSidecarRow),
	}
}

func (m *MockSidecarRepo) Insert(ctx context.Context, row store.ToolResultSidecarRow) error {
	key := row.TenantID + ":" + row.SessionID + ":" + row.ToolCallID
	m.rows[key] = &row
	return nil
}

func (m *MockSidecarRepo) GetByToolCallID(ctx context.Context, workspaceID, sessionID, toolCallID string) (store.ToolResultSidecarRow, error) {
	key := workspaceID + ":" + sessionID + ":" + toolCallID
	if row, ok := m.rows[key]; ok {
		return *row, nil
	}
	return store.ToolResultSidecarRow{}, nil
}

func (m *MockSidecarRepo) DeleteBySession(ctx context.Context, workspaceID, sessionID string) error {
	prefix := workspaceID + ":" + sessionID + ":"
	for key := range m.rows {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(m.rows, key)
		}
	}
	return nil
}

func TestIngestToolResult_BelowThreshold(t *testing.T) {
	ctx := context.Background()
	sidecar := NewMockSidecarRepo()

	content := "short result"
	result := IngestToolResult(ctx, content, IngestIDs{
		Scope:      serverutil.Scope{TenantID: "user1"},
		SessionID:  "sess1",
		TurnID:     "turn1",
		ToolCallID: "tool1",
		ToolName:   "test_tool",
	}, Deps{
		Sidecar: sidecar,
		Settings: Settings{
			Enabled:          true,
			MinThresholdToks: 2000,
			MinifyDedup:      true,
		},
	})

	if result.Compressed {
		t.Errorf("Expected no compression for content below threshold, got compressed")
	}
	if result.Wire != content {
		t.Errorf("Expected wire=original, got different content")
	}
}

func TestIngestToolResult_CompressionDisabled(t *testing.T) {
	ctx := context.Background()

	// Generate content > 2000 tokens
	content := generateLargeContent(3000)
	result := IngestToolResult(ctx, content, IngestIDs{
		Scope:      serverutil.Scope{TenantID: "user1"},
		SessionID:  "sess1",
		TurnID:     "turn1",
		ToolCallID: "tool1",
		ToolName:   "test_tool",
	}, Deps{
		Sidecar: nil, // disabled
		Settings: Settings{
			Enabled: false,
		},
	})

	if result.Compressed {
		t.Errorf("Expected no compression when disabled")
	}
}

// TestIngestToolResult_CompressesAboveThreshold proves that an enabled session
// with compressible content above the threshold actually compresses: the wire is
// smaller, Compressed=true, and the original is stashed in the sidecar.
func TestIngestToolResult_CompressesAboveThreshold(t *testing.T) {
	ctx := context.Background()
	sidecar := NewMockSidecarRepo()

	// Build highly compressible content: many blank + comment lines that
	// structuralMinify strips, padded above a low threshold.
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString("// throwaway comment line that minify removes\n")
		b.WriteString("\n")
		b.WriteString("keep this line\n")
	}
	content := b.String()

	origToks := tokencount.Count(content)
	if origToks < 200 {
		t.Fatalf("test content too small (%d toks); need > threshold", origToks)
	}

	result := IngestToolResult(ctx, content, IngestIDs{
		Scope:      serverutil.Scope{TenantID: "user1"},
		SessionID:  "sess1",
		TurnID:     "turn1",
		ToolCallID: "toolA",
		ToolName:   "read_code",
	}, Deps{
		Sidecar: sidecar,
		Settings: Settings{
			Enabled:          true,
			MinThresholdToks: 200,
			MinifyDedup:      true,
		},
	})

	if !result.Compressed {
		t.Fatalf("expected compression to occur, got Compressed=false")
	}
	if result.WireTokens >= result.OriginalTokens {
		t.Errorf("expected wire < original: orig=%d wire=%d", result.OriginalTokens, result.WireTokens)
	}
	if !strings.Contains(result.Wire, bannerTag) {
		t.Errorf("expected compression banner in wire output")
	}
	// Original must be stashed for expand_tool_result.
	row, err := sidecar.GetByToolCallID(ctx, "user1", "sess1", "toolA")
	if err != nil {
		t.Fatalf("sidecar lookup: %v", err)
	}
	if row.Original != content {
		t.Errorf("sidecar did not store the original content verbatim")
	}
}

// TestIngestToolResult_DedupSameSession proves that an identical large result
// seen twice in the same session collapses to a back-reference on the second hit.
func TestIngestToolResult_DedupSameSession(t *testing.T) {
	ctx := context.Background()
	sidecar := NewMockSidecarRepo()
	dedup := &SessionDedupMap{}

	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString("// comment removed by minify\n")
		b.WriteString("\n")
		b.WriteString("identical payload line\n")
	}
	content := b.String()

	deps := Deps{
		Sidecar: sidecar,
		Settings: Settings{
			Enabled:          true,
			MinThresholdToks: 200,
			MinifyDedup:      true,
		},
		SessionDedup: dedup,
		// Explicit, tool-provided identity key shared by both calls — dedup fires
		// only on an exact key match (no content hashing).
		DedupKey: "payload-key",
	}

	first := IngestToolResult(ctx, content, IngestIDs{
		Scope: serverutil.Scope{TenantID: "u"}, SessionID: "s", TurnID: "t1", ToolCallID: "call1", ToolName: "read_code",
	}, deps)
	if !first.Compressed {
		t.Fatalf("expected first result to compress")
	}

	second := IngestToolResult(ctx, content, IngestIDs{
		Scope: serverutil.Scope{TenantID: "u"}, SessionID: "s", TurnID: "t2", ToolCallID: "call2", ToolName: "read_code",
	}, deps)
	if !second.Compressed {
		t.Fatalf("expected second (duplicate) result to compress")
	}
	if !strings.Contains(second.Wire, "ref_tool_call_id=\"call1\"") {
		t.Errorf("expected dedup back-reference to call1, got: %q", second.Wire)
	}
	if second.WireTokens >= first.WireTokens {
		t.Errorf("expected deduped result to be smaller than first: first=%d second=%d", first.WireTokens, second.WireTokens)
	}
}

func TestStructuralMinify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "remove blank lines",
			input:    "line1\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "remove comments",
			input:    "// comment\nline1",
			expected: "line1",
		},
		{
			name:     "remove hash comments",
			input:    "# comment\nline1",
			expected: "line1",
		},
		{
			name:     "collapse whitespace",
			input:    "line1   with   spaces",
			expected: "line1   with   spaces", // internal not collapsed, only trims ends
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := structuralMinify(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestSessionDedupMap(t *testing.T) {
	dedup := &SessionDedupMap{}

	// First insert
	existing1, loaded1 := dedup.getOrStore("hash1", "tool1")
	if loaded1 {
		t.Errorf("Expected new hash to not be loaded, got loaded")
	}
	if existing1 != "tool1" {
		t.Errorf("Expected tool1, got %s", existing1)
	}

	// Second insert (duplicate hash)
	existing2, loaded2 := dedup.getOrStore("hash1", "tool2")
	if !loaded2 {
		t.Errorf("Expected duplicate hash to be loaded")
	}
	if existing2 != "tool1" {
		t.Errorf("Expected existing tool1, got %s", existing2)
	}

	// Third insert (new hash)
	existing3, loaded3 := dedup.getOrStore("hash2", "tool3")
	if loaded3 {
		t.Errorf("Expected new hash to not be loaded")
	}
	if existing3 != "tool3" {
		t.Errorf("Expected tool3, got %s", existing3)
	}
}

func generateLargeContent(estimatedTokens int) string {
	// Generate roughly n tokens of content
	// Rough estimate: ~4 chars per token
	size := estimatedTokens * 4
	content := ""
	for i := 0; i < size; i++ {
		if i%80 == 0 && i > 0 {
			content += "\n"
		}
		content += "a"
	}
	return content
}
