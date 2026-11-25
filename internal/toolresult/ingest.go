// Package toolresult is the single ingest chokepoint for all tool results.
// All content destined for a tool-role LLM message passes through IngestToolResult.
package toolresult

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/tokencount"
	"github.com/johnewart/pyxis/llmdlog"
)

const logModule = "toolresult"

// Settings governs compression behaviour. Read at call time (env-var backed).
type Settings struct {
	Enabled          bool
	MinThresholdToks int64 // compress only when token count exceeds this (default 2000)
	MinifyDedup      bool  // run lossless structural minify + dedup
	// AgeKeepRecent: number of most-recent tool results kept in full before older
	// ones collapse to expandable stale references. 0 ⇒ aging disabled.
	AgeKeepRecent int64
	// ExtractiveSearch is retired and has no effect. It once dropped two thirds
	// of a large `search` result by embedding similarity to the result's own
	// tail — a poor relevance signal that cost a blocking embed per call. The
	// field stays so persisted sessions and the wire protocol are unchanged.
	ExtractiveSearch bool
}

// IngestIDs groups identifiers for one tool call.
type IngestIDs struct {
	Scope                                   serverutil.Scope
	SessionID, TurnID, ToolCallID, ToolName string
}

// SessionDedupMap maps the opaque, tool-provided dedup key → tool_call_id of the
// first occurrence (no content hashing; dedup fires only on an exact key match).
// Maintained per session in LlmServer; safe for concurrent access via sync.Map.
type SessionDedupMap struct{ m sync.Map }

func (d *SessionDedupMap) getOrStore(hash, toolCallID string) (existing string, loaded bool) {
	v, ok := d.m.LoadOrStore(hash, toolCallID)
	if ok {
		return v.(string), true
	}
	return toolCallID, false
}

// SidecarRepo is the minimal interface for storing compressed tool result originals.
type SidecarRepo interface {
	Insert(ctx context.Context, row store.ToolResultSidecarRow) error
}

// Deps are injected by the server.
type Deps struct {
	Sidecar      SidecarRepo
	Settings     Settings
	SessionDedup *SessionDedupMap // nil = dedup disabled (unary Chat)
	// DedupKey is an opaque, tool-provided content-identity key. Dedup fires ONLY
	// when two results in the same session present the same non-empty key. Empty
	// ⇒ never deduped (the tool owns the identity decision; volatile output is safe).
	DedupKey string
	// DedupSummary is an optional, tool-provided one-line digest of the result
	// (e.g. "422 hits for \"Strategy\" — top files: ..."). When a dedup back-
	// reference fires it is appended to the wire so the model can judge relevance
	// without expanding. Empty ⇒ omitted.
	DedupSummary string
}

// IngestResult is returned for each tool result.
type IngestResult struct {
	Wire                       string
	Compressed                 bool
	OriginalTokens, WireTokens int64
	// Method records which compression mechanism produced Wire ("minify",
	// "dedup", "extractive", "minify+extractive"); "" when not compressed.
	Method string
	// DedupRefToolCallID is the tool_call_id of the first occurrence this result
	// was collapsed into when Method == "dedup"; "" otherwise. Surfaced so
	// callers can record the back-reference as structured metadata instead of
	// parsing the wire banner.
	DedupRefToolCallID string
}

const bannerTag = "[COMPRESSED"

// IsCompressed reports whether content already carries a compression banner,
// i.e. it has already passed through IngestToolResult. Callers use this to avoid
// re-ingesting already-compressed content (e.g. resent conversation history).
func IsCompressed(s string) bool {
	return strings.HasPrefix(s, bannerTag)
}

