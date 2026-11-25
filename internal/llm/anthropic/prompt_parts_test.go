package anthropicllm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func partsOpt(mode llmv1.CacheOptions_PromptCacheMode) *llmv1.CallOptions {
	return &llmv1.CallOptions{CacheOptions: &llmv1.CacheOptions{PromptCacheMode: mode}}
}

// textOf returns the text block inside a user content block union.
func textOf(t *testing.T, b anthropic.ContentBlockParamUnion) *anthropic.TextBlockParam {
	t.Helper()
	if b.OfText == nil {
		t.Fatalf("block is not a text block: %+v", b)
	}
	return b.OfText
}

func hasCacheControl(t *testing.T, b anthropic.ContentBlockParamUnion) bool {
	t.Helper()
	raw, err := textOf(t, b).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), "cache_control")
}

func TestMaxCacheMarkers(t *testing.T) {
	if maxCacheMarkers != 4 {
		t.Fatalf("maxCacheMarkers=%d want 4", maxCacheMarkers)
	}
}

func TestBuildUserBlocks_CacheControlOnBreakpointsOnly(t *testing.T) {
	parts := []*llmv1.PromptPart{
		{Text: "summary"},
		{Text: "turn1", CacheBreakpoint: true},
		{Text: "turn2"},
	}
	blocks := buildUserBlocks(parts, partsOpt(llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL))
	if len(blocks) != 3 {
		t.Fatalf("blocks=%d want 3", len(blocks))
	}
	for i, p := range parts {
		if got := textOf(t, blocks[i]).Text; got != p.Text {
			t.Errorf("block %d text=%q want %q", i, got, p.Text)
		}
		if got := hasCacheControl(t, blocks[i]); got != p.CacheBreakpoint {
			t.Errorf("block %d cache_control=%v want %v", i, got, p.CacheBreakpoint)
		}
	}
	if ttl := textOf(t, blocks[1]).CacheControl.TTL; ttl != anthropic.CacheControlEphemeralTTLTTL5m {
		t.Errorf("TTL=%v want 5m", ttl)
	}

	long := buildUserBlocks(parts, partsOpt(llmv1.CacheOptions_PROMPT_CACHE_MODE_LONG_TTL))
	if ttl := textOf(t, long[1]).CacheControl.TTL; ttl != anthropic.CacheControlEphemeralTTLTTL1h {
		t.Errorf("TTL=%v want 1h", ttl)
	}

	off := buildUserBlocks(parts, partsOpt(llmv1.CacheOptions_PROMPT_CACHE_MODE_DISABLED))
	if len(off) != 3 {
		t.Fatalf("blocks=%d want 3", len(off))
	}
	for i := range off {
		if hasCacheControl(t, off[i]) {
			t.Errorf("block %d has cache_control with caching disabled", i)
		}
	}
}

func TestBuildUserBlocks_KeepsLastMarkers(t *testing.T) {
	var parts []*llmv1.PromptPart
	for i := 0; i < 6; i++ {
		parts = append(parts, &llmv1.PromptPart{Text: string(rune('a' + i)), CacheBreakpoint: true})
	}
	blocks := buildUserBlocks(parts, partsOpt(llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL))
	if len(blocks) != 6 {
		t.Fatalf("blocks=%d want 6", len(blocks))
	}
	marked := 0
	firstMarked := -1
	for i, b := range blocks {
		if hasCacheControl(t, b) {
			marked++
			if firstMarked < 0 {
				firstMarked = i
			}
		}
	}
	if marked == 0 || marked > maxCacheMarkers {
		t.Fatalf("markers=%d want 1..%d", marked, maxCacheMarkers)
	}
	// The kept markers are the last ones: a contiguous run ending at the end.
	if firstMarked != len(blocks)-marked {
		t.Errorf("first marker at %d with %d markers; want the last %d blocks marked", firstMarked, marked, marked)
	}
}

// TestGenerateParts_SystemMarkerPlusUserMarkersWithinLimit sends more
// breakpoints than the limit together with a marked system prompt and checks
// the request body never carries more than maxCacheMarkers cache_control
// entries in total, and that the system marker is kept.
func TestGenerateParts_SystemMarkerPlusUserMarkersWithinLimit(t *testing.T) {
	var body string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicMessageJSONTextOnly)
	})
	var parts []*llmv1.PromptPart
	for i := 0; i < 6; i++ {
		parts = append(parts, &llmv1.PromptPart{Text: string(rune('a' + i)), CacheBreakpoint: true})
	}
	opt := partsOpt(llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL)
	if _, err := c.GenerateParts(context.Background(), "system text", parts, opt, nil); err != nil {
		t.Fatal(err)
	}
	var req struct {
		System   []map[string]any `json:"system"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 1 || req.System[0]["cache_control"] == nil {
		t.Fatalf("system marker missing: %v", req.System)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 6 {
		t.Fatalf("want one user message with 6 text blocks, got %+v", req.Messages)
	}
	if n := strings.Count(body, "cache_control"); n > maxCacheMarkers {
		t.Fatalf("cache_control markers=%d exceed limit %d", n, maxCacheMarkers)
	}
	last := req.Messages[0].Content[5]
	if last["cache_control"] == nil {
		t.Errorf("the last breakpoint must be kept: %v", last)
	}
	if req.Messages[0].Content[0]["cache_control"] != nil {
		t.Errorf("the earliest breakpoint must be dropped")
	}
}
