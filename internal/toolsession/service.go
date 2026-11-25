// Package toolsession owns per-session tool-result handling: compression-settings
// resolution, structural minify + dedup, and sidecar persistence. Both the gRPC
// tool loop (ChatStream) and the MCP delegated-tool path (CLI/agentic providers)
// hold a pointer to ONE shared Service instance, so dedup state and sidecar writes
// on either surface are visible to the other and flow through identical
// transformations. This is the seam for future transformations and metrics.
package toolsession

import (
	"context"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolresult"
	"github.com/johnewart/pyxis/llmdlog"
)

const logModule = "toolsession"

// Service is the single chokepoint for a session's tool results.
type Service struct {
	sidecar  *store.ToolResultSidecarRepo
	sessions *store.SessionRepo
	// dedupMaps holds per-session content-dedup state (sessionID → *SessionDedupMap),
	// lazily created by Dedup and cleared on session history delete.
	dedupMaps sync.Map
	// toolDedupKeys maps sessionID → *sync.Map(toolCallID → semantic dedupKey),
	// recorded when a delegated tool (e.g. read_code via the provider hub) returns
	// a content-identity key. The gRPC ChatStream path consults this so API
	// providers (OpenAI/ollama) get the SAME semantic dedup as CLI providers,
	// rather than only tool_call_id-keyed resend dedup. Cleared with dedup state.
	toolDedupKeys sync.Map
	// decorations persists typed turn/tool-result decorations
	// (llmd_turn_decorations) written through AddDecoration. May be nil
	// (decorations then degrade to a no-op).
	decorations *store.TurnDecorationRepo
}

// New constructs a Service over the sidecar and session repositories. Any may
// be nil (the corresponding feature then degrades to a no-op).
func New(sidecar *store.ToolResultSidecarRepo, sessions *store.SessionRepo, decorations *store.TurnDecorationRepo) *Service {
	return &Service{sidecar: sidecar, sessions: sessions, decorations: decorations}
}

// SettingsFor resolves compression settings from a session row. Pure and
// dependency-free and deterministic: the master ToolResultCompressionEnabled flag
// gates everything; the mechanism flags select what runs. Defaults: threshold=2000.
func SettingsFor(session *store.SessionRow) toolresult.Settings {
	disabled := toolresult.Settings{Enabled: false, MinThresholdToks: 2000}
	if session == nil {
		return disabled
	}
	enabled := session.ToolResultCompressionEnabled != nil && *session.ToolResultCompressionEnabled
	if !enabled {
		return disabled
	}
	minify := session.ToolResultMinifyDedup != nil && *session.ToolResultMinifyDedup
	extractiveSearch := session.ToolResultExtractiveSearch != nil && *session.ToolResultExtractiveSearch
	threshold := int64(2000)
	if session.ToolResultCompressionThreshold != nil && *session.ToolResultCompressionThreshold > 0 {
		threshold = *session.ToolResultCompressionThreshold
	}
	ageKeep := int64(6)
	if session.ToolResultAgeKeep != nil {
		ageKeep = *session.ToolResultAgeKeep
	}
	return toolresult.Settings{
		Enabled:          true,
		MinThresholdToks: threshold,
		MinifyDedup:      minify,
		AgeKeepRecent:    ageKeep,
		ExtractiveSearch: extractiveSearch,
	}
}

// Dedup returns (or lazily creates) the SessionDedupMap for a session. Returns
// nil for an empty session id (dedup disabled, e.g. unary Chat).
func (s *Service) Dedup(sessionID string) *toolresult.SessionDedupMap {
	if s == nil || sessionID == "" {
		return nil
	}
	v, _ := s.dedupMaps.LoadOrStore(sessionID, &toolresult.SessionDedupMap{})
	return v.(*toolresult.SessionDedupMap)
}

// ClearDedup drops the in-memory dedup state for a session (called on session
// history delete so a reused session id starts with a clean slate).
func (s *Service) ClearDedup(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.dedupMaps.Delete(sessionID)
	s.toolDedupKeys.Delete(sessionID)
}

// RecordToolDedupKey remembers the semantic dedupKey a delegated tool returned
// for a tool_call_id, so the ChatStream send-time compression can prefer it over
// the tool_call_id. No-op for empty ids/key.
func (s *Service) RecordToolDedupKey(sessionID, toolCallID, dedupKey string) {
	if s == nil || sessionID == "" || toolCallID == "" || dedupKey == "" {
		return
	}
	v, _ := s.toolDedupKeys.LoadOrStore(sessionID, &sync.Map{})
	v.(*sync.Map).Store(toolCallID, dedupKey)
}

