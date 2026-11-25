package sessionmanager

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"google.golang.org/protobuf/proto"
)

func testManagerWithSQLite(t *testing.T) (*Manager, *store.SessionRepo) {
	t.Helper()
	db := store.NewTestSQLite(t)
	sessionRepo := store.NewSessionRepo(db)
	mgr, err := New(session.HubOptions{}, Options{
		SessionPersist: sessionRepo,
		NewSessionHistory: func(sc serverutil.Scope, runtimeID string) history.Store {
			return store.NewHistory(db, sc, runtimeID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mgr, sessionRepo
}

func TestEnsureSession_persistsSessionRow(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-native-1"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		LlmProviderId: proto.String("cursor"),
		DefaultModel:  proto.String("default"),
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.ProviderID != "cursor" || row.DefaultModel != "default" {
		t.Fatalf("session row=%+v", row)
	}
	if _, ok := mgr.Get(scopeFor(workspaceID), sid); !ok {
		t.Fatal("expected in-memory session runtime")
	}
}

// TestEnsureSession_SystemPromptSections_SetOnceAndPersisted pins the actual
// complaint this exists to fix: a caller sets the structured system prompt
// ONCE via EnsureSession, and it is both applied in-memory immediately and
// durably persisted — no ChatStreamRequest.system_prompt_sections resend
// required on every turn.
func TestEnsureSession_SystemPromptSections_SetOnceAndPersisted(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-prompt-1"

	sections := []*llmv1.SystemPromptSection{
		{Id: "agent.role", Content: "You are helpful."},
	}
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:            sid,
		SystemPromptSections: sections,
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	ma, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected in-memory session runtime")
	}
	if len(ma.SystemPromptSections) != 1 || ma.SystemPromptSections[0].GetContent() != "You are helpful." {
		t.Fatalf("expected in-memory sections set immediately, got %+v", ma.SystemPromptSections)
	}

	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.SystemPromptSectionsJSON == "" {
		t.Fatal("expected system_prompt_sections_json persisted on the session row")
	}
	restored, err := store.UnmarshalSystemPromptSectionsJSON(row.SystemPromptSectionsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].GetId() != "agent.role" {
		t.Fatalf("expected persisted sections to round-trip, got %+v", restored)
	}
}

// TestEnsureSession_SystemPromptSections_SurvivesRestore pins the runtime-eviction
// case: a session's default prompt must still be there after the in-memory
// runtime is evicted and reloaded from the DB (RestoreSession), same as tools
// and provider binding already do.
func TestEnsureSession_SystemPromptSections_SurvivesRestore(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-prompt-restore"

	sections := []*llmv1.SystemPromptSection{{Id: "agent.role", Content: "Persisted default."}}
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:            sid,
		SystemPromptSections: sections,
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	// Simulate eviction: drop the in-memory runtime, forcing EnsureSession to
	// reload it from the SessionRow via RestoreSession.
	mgr.mu.Lock()
	delete(mgr.sessions, runtimeKey{scope: scopeFor(workspaceID), sessionID: sid})
	mgr.mu.Unlock()

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime reloaded after eviction")
	}
	if len(ma.SystemPromptSections) != 1 || ma.SystemPromptSections[0].GetContent() != "Persisted default." {
		t.Fatalf("expected restored sections, got %+v", ma.SystemPromptSections)
	}
}

// TestEnsureSession_SystemPromptSections_OmittedFieldDoesNotWipeDefault pins the
// safety property called out in the proto doc: an ordinary reconnect
// EnsureSession call that does not repeat system_prompt_sections (e.g. one
// only touching llm_provider_id) must NOT clear the previously-set default.
func TestEnsureSession_SystemPromptSections_OmittedFieldDoesNotWipeDefault(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-prompt-omit"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:            sid,
		SystemPromptSections: []*llmv1.SystemPromptSection{{Id: "agent.role", Content: "Original."}},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	// Reconnect-style call: same in-memory runtime still loaded, only touches
	// the provider binding.
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		LlmProviderId: proto.String("anthropic"),
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	ma, _ := mgr.Get(scopeFor(workspaceID), sid)
	if len(ma.SystemPromptSections) != 1 || ma.SystemPromptSections[0].GetContent() != "Original." {
		t.Fatalf("expected original sections preserved, got %+v", ma.SystemPromptSections)
	}
	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.UnmarshalSystemPromptSectionsJSON(row.SystemPromptSectionsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].GetContent() != "Original." {
		t.Fatalf("expected persisted sections preserved, got %+v", restored)
	}
}

// TestSessionRuntime_DisabledToolSet_ReturnsCopy pins the accessor contract:
// nil receiver and nil field are safe, and the returned map is never the live
// one (mutating it leaves the runtime untouched).
func TestSessionRuntime_DisabledToolSet_ReturnsCopy(t *testing.T) {
	t.Parallel()
	var nilRT *SessionRuntime
	if got := nilRT.DisabledToolSet(); len(got) != 0 {
		t.Fatalf("nil receiver: expected empty set, got %v", got)
	}
	if got := (&SessionRuntime{}).DisabledToolSet(); len(got) != 0 {
		t.Fatalf("nil field: expected empty set, got %v", got)
	}

	rt := &SessionRuntime{DisabledTools: disabledToolSet([]string{" probe_tool ", "", "probe_tool", "other"})}
	if len(rt.DisabledTools) != 2 {
		t.Fatalf("expected trimmed/deduped set of 2, got %v", rt.DisabledTools)
	}
	got := rt.DisabledToolSet()
	if _, ok := got["probe_tool"]; !ok {
		t.Fatalf("expected probe_tool in copy, got %v", got)
	}
	delete(got, "probe_tool")
	got["injected"] = struct{}{}
	if _, ok := rt.DisabledTools["probe_tool"]; !ok {
		t.Fatal("mutating the copy removed probe_tool from the live set")
	}
	if _, ok := rt.DisabledTools["injected"]; ok {
		t.Fatal("mutating the copy leaked a name into the live set")
	}
}

// TestEnsureSession_DisabledTools_SetOnCreateAndPersisted: create with
// disabled_tools populates the runtime set immediately and writes the
// disabled_tools_json column.
func TestEnsureSession_DisabledTools_SetOnCreateAndPersisted(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-disabled-create"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		DisabledTools: []string{"probe_tool"},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	rt, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected in-memory session runtime")
	}
	if _, ok := rt.DisabledToolSet()["probe_tool"]; !ok {
		t.Fatalf("expected probe_tool in runtime set, got %v", rt.DisabledToolSet())
	}

	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.DisabledToolsJSON == "" {
		t.Fatal("expected disabled_tools_json persisted on the session row")
	}
	names, err := store.UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "probe_tool" {
		t.Fatalf("expected persisted disabled tools [probe_tool], got %v", names)
	}
}

