package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	llmtools "github.com/johnewart/pyxis/internal/llm/tools"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

// ErrSessionExists is returned when EnsureSession is called for an existing session id.
var ErrSessionExists = errors.New("session already exists")

// EnsureSession provisions or reloads a chat session runtime (persisted in llmd_sessions).
func (mgr *Manager) EnsureSession(ctx context.Context, req *llmv1.EnsureSessionRequest, sc serverutil.Scope) error {
	if mgr == nil || mgr.sessionPersist == nil {
		return fmt.Errorf("session manager not configured")
	}
	if req == nil {
		return fmt.Errorf("request is nil")
	}
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return fmt.Errorf("session_id is required")
	}
	sc = normScope(sc)
	if sc.TenantID == "" {
		return fmt.Errorf("tenant is required")
	}

	if _, ok := mgr.Get(sc, sid); ok {
		return mgr.applySessionLlmUpdate(ctx, sc, sid, req)
	}

	if row, err := mgr.sessionPersist.GetSession(ctx, sc, sid); err == nil {
		tools, err := store.UnmarshalToolsJSON(row.ToolsJSON)
		if err != nil {
			return fmt.Errorf("session %q tools: %w", sid, err)
		}
		if err := mgr.RestoreSession(*row, tools); err != nil {
			return err
		}
		return mgr.applySessionLlmUpdate(ctx, sc, sid, req)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	reg := toolregistry.New()
	for _, td := range req.GetInitialTools() {
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

	now := time.Now()
	providerID := ""
	defaultModel := ""
	if req.LlmProviderId != nil {
		providerID = strings.TrimSpace(*req.LlmProviderId)
	}
	if req.DefaultModel != nil {
		defaultModel = strings.TrimSpace(*req.DefaultModel)
	}
	sectionsJSON, err := store.MarshalSystemPromptSectionsJSON(req.GetSystemPromptSections())
	if err != nil {
		return fmt.Errorf("session %q system_prompt_sections: %w", sid, err)
	}
	disabledJSON, err := store.MarshalDisabledToolsJSON(req.GetDisabledTools())
	if err != nil {
		return fmt.Errorf("session %q disabled_tools: %w", sid, err)
	}

	toolContextJSON, err := store.MarshalToolContextJSON(req.GetToolContext())
	if err != nil {
		return fmt.Errorf("session %q tool_context: %w", sid, err)
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	key := runtimeKey{scope: sc, sessionID: sid}
	if _, exists := mgr.sessions[key]; exists {
		return mgr.applySessionLlmUpdateLocked(ctx, sc, sid, req)
	}

	hst := mgr.makeHistory(sc, sid)
	core := session.NewAgent(sid, hst, mgr.hubOpts)
	rt := &SessionRuntime{
		ID:                   sid,
		Scope:                sc,
		DisplayName:          strings.TrimSpace(req.GetDisplayName()),
		ProviderID:           providerID,
		DefaultModel:         defaultModel,
		SystemPromptSections: req.GetSystemPromptSections(),
		DisabledTools:        disabledToolSet(req.GetDisabledTools()),
		ToolPolicy:           toolPolicyName(req.GetToolPolicy()),
		ToolContext:          copyToolContext(req.GetToolContext()),
		CreatedAt:            now,
		Tools:                reg,
		Core:                 core,
	}
	rt.lastMS.Store(now.UnixMilli())
	rt.loadedMS.Store(now.UnixMilli())
	mgr.sessions[key] = rt

	tj, err := store.MarshalToolsJSON(reg.Snapshot())
	if err != nil {
		delete(mgr.sessions, key)
		return err
	}
	row := store.SessionRow{
		TenantID:                 sc.TenantID,
		ScopeID:                  sc.ScopeID,
		SessionID:                sid,
		ProviderID:               providerID,
		DefaultModel:             defaultModel,
		ToolsJSON:                tj,
		SystemPromptSectionsJSON: sectionsJSON,
		// "" when the caller omits disabled_tools; the conflict clause keeps any
		// previously-stored set (keepIfBlank) rather than blanking it.
		DisabledToolsJSON: disabledJSON,
		// "" when the caller omits tool_policy; keepIfBlank then preserves a
		// previously-stored policy rather than widening the session.
		ToolPolicy: toolPolicyName(req.GetToolPolicy()),
		// Empty when the caller omits it; EnsureSessionRow's conflict clause then
		// leaves any previously-stored surface alone rather than blanking it.
		ChatSurface: strings.TrimSpace(req.GetChatSurface()),
		// "" when tool_context is omitted; keepIfBlank preserves a stored map.
		ToolContextJSON: toolContextJSON,
		CreatedAt:       now.UTC(),
	}
	// EnsureSessionRow uses INSERT ... ON CONFLICT DO UPDATE for only the runtime
	// columns so that compression settings pre-written by UpdateSessionCompressionSettings
	// (before the first turn) are preserved rather than overwritten with NULL.
	if err := mgr.sessionPersist.EnsureSessionRow(ctx, row); err != nil {
		delete(mgr.sessions, key)
		return err
	}
	return nil
}

func (mgr *Manager) applySessionLlmUpdate(ctx context.Context, sc serverutil.Scope, sid string, req *llmv1.EnsureSessionRequest) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.applySessionLlmUpdateLocked(ctx, sc, sid, req)
}

func (mgr *Manager) applySessionLlmUpdateLocked(ctx context.Context, sc serverutil.Scope, sid string, req *llmv1.EnsureSessionRequest) error {
	sc = normScope(sc)
	hasSections := len(req.GetSystemPromptSections()) > 0
	hasDisabled := len(req.GetDisabledTools()) > 0
	// TOOL_POLICY_UNSPECIFIED means "not supplied": an idempotent EnsureSession
	// that omits the field must leave a PROFILE_ONLY session as it is, or a bare
	// reconnect would silently hand it every server-wide tool.
	hasPolicy := req.GetToolPolicy() != llmv1.ToolPolicy_TOOL_POLICY_UNSPECIFIED
	hasReplace := req.GetReplaceTools()
	hasToolContext := len(req.GetToolContext()) > 0
	if req.LlmProviderId == nil && req.DefaultModel == nil && !hasSections && !hasDisabled && !hasPolicy && !hasReplace && !hasToolContext {
		return nil
	}
	rt, ok := mgr.sessions[runtimeKey{scope: sc, sessionID: sid}]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownSession, sid)
	}
	if rt.Scope != sc {
		return fmt.Errorf("%w", ErrWrongScope)
	}
	if req.LlmProviderId != nil {
		rt.ProviderID = strings.TrimSpace(*req.LlmProviderId)
	}
	if req.DefaultModel != nil {
		rt.DefaultModel = strings.TrimSpace(*req.DefaultModel)
	}
	rt.Touch()
	if req.LlmProviderId != nil || req.DefaultModel != nil {
		if err := mgr.sessionPersist.UpdateSessionLlm(ctx, sc, sid, rt.ProviderID, rt.DefaultModel); err != nil {
			return err
		}
	}
	// Only an EXPLICIT non-empty system_prompt_sections list overwrites the
	// persisted session default — an ordinary reconnect EnsureSession call that
	// omits the field (e.g. only touching llm_provider_id) must never wipe it.
	if hasSections {
		rt.SystemPromptSections = req.GetSystemPromptSections()
		sectionsJSON, err := store.MarshalSystemPromptSectionsJSON(rt.SystemPromptSections)
		if err != nil {
			return fmt.Errorf("session %q system_prompt_sections: %w", sid, err)
		}
		if err := mgr.sessionPersist.UpdateSessionSystemPromptSections(ctx, sc, sid, sectionsJSON); err != nil {
			return err
		}
	}
	// Same rule for disabled_tools: only an EXPLICIT non-empty list replaces the
	// persisted set wholesale. An omitted field leaves both the in-memory set
	// and the disabled_tools_json column untouched.
	if hasDisabled {
		rt.DisabledTools = disabledToolSet(req.GetDisabledTools())
		disabledJSON, err := store.MarshalDisabledToolsJSON(req.GetDisabledTools())
		if err != nil {
			return fmt.Errorf("session %q disabled_tools: %w", sid, err)
		}
		if err := mgr.sessionPersist.UpdateSessionDisabledTools(ctx, sc, sid, disabledJSON); err != nil {
			return fmt.Errorf("session %q disabled_tools: %w", sid, err)
		}
	}
	// Same rule for tool_policy: only an EXPLICIT policy replaces the stored
	// one. Note this allows a deliberate widening (sending
	// INHERIT_SERVER_WIDE) while an omission does not.
	if hasPolicy {
		rt.ToolPolicy = toolPolicyName(req.GetToolPolicy())
		if err := mgr.sessionPersist.UpdateSessionToolPolicy(ctx, sc, sid, rt.ToolPolicy); err != nil {
			return fmt.Errorf("session %q tool_policy: %w", sid, err)
		}
	}
	// Only an EXPLICIT non-empty tool_context replaces the stored map; an
	// omitted one leaves it alone.
	if hasToolContext {
		tcJSON, err := store.MarshalToolContextJSON(req.GetToolContext())
		if err != nil {
			return fmt.Errorf("session %q tool_context: %w", sid, err)
		}
		if err := mgr.sessionPersist.UpdateSessionToolContext(ctx, sc, sid, tcJSON); err != nil {
			return fmt.Errorf("session %q tool_context: %w", sid, err)
		}
		rt.ToolContext = copyToolContext(req.GetToolContext())
	}
	if hasReplace {
		if err := replaceSessionTools(rt.Tools, req.GetInitialTools()); err != nil {
			return fmt.Errorf("session %q replace_tools: %w", sid, err)
		}
		tj, err := store.MarshalToolsJSON(rt.Tools.Snapshot())
		if err != nil {
			return err
		}
		if err := mgr.sessionPersist.SaveToolsJSON(ctx, sc, sid, tj); err != nil {
			return fmt.Errorf("session %q replace_tools: %w", sid, err)
		}
	}
	return nil
}