// IngestToolResult processes one tool result through the compression pipeline.
// Wire == content when compression is off or content is below the threshold.
func IngestToolResult(ctx context.Context, content string, ids IngestIDs, deps Deps) IngestResult {
	origToks := tokencount.Count(content)
	if !deps.Settings.Enabled || deps.Sidecar == nil || origToks < deps.Settings.MinThresholdToks {
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"compression skip: tool=%q call=%q orig_tokens=%d enabled=%v sidecar=%v threshold=%d",
			ids.ToolName, ids.ToolCallID, origToks, deps.Settings.Enabled, deps.Sidecar != nil, deps.Settings.MinThresholdToks)
		return IngestResult{Wire: content, OriginalTokens: origToks, WireTokens: origToks}
	}

	llmdlog.Stdout(logModule, llmdlog.LevelInfo,
		"compression start: tool=%q call=%q session=%q orig_tokens=%d minify_dedup=%v threshold=%d",
		ids.ToolName, ids.ToolCallID, ids.SessionID, origToks, deps.Settings.MinifyDedup, deps.Settings.MinThresholdToks)

	// Pass 1: structural minify (gated on the minify/dedup mechanism flag).
	wire := content
	if deps.Settings.MinifyDedup {
		wire = structuralMinify(content)
	}

	// Pass 2: explicit-key dedup against prior session results. Fires ONLY when a
	// non-empty DedupKey matches a prior result's key in this session. The first
	// occurrence of a key registers this tool_call_id and MUST be persisted below
	// (even with no size win) so a later back-reference to it can be expanded.
	deduped := false
	firstOccurrence := false
	dedupRef := ""
	if deps.Settings.MinifyDedup && deps.SessionDedup != nil && deps.DedupKey != "" {
		if prior, loaded := deps.SessionDedup.getOrStore(deps.DedupKey, ids.ToolCallID); loaded {
			dedupRef = prior
			wire = fmt.Sprintf("[unchanged: ref_tool_call_id=%q]", prior)
			if sum := strings.TrimSpace(deps.DedupSummary); sum != "" {
				wire += "\nSummary: " + sum
			}
			deduped = true
		} else {
			firstOccurrence = true
		}
	}

	wireToks := tokencount.Count(wire)
	improved := wireToks < origToks

	// Return the original to the model when nothing shrank it — UNLESS this is the
	// first occurrence of a dedup key, in which case we must still persist the
	// original so a future back-reference to this tool_call_id can be expanded.
	if !improved && !firstOccurrence {
		llmdlog.Stdout(logModule, llmdlog.LevelDebug,
			"compression no-op: tool=%q call=%q orig_tokens=%d wire_tokens=%d (no savings, returning original)",
			ids.ToolName, ids.ToolCallID, origToks, wireToks)
		return IngestResult{Wire: content, OriginalTokens: origToks, WireTokens: origToks}
	}

	method := ""
	if deps.Settings.MinifyDedup {
		method = "minify"
	}
	if deduped {
		method = "dedup"
	}

	// Build the wire form sent to the model. When there was a real size win we wrap
	// it in an expand banner; a first-occurrence anchor with no win is sent as the
	// untouched original (no banner) but still persisted for future dedup.
	outWire := content
	if improved {
		banner := fmt.Sprintf("%s tool_call_id=%q original_tokens=%d wire_tokens=%d method=%s expand=expand_tool_result(%q)]\n",
			bannerTag, ids.ToolCallID, origToks, wireToks, method, ids.ToolCallID)
		outWire = banner + wire
	}

	finalWireToks := tokencount.Count(outWire)

	// Stash original + wire + diagnostics (non-fatal on write error; expansion
	// degrades gracefully and the row aids diagnosis/verification).
	_ = deps.Sidecar.Insert(ctx, store.ToolResultSidecarRow{
		TenantID:       ids.Scope.TenantID,
		ScopeID:        ids.Scope.ScopeID,
		SessionID:      ids.SessionID,
		ToolCallID:     ids.ToolCallID,
		ToolName:       ids.ToolName,
		Original:       content,
		Compressed:     improved,
		Wire:           outWire,
		Method:         method,
		OriginalTokens: origToks,
		WireTokens:     finalWireToks,
	})

	savedToks := origToks - finalWireToks
	savedPct := float64(savedToks) * 100.0 / float64(origToks)
	llmdlog.Stdout(logModule, llmdlog.LevelDebug,
		"compression done: tool=%q call=%q session=%q method=%s anchor=%v orig_tokens=%d wire_tokens=%d saved=%d (%.1f%%)",
		ids.ToolName, ids.ToolCallID, ids.SessionID, method, firstOccurrence && !improved, origToks, finalWireToks, savedToks, savedPct)

	res := IngestResult{
		Wire:           outWire,
		Compressed:     improved,
		OriginalTokens: origToks,
		WireTokens:     finalWireToks,
	}
	if improved {
		res.Method = method
		if deduped {
			res.DedupRefToolCallID = dedupRef
		}
	}
	return res
}

// AgeToolResult collapses an older tool result into a compact, expandable stale
// reference for the send only. It persists the original to the sidecar (so
// expand_tool_result can recover it) and returns the wire form.
//
// Unlike IngestToolResult this is a position-driven policy: the caller decides
// which results are "old" (e.g. beyond the most-recent window). It does not
// depend on dedup keys or content changes. Returns ("", false) when the content
// is already compressed, the sidecar is unavailable, the result is below the
// size threshold, or the stale banner would not actually be smaller.
func AgeToolResult(ctx context.Context, content string, ids IngestIDs, deps Deps) (string, bool) {
	if deps.Sidecar == nil || IsCompressed(content) {
		return "", false
	}
	origToks := tokencount.Count(content)
	if origToks < deps.Settings.MinThresholdToks {
		return "", false
	}
	wire := fmt.Sprintf("%s tool_call_id=%q original_tokens=%d method=stale expand=expand_tool_result(%q)]\n[aged: earlier tool result elided to save context — call expand_tool_result(%q) to retrieve the full output]",
		bannerTag, ids.ToolCallID, origToks, ids.ToolCallID, ids.ToolCallID)
	wireToks := tokencount.Count(wire)
	if wireToks >= origToks {
		return "", false
	}

	// Persist the original so the stale reference stays expandable. Non-fatal on
	// duplicate tool_call_id (a prior turn may have already stored it).
	_ = deps.Sidecar.Insert(ctx, store.ToolResultSidecarRow{
		TenantID:       ids.Scope.TenantID,
		ScopeID:        ids.Scope.ScopeID,
		SessionID:      ids.SessionID,
		ToolCallID:     ids.ToolCallID,
		ToolName:       ids.ToolName,
		Original:       content,
		Compressed:     true,
		Wire:           wire,
		Method:         "stale",
		OriginalTokens: origToks,
		WireTokens:     wireToks,
	})

	llmdlog.Stdout(logModule, llmdlog.LevelDebug,
		"aging: tool=%q call=%q session=%q orig_tokens=%d wire_tokens=%d (stale ref)",
		ids.ToolName, ids.ToolCallID, ids.SessionID, origToks, wireToks)

	return wire, true
}

// structuralMinify collapses noise without removing semantic content:
// - strips blank lines and pure-comment lines (// … or # …)
// - collapses repeated interior whitespace within a line
// - strips a common bare JSON envelope wrapper if present ({\"result\": …} → inner value)
func structuralMinify(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "// ") || strings.HasPrefix(t, "# ") {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}