// TestEnsureSession_DisabledTools_SurvivesRestore: the set must come back
// after the runtime is evicted and reloaded via RestoreSession, both through
// EnsureSession's reload path and through RestoreSession on a fresh Manager.
func TestEnsureSession_DisabledTools_SurvivesRestore(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-disabled-restore"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		DisabledTools: []string{"probe_tool"},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	// Simulate eviction: drop the in-memory runtime, forcing EnsureSession to
	// reload it from the SessionRow via RestoreSession.
	mgr.mu.Lock()
	delete(mgr.sessions, runtimeKey{scope: scopeFor(workspaceID), sessionID: sid})
	mgr.mu.Unlock()

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	rt, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime reloaded after eviction")
	}
	if _, ok := rt.DisabledToolSet()["probe_tool"]; !ok {
		t.Fatalf("expected probe_tool restored after eviction, got %v", rt.DisabledToolSet())
	}

	// RestoreSession directly on a Manager that shares no memory with mgr.
	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	mgr2, _ := testManagerWithSQLite(t)
	if err := mgr2.RestoreSession(*row, nil); err != nil {
		t.Fatal(err)
	}
	rt2, ok := mgr2.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime on fresh manager after RestoreSession")
	}
	if _, ok := rt2.DisabledToolSet()["probe_tool"]; !ok {
		t.Fatalf("expected probe_tool on fresh manager, got %v", rt2.DisabledToolSet())
	}
}