// ToolDedupKey returns the semantic dedupKey recorded for a tool_call_id, or "".
func (s *Service) ToolDedupKey(sessionID, toolCallID string) string {
	if s == nil || sessionID == "" || toolCallID == "" {
		return ""
	}
	v, ok := s.toolDedupKeys.Load(sessionID)
	if !ok {
		return ""
	}
	k, ok := v.(*sync.Map).Load(toolCallID)
	if !ok {
		return ""
	}
	return k.(string)
}

// ToolInvocationEnvelope carries the identifiers, dedup metadata, and optional
// passive decorations for one delegated tool invocation, regardless of which
// transport surfaced it. It lets IngestDelegatedResult give the gRPC tool loop,
// the MCP hub path, and the MCP HTTP-delegate path identical dedup-key
// recording and compression through one call.
type ToolInvocationEnvelope struct {
	Scope     serverutil.Scope
	SessionID string
	// TurnID is the legacy/external turn label; "" for MCP-authored results
	// (which have no external turn id), the gRPC loop's turn id otherwise.
	TurnID     string
	ToolCallID string
	ToolName   string
	// TurnSeq is the session's turn_seq (from store.TurnRegister.Current) as of
	// the moment this tool invocation STARTED, not when the result was ingested.
	// Callers must capture it via TurnRegister.Current(sessionID) at invocation
	// start, before dispatching the tool, so a still-in-flight tool's envelope
	// keeps carrying the turn that was active when it began even if the turn
	// register advances again while the tool runs.
	TurnSeq int64
	// Source identifies the transport that produced this invocation:
	// "grpc-loop", "mcp-hub", or "mcp-http".
	Source string
	// DedupKey is the opaque, tool-provided content-identity key (optional).
	DedupKey string
	// DedupSummary is an optional one-line digest shown on dedup back-references.
	DedupSummary string
	// Decorations are optional passive, out-of-band pointers the caller wants
	// attached to this turn/result (nil/empty => none). llmd persists them
	// verbatim through the single IngestDelegatedResult chokepoint so they
	// inherit this envelope's TurnSeq/TurnID; it never interprets Kind or URI.
	Decorations []Decoration
}

// Decoration is one passive, out-of-band pointer attached to a turn or a tool
// result — never interpreted by llmd. The emitting system owns Kind's
// vocabulary and URI's scheme; any data (confidence, labels, counts, …) lives
// at whatever URI resolves to, not here. Decorations NEVER feed back into
// chat_messages content or prompt bytes, and the model/user never see them.
type Decoration struct {
	// Kind discriminates the decoration type for interested readers (e.g.
	// "host_provenance", "entity_reference"). Required. Opaque to llmd.
	Kind string
	// URI is an opaque pointer to the emitter-owned data this decoration
	// references (e.g. "host://provenance/<id>", "owlbert://entity/<id>").
	URI string
}

// AddDecoration persists one decoration keyed by the envelope's canonical
// identity: tool-result-level when env.ToolCallID is set, turn-level otherwise.
// Best-effort by design: failures are logged, never surfaced to the tool path.
// No-op when the decorations repo is nil, the session id is empty, or Kind is
// empty.
func (s *Service) AddDecoration(ctx context.Context, env ToolInvocationEnvelope, d Decoration) {
	if s == nil || s.decorations == nil || env.SessionID == "" || strings.TrimSpace(d.Kind) == "" {
		return
	}
	target := "turn"
	if strings.TrimSpace(env.ToolCallID) != "" {
		target = "tool_result"
	}
	var turnSeq *int64
	if env.TurnSeq > 0 {
		seq := env.TurnSeq
		turnSeq = &seq
	}
	row := store.TurnDecorationRow{
		TenantID:   strings.TrimSpace(env.Scope.TenantID),
		ScopeID:    strings.TrimSpace(env.Scope.ScopeID),
		SessionID:  env.SessionID,
		TurnSeq:    turnSeq,
		TurnID:     env.TurnID,
		Target:     target,
		ToolCallID: strings.TrimSpace(env.ToolCallID),
		Kind:       strings.TrimSpace(d.Kind),
		URI:        d.URI,
	}
	if err := s.decorations.Insert(ctx, []store.TurnDecorationRow{row}); err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"decoration insert failed: session=%q kind=%q target=%q call=%q: %v",
			env.SessionID, d.Kind, target, env.ToolCallID, err)
	}
}

// ListDecorations returns a session's persisted decorations in insertion order.
func (s *Service) ListDecorations(ctx context.Context, sc serverutil.Scope, sessionID string) ([]store.TurnDecorationRow, error) {
	if s == nil || s.decorations == nil {
		return nil, nil
	}
	return s.decorations.ListBySession(ctx, sc, sessionID)
}

