package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/tools"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

// ErrUnknownSession is returned when mutating a non-existent session id.
var ErrUnknownSession = errors.New("unknown session")

// ErrWrongScope is returned when a session belongs to a different scope.
var ErrWrongScope = errors.New("session belongs to a different scope")

// logModule tags sessionmanager log lines.
const logModule = "sessionmanager"

// runtimeKey identifies one in-memory runtime: the same session id under two
// scopes is two distinct runtimes.
type runtimeKey struct {
	scope     serverutil.Scope
	sessionID string
}

// normScope trims the scope's fields so map keys are stable, and DROPS the
// principal: a session belongs to a tenant and scope, not to the person whose
// request happened to load it, so two people in one scope must reach one
// runtime.
func normScope(sc serverutil.Scope) serverutil.Scope {
	return serverutil.Scope{TenantID: strings.TrimSpace(sc.TenantID), ScopeID: strings.TrimSpace(sc.ScopeID)}
}

// NormalizeScope is normScope for callers outside this package.
//
// Anyone comparing a runtime's Scope against a request's must normalize first.
// A runtime's scope has been through normScope and therefore carries no
// principal; a request's scope does when the caller sent one. Comparing them
// raw made every session call from a principal-carrying deployment fail as
// "unknown session_id" — the session was right there, under a scope that
// differed only in a field runtimes deliberately ignore.
func NormalizeScope(sc serverutil.Scope) serverutil.Scope { return normScope(sc) }

// ErrMissingStore is returned by New when persistence repos or NewSessionHistory is nil.
var ErrMissingStore = errors.New("sessionmanager: SessionPersist and NewSessionHistory are required")

// Options configures database persistence and GORM-backed chat history (all required).
type Options struct {
	// SessionPersist stores chat session runtimes.
	SessionPersist *store.SessionRepo
	// NewSessionHistory builds a history.Store per session runtime id.
	NewSessionHistory func(sc serverutil.Scope, sessionID string) history.Store
}

// Manager holds in-memory chat session runtimes, keyed by (scope, session id),
// restored on demand from llmd_sessions.
type Manager struct {
	mu             sync.RWMutex
	sessions       map[runtimeKey]*SessionRuntime
	hubOpts        session.HubOptions
	sessionPersist *store.SessionRepo
	newHist        func(sc serverutil.Scope, sessionID string) history.Store
}

// SessionRuntime is one session's live in-memory runtime: observation hub(s),
// tool registry, and history store. Held by Manager, keyed by (scope, session id).
type SessionRuntime struct {
	ID          string
	Scope       serverutil.Scope
	DisplayName string
	CreatedAt   time.Time
	Tools       *toolregistry.Registry
	Core        *session.Agent

	ProviderID   string
	DefaultModel string

	// SystemPromptSections is the session's persisted default structured system
	// prompt (see llm.v1.EnsureSessionRequest.system_prompt_sections), set once
	// at EnsureSession time. ChatStream falls back to this when a given
	// ChatStreamRequest carries no system_prompt_sections of its own.
	SystemPromptSections []*llmv1.SystemPromptSection

	// DisabledTools is the set of server-wide tool names hidden from this
	// session's model-facing tool list by default (see
	// llm.v1.EnsureSessionRequest.disabled_tools). Names are stored as given
	// (trimmed, empties dropped, de-duplicated) with no validation against any
	// registry — a host may register the named tool later. nil/empty = no
	// exclusions. This is the raw persisted set: explicit per-session tool
	// registration does NOT undo an entry (deny wins); only core tools are
	// exempt. The precedence is enforced by toolmerge.Effective, not here.
	DisabledTools map[string]struct{}

	// ToolPolicy is the persisted spelling of this session's tool policy (see
	// llm.v1.EnsureSessionRequest.tool_policy and agentprofile.PolicyName): ""
	// = inherit server-wide, "profile_only" = only core tools and the tools
	// this session registered itself. Parsed with toolmerge.ParsePolicy at the
	// merge sites, which fail open to inherit for an unrecognised value.
	ToolPolicy string

	// ToolContext is the session's persisted default tool_context (see
	// llm.v1.EnsureSessionRequest.tool_context). ChatStream lets a request's own
	// tool_context override it. Read it through ToolContextCopy.
	ToolContext map[string]string

	lastMS  atomic.Int64
	streams atomic.Int32
	// loadedMS is when this runtime was constructed/restored into memory. Idle
	// eviction keys off max(lastMS, loadedMS) so a runtime restored merely for a
	// status read (lastMS = old CreatedAt) is not evicted and reloaded on every
	// sweep.
	loadedMS atomic.Int64
}