// TestEnsureSession_ToolPolicy_SurvivesRestore: PROFILE_ONLY must come back
// after eviction, through both reload paths. The deny-by-default guarantee is
// only as good as this: if the policy were lost on restore, an evicted
// PROFILE_ONLY session would silently reload as inherit-everything and regain
// every server-wide tool — the original bug, reached via the cache.
func TestEnsureSession_ToolPolicy_SurvivesRestore(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-policy-restore"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:  sid,
		ToolPolicy: llmv1.ToolPolicy_TOOL_POLICY_PROFILE_ONLY,
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	mgr.mu.Lock()
	delete(mgr.sessions, runtimeKey{scope: scopeFor(workspaceID), sessionID: sid})
	mgr.mu.Unlock()

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	rt, ok := mgr.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime reloaded after eviction")
	}
	if got := rt.ToolPolicyName(); got != toolmerge.PolicyProfileOnlyName {
		t.Fatalf("tool policy after eviction = %q, want %q", got, toolmerge.PolicyProfileOnlyName)
	}

	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.ToolPolicy != toolmerge.PolicyProfileOnlyName {
		t.Fatalf("persisted tool_policy = %q, want %q", row.ToolPolicy, toolmerge.PolicyProfileOnlyName)
	}
	mgr2, _ := testManagerWithSQLite(t)
	if err := mgr2.RestoreSession(*row, nil); err != nil {
		t.Fatal(err)
	}
	rt2, ok := mgr2.Get(scopeFor(workspaceID), sid)
	if !ok {
		t.Fatal("expected runtime on fresh manager after RestoreSession")
	}
	if got := rt2.ToolPolicyName(); got != toolmerge.PolicyProfileOnlyName {
		t.Fatalf("tool policy on fresh manager = %q, want %q", got, toolmerge.PolicyProfileOnlyName)
	}
}

// TestEnsureSession_ToolPolicy_OmittedFieldDoesNotWipe: an ensure that omits
// tool_policy leaves the stored policy intact (UNSPECIFIED means "not
// supplied"), while an explicit INHERIT_SERVER_WIDE is a deliberate widening
// and does take effect.
func TestEnsureSession_ToolPolicy_OmittedFieldDoesNotWipe(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-policy-keep"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:  sid,
		ToolPolicy: llmv1.ToolPolicy_TOOL_POLICY_PROFILE_ONLY,
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	// Reconnect-style: touches the model only.
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:    sid,
		DefaultModel: proto.String("model-z"),
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	rt, _ := mgr.Get(scopeFor(workspaceID), sid)
	if got := rt.ToolPolicyName(); got != toolmerge.PolicyProfileOnlyName {
		t.Fatalf("omitted tool_policy cleared the policy: got %q", got)
	}
	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.ToolPolicy != toolmerge.PolicyProfileOnlyName {
		t.Fatalf("omitted tool_policy cleared the column: got %q", row.ToolPolicy)
	}

	// Explicit widening is honoured.
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:  sid,
		ToolPolicy: llmv1.ToolPolicy_TOOL_POLICY_INHERIT_SERVER_WIDE,
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	rt, _ = mgr.Get(scopeFor(workspaceID), sid)
	if got := rt.ToolPolicyName(); got != "" {
		t.Fatalf("explicit INHERIT_SERVER_WIDE was not applied: got %q", got)
	}
}

// TestEnsureSession_DisabledTools_OmittedFieldDoesNotWipe: a reconnect-style
// EnsureSession that omits disabled_tools must leave both the runtime set and
// the persisted column alone.
func TestEnsureSession_DisabledTools_OmittedFieldDoesNotWipe(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-disabled-omit"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		DisabledTools: []string{"probe_tool"},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		LlmProviderId: proto.String("anthropic"),
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	rt, _ := mgr.Get(scopeFor(workspaceID), sid)
	if _, ok := rt.DisabledToolSet()["probe_tool"]; !ok {
		t.Fatalf("expected runtime set preserved, got %v", rt.DisabledToolSet())
	}
	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	names, err := store.UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "probe_tool" {
		t.Fatalf("expected persisted set preserved as [probe_tool], got %v", names)
	}
}

// TestEnsureSession_DisabledTools_ExplicitListReplaces: an explicit non-empty
// list on re-ensure replaces the set wholesale, in memory and on disk.
func TestEnsureSession_DisabledTools_ExplicitListReplaces(t *testing.T) {
	t.Parallel()
	mgr, sessionRepo := testManagerWithSQLite(t)
	ctx := context.Background()
	const workspaceID = "workspace-1"
	const sid = "sess-disabled-replace"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		DisabledTools: []string{"a_tool"},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		DisabledTools: []string{"b_tool"},
	}, scopeFor(workspaceID)); err != nil {
		t.Fatal(err)
	}

	rt, _ := mgr.Get(scopeFor(workspaceID), sid)
	set := rt.DisabledToolSet()
	if _, ok := set["b_tool"]; !ok || len(set) != 1 {
		t.Fatalf("expected runtime set exactly {b_tool}, got %v", set)
	}
	row, err := sessionRepo.GetSession(ctx, serverutil.Scope{TenantID: workspaceID}, sid)
	if err != nil {
		t.Fatal(err)
	}
	names, err := store.UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "b_tool" {
		t.Fatalf("expected persisted set [b_tool], got %v", names)
	}
}