// ids converts the envelope into the toolresult identity struct — the single
// place where the envelope's fields map onto toolresult.IngestIDs.
func (env ToolInvocationEnvelope) ids() toolresult.IngestIDs {
	return toolresult.IngestIDs{
		Scope: serverutil.Scope{
			TenantID: strings.TrimSpace(env.Scope.TenantID),
			ScopeID:  strings.TrimSpace(env.Scope.ScopeID),
		},
		SessionID:  env.SessionID,
		TurnID:     env.TurnID,
		ToolCallID: env.ToolCallID,
		ToolName:   env.ToolName,
	}
}

// IngestDelegatedResult is the unified chokepoint entry point for a delegated
// tool invocation, used by the MCP transports (hub and HTTP-delegate paths) via
// a ToolInvocationEnvelope. It runs both phases back to back: RecordInvocation
// (dedup key + decorations) then CompressResult (settings load + shared
// compression pipeline). The gRPC tool loop calls the two phases separately —
// RecordInvocation at tool-finish, CompressResult at history-append — to keep
// API-provider dedup parity.
//
// On error (isError=true), neither the dedup key nor compression is recorded,
// matching the existing !isErr guards at the call sites. content is returned
// unchanged when compression is disabled, unavailable, or already compressed.
func (s *Service) IngestDelegatedResult(ctx context.Context, env ToolInvocationEnvelope, content string, isError bool) string {
	if s == nil || s.sessions == nil || env.SessionID == "" {
		return content
	}
	if isError {
		return content
	}
	s.RecordInvocation(ctx, env)
	return s.CompressResult(ctx, env, content).Wire
}

// RecordInvocation records the content-free side-channel metadata for one
// delegated tool invocation: the semantic dedup key and any passive
// decorations. It is the "record-early" phase — call it at tool-finish,
// before the result content is (later) compressed at history-append, so a
// subsequent result in the same turn can back-reference this invocation's
// dedup key. Never call for error results; errors record nothing.
func (s *Service) RecordInvocation(ctx context.Context, env ToolInvocationEnvelope) {
	if s == nil || env.SessionID == "" {
		return
	}
	if env.DedupKey != "" {
		s.RecordToolDedupKey(env.SessionID, env.ToolCallID, env.DedupKey)
	}
	// Decorations ride the same envelope as the dedup key so they inherit
	// TurnSeq/TurnID exactly as the tool result carried them. llmd persists
	// each verbatim — it never interprets Kind or URI.
	for _, d := range env.Decorations {
		s.AddDecoration(ctx, env, d)
	}
}

// CompressResult runs the compression phase for one delegated tool result:
// loads the session's compression settings and pushes the content through the
// shared toolresult pipeline (minify + dedup, original stashed in the sidecar).
// Returns Wire==content (Compressed=false) when compression is disabled,
// unavailable, or the content is empty/already compressed. The "compress-late"
// counterpart to RecordInvocation; IngestDelegatedResult runs both back to back.
func (s *Service) CompressResult(ctx context.Context, env ToolInvocationEnvelope, content string) toolresult.IngestResult {
	if s == nil || s.sessions == nil || env.SessionID == "" {
		return toolresult.IngestResult{Wire: content}
	}
	if content == "" || toolresult.IsCompressed(content) {
		return toolresult.IngestResult{Wire: content}
	}
	if s.sidecar == nil {
		return toolresult.IngestResult{Wire: content}
	}
	sess, serr := s.sessions.GetSession(ctx, env.Scope, env.SessionID)
	if serr != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"delegated tool result: load session for compression settings failed %s session=%q call=%q: %v (using defaults)",
			env.Scope.String(), env.SessionID, env.ToolCallID, serr)
		sess = nil
	}
	settings := SettingsFor(sess)
	if !settings.Enabled {
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"delegated tool result: compression disabled session=%q tool=%q call=%q source=%q",
			env.SessionID, env.ToolName, env.ToolCallID, env.Source)
		return toolresult.IngestResult{Wire: content}
	}
	llmdlog.Stdout(logModule, llmdlog.LevelInfo,
		"delegated tool result ingest: session=%q tool=%q call=%q source=%q orig_bytes=%d",
		env.SessionID, env.ToolName, env.ToolCallID, env.Source, len(content))
	res := toolresult.IngestToolResult(ctx, content, env.ids(), toolresult.Deps{
		Sidecar:      s.sidecar,
		Settings:     settings,
		SessionDedup: s.Dedup(env.SessionID),
		DedupKey:     env.DedupKey,
		DedupSummary: env.DedupSummary,
	})
	return res
}