// New constructs a Manager. SessionPersist and NewSessionHistory must both be non-nil (use in-memory SQLite in tests).
func New(hubOpts session.HubOptions, opt Options) (*Manager, error) {
	if opt.SessionPersist == nil || opt.NewSessionHistory == nil {
		return nil, ErrMissingStore
	}
	return &Manager{
		sessions:       make(map[runtimeKey]*SessionRuntime),
		hubOpts:        hubOpts,
		sessionPersist: opt.SessionPersist,
		newHist:        opt.NewSessionHistory,
	}, nil
}

// RuntimeCount returns the number of session runtimes currently loaded in
// memory. Runtimes are only a cache over DB state (see EvictIdle), so this
// is a diagnostics gauge, not an authoritative session count.
func (mgr *Manager) RuntimeCount() int {
	if mgr == nil {
		return 0
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return len(mgr.sessions)
}

func (mgr *Manager) makeHistory(sc serverutil.Scope, sessionID string) history.Store {
	return mgr.newHist(sc, sessionID)
}

// HistoryFor returns the persisted history store for a session WITHOUT loading
// (or requiring) its runtime: for read paths that only need the transcript.
// Prefers a loaded runtime's own store so in-memory history stays coherent.
// Returns nil for a nil manager or blank session id.
func (mgr *Manager) HistoryFor(sc serverutil.Scope, sessionID string) history.Store {
	sid := strings.TrimSpace(sessionID)
	if mgr == nil || sid == "" {
		return nil
	}
	if rt, ok := mgr.Get(sc, sid); ok && rt != nil && rt.Core != nil {
		if h := rt.Core.History(); h != nil {
			return h
		}
	}
	return mgr.makeHistory(normScope(sc), sid)
}

// Touch updates last-activity timestamp (Unix ms).
func (rt *SessionRuntime) Touch() {
	rt.lastMS.Store(time.Now().UnixMilli())
}

// DisabledToolSet returns a copy of the session's default tool exclusions
// (see DisabledTools). The live map never escapes: callers hand the result to
// merge logic outside mgr.mu and may mutate it freely. A nil receiver or a nil
// set yields nil.
func (rt *SessionRuntime) DisabledToolSet() map[string]struct{} {
	if rt == nil || len(rt.DisabledTools) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(rt.DisabledTools))
	for name := range rt.DisabledTools {
		out[name] = struct{}{}
	}
	return out
}

// ToolPolicyName returns the session's persisted tool policy spelling, "" for
// inherit-server-wide. Nil-receiver safe so a caller with no runtime loaded
// gets the permissive default rather than a panic.
func (rt *SessionRuntime) ToolPolicyName() string {
	if rt == nil {
		return ""
	}
	return rt.ToolPolicy
}

// toolPolicyName maps the request enum to its persisted spelling. Kept here
// rather than imported from agentprofile so sessionmanager does not depend on
// the profile package; toolmerge owns the one spelling both agree on.
func toolPolicyName(p llmv1.ToolPolicy) string {
	if p == llmv1.ToolPolicy_TOOL_POLICY_PROFILE_ONLY {
		return toolmerge.PolicyProfileOnlyName
	}
	return ""
}

// disabledToolSet builds the in-memory DisabledTools set from a list of tool
// names: trimmed, empties dropped, de-duplicated. Returns nil when nothing
// survives so "no exclusions" is always represented the same way.
func disabledToolSet(names []string) map[string]struct{} {
	var out map[string]struct{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if out == nil {
			out = make(map[string]struct{})
		}
		out[name] = struct{}{}
	}
	return out
}

// MarkChatStreamStart increments active stream count (call when a ChatStream RPC begins for this session).
func (rt *SessionRuntime) MarkChatStreamStart() {
	rt.streams.Add(1)
	rt.Touch()
}

// MarkChatStreamEnd decrements active stream count (defer when the RPC completes).
func (rt *SessionRuntime) MarkChatStreamEnd() {
	rt.streams.Add(-1)
	rt.Touch()
}

