package providers

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/johnewart/pyxis/internal/llm/backend"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
)

// CatalogModel is one discovered model id for a provider.
type CatalogModel struct {
	ProviderID string
	ModelID    string
	Display    string
}

// Entry is one configured provider plus cached model catalog metadata.
type Entry struct {
	ID           string
	Kind         providerkind.Kind
	Backend      backend.Backend
	DefaultModel string

	// Scan endpoints / credentials (not exposed over gRPC).
	ollamaBase    string
	openAIBase    string
	openAIKey     string
	anthropicBase string
	anthropicKey  string

	cursorBinary    string
	cursorCwd       string // subprocess working directory (matches TS spawn cwd)
	cursorAPIKey    string
	cursorWorkspace string // --workspace path for Cursor; used to write .cursor/mcp.json

	claudeBinary     string
	claudeCwd        string
	claudeConfigDir  string
	claudeOAuthToken string

	geminiBase string
	geminiKey  string

	modelsMu sync.RWMutex
	models   []CatalogModel

	refreshedUnixMs atomic.Int64
}

// ProbeClaudeCodeV2Resume runs the headless resume capability check for Claude Code v2 providers.
func (e *Entry) ProbeClaudeCodeV2Resume() error {
	if e == nil || e.Kind != providerkind.ClaudeCodeV2 {
		return nil
	}
	r := claudecodeclillm.ProbeClaudeCode(e.claudeProbeOptions())
	if r.OK {
		return nil
	}
	msg := strings.TrimSpace(r.Error)
	if msg == "" {
		msg = claudecodeclillm.ClaudeV2ProbeUserFacingError()
	}
	if d := strings.TrimSpace(r.Details); d != "" {
		return fmt.Errorf("%s (%s)", msg, d)
	}
	return fmt.Errorf("%s", msg)
}

// claudeProbeOptions probes with the provider's own config dir and OAuth token,
// so the check authenticates exactly as a real turn would.
func (e *Entry) claudeProbeOptions() claudecodeclillm.ProbeOptions {
	return claudecodeclillm.ProbeOptions{
		Binary:     e.claudeBinary,
		Cwd:        e.claudeCwd,
		ConfigDir:  e.claudeConfigDir,
		OAuthToken: e.claudeOAuthToken,
	}
}

// RefreshedUnixMs returns last successful catalog refresh, or 0.
func (e *Entry) RefreshedUnixMs() int64 {
	if e == nil {
		return 0
	}
	return e.refreshedUnixMs.Load()
}

// CatalogLen returns the number of cached catalog entries.
// CursorWorkspace returns the configured cursor_cli.workspace path when this entry is a Cursor CLI provider.
func (e *Entry) CursorWorkspace() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.cursorWorkspace)
}

func (e *Entry) CatalogLen() int {
	if e == nil {
		return 0
	}
	e.modelsMu.RLock()
	defer e.modelsMu.RUnlock()
	return len(e.models)
}

// SnapshotCatalog returns a copy of cached models (read lock).
func (e *Entry) SnapshotCatalog() []CatalogModel {
	if e == nil {
		return nil
	}
	e.modelsMu.RLock()
	defer e.modelsMu.RUnlock()
	out := make([]CatalogModel, len(e.models))
	copy(out, e.models)
	return out
}

func (e *Entry) replaceModels(models []CatalogModel, refreshedMs int64) {
	e.modelsMu.Lock()
	e.models = models
	e.modelsMu.Unlock()
	e.refreshedUnixMs.Store(refreshedMs)
}
