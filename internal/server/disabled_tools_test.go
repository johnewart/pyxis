package server

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"google.golang.org/protobuf/proto"
)

// Handler-level coverage for EnsureSessionRequest.disabled_tools on the gRPC
// surface. The rule under test lives in toolmerge.MergeDefinitionsExcluding;
// these tests pin that both consumers in this package — ListSessionTools
// (what a client sees) and mergedToolDefinitions (what the provider sees via
// mergedToolsStream) — are wired through it and agree with each other.

const (
	disabledToolsTestWorkspace = "ws-disabled-tools"
	disabledToolsProbeName     = "probe_tool"
)

// newDisabledToolsServer is newCoreToolsServer (InstallCoreTools over a SQLite
// session manager, so every coretools.Names() entry is genuinely
// server-registered) plus one fake server-wide tool, probe_tool, that a
// session may legitimately hide.
func newDisabledToolsServer(t *testing.T) *LlmServer {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	s := &LlmServer{
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
	}
	if err := InstallCoreTools(s, toolexec.SessionToolsDeps{
		Sessions:    store.NewSessionRepo(db),
		Events:      store.NewSessionEventRepo(db),
		Turns:       store.NewTurnRegister(db),
		Attachments: store.NewChatAttachmentRepo(db),
	}); err != nil {
		t.Fatalf("InstallCoreTools: %v", err)
	}
	if err := s.Tools.Register(probeToolDefinition()); err != nil {
		t.Fatalf("register server-wide %s: %v", disabledToolsProbeName, err)
	}
	return s
}

func probeToolDefinition() *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:                 disabledToolsProbeName,
		Description:          "probe",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
	}
}

// disabledToolsCtx is the workspace-scoped incoming context every handler in
// this file shares: ensureKnownSession rejects a session whose WorkspaceID
// differs from the caller's, so the session must be created and queried
// through the same ctx.
func disabledToolsCtx() context.Context {
	return serverutil.Scope{TenantID: disabledToolsTestWorkspace}.IncomingContext(context.Background())
}

func ensureDisabledToolsSession(t *testing.T, ctx context.Context, s *LlmServer, sid string, disabled ...string) {
	t.Helper()
	if _, err := s.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid, DisabledTools: disabled}); err != nil {
		t.Fatalf("EnsureSession(%q, disabled=%v): %v", sid, disabled, err)
	}
}

func listSessionTools(t *testing.T, ctx context.Context, s *LlmServer, sid string) []*llmv1.ToolDefinition {
	t.Helper()
	resp, err := s.ListSessionTools(ctx, &llmv1.ListSessionToolsRequest{SessionId: sid})
	if err != nil {
		t.Fatalf("ListSessionTools(%q): %v", sid, err)
	}
	return resp.GetTools()
}

// A server-wide tool named in disabled_tools is not reported by
// ListSessionTools, while every core tool still is.
func TestDisabledTools_ListSessionToolsOmitsHiddenServerTool(t *testing.T) {
	ctx := disabledToolsCtx()
	s := newDisabledToolsServer(t)
	const sid = "hide-probe"
	ensureDisabledToolsSession(t, ctx, s, sid, disabledToolsProbeName)

	tools := listSessionTools(t, ctx, s, sid)
	if toolDefinitionsContainName(tools, disabledToolsProbeName) {
		t.Errorf("ListSessionTools still lists disabled %q; got %v", disabledToolsProbeName, toolDefNames(tools))
	}
	assertHasEveryCoreTool(t, tools, "ListSessionTools with probe_tool disabled")
}

// DENY WINS: a session that explicitly registers a tool of the same name does
// NOT get it back — RegisterSessionTool succeeds (it is a valid registration)
// but the tool stays hidden and stays refused at dispatch. Registering a
// session tool is reachable by any client, so an exclusion a client could lift
// this way would not be a control at all.
func TestDisabledTools_ExplicitRegistrationDoesNotWin(t *testing.T) {
	ctx := disabledToolsCtx()
	s := newDisabledToolsServer(t)
	const sid = "deny-wins"
	ensureDisabledToolsSession(t, ctx, s, sid, disabledToolsProbeName)

	if toolDefinitionsContainName(listSessionTools(t, ctx, s, sid), disabledToolsProbeName) {
		t.Fatalf("precondition: %q should be hidden before explicit registration", disabledToolsProbeName)
	}

	if _, err := s.RegisterSessionTool(ctx, &llmv1.RegisterSessionToolRequest{SessionId: sid, Tool: probeToolDefinition()}); err != nil {
		t.Fatalf("RegisterSessionTool: %v", err)
	}
	after := listSessionTools(t, ctx, s, sid)
	if toolDefinitionsContainName(after, disabledToolsProbeName) {
		t.Errorf("registering %q lifted the exclusion; ListSessionTools = %v", disabledToolsProbeName, toolDefNames(after))
	}
	if toolDefinitionsContainName(s.mergedToolDefinitions(disabledToolsCtx(), sid), disabledToolsProbeName) {
		t.Errorf("registering %q lifted the exclusion on the provider-facing set", disabledToolsProbeName)
	}
	assertHasEveryCoreTool(t, after, "ListSessionTools after attempted override")

	// Hidden is not enough: the side door must refuse it too.
	if _, err := s.InvokeTool(ctx, &llmv1.InvokeToolRequest{
		ToolId:    disabledToolsProbeName,
		SessionId: proto.String(sid),
	}); err == nil {
		t.Errorf("InvokeTool ran disabled %q on session %q", disabledToolsProbeName, sid)
	}
}