// ActiveStreams returns in-flight ChatStream RPCs associated with this session.
func (rt *SessionRuntime) ActiveStreams() int32 {
	return rt.streams.Load()
}

// LastActivityUnixMs returns last recorded activity in Unix milliseconds.
func (rt *SessionRuntime) LastActivityUnixMs() int64 {
	return rt.lastMS.Load()
}

// idleRefUnixMs is the reference point for idle eviction: the newer of last
// activity and in-memory load time.
func (rt *SessionRuntime) idleRefUnixMs() int64 {
	last := rt.lastMS.Load()
	if loaded := rt.loadedMS.Load(); loaded > last {
		return loaded
	}
	return last
}

// EvictIdle unloads runtimes with no active ChatStream and no activity (or
// load) newer than maxIdle ago, returning the evicted ids sorted. A runtime is
// only a cache: history, tools JSON, and session rows are all DB-persisted,
// and EnsureLoaded/EnsureSession restore an evicted runtime on demand. A
// stream that starts between the ActiveStreams check and the delete keeps
// its *SessionRuntime pointer and completes normally (history writes go to
// the DB either way), so that race is benign.
func (mgr *Manager) EvictIdle(maxIdle time.Duration) []string {
	if mgr == nil || maxIdle <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-maxIdle).UnixMilli()
	var evicted []string
	mgr.mu.Lock()
	for key, rt := range mgr.sessions {
		if rt == nil {
			delete(mgr.sessions, key)
			continue
		}
		if rt.ActiveStreams() > 0 || rt.idleRefUnixMs() > cutoff {
			continue
		}
		delete(mgr.sessions, key)
		evicted = append(evicted, key.sessionID)
	}
	mgr.mu.Unlock()
	sort.Strings(evicted)
	return evicted
}

// Get returns a session's runtime by scope and id.
func (mgr *Manager) Get(sc serverutil.Scope, id string) (*SessionRuntime, bool) {
	id = strings.TrimSpace(id)
	if mgr == nil || id == "" {
		return nil, false
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	rt, ok := mgr.sessions[runtimeKey{scope: normScope(sc), sessionID: id}]
	return rt, ok
}

// LoadedInAnyScope reports whether a runtime for id is in memory under any
// scope. Occupancy only — for hosts whose API carries no scope.
func (mgr *Manager) LoadedInAnyScope(id string) bool {
	id = strings.TrimSpace(id)
	if mgr == nil || id == "" {
		return false
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	for key := range mgr.sessions {
		if key.sessionID == id {
			return true
		}
	}
	return false
}

// EnsureLoaded loads a runtime from persistence into memory when it is missing from the map.
func (mgr *Manager) EnsureLoaded(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if mgr == nil || mgr.sessionPersist == nil {
		return fmt.Errorf("session manager not configured")
	}
	sc = normScope(sc)
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("tenant and session_id are required")
	}
	if _, ok := mgr.Get(sc, sid); ok {
		return nil
	}
	if row, err := mgr.sessionPersist.GetSession(ctx, sc, sid); err == nil {
		tools, err := store.UnmarshalToolsJSON(row.ToolsJSON)
		if err != nil {
			return fmt.Errorf("session %q tools: %w", sid, err)
		}
		return mgr.RestoreSession(*row, tools)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return fmt.Errorf("%w: %q", ErrUnknownSession, sid)
}

// Destroy removes a runtime and purges its persisted llmd_sessions row.
func (mgr *Manager) Destroy(ctx context.Context, sc serverutil.Scope, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	sc = normScope(sc)
	if sc.TenantID == "" {
		return false
	}
	key := runtimeKey{scope: sc, sessionID: id}

	mgr.mu.Lock()
	_, ok := mgr.sessions[key]
	mgr.mu.Unlock()

	if ok {
		if err := mgr.sessionPersist.PurgeSession(ctx, sc, id); err != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn, "destroy: purge session %q (%s): %v", id, sc.String(), err)
			return false
		}
		mgr.mu.Lock()
		delete(mgr.sessions, key)
		mgr.mu.Unlock()
		return true
	}

	if _, err := mgr.sessionPersist.GetSession(ctx, sc, id); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn, "destroy: load session %q (%s): %v", id, sc.String(), err)
		}
		return false
	}
	if err := mgr.sessionPersist.PurgeSession(ctx, sc, id); err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "destroy: purge session %q (%s): %v", id, sc.String(), err)
		return false
	}
	return true
}

