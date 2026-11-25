package server

// Acceptance coverage for TOOL_POLICY_PROFILE_ONLY — deny-by-default tool
// gating on agent profiles.
//
// The failure these guard against: a session was given every server-wide tool
// unless a profile explicitly named each one as disabled, so any tool
// registered server-wide LATER was silently gained by every profile written
// before it. Enumerating what you do NOT want cannot express an allowlist, and
// the set of things to enumerate grows without the profile author knowing.
//
// The checks below pin, in order: the tool is absent from the model-facing list
// (1), absent from ListSessionTools (1), refused by the ChatStream dispatch
// index (2), refused by the unary InvokeTool RPC (3), core tools survive
// regardless (4), and — the regression guard for the whole class — a tool
// registered server-wide AFTER the session exists does not appear in it (7).

import (
	"context"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// profileOnlyReadCode enables exactly one tool and denies by default. Note
// what is absent: no Enabled=false entries at all. That is the point of
// PROFILE_ONLY — silence means no, so the author never has to know what else
// exists.
func profileOnlyReadCode() agentprofile.Profile {
	return agentprofile.Profile{
		ID:            "static-profile-only",
		Name:          "Profile Only",
		Description:   "deny-by-default profile",
		LlmProviderID: "provider_a",
		DefaultModel:  "model-a",
		ToolPolicy:    agentprofile.ProfileOnly,
		ToolMapping:   []agentprofile.ToolMapping{{ToolName: "read_code", Enabled: true}},
	}
}

// registerServerWide adds a tool to the fixture's server-wide registry, the
// way a host subsystem does at startup (or, for check 7, long after).
func registerServerWide(t *testing.T, f *agentProfileFixture, name string) {
	t.Helper()
	if err := f.srv.Tools.Register(&llmv1.ToolDefinition{
		Name:                 name,
		Description:          name + " (server-wide)",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
	}); err != nil {
		t.Fatalf("register server-wide %q: %v", name, err)
	}
}

// newProfileOnlySession builds the fixture, registers read_code and write_code
// server-wide, and creates one session from the PROFILE_ONLY profile.
func newProfileOnlySession(t *testing.T, sid string) (*agentProfileFixture, func() []*llmv1.ToolDefinition) {
	t.Helper()
	f := newAgentProfileFixture(t, profileOnlyReadCode())
	installCoreToolsOnFixture(t, f)
	registerServerWide(t, f, "read_code")
	registerServerWide(t, f, "write_code")

	ctx := agentProfileCtx()
	createSessionFromProfile(t, f, ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-profile-only",
		SessionId:      sid,
	})
	return f, func() []*llmv1.ToolDefinition { return listSessionTools(t, ctx, f.srv, sid) }
}

// invokeForSession calls the unary RPC the way an arbitrary client would.
func invokeForSession(f *agentProfileFixture, sid, tool string) error {
	_, err := f.srv.InvokeTool(agentProfileCtx(), &llmv1.InvokeToolRequest{
		ToolId:        tool,
		SessionId:     proto.String(sid),
		ArgumentsJson: `{}`,
	})
	return err
}

// Check 1: write_code is registered server-wide and the profile never mentions
// it, so it must be absent from both the model-facing list and
// ListSessionTools. read_code, which the profile enabled, must be present.
func TestToolPolicy_ProfileOnly_ExcludesUnlistedServerWideTool(t *testing.T) {
	const sid = "po-list"
	f, listed := newProfileOnlySession(t, sid)

	tools := listed()
	if toolDefinitionsContainName(tools, "write_code") {
		t.Errorf("ListSessionTools advertises write_code although the profile never enabled it: %v", toolDefNames(tools))
	}
	if !toolDefinitionsContainName(tools, "read_code") {
		t.Errorf("ListSessionTools lost the profile's enabled read_code: %v", toolDefNames(tools))
	}

	// The model-facing source must agree with the client-facing one, or a
	// settings screen and the running session disagree — which is how this
	// stayed invisible the first time.
	merged := f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), sid)
	if toolDefinitionsContainName(merged, "write_code") {
		t.Errorf("mergedToolDefinitions offers write_code to the provider: %v", toolDefNames(merged))
	}
	if len(merged) != len(tools) {
		t.Errorf("ListSessionTools (%d) and mergedToolDefinitions (%d) disagree: %v vs %v",
			len(tools), len(merged), toolDefNames(tools), toolDefNames(merged))
	}
}

// Check 2: hidden is not the same as blocked. The ChatStream tool loop builds
// its dispatch index from mergedToolDefinitions, so a tool missing there cannot
// be dispatched no matter how the model came to name it.
func TestToolPolicy_ProfileOnly_DispatchIndexHasNoHiddenTool(t *testing.T) {
	const sid = "po-dispatch"
	f, _ := newProfileOnlySession(t, sid)

	by := toolDefByName(f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), sid))
	if _, present := by["write_code"]; present {
		t.Error("the ChatStream dispatch index contains write_code; a hallucinated call would execute it")
	}
	if _, present := by["read_code"]; !present {
		t.Error("the ChatStream dispatch index is missing the profile's enabled read_code")
	}
}