// copyToolContext returns an independent copy of m (nil when empty) so a
// request's map is never aliased by session state.
func copyToolContext(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ToolContextCopy returns a copy of the session's default tool_context (nil
// when none), safe for the caller to retain or mutate.
func (rt *SessionRuntime) ToolContextCopy() map[string]string {
	if rt == nil {
		return nil
	}
	return copyToolContext(rt.ToolContext)
}

// replaceSessionTools makes want the registry's tool set, keeping the
// client-delegated tools (clients own and reconcile those every turn).
// Every definition is validated BEFORE anything changes, so a bad schema
// leaves the session exactly as it was rather than half-replaced.
func replaceSessionTools(reg *toolregistry.Registry, want []*llmv1.ToolDefinition) error {
	keep := make(map[string]bool, len(want))
	var defs []*llmv1.ToolDefinition
	for _, td := range want {
		if td == nil || strings.TrimSpace(td.GetName()) == "" {
			continue
		}
		if err := llmtools.ValidateParametersJSON(td.GetParametersSchemaJson()); err != nil {
			return fmt.Errorf("tool %q: %w", td.GetName(), err)
		}
		keep[strings.TrimSpace(td.GetName())] = true
		defs = append(defs, td)
	}
	for _, cur := range reg.Snapshot() {
		if keep[cur.GetName()] || cur.GetDelegateToClient() != nil {
			continue
		}
		reg.Unregister(cur.GetName())
	}
	for _, td := range defs {
		if err := reg.Register(td); err != nil {
			return err
		}
	}
	return nil
}