// UpdateLlm changes provider binding and/or default model for a session runtime.
func (mgr *Manager) UpdateLlm(ctx context.Context, sc serverutil.Scope, sessionID string, providerID, model *string) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("session_id is required")
	}
	sc = normScope(sc)
	if sc.TenantID == "" {
		return fmt.Errorf("tenant is required")
	}
	if err := mgr.EnsureLoaded(ctx, sc, sid); err != nil {
		return err
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	rt, ok := mgr.sessions[runtimeKey{scope: sc, sessionID: sid}]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSession, sid)
	}
	if rt.Scope != sc {
		return fmt.Errorf("%w", ErrWrongScope)
	}

	if providerID != nil {
		rt.ProviderID = strings.TrimSpace(*providerID)
	}
	if model != nil {
		rt.DefaultModel = strings.TrimSpace(*model)
	}
	rt.Touch()

	return mgr.sessionPersist.UpdateSessionLlm(ctx, sc, sid, rt.ProviderID, rt.DefaultModel)
}

// PersistToolSnapshot writes the current tool registry to the database for this session.
func (mgr *Manager) PersistToolSnapshot(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if mgr == nil {
		return nil
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return fmt.Errorf("session_id is required")
	}
	sc = normScope(sc)
	if sc.TenantID == "" {
		return fmt.Errorf("tenant is required")
	}
	rt, ok := mgr.Get(sc, sid)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSession, sid)
	}
	if rt.Scope != sc {
		return fmt.Errorf("%w", ErrWrongScope)
	}
	tj, err := store.MarshalToolsJSON(rt.Tools.Snapshot())
	if err != nil {
		return err
	}
	return mgr.sessionPersist.SaveToolsJSON(ctx, sc, sid, tj)
}

// RestoreSession loads one chat session runtime from llmd_sessions into memory.
func (mgr *Manager) RestoreSession(row store.SessionRow, tools []*llmv1.ToolDefinition) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	sid := strings.TrimSpace(row.SessionID)
	if sid == "" {
		return fmt.Errorf("session_id is required")
	}
	sc := normScope(serverutil.Scope{TenantID: row.TenantID, ScopeID: row.ScopeID})
	key := runtimeKey{scope: sc, sessionID: sid}
	if _, exists := mgr.sessions[key]; exists {
		return nil
	}
	reg := toolregistry.New()
	for _, td := range tools {
		if td == nil {
			continue
		}
		if err := llmtools.ValidateParametersJSON(td.GetParametersSchemaJson()); err != nil {
			return err
		}
		if err := reg.Register(td); err != nil {
			return err
		}
	}
	sections, err := store.UnmarshalSystemPromptSectionsJSON(row.SystemPromptSectionsJSON)
	if err != nil {
		return fmt.Errorf("session %q system_prompt_sections: %w", sid, err)
	}
	disabledNames, err := store.UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		return fmt.Errorf("session %q disabled_tools: %w", sid, err)
	}
	toolCtx, err := store.UnmarshalToolContextJSON(row.ToolContextJSON)
	if err != nil {
		return fmt.Errorf("session %q tool_context: %w", sid, err)
	}

	hst := mgr.makeHistory(sc, sid)
	core := session.NewAgent(sid, hst, mgr.hubOpts)
	rt := &SessionRuntime{
		ID:                   sid,
		Scope:                sc,
		ProviderID:           row.ProviderID,
		DefaultModel:         row.DefaultModel,
		SystemPromptSections: sections,
		DisabledTools:        disabledToolSet(disabledNames),
		ToolPolicy:           strings.TrimSpace(row.ToolPolicy),
		ToolContext:          toolCtx,
		CreatedAt:            row.CreatedAt,
		Tools:                reg,
		Core:                 core,
	}
	if rt.CreatedAt.IsZero() {
		rt.CreatedAt = time.Now()
	}
	rt.lastMS.Store(rt.CreatedAt.UnixMilli())
	rt.loadedMS.Store(time.Now().UnixMilli())
	mgr.sessions[key] = rt
	return nil
}
