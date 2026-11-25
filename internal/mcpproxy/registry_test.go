/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package mcpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture MCP servers
// ---------------------------------------------------------------------------

const fixtureEnvGuard = "LLMD_MCP_FIXTURE_STDIO"

// TestStdioFixtureServer is not a test: when the guard env var is set the test
// binary re-execs into a minimal stdio MCP server. This is how the stdio
// transport gets a real subprocess to talk to.
func TestStdioFixtureServer(t *testing.T) {
	if os.Getenv(fixtureEnvGuard) != "1" {
		t.Skip("fixture server; runs only as a re-exec child")
	}
	serveFixtureStdio()
	os.Exit(0)
}

func serveFixtureStdio() {
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	for {
		line, err := in.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req rpcRequest
			if json.Unmarshal(line, &req) == nil && req.ID != nil {
				result := fixtureResult(req.Method)
				payload, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
				_, _ = out.Write(append(payload, '\n'))
			}
		}
		if err != nil {
			return
		}
	}
}

func fixtureResult(method string) json.RawMessage {
	switch method {
	case "initialize":
		return json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}`)
	case "tools/list":
		return json.RawMessage(`{"tools":[
			{"name":"search files","description":"Search","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}},
			{"name":"whoami"}
		]}`)
	case "tools/call":
		token := os.Getenv("MCP_TOKEN")
		body, _ := json.Marshal(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "token=" + token}},
		})
		return body
	}
	return json.RawMessage(`{}`)
}

func stdioFixtureConfig() ServerConfig {
	return ServerConfig{
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: os.Args[0],
			Args:    []string{"-test.run=TestStdioFixtureServer", "-test.v=false"},
		},
		CallTimeoutMs: 10_000,
	}
}

func stdioFixtureEnv(extra map[string]string) map[string]string {
	env := map[string]string{fixtureEnvGuard: "1"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func newHTTPFixture(t *testing.T, seenAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenAuth != nil {
			*seenAuth = r.Header.Get("Authorization")
		}
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: fixtureResult(req.Method)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// Naming parity with mcpToolNaming.ts
// ---------------------------------------------------------------------------

func TestProxiedToolNamingParity(t *testing.T) {
	cases := []struct{ provider, upstream, want string }{
		{"github", "create_issue", "github_create_issue"},
		{"github", "search files", "github_search_files"},
		{"linear", "list--issues", "linear_list_issues"},
		{"linear", "__weird__name__", "linear_weird_name"},
		{"jira", "issue.create", "jira_issue_create"},
	}
	for _, c := range cases {
		got, err := ProxiedToolID(c.provider, c.upstream)
		if err != nil {
			t.Fatalf("ProxiedToolID(%q,%q): %v", c.provider, c.upstream, err)
		}
		if got != c.want {
			t.Fatalf("ProxiedToolID(%q,%q) = %q, want %q", c.provider, c.upstream, got, c.want)
		}
	}

	if _, err := ProxiedToolID("github", "***"); err == nil {
		t.Fatal("expected an error for an upstream name that normalizes to empty")
	}

	pid, name, ok := ParseProxiedToolID("github_create_issue")
	if !ok || pid != "github" || name != "create_issue" {
		t.Fatalf("ParseProxiedToolID = %q,%q,%v", pid, name, ok)
	}
	if _, _, ok := ParseProxiedToolID("noseparator"); ok {
		t.Fatal("expected ParseProxiedToolID to reject an id with no separator")
	}

	err := AssertProxiedToolIDAvailable("read_code", map[string]struct{}{"read_code": {}})
	var collision *ToolIDCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("expected ToolIDCollisionError, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Transports
// ---------------------------------------------------------------------------

func TestStdioTransportConnectsAndListsTools(t *testing.T) {
	cfg := stdioFixtureConfig()
	cfg.Transport.Env = stdioFixtureEnv(nil)

	reg := NewRegistry(RegistryOptions{Enabled: true, Factory: DefaultTransportFactory(nil)})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"fixture": cfg})

	snaps := reg.Snapshot()
	if len(snaps) != 1 || snaps[0].Status != StatusReady {
		t.Fatalf("expected one ready provider, got %+v", snaps)
	}
	ids := reg.RegisteredToolIDs()
	want := []string{"fixture_search_files", "fixture_whoami"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("registered tool ids = %v, want %v", ids, want)
	}
}

func TestHTTPTransportConnectsAndListsTools(t *testing.T) {
	var auth string
	srv := newHTTPFixture(t, &auth)

	cfg := ServerConfig{
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportHTTP,
			URL:     srv.URL,
			Headers: map[string]string{"Authorization": "Bearer literal-token"},
		},
		CallTimeoutMs: 10_000,
	}

	reg := NewRegistry(RegistryOptions{Enabled: true, Factory: DefaultTransportFactory(nil)})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"remote": cfg})

	snaps := reg.Snapshot()
	if len(snaps) != 1 || snaps[0].Status != StatusReady {
		t.Fatalf("expected one ready provider, got %+v", snaps)
	}
	if got := reg.RegisteredToolIDs(); len(got) != 2 {
		t.Fatalf("registered tool ids = %v, want 2", got)
	}
	if auth != "Bearer literal-token" {
		t.Fatalf("configured header did not reach the upstream server: %q", auth)
	}

	out, err := reg.CallTool(context.Background(), "remote_whoami", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.HasPrefix(out, "token=") {
		t.Fatalf("CallTool returned %q", out)
	}
}

// ---------------------------------------------------------------------------
// Additive gating
// ---------------------------------------------------------------------------

func TestDisabledFeatureRegistersNoProxiedToolAnywhere(t *testing.T) {
	cfg := stdioFixtureConfig()
	cfg.Transport.Env = stdioFixtureEnv(nil)

	reg := NewRegistry(RegistryOptions{Enabled: false, Factory: DefaultTransportFactory(nil)})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"fixture": cfg})

	if ids := reg.RegisteredToolIDs(); len(ids) != 0 {
		t.Fatalf("gated-off registry exposed tool ids: %v", ids)
	}
	if snaps := reg.Snapshot(); len(snaps) != 0 {
		t.Fatalf("gated-off registry exposed providers: %+v", snaps)
	}
	if ids := reg.AllReadyToolIDsForScope(serverutil.Scope{TenantID: "t", ScopeID: "s1"}); len(ids) != 0 {
		t.Fatalf("gated-off registry exposed scope tool ids: %v", ids)
	}
	if ids := reg.ToolIDsForAgent("implementation", serverutil.Scope{TenantID: "t", ScopeID: "s1"}); len(ids) != 0 {
		t.Fatalf("gated-off registry exposed agent tool ids: %v", ids)
	}
	if desc := reg.BuildToolDescription(); desc != "" {
		t.Fatalf("gated-off registry contributed description text: %q", desc)
	}

	err := reg.ValidateToolID("fixture_whoami")
	if err == nil {
		t.Fatal("expected the gated-off registry to refuse a proxied tool id")
	}
	if strings.Contains(err.Error(), "fixture_search_files") || strings.Contains(strings.ToLower(err.Error()), "mcp") {
		t.Fatalf("validator error discloses the gated feature: %q", err)
	}

	// And with the gate ON the same registry does produce the content, so the
	// assertions above cannot pass by simply returning nothing.
	on := NewRegistry(RegistryOptions{Enabled: true, Factory: DefaultTransportFactory(nil)})
	t.Cleanup(on.Close)
	on.Refresh(context.Background(), map[string]ServerConfig{"fixture": cfg})
	if len(on.RegisteredToolIDs()) == 0 {
		t.Fatal("expected the enabled registry to register proxied tools")
	}
	if !strings.Contains(on.BuildToolDescription(), "fixture_search_files") {
		t.Fatalf("enabled registry description missing tools: %q", on.BuildToolDescription())
	}
}

func TestAgentAttachmentAndScopeAllowlist(t *testing.T) {
	cfg := stdioFixtureConfig()
	cfg.Transport.Env = stdioFixtureEnv(nil)
	cfg.AttachToAgents = []string{"implementation"}
	cfg.Scopes = []string{"s1"}
	in := serverutil.Scope{TenantID: "t", ScopeID: "s1"}
	out := serverutil.Scope{TenantID: "t", ScopeID: "s2"}

	reg := NewRegistry(RegistryOptions{Enabled: true, Factory: DefaultTransportFactory(nil)})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"fixture": cfg})

	if got := reg.ToolIDsForAgent("implementation", in); len(got) != 2 {
		t.Fatalf("attached agent got %v", got)
	}
	if got := reg.ToolIDsForAgent("ask", in); len(got) != 0 {
		t.Fatalf("unattached agent got %v", got)
	}
	if got := reg.AllReadyToolIDsForScope(out); len(got) != 0 {
		t.Fatalf("out-of-allowlist scope got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Secrets: resolved at point of use, never stored
// ---------------------------------------------------------------------------

type fakeSecretResolver struct {
	values map[string]string
	calls  []string
}

func (f *fakeSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	f.calls = append(f.calls, ref)
	v, ok := f.values[ref]
	if !ok {
		return "", fmt.Errorf("caller is not entitled to %s", ref)
	}
	return v, nil
}

func TestSecretRefResolvesAtSpawnAndDoesNotLeakToTheNextSpawn(t *testing.T) {
	res := &fakeSecretResolver{values: map[string]string{
		"secret://ws1/github-token": "s3cr3t",
	}}
	factory := DefaultTransportFactory(res)

	withToken := stdioFixtureConfig()
	withToken.Transport.Env = stdioFixtureEnv(map[string]string{
		"MCP_TOKEN": "secret://ws1/github-token",
	})

	reg := NewRegistry(RegistryOptions{Enabled: true, Factory: factory})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"fixture": withToken})

	out, err := reg.CallTool(context.Background(), "fixture_whoami", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "token=s3cr3t" {
		t.Fatalf("resolved secret did not reach the spawned server: %q", out)
	}
	if len(res.calls) != 1 || res.calls[0] != "secret://ws1/github-token" {
		t.Fatalf("expected exactly one resolve of the ref, got %v", res.calls)
	}

	// The next spawn resolves again from configuration; nothing is carried over.
	withoutToken := stdioFixtureConfig()
	withoutToken.Transport.Env = stdioFixtureEnv(nil)
	reg2 := NewRegistry(RegistryOptions{Enabled: true, Factory: factory})
	t.Cleanup(reg2.Close)
	reg2.Refresh(context.Background(), map[string]ServerConfig{"fixture": withoutToken})

	out2, err := reg2.CallTool(context.Background(), "fixture_whoami", nil)
	if err != nil {
		t.Fatalf("CallTool (second spawn): %v", err)
	}
	if out2 != "token=" {
		t.Fatalf("resolved value leaked into the next spawn's environment: %q", out2)
	}
}

func TestSecretUnentitledRefIsRefusedAndNoToolIsRegistered(t *testing.T) {
	res := &fakeSecretResolver{values: map[string]string{}}

	cfg := stdioFixtureConfig()
	cfg.Transport.Env = stdioFixtureEnv(map[string]string{
		"MCP_TOKEN": "secret://other-workspace/github-token",
	})

	reg := NewRegistry(RegistryOptions{Enabled: true, Factory: DefaultTransportFactory(res)})
	t.Cleanup(reg.Close)
	reg.Refresh(context.Background(), map[string]ServerConfig{"fixture": cfg})

	snaps := reg.Snapshot()
	if len(snaps) != 1 || snaps[0].Status != StatusError {
		t.Fatalf("expected the provider to fail on a refused ref, got %+v", snaps)
	}
	if !strings.Contains(snaps[0].LastError, "not entitled") {
		t.Fatalf("expected the refusal to surface verbatim, got %q", snaps[0].LastError)
	}
	if ids := reg.RegisteredToolIDs(); len(ids) != 0 {
		t.Fatalf("a provider that could not authenticate registered tools: %v", ids)
	}
}

func TestSecretRefsAreNotResolvedWithoutAResolver(t *testing.T) {
	cfg := stdioFixtureConfig()
	cfg.Transport.Env = stdioFixtureEnv(map[string]string{"MCP_TOKEN": "secret://ws1/tok"})

	_, err := DefaultTransportFactory(nil)(context.Background(), "fixture", cfg)
	if err == nil {
		t.Fatal("expected a ref to be refused when no resolver is wired")
	}
	if !IsSecretRef("secret://ws1/tok") || !IsSecretRef("slot://github_token") || IsSecretRef("plain") {
		t.Fatal("IsSecretRef misclassified a value")
	}
}