// Naming a core tool in disabled_tools has no effect: core tools are exempt
// from the exclusion and the session still gets all of them.
func TestDisabledTools_CoreToolCannotBeHidden(t *testing.T) {
	ctx := disabledToolsCtx()
	s := newDisabledToolsServer(t)
	const sid = "hide-core"
	ensureDisabledToolsSession(t, ctx, s, sid, coretools.SetStatus)

	tools := listSessionTools(t, ctx, s, sid)
	if !toolDefinitionsContainName(tools, coretools.SetStatus) {
		t.Errorf("core tool %q was hidden by disabled_tools; got %v", coretools.SetStatus, toolDefNames(tools))
	}
	assertHasEveryCoreTool(t, tools, "ListSessionTools with a core tool named in disabled_tools")
}

// ask_user is core but optional: named in disabled_tools it is neither
// listed, nor offered to the provider, nor dispatchable; every guaranteed
// core tool stays.
func TestDisabledTools_AskUserCanBeDisabled(t *testing.T) {
	ctx := disabledToolsCtx()
	s := newDisabledToolsServer(t)
	const sid = "no-asking"
	ensureDisabledToolsSession(t, ctx, s, sid, coretools.AskUser)

	for where, tools := range map[string][]*llmv1.ToolDefinition{
		"ListSessionTools":      listSessionTools(t, ctx, s, sid),
		"mergedToolDefinitions": s.mergedToolDefinitions(disabledToolsCtx(), sid),
	} {
		if toolDefinitionsContainName(tools, coretools.AskUser) {
			t.Errorf("%s still offers disabled ask_user: %v", where, toolDefNames(tools))
		}
		for _, name := range coretools.Names() {
			if coretools.IsGuaranteed(name) && !toolDefinitionsContainName(tools, name) {
				t.Errorf("%s lost guaranteed core tool %q: %v", where, name, toolDefNames(tools))
			}
		}
	}
	if s.toolAllowedForSession(ctx, sid, coretools.AskUser) {
		t.Error("dispatch would still run disabled ask_user")
	}
	// Another session is unaffected: ask_user is on by default.
	ensureDisabledToolsSession(t, ctx, s, "may-ask")
	if !toolDefinitionsContainName(listSessionTools(t, ctx, s, "may-ask"), coretools.AskUser) {
		t.Error("a session with no disabled list lost ask_user")
	}
}

// The provider-facing source (mergedToolDefinitions, wrapped by
// mergedToolsStream and used for the chat loop's dispatch index) applies the
// same exclusion, and only for the session that asked for it.
func TestDisabledTools_MergedToolDefinitionsOmitsHiddenServerTool(t *testing.T) {
	ctx := disabledToolsCtx()
	s := newDisabledToolsServer(t)
	const hidden, plain = "merged-hidden", "merged-plain"
	ensureDisabledToolsSession(t, ctx, s, hidden, disabledToolsProbeName)
	ensureDisabledToolsSession(t, ctx, s, plain)

	hiddenDefs := s.mergedToolDefinitions(disabledToolsCtx(), hidden)
	if toolDefinitionsContainName(hiddenDefs, disabledToolsProbeName) {
		t.Errorf("mergedToolDefinitions(%q) still offers disabled %q; got %v", hidden, disabledToolsProbeName, toolDefNames(hiddenDefs))
	}
	assertHasEveryCoreTool(t, hiddenDefs, "mergedToolDefinitions with probe_tool disabled")

	plainDefs := s.mergedToolDefinitions(disabledToolsCtx(), plain)
	if !toolDefinitionsContainName(plainDefs, disabledToolsProbeName) {
		t.Errorf("mergedToolDefinitions(%q) (no disabled list) lost server-wide %q; got %v", plain, disabledToolsProbeName, toolDefNames(plainDefs))
	}

	// Both consumers agree for the hidden session.
	listed := listSessionTools(t, ctx, s, hidden)
	if len(listed) != len(hiddenDefs) {
		t.Errorf("ListSessionTools (%d tools) and mergedToolDefinitions (%d tools) disagree for %q: %v vs %v",
			len(listed), len(hiddenDefs), hidden, toolDefNames(listed), toolDefNames(hiddenDefs))
	}
}