// Check 3: the side door. InvokeTool sits outside the agent loop and is
// reachable by any client with a hand-crafted request, so it must refuse the
// same tool the list omits — naming the tool, and distinctly from "no such
// tool anywhere".
func TestToolPolicy_ProfileOnly_InvokeToolRefusesHiddenTool(t *testing.T) {
	const sid = "po-invoke"
	f, _ := newProfileOnlySession(t, sid)

	err := invokeForSession(f, sid, "write_code")
	if err == nil {
		t.Fatal("InvokeTool executed write_code on a PROFILE_ONLY session that never enabled it")
	}
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Errorf("InvokeTool refusal code = %v, want PermissionDenied (distinct from an unknown tool)", got)
	}
	if !strings.Contains(err.Error(), "write_code") {
		t.Errorf("InvokeTool refusal does not name the tool: %v", err)
	}

	// Control: the tool the profile DID enable still runs the normal path
	// (whatever the handler returns, it must not be refused by the gate).
	if err := invokeForSession(f, sid, "read_code"); status.Code(err) == codes.PermissionDenied {
		t.Errorf("InvokeTool refused the profile's enabled read_code: %v", err)
	}
}

// Check 4: core tools are present even though no mapping names them. An agent
// that cannot set its status or ask a question is broken regardless of policy,
// so deny-by-default must not reach them.
func TestToolPolicy_ProfileOnly_CoreToolsSurviveWithoutMapping(t *testing.T) {
	const sid = "po-core"
	f, listed := newProfileOnlySession(t, sid)

	assertHasEveryCoreTool(t, listed(), "ListSessionTools under PROFILE_ONLY")
	assertHasEveryCoreTool(t, f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), sid), "mergedToolDefinitions under PROFILE_ONLY")
}

// Check 7 — THE REGRESSION GUARD. A server-wide tool registered AFTER the
// PROFILE_ONLY session already exists must not appear in it, and must not be
// callable in it. This is the only check that catches the failure arriving
// later, which is exactly how the original bug bit: profiles were written, and
// then someone registered a new tool.
func TestToolPolicy_ProfileOnly_LaterServerWideToolDoesNotLeakIn(t *testing.T) {
	const sid = "po-later"
	f, listed := newProfileOnlySession(t, sid)

	before := listed()
	if toolDefinitionsContainName(before, "run_command") {
		t.Fatalf("precondition: run_command must not exist yet; got %v", toolDefNames(before))
	}

	// The event that used to widen every existing session.
	registerServerWide(t, f, "run_command")

	after := listed()
	if toolDefinitionsContainName(after, "run_command") {
		t.Errorf("a tool registered server-wide AFTER the session was created leaked into it: %v", toolDefNames(after))
	}
	if toolDefinitionsContainName(f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), sid), "run_command") {
		t.Error("the late-registered run_command reached the provider-facing tool list")
	}
	if len(after) != len(before) {
		t.Errorf("session tool count changed from %d to %d after an unrelated server-wide registration: %v",
			len(before), len(after), toolDefNames(after))
	}

	// And it is blocked, not merely hidden.
	err := invokeForSession(f, sid, "run_command")
	if err == nil {
		t.Fatal("InvokeTool ran a late-registered run_command on a PROFILE_ONLY session")
	}
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Errorf("refusal code = %v, want PermissionDenied", got)
	}
}

// A session with no policy keeps today's behaviour: an unlisted server-wide
// tool stays visible. Without this, adding the policy would silently strip
// tools from every existing session.
func TestToolPolicy_InheritServerWide_IsStillPermissive(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	installCoreToolsOnFixture(t, f)
	registerServerWide(t, f, "write_code")
	ctx := agentProfileCtx()

	createSessionFromProfile(t, f, ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-a",
		SessionId:      "inherit-1",
	})

	tools := listSessionTools(t, ctx, f.srv, "inherit-1")
	if !toolDefinitionsContainName(tools, "write_code") {
		t.Errorf("a profile with no policy lost the server-wide write_code: %v", toolDefNames(tools))
	}
}

// Check 8's analogue for the policy: a reconnect-style EnsureSession that omits
// tool_policy must leave a PROFILE_ONLY session as it was. If an omitted field
// reset the policy, any reconnect would hand the session every server-wide
// tool — the original bug, reached by a different route.
func TestToolPolicy_OmittedOnEnsureSessionDoesNotWidenSession(t *testing.T) {
	const sid = "po-reconnect"
	f, listed := newProfileOnlySession(t, sid)
	ctx := agentProfileCtx()

	// A bare ensure that only touches the model, as a reconnect would.
	if _, err := f.srv.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:    sid,
		DefaultModel: proto.String("model-b"),
	}); err != nil {
		t.Fatalf("EnsureSession (reconnect): %v", err)
	}

	rt, ok := f.mgr.Get(agentProfileTestScope, sid)
	if !ok {
		t.Fatalf("session %q not in manager", sid)
	}
	if rt.ToolPolicyName() != "profile_only" {
		t.Errorf("reconnect cleared the tool policy: got %q, want %q", rt.ToolPolicyName(), "profile_only")
	}
	if tools := listed(); toolDefinitionsContainName(tools, "write_code") {
		t.Errorf("reconnect widened the session to server-wide tools: %v", toolDefNames(tools))
	}
}

// Provenance: the effective set must say WHY each tool is there, so "why does
// this agent have X" is answerable without reading code.
func TestToolPolicy_EffectiveSetCarriesProvenance(t *testing.T) {
	const sid = "po-provenance"
	f, _ := newProfileOnlySession(t, sid)

	origins := map[string]string{}
	for _, e := range f.srv.effectiveToolProvenance(agentProfileTestScope.IncomingContext(context.Background()), sid) {
		origins[e.Name()] = string(e.Origin)
	}
	// read_code came from the profile, which materializes as a session
	// registration (initial_tools).
	if got := origins["read_code"]; got != "session-registered" {
		t.Errorf("read_code origin = %q, want %q", got, "session-registered")
	}
	if got := origins["set_status"]; got != "core" {
		t.Errorf("set_status origin = %q, want %q", got, "core")
	}
	if _, present := origins["write_code"]; present {
		t.Errorf("write_code should have no provenance entry at all; origins = %v", origins)
	}
}
